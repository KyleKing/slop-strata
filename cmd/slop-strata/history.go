package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/kyleking/aragonite/vcs"

	"github.com/kyleking/slop-strata/internal/intent"
	"github.com/kyleking/slop-strata/internal/lineage"
	"github.com/kyleking/slop-strata/internal/report"
	"github.com/kyleking/slop-strata/internal/structure"
)

// historyResult is the history subcommand's JSON shape.
type historyResult struct {
	Range   string          `json:"range"`
	Lineage *lineage.Result `json:"lineage"`
	Weeks   []weekRow       `json:"weeks"`
	Viral   []viralRow      `json:"virality"`
	Pairs   []pairRow       `json:"pairing"`
	Changes int             `json:"changes"`
}

// weekRow is one week of first-parent activity.
type weekRow struct {
	Week    string  `json:"week"`
	Commits int     `json:"commits"`
	Added   int     `json:"added"`
	Removed int     `json:"removed"`
	MassNet float64 `json:"mass_net"`
}

// viralRow tracks one rule's spread after its first hit.
type viralRow struct {
	Rule        string         `json:"rule"`
	FirstWeek   string         `json:"first_week"`
	HitsByClass map[string]int `json:"hits_by_class"`
}

// pairRow compares author classes on the same week and directories.
type pairRow struct {
	Week       string             `json:"week"`
	Dir        string             `json:"dir"`
	ClassMass  map[string]float64 `json:"class_mass_net"`
	ClassChurn map[string]int     `json:"class_churn"`
}

func (s *session) historyCmd(ctx context.Context, a *args, w io.Writer) error {
	since := a.since
	if since == "" {
		since = "180 days ago"
	}
	rangeSpec := a.rev
	if rangeSpec == "" {
		rangeSpec = defaultRev
	}

	walker := lineage.NewWalker(s.cfg.Classify)
	weeks := map[string]*weekRow{}
	pairs := map[string]*pairRow{}
	viral := map[string]*viralRow{}
	ruleFirst := map[string]string{}
	changes := 0

	for rec, err := range s.ops.LogHistory(ctx, s.root, vcs.LogOptions{
		Range: rangeSpec, FirstParent: true, Reverse: true, Since: since,
	}) {
		if err != nil {
			return fmt.Errorf("walking history: %w", err)
		}
		changes++
		in := intent.Of(rec.Subject, "", rec.Body)
		klass := s.cfg.Classify(rec.AuthorName+" <"+rec.AuthorEmail+">", rec.CommitterName+" <"+rec.CommitterEmail+">", rec.Body)
		// Week buckets by Monday: truncate to weekday.
		monday := rec.AuthorTime.AddDate(0, 0, -int((rec.AuthorTime.Weekday()+6)%7))
		week := monday.Format("2006-01-02")

		walker.Commit(rec, in.Kind == intent.Fix, in.Revert)

		if weeks[week] == nil {
			weeks[week] = &weekRow{Week: week}
		}
		wk := weeks[week]
		wk.Commits++
		for _, f := range rec.Files {
			wk.Added += f.LinesAdded()
			wk.Removed += f.LinesDeleted()
		}

		// Score this commit's new blobs for mass delta and rule hits.
		s.scoreCommit(ctx, rec, wk, pairs, viral, ruleFirst, klass, week)
	}

	res := &historyResult{Range: rangeSpec, Lineage: walker.Finish(), Changes: changes}
	for _, wk := range weeks {
		res.Weeks = append(res.Weeks, *wk)
	}
	sort.Slice(res.Weeks, func(i, j int) bool { return res.Weeks[i].Week < res.Weeks[j].Week })
	for _, p := range pairs {
		res.Pairs = append(res.Pairs, *p)
	}
	sort.Slice(res.Pairs, func(i, j int) bool {
		if res.Pairs[i].Week != res.Pairs[j].Week {
			return res.Pairs[i].Week < res.Pairs[j].Week
		}
		return res.Pairs[i].Dir < res.Pairs[j].Dir
	})
	for _, v := range viral {
		res.Viral = append(res.Viral, *v)
	}
	sort.Slice(res.Viral, func(i, j int) bool { return res.Viral[i].Rule < res.Viral[j].Rule })

	if a.json {
		return report.WriteJSON(w, "history", s.root, res, nil)
	}

	printHistory(w, res)

	return nil
}

// scoreCommit analyzes a commit's after-blobs for mass delta, findings, and
// the per-class pairing.
func (s *session) scoreCommit(ctx context.Context, rec vcs.CommitRecord, wk *weekRow,
	pairs map[string]*pairRow, viral map[string]*viralRow, ruleFirst map[string]string, klass, week string,
) {
	var shas []string
	for _, f := range rec.Files {
		if f.NewSHA != "" {
			shas = append(shas, f.NewSHA)
		}
	}
	blobs, err := s.ops.BlobContents(ctx, s.root, shas)
	if err != nil {
		return
	}
	for _, f := range rec.Files {
		content, ok := blobs[f.NewSHA]
		if !ok {
			continue
		}
		rep := s.eng.Analyze(f.Path, f.NewSHA, content)
		if rep.Skipped != "" {
			continue
		}
		mass := 0.0
		for _, fn := range rep.Metrics.Funcs {
			mass += structure.Mass(fn)
		}
		wk.MassNet += mass
		d := lineage.DirOf(f.Path)
		key := week + "\x00" + d
		if pairs[key] == nil {
			pairs[key] = &pairRow{Week: week, Dir: d, ClassMass: map[string]float64{}, ClassChurn: map[string]int{}}
		}
		pairs[key].ClassMass[klass] += mass
		for _, fn := range rep.Findings {
			if viral[fn.Rule] == nil {
				viral[fn.Rule] = &viralRow{Rule: fn.Rule, FirstWeek: week, HitsByClass: map[string]int{}}
			}
			viral[fn.Rule].HitsByClass[klass]++
			if _, ok := ruleFirst[fn.Rule]; !ok {
				ruleFirst[fn.Rule] = week
			}
		}
	}
}

// printHistory renders trends per directory with sparklines.
func printHistory(w io.Writer, res *historyResult) {
	report.Fprintf(w, "%d first-parent commits · %d reverts\n\n", res.Changes, res.Lineage.Reverts)

	if len(res.Weeks) > 0 {
		var rows [][]string
		for _, wk := range res.Weeks {
			rows = append(rows, []string{
				wk.Week, fmt.Sprint(wk.Commits), fmt.Sprintf("+%d", wk.Added),
				fmt.Sprintf("-%d", wk.Removed), fmt.Sprintf("%+.0f", wk.MassNet),
			})
		}
		report.Table(w, []string{"week", "commits", "added", "removed", "mass"}, rows)
	}

	if len(res.Lineage.Survival) > 0 {
		report.Fprintln(w)
		var rows [][]string
		for _, sv := range res.Lineage.Survival {
			rows = append(rows, []string{
				sv.Cohort, fmt.Sprint(sv.Added), fmt.Sprint(sv.Surviving),
				fmt.Sprintf("%.2f", sv.S30), fmt.Sprintf("%.2f", sv.S90),
			})
		}
		report.Table(w, []string{"cohort", "added", "alive", "s30", "s90"}, rows)
	}

	if len(res.Lineage.Dirs) > 0 {
		report.Fprintln(w)
		var rows [][]string
		for _, d := range report.SortedKeys(res.Lineage.Dirs) {
			ds := res.Lineage.Dirs[d]
			rows = append(rows, []string{
				d, fmt.Sprint(ds.Added), fmt.Sprint(ds.Removed), fmt.Sprint(ds.Churn30),
				fmt.Sprint(ds.Rework), fmt.Sprintf("%.2f", res.Lineage.Concentration[d]),
			})
		}
		report.Table(w, []string{"dir", "added", "removed", "churn30", "rework", "concentr."}, rows)
	}

	if len(res.Viral) > 0 {
		report.Fprintln(w)
		var rows [][]string
		for _, v := range res.Viral {
			var parts []string
			for _, k := range report.SortedKeys(v.HitsByClass) {
				parts = append(parts, fmt.Sprintf("%s:%d", k, v.HitsByClass[k]))
			}
			rows = append(rows, []string{v.Rule, v.FirstWeek, strings.Join(parts, " ")})
		}
		report.Table(w, []string{"rule", "first hit", "spread by class"}, rows)
	}
}

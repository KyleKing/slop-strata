package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/kyleking/aragonite/vcs"

	"github.com/kyleking/slop-strata/internal/intent"
	"github.com/kyleking/slop-strata/internal/lineage"
	"github.com/kyleking/slop-strata/internal/report"
)

// daysPerWeek buckets author dates into Monday weeks.
const daysPerWeek = 7

// provisionalBelow is the commit count under which a pairing cell is too thin
// to compare.
const provisionalBelow = 5

// historyResult is the history subcommand's JSON shape.
type historyResult struct {
	Lineage  *lineage.Result    `json:"lineage"`
	Hotspots map[string]float64 `json:"hotspots"`
	Range    string             `json:"range"`
	Weeks    []weekRow          `json:"weeks"`
	Viral    []viralRow         `json:"virality"`
	Pairs    []pairRow          `json:"pairing"`
	Changes  int                `json:"changes"`
}

// weekRow is one week of first-parent activity.
type weekRow struct {
	Kinds   map[string]int `json:"kinds"`
	Week    string         `json:"week"`
	Commits int            `json:"commits"`
	Added   int            `json:"added"`
	Removed int            `json:"removed"`
	MassNet float64        `json:"mass_net"`
}

// viralRow tracks one rule's spread after its first hit.
type viralRow struct {
	HitsByClass map[string]int `json:"hits_by_class"`
	Rule        string         `json:"rule"`
	FirstWeek   string         `json:"first_week"`
}

// pairRow compares author classes on the same week and directories.
// Provisional marks cells too thin to compare (under 5 commits).
type pairRow struct {
	ClassMass    map[string]float64 `json:"class_mass_net"`
	ClassChurn   map[string]int     `json:"class_churn"`
	ClassCommits map[string]int     `json:"class_commits"`
	Week         string             `json:"week"`
	Dir          string             `json:"dir"`
	Provisional  bool               `json:"provisional"`
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
	liveMass := map[string]float64{} // path -> mass of its latest after-blob
	changes := 0

	for rec, err := range s.ops.LogHistory(ctx, s.root, vcs.LogOptions{
		Range: rangeSpec, FirstParent: true, Reverse: true, Since: since,
	}) {
		if err != nil {
			return fmt.Errorf("walking history: %w", err)
		}
		changes++
		in := intent.Of(rec.Subject, "", rec.Body)
		klass := s.cfg.Classify(rec.AuthorName+" <"+rec.AuthorEmail+">",
			rec.CommitterName+" <"+rec.CommitterEmail+">", rec.Body)
		// Week buckets by Monday: truncate to weekday.
		monday := rec.AuthorTime.AddDate(0, 0, -int((rec.AuthorTime.Weekday()+daysPerWeek-1)%daysPerWeek))
		week := monday.Format("2006-01-02")

		walker.Commit(rec, in.Kind == intent.Fix, in.Revert)

		if weeks[week] == nil {
			weeks[week] = &weekRow{Week: week, Kinds: map[string]int{}}
		}
		wk := weeks[week]
		wk.Commits++
		wk.Kinds[string(in.Kind)]++
		for i := range rec.Files {
			wk.Added += rec.Files[i].LinesAdded()
			wk.Removed += rec.Files[i].LinesDeleted()
		}

		// Score this commit's new blobs for mass delta and rule hits.
		s.scoreCommit(ctx, rec, &commitScore{
			wk: wk, pairs: pairs, viral: viral, ruleFirst: ruleFirst,
			liveMass: liveMass, klass: klass, week: week,
		})
	}

	res := &historyResult{Range: rangeSpec, Lineage: walker.Finish(), Changes: changes}
	collectRollups(res, weeks, pairs, viral)
	res.Hotspots = hotspotScores(res.Lineage, liveMass)

	if a.json {
		if err := report.WriteJSON(w, "history", s.root, res, nil); err != nil {
			return fmt.Errorf("writing result: %w", err)
		}

		return nil
	}

	printHistory(w, res)

	return nil
}

// collectRollups folds the accumulated maps into res's sorted slices.
func collectRollups(res *historyResult, weeks map[string]*weekRow,
	pairs map[string]*pairRow, viral map[string]*viralRow,
) {
	for _, wk := range weeks {
		res.Weeks = append(res.Weeks, *wk)
	}
	sort.Slice(res.Weeks, func(i, j int) bool { return res.Weeks[i].Week < res.Weeks[j].Week })
	for _, p := range pairs {
		total := 0
		for _, n := range p.ClassCommits {
			total += n
		}
		p.Provisional = total < provisionalBelow
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
}

// hotspotScores is churn × live complexity mass per directory: directories
// where young code dies fast and what's there is heavy. Live mass covers
// files touched in the range, the same files churn is counted on.
func hotspotScores(lin *lineage.Result, liveMass map[string]float64) map[string]float64 {
	dirMass := map[string]float64{}
	for path, m := range liveMass {
		dirMass[lineage.DirOf(path)] += m
	}
	out := map[string]float64{}
	for d, ds := range lin.Dirs {
		if ds.Churn30 > 0 && dirMass[d] > 0 {
			out[d] = float64(ds.Churn30) * dirMass[d]
		}
	}

	return out
}

// commitScore is what one commit's blob scoring feeds: the week row, the
// pairing maps, the virality trackers, and each path's latest live mass.
type commitScore struct {
	wk        *weekRow
	pairs     map[string]*pairRow
	viral     map[string]*viralRow
	ruleFirst map[string]string
	liveMass  map[string]float64
	klass     string
	week      string
}

// scoreCommit analyzes both sides of a commit's changed blobs for mass
// delta, findings, and the per-class pairing.
func (s *session) scoreCommit(ctx context.Context, rec vcs.CommitRecord, cs *commitScore) {
	var shas []string
	for i := range rec.Files {
		f := &rec.Files[i]
		if f.NewSHA != "" {
			shas = append(shas, f.NewSHA)
		}
		if f.OldSHA != "" {
			shas = append(shas, f.OldSHA)
		}
	}
	blobs, err := s.ops.BlobContents(ctx, s.root, shas)
	if err != nil {
		return
	}
	for i := range rec.Files {
		f := &rec.Files[i]
		d := lineage.DirOf(f.Path)
		if f.Status == vcs.StatusRenamed && f.OldPath != "" {
			cs.liveMass[f.Path] = cs.liveMass[f.OldPath]
			delete(cs.liveMass, f.OldPath)
		}
		pairCell(cs.pairs, cs.week, d).ClassCommits[cs.klass]++

		content, ok := blobs[f.NewSHA]
		if !ok {
			delete(cs.liveMass, f.Path)

			continue
		}
		s.scoreBlob(ctx, cs, *f, content, s.oldMass(ctx, *f, blobs))
	}
}

// pairCell returns the week's pairing row for a directory, creating it on
// first use.
func pairCell(pairs map[string]*pairRow, week, d string) *pairRow {
	key := week + "\x00" + d
	if pairs[key] == nil {
		pairs[key] = &pairRow{
			Week: week, Dir: d, ClassMass: map[string]float64{},
			ClassChurn: map[string]int{}, ClassCommits: map[string]int{},
		}
	}

	return pairs[key]
}

// oldMass is a file's before-side function mass, zero when no before blob
// exists or it does not analyze.
func (s *session) oldMass(ctx context.Context, f vcs.CommitFile, blobs map[string][]byte) float64 {
	old, ok := blobs[f.OldSHA]
	if f.OldSHA == "" || !ok {
		return 0
	}
	rep := s.eng.Analyze(ctx, oldPathOf(f), f.OldSHA, old)
	if rep.Skipped != "" {
		return 0
	}

	return fileMass(rep.Metrics)
}

// scoreBlob folds one after-blob's report into the commit score, as a delta
// against the before-blob's mass.
func (s *session) scoreBlob(
	ctx context.Context, cs *commitScore, f vcs.CommitFile, content []byte, massBefore float64,
) {
	rep := s.eng.Analyze(ctx, f.Path, f.NewSHA, content)
	if rep.Skipped != "" {
		return
	}
	mass := fileMass(rep.Metrics)
	cs.liveMass[f.Path] = mass
	delta := mass - massBefore
	cs.wk.MassNet += delta
	pairCell(cs.pairs, cs.week, lineage.DirOf(f.Path)).ClassMass[cs.klass] += delta
	for _, fn := range rep.Findings {
		if cs.viral[fn.Rule] == nil {
			cs.viral[fn.Rule] = &viralRow{Rule: fn.Rule, FirstWeek: cs.week, HitsByClass: map[string]int{}}
		}
		cs.viral[fn.Rule].HitsByClass[cs.klass]++
		if _, ok := cs.ruleFirst[fn.Rule]; !ok {
			cs.ruleFirst[fn.Rule] = cs.week
		}
	}
}

// printHistory renders trends per directory with sparklines.
func printHistory(w io.Writer, res *historyResult) {
	report.Fprintf(w, "%d first-parent commits · %d reverts\n\n", res.Changes, res.Lineage.Reverts)

	if len(res.Weeks) > 0 {
		rows := make([][]string, 0, len(res.Weeks))
		for _, wk := range res.Weeks {
			rows = append(rows, []string{
				wk.Week, strconv.Itoa(wk.Commits), fmt.Sprintf("+%d", wk.Added),
				fmt.Sprintf("-%d", wk.Removed), fmt.Sprintf("%+.0f", wk.MassNet),
			})
		}
		report.Table(w, []string{"week", "commits", colAdded, "removed", "mass"}, rows)
	}

	if len(res.Lineage.Survival) > 0 {
		report.Fprintln(w)
		rows := make([][]string, 0, len(res.Lineage.Survival))
		for _, sv := range res.Lineage.Survival {
			rows = append(rows, []string{
				sv.Cohort, strconv.Itoa(sv.Added), strconv.Itoa(sv.Surviving),
				fmt.Sprintf("%.2f", sv.S30), fmt.Sprintf("%.2f", sv.S90),
			})
		}
		report.Table(w, []string{"cohort", colAdded, "alive", "s30", "s90"}, rows)
	}

	printTestSurvival(w, res)

	printHotspots(w, res)

	if len(res.Lineage.Dirs) > 0 {
		report.Fprintln(w)
		rows := make([][]string, 0, len(res.Lineage.Dirs))
		for _, d := range report.SortedKeys(res.Lineage.Dirs) {
			ds := res.Lineage.Dirs[d]
			rows = append(rows, []string{
				d, strconv.Itoa(ds.Added), strconv.Itoa(ds.Removed), strconv.Itoa(ds.Churn30),
				strconv.Itoa(ds.Rework), fmt.Sprintf("%.2f", res.Lineage.Concentration[d]),
			})
		}
		report.Table(w, []string{colDir, colAdded, "removed", "churn30", "rework", "concentr."}, rows)
	}

	if len(res.Viral) > 0 {
		report.Fprintln(w)
		rows := make([][]string, 0, len(res.Viral))
		for _, v := range res.Viral {
			parts := make([]string, 0, len(v.HitsByClass))
			for _, k := range report.SortedKeys(v.HitsByClass) {
				parts = append(parts, fmt.Sprintf("%s:%d", k, v.HitsByClass[k]))
			}
			rows = append(rows, []string{v.Rule, v.FirstWeek, strings.Join(parts, " ")})
		}
		report.Table(w, []string{"rule", "first hit", "spread by class"}, rows)
	}
}

// printTestSurvival renders the per-class test-file deletion table.
func printTestSurvival(w io.Writer, res *historyResult) {
	if len(res.Lineage.TestSurvival) == 0 {
		return
	}
	report.Fprintln(w)
	rows := make([][]string, 0, len(res.Lineage.TestSurvival))
	for _, ts := range res.Lineage.TestSurvival {
		rows = append(rows, []string{
			ts.Class, strconv.Itoa(ts.Added), strconv.Itoa(ts.Dead30), strconv.Itoa(ts.Dead90),
			fmt.Sprintf("%.2f", ts.S30), fmt.Sprintf("%.2f", ts.S90),
		})
	}
	report.Table(w, []string{"test files by class", "added", "dead<30d", "dead<90d", "s30", "s90"}, rows)
}

// printHotspots renders the churn-weighted directory mass table.
func printHotspots(w io.Writer, res *historyResult) {
	if len(res.Hotspots) == 0 {
		return
	}
	report.Fprintln(w)
	rows := make([][]string, 0, len(res.Hotspots))
	for _, d := range report.SortedKeys(res.Hotspots) {
		rows = append(rows, []string{d, fmt.Sprintf("%.0f", res.Hotspots[d])})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i][1] > rows[j][1] })
	report.Table(w, []string{"hotspot (churn30 × mass)", "score"}, rows)
}

package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kyleking/aragonite/vcs"

	"github.com/kyleking/slop-strata/internal/engine"
	"github.com/kyleking/slop-strata/internal/intent"
	"github.com/kyleking/slop-strata/internal/lineage"
	"github.com/kyleking/slop-strata/internal/report"
)

// daysPerWeek buckets author dates into Monday weeks.
const daysPerWeek = 7

// weekLayout formats a Monday week bucket.
const weekLayout = "2006-01-02"

// pairOfMiddles divides the two middle values of an even-length median.
const pairOfMiddles = 2

// sparkWeeks is the trailing window of the per-directory mass sparkline.
const sparkWeeks = 8

// provisionalMark follows a rate printed from a thin sample in text output.
const provisionalMark = "*"

// noiseMark follows a week whose net mass lies within the noise floor.
const noiseMark = "~"

// provisionalBelow is the commit count under which a pairing cell is too thin
// to compare.
const provisionalBelow = 5

// historyResult is the history subcommand's JSON shape.
type historyResult struct {
	Lineage    *lineage.Result    `json:"lineage"`
	Hotspots   map[string]float64 `json:"hotspots"`
	Range      string             `json:"range"`
	Weeks      []weekRow          `json:"weeks"`
	Viral      []viralRow         `json:"virality"`
	Pairs      []pairRow          `json:"pairing"`
	Changes    int                `json:"changes"`
	NoiseFloor float64            `json:"noise_floor"`
}

// weekRow is one week of first-parent activity.
type weekRow struct {
	Kinds       map[string]int `json:"kinds"`
	Week        string         `json:"week"`
	Commits     int            `json:"commits"`
	Added       int            `json:"added"`
	Removed     int            `json:"removed"`
	MassNet     float64        `json:"mass_net"`
	WithinNoise bool           `json:"within_noise,omitempty"`
}

// viralRow tracks one rule's spread after its first hit.
type viralRow struct {
	HitsByClass map[string]int `json:"hits_by_class"`
	dirs        map[string]bool
	Rule        string `json:"rule"`
	FirstWeek   string `json:"first_week"`
	SpreadDirs  int    `json:"spread_dirs"`
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

	res, gaps, err := s.walkHistory(ctx, rangeSpec, since)
	if err != nil {
		return err
	}

	if a.json {
		if err := report.WriteJSON(w, "history", s.root, res, gaps.counts(), nil); err != nil {
			return fmt.Errorf("writing result: %w", err)
		}

		return nil
	}

	printHistory(w, res)
	report.WarnCounts(s.stderr, gaps.counts())

	return nil
}

// A scoring window closes at windowCommits commits or windowBlobs changed files,
// so one parse batch and one cache transaction cover many commits.
const (
	windowCommits = 256
	windowBlobs   = 2048
)

// historyWalk accumulates one walk: the lineage walker, the week rows, the
// pairing and virality maps, and each path's latest live mass.
type historyWalk struct {
	walker    *lineage.Walker
	weeks     map[string]*weekRow
	pairs     map[string]*pairRow
	viral     map[string]*viralRow
	ruleFirst map[string]string
	liveMass  map[string]float64 // path -> mass of its latest after-blob
	gaps      *scoreGaps
	progress  *progress
	changes   int
}

// historyProgress starts the walk's progress line when stderr is a terminal, and
// a silent one otherwise.
func (s *session) historyProgress(ctx context.Context, opts vcs.LogOptions) (*progress, error) {
	if !isTerminal(s.stderr) {
		return &progress{}, nil
	}
	total, err := s.ops.CountCommits(ctx, s.root, opts)
	if err != nil {
		return nil, fmt.Errorf("counting commits: %w", err)
	}

	return newProgress(s.stderr, total), nil
}

// walkHistory walks the range oldest first, feeding each commit to the lineage
// walker, the week rows, and the blob scoring, and rolls the result up.
func (s *session) walkHistory(ctx context.Context, rangeSpec, since string) (*historyResult, *scoreGaps, error) {
	reader, err := s.ops.OpenBlobReader(ctx, s.root)
	if err != nil {
		return nil, nil, fmt.Errorf("reading blobs: %w", err)
	}
	opts := vcs.LogOptions{
		Range: rangeSpec, FirstParent: true, Reverse: true, Since: since, MaxDiffBytes: s.cfg.MaxFileBytes,
	}
	prog, err := s.historyProgress(ctx, opts)
	if err != nil {
		return nil, nil, errors.Join(err, reader.Close())
	}
	defer prog.clear()
	hw := &historyWalk{
		walker: lineage.NewWalker(s.cfg.Classify), weeks: map[string]*weekRow{}, pairs: map[string]*pairRow{},
		viral: map[string]*viralRow{}, ruleFirst: map[string]string{}, liveMass: map[string]float64{},
		gaps: newScoreGaps(), progress: prog,
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var window []vcs.CommitRecord
	files := 0
	for item := range s.streamLog(ctx, opts) {
		if item.err != nil {
			return nil, nil, errors.Join(fmt.Errorf("walking history: %w", item.err), reader.Close())
		}
		rec := item.rec
		window = append(window, rec)
		files += len(rec.Files)
		if len(window) < windowCommits && files < windowBlobs {
			continue
		}
		if err := s.walkWindow(ctx, hw, window, reader); err != nil {
			return nil, nil, errors.Join(err, reader.Close())
		}
		window, files = window[:0], 0
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, errors.Join(fmt.Errorf("walking history: %w", err), reader.Close())
	}
	if err := s.walkWindow(ctx, hw, window, reader); err != nil {
		return nil, nil, errors.Join(err, reader.Close())
	}
	if err := reader.Close(); err != nil {
		return nil, nil, fmt.Errorf("reading blobs: %w", err)
	}

	res := &historyResult{Range: rangeSpec, Lineage: hw.walker.Finish(), Changes: hw.changes}
	collectRollups(res, hw.weeks, hw.pairs, hw.viral)
	markNoise(res)
	res.Hotspots = hotspotScores(res.Lineage, hw.liveMass)

	return res, hw.gaps, nil
}

// logItem is one step of a log walk: a commit, or the error that ended it.
type logItem struct {
	err error
	rec vcs.CommitRecord
}

// streamLog runs a log walk on its own goroutine, a window ahead of the caller,
// so git keeps writing while the caller scores. It stops when ctx is canceled.
func (s *session) streamLog(ctx context.Context, opts vcs.LogOptions) <-chan logItem {
	out := make(chan logItem, windowCommits)
	go func() {
		defer close(out)
		for rec, err := range s.ops.LogHistory(ctx, s.root, opts) {
			select {
			case out <- logItem{rec: rec, err: err}:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

// walkWindow scores a window's blobs, then applies its commits in order.
func (s *session) walkWindow(ctx context.Context, hw *historyWalk, window []vcs.CommitRecord,
	reader *vcs.BlobReader,
) error {
	reports, err := s.windowReports(ctx, window, reader)
	if err != nil {
		return err
	}
	for i := range window {
		s.walkCommit(hw, &window[i], reports)
	}

	return nil
}

// walkCommit applies one commit to the lineage walker, its week row, and the
// blob scoring.
func (s *session) walkCommit(hw *historyWalk, rec *vcs.CommitRecord, reports map[engine.Blob]*engine.FileReport) {
	hw.changes++
	hw.progress.step()
	in := intent.Of(rec.Subject, "", rec.Body)
	klass := s.cfg.Classify(rec.AuthorName+" <"+rec.AuthorEmail+">",
		rec.CommitterName+" <"+rec.CommitterEmail+">", rec.Body)
	hw.walker.Commit(*rec, in.Kind == intent.Fix, in.Revert)
	week := mondayOf(rec.AuthorTime)
	wk := weekCell(hw.weeks, week)
	wk.Commits++
	wk.Kinds[string(in.Kind)]++
	for i := range rec.Files {
		wk.Added += rec.Files[i].LinesAdded()
		wk.Removed += rec.Files[i].LinesDeleted()
	}
	scoreCommit(rec, &commitScore{
		wk: wk, pairs: hw.pairs, viral: hw.viral, ruleFirst: hw.ruleFirst, reports: reports,
		liveMass: hw.liveMass, klass: klass, week: week, gaps: hw.gaps,
	})
}

// mondayOf names the Monday week a commit's author date falls in.
func mondayOf(t time.Time) string {
	return t.AddDate(0, 0, -int((t.Weekday()+daysPerWeek-1)%daysPerWeek)).Format(weekLayout)
}

// weekCell returns a week's row, creating it on first use.
func weekCell(weeks map[string]*weekRow, week string) *weekRow {
	if weeks[week] == nil {
		weeks[week] = &weekRow{Week: week, Kinds: map[string]int{}}
	}

	return weeks[week]
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

// massSeries answers each directory's net mass per week over the last sparkWeeks
// weeks of the range, oldest first. A directory with no change in that window has
// no entry.
func massSeries(res *historyResult) map[string][]float64 {
	if len(res.Weeks) == 0 {
		return nil
	}
	last, err := time.Parse(weekLayout, res.Weeks[len(res.Weeks)-1].Week)
	if err != nil {
		return nil
	}
	index := map[string]int{}
	for i := range sparkWeeks {
		index[last.AddDate(0, 0, -daysPerWeek*(sparkWeeks-1-i)).Format(weekLayout)] = i
	}
	out := map[string][]float64{}
	for _, p := range res.Pairs {
		i, ok := index[p.Week]
		if !ok {
			continue
		}
		if out[p.Dir] == nil {
			out[p.Dir] = make([]float64, sparkWeeks)
		}
		for _, m := range p.ClassMass {
			out[p.Dir][i] += m
		}
	}

	return out
}

// markNoise sets the noise floor to the median absolute deviation of weekly net
// mass, and flags each week within one floor of the median week.
func markNoise(res *historyResult) {
	if len(res.Weeks) == 0 {
		return
	}
	mass := make([]float64, len(res.Weeks))
	for i := range res.Weeks {
		mass[i] = res.Weeks[i].MassNet
	}
	center := median(mass)
	dev := make([]float64, len(mass))
	for i, m := range mass {
		dev[i] = math.Abs(m - center)
	}
	res.NoiseFloor = median(dev)
	for i := range res.Weeks {
		res.Weeks[i].WithinNoise = dev[i] <= res.NoiseFloor
	}
}

// median answers the middle of values, the mean of the two middle values when
// there is an even count, and 0 when empty.
func median(values []float64) float64 {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	n := len(sorted)
	switch {
	case n == 0:
		return 0
	case n%2 == 1:
		return sorted[n/2]
	default:
		return (sorted[n/2-1] + sorted[n/2]) / pairOfMiddles
	}
}

// hotspotScores is churn × live complexity mass per directory: directories
// where young code dies fast and what's there is heavy. Live mass covers
// files touched in the range, the same files churn is counted on.
func hotspotScores(lin *lineage.Result, liveMass map[string]float64) map[string]float64 {
	dirMass := map[string]float64{}
	for _, path := range report.SortedKeys(liveMass) {
		dirMass[lineage.DirOf(path)] += liveMass[path]
	}
	out := map[string]float64{}
	for d, ds := range lin.Dirs {
		if ds.Churn30 > 0 && dirMass[d] > 0 {
			out[d] = float64(ds.Churn30) * dirMass[d]
		}
	}

	return out
}

// commitScore is what one commit's scoring reads, its window's reports, and the
// week row, pairing, virality, and live mass it feeds.
type commitScore struct {
	wk        *weekRow
	pairs     map[string]*pairRow
	viral     map[string]*viralRow
	ruleFirst map[string]string
	liveMass  map[string]float64
	gaps      *scoreGaps
	reports   map[engine.Blob]*engine.FileReport
	klass     string
	week      string
}

// scoreGaps records the distinct blobs a walk could not score, keyed by path and
// blob id, so a blob that several commits touch counts once.
type scoreGaps struct {
	skipped map[string]bool
	errors  map[string]bool
}

func newScoreGaps() *scoreGaps {
	return &scoreGaps{skipped: map[string]bool{}, errors: map[string]bool{}}
}

func (g *scoreGaps) note(path, sha string, rep *engine.FileReport) {
	key := path + "\x00" + sha
	switch {
	case rep.Skipped != "" && rep.Skipped != engine.ReasonUnsupported:
		g.skipped[key] = true
	case rep.Metrics.ParseError:
		g.errors[key] = true
	}
}

func (g *scoreGaps) counts() report.Counts {
	return report.Counts{Errors: len(g.errors), Skipped: len(g.skipped)}
}

// scoreCommit folds both sides of a commit's changed blobs into mass delta,
// findings, and the per-class pairing. A side with no report does not exist.
func scoreCommit(rec *vcs.CommitRecord, cs *commitScore) {
	for i := range rec.Files {
		f := &rec.Files[i]
		d := lineage.DirOf(f.Path)
		if f.Status == vcs.StatusRenamed && f.OldPath != "" {
			cs.liveMass[f.Path] = cs.liveMass[f.OldPath]
			delete(cs.liveMass, f.OldPath)
		}
		pairCell(cs.pairs, cs.week, d).ClassCommits[cs.klass]++

		rep, ok := cs.reports[engine.Blob{Path: f.Path, SHA: f.NewSHA}]
		if !ok {
			delete(cs.liveMass, f.Path)

			continue
		}
		scoreBlob(cs, *f, rep, oldMass(cs, *f))
	}
}

// windowReports answers the reports a window's commits score, reading only the
// blobs the path and the cache cannot answer. A blob git lacks gets none.
func (s *session) windowReports(ctx context.Context, window []vcs.CommitRecord, reader *vcs.BlobReader,
) (map[engine.Blob]*engine.FileReport, error) {
	var asks []engine.Blob
	for c := range window {
		for i := range window[c].Files {
			f := &window[c].Files[i]
			if !isBlobSHA(f.NewSHA) {
				continue
			}
			asks = append(asks, engine.Blob{Path: f.Path, SHA: f.NewSHA})
			if isBlobSHA(f.OldSHA) {
				asks = append(asks, engine.Blob{Path: oldPathOf(*f), SHA: f.OldSHA})
			}
		}
	}
	reports := s.eng.Lookup(ctx, asks)
	var shas []string
	for _, b := range asks {
		if _, ok := reports[b]; !ok {
			shas = append(shas, b.SHA)
		}
	}
	content, err := reader.Read(shas)
	if err != nil {
		return nil, fmt.Errorf("reading blobs: %w", err)
	}
	var reqs []engine.Request
	for _, b := range asks {
		if c, ok := content[b.SHA]; ok {
			if _, done := reports[b]; !done {
				reqs = append(reqs, engine.Request{Path: b.Path, SHA: b.SHA, Content: c})
			}
		}
	}
	maps.Copy(reports, s.eng.AnalyzeAll(ctx, reqs))

	return reports, nil
}

// isBlobSHA reports whether a diff side names a blob, as opposed to the empty
// or all-zero id of a side that does not exist.
func isBlobSHA(sha string) bool {
	return strings.Trim(sha, "0") != ""
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
func oldMass(cs *commitScore, f vcs.CommitFile) float64 {
	rep, ok := cs.reports[engine.Blob{Path: oldPathOf(f), SHA: f.OldSHA}]
	if !ok {
		return 0
	}
	cs.gaps.note(oldPathOf(f), f.OldSHA, rep)
	if rep.Skipped != "" {
		return 0
	}

	return fileMass(rep.Metrics)
}

// scoreBlob folds one after-blob's report into the commit score, as a delta
// against the before-blob's mass.
func scoreBlob(cs *commitScore, f vcs.CommitFile, rep *engine.FileReport, massBefore float64) {
	cs.gaps.note(f.Path, f.NewSHA, rep)
	if rep.Skipped != "" {
		return
	}
	mass := fileMass(rep.Metrics)
	cs.liveMass[f.Path] = mass
	delta := mass - massBefore
	cs.wk.MassNet += delta
	pairCell(cs.pairs, cs.week, lineage.DirOf(f.Path)).ClassMass[cs.klass] += delta
	for _, fn := range rep.Findings {
		v := cs.viral[fn.Rule]
		if v == nil {
			v = &viralRow{Rule: fn.Rule, FirstWeek: cs.week, HitsByClass: map[string]int{}, dirs: map[string]bool{}}
			cs.viral[fn.Rule] = v
		}
		v.HitsByClass[cs.klass]++
		v.dirs[lineage.DirOf(f.Path)] = true
		v.SpreadDirs = len(v.dirs)
		if _, ok := cs.ruleFirst[fn.Rule]; !ok {
			cs.ruleFirst[fn.Rule] = cs.week
		}
	}
}

// printHistory renders trends per directory with sparklines.
func printHistory(w io.Writer, res *historyResult) {
	report.Fprintf(w, "%d first-parent commits · %d reverts\n\n", res.Changes, res.Lineage.Reverts)

	printWeeks(w, res)

	if len(res.Lineage.Survival) > 0 {
		report.Fprintln(w)
		rows := make([][]string, 0, len(res.Lineage.Survival))
		for _, sv := range res.Lineage.Survival {
			rows = append(rows, []string{
				sv.Cohort, strconv.Itoa(sv.Added), strconv.Itoa(sv.Surviving),
				rateCell(sv.S30, sv.S30CI, sv.Provisional), rateCell(sv.S90, sv.S90CI, sv.Provisional),
			})
		}
		report.Table(w, []string{"cohort", colAdded, "alive", "s_30d", "s_90d"}, rows)
		provisionalNote(w, res.Lineage.Survival, func(s lineage.Survival) bool {
			return s.Provisional && (s.S30 != nil || s.S90 != nil)
		}, lineage.ProvisionalLines)
	}

	printTestSurvival(w, res)

	printHotspots(w, res)

	if len(res.Lineage.Dirs) > 0 {
		report.Fprintln(w)
		series := massSeries(res)
		rows := make([][]string, 0, len(res.Lineage.Dirs))
		for _, d := range report.SortedKeys(res.Lineage.Dirs) {
			ds := res.Lineage.Dirs[d]
			rows = append(rows, []string{
				d, strconv.Itoa(ds.Added), strconv.Itoa(ds.Removed), strconv.Itoa(ds.Churn14),
				strconv.Itoa(ds.Churn30), strconv.Itoa(ds.Rework),
				rateCell(res.Lineage.Concentration[d], nil, false), report.Sparkline(series[d]),
			})
		}
		report.Table(w, []string{
			colDir, colAdded, "removed", "churn_14d", "churn_30d", "rework",
			"concentration", "mass_8w",
		}, rows)
	}

	printDeaths(w, res)

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

// printDeaths renders how young lines died: shares of the churn_30d lines per
// directory with enough of them, plus an overall row. The self column drops out
// when the window has one author, since it would read 1.00 everywhere.
func printDeaths(w io.Writer, res *historyResult) {
	lin := res.Lineage
	if lin.Deaths30.Total == 0 {
		return
	}
	showSelf := lin.Authors > 1
	header := []string{colDir, "deaths_30d"}
	if showSelf {
		header = append(header, "self")
	}
	header = append(header, "pure", "whole_file", "within_7d")
	rows := [][]string{deathRow("(all)", lin.Deaths30, showSelf)}
	hidden := 0
	for _, d := range report.SortedKeys(lin.Dirs) {
		switch dd := lin.Dirs[d].Deaths30; {
		case dd.Total >= lineage.ProvisionalLines:
			rows = append(rows, deathRow(d, dd, showSelf))
		case dd.Total > 0:
			hidden++
		}
	}
	report.Fprintln(w)
	report.Table(w, header, rows)
	if lin.Deaths30.Total < lineage.ProvisionalLines {
		report.Fprintf(w, "%s fewer than %d samples\n", provisionalMark, lineage.ProvisionalLines)
	}
	if hidden > 0 {
		report.Fprintf(w, "%d directories with fewer than %d young deaths not listed\n",
			hidden, lineage.ProvisionalLines)
	}
}

func deathRow(label string, d lineage.Deaths, showSelf bool) []string {
	provisional := d.Total < lineage.ProvisionalLines
	share := func(n int) string {
		return provisionalMarked(fmt.Sprintf("%.2f", float64(n)/float64(d.Total)), provisional)
	}
	row := []string{label, strconv.Itoa(d.Total)}
	if showSelf {
		row = append(row, share(d.Self))
	}

	return append(row, share(d.Pure), share(d.WholeFile), share(d.AgeDay1+d.AgeWeek))
}

// printWeeks renders the first-parent week table with its noise footnote.
func printWeeks(w io.Writer, res *historyResult) {
	if len(res.Weeks) > 0 {
		rows := make([][]string, 0, len(res.Weeks))
		noisy := false
		for _, wk := range res.Weeks {
			mass := fmt.Sprintf("%+.0f", wk.MassNet)
			if wk.WithinNoise {
				mass += noiseMark
				noisy = true
			}
			rows = append(rows, []string{
				wk.Week, strconv.Itoa(wk.Commits), fmt.Sprintf("+%d", wk.Added),
				fmt.Sprintf("-%d", wk.Removed), mass,
			})
		}
		report.Table(w, []string{"week", "commits", colAdded, "removed", colMass}, rows)
		if noisy {
			report.Fprintf(w, "%s within one noise floor (%.0f) of the median week\n", noiseMark, res.NoiseFloor)
		}
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
			rateCell(ts.S30, nil, ts.Provisional), rateCell(ts.S90, nil, ts.Provisional),
		})
	}
	report.Table(w, []string{"test files by class", "added", "dead<30d", "dead<90d", "s_30d", "s_90d"}, rows)
	provisionalNote(w, res.Lineage.TestSurvival, func(t lineage.TestSurvival) bool {
		return t.Provisional && (t.S30 != nil || t.S90 != nil)
	}, lineage.ProvisionalTests)
}

// rateCell formats a survival share with its Greenwood interval when one is
// given, or an em dash when the horizon runs past the data.
func rateCell(share *float64, ci *[2]float64, provisional bool) string {
	if share == nil {
		return "—"
	}
	cell := fmt.Sprintf("%.2f", *share)
	if ci != nil {
		cell += fmt.Sprintf(" [%.2f, %.2f]", ci[0], ci[1])
	}

	return provisionalMarked(cell, provisional)
}

// provisionalMarked appends the provisional mark to a formatted rate.
func provisionalMarked(cell string, provisional bool) string {
	if provisional {
		return cell + provisionalMark
	}

	return cell
}

// provisionalNote prints the footnote a table's marks refer to, once, when any
// of its rows is provisional.
func provisionalNote[T any](w io.Writer, rows []T, provisional func(T) bool, samples int) {
	for _, r := range rows {
		if provisional(r) {
			report.Fprintf(w, "%s fewer than %d samples\n", provisionalMark, samples)

			return
		}
	}
}

// printHotspots renders the churn-weighted directory mass table.
func printHotspots(w io.Writer, res *historyResult) {
	if len(res.Hotspots) == 0 {
		return
	}
	report.Fprintln(w)
	dirs := report.SortedKeys(res.Hotspots)
	slices.SortStableFunc(dirs, func(a, b string) int {
		return cmp.Or(cmp.Compare(res.Hotspots[b], res.Hotspots[a]), strings.Compare(a, b))
	})
	rows := make([][]string, 0, len(dirs))
	for _, d := range dirs {
		rows = append(rows, []string{d, fmt.Sprintf("%.0f", res.Hotspots[d])})
	}
	report.Table(w, []string{"hotspot", "score"}, rows)
}

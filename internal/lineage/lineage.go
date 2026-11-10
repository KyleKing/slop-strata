// Package lineage walks a commit range oldest-first and tracks which cohort
// every live line belongs to, so survival, churn, rework, hotspots, coupling,
// and concentration all come off one pass over the diffs.
package lineage

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/kyleking/aragonite/diff"
	"github.com/kyleking/aragonite/vcs"

	"github.com/kyleking/slop-strata/internal/lang"
)

const (
	// Months in a year, the unit cohortOf buckets by.
	monthsPerYear = 12
	// Line ages Churn14 and Churn30 count, in days.
	churn14Days = 14
	churn30Days = 30
	// Code-maat's "big commit" cut past which a commit is skipped for
	// co-change counting; minPairFiles is the smallest commit that can pair.
	bigCommit    = 30
	minPairFiles = 2
	// Co-change count at which a pair is reported.
	minCouples = 3
	// Survival marks S30 and S90 read, in days.
	horizon30Days = 30
	horizon90Days = 90
	// Lines that must reach a survival horizon before its estimate is reported;
	// fewer leave the curve resting on a handful of lines.
	minAtRisk = 30
	// Two-sided 95% normal quantile for the Greenwood interval.
	zNormal95 = 1.959963984540054
)

// Sample counts under which a survival cohort or a test class is marked
// provisional: its rate rests on too few lines or test files to read.
const (
	ProvisionalLines = 30
	ProvisionalTests = 5
)

// tag is one live line's provenance.
type tag struct {
	class  string
	author string
	dir    string // directory the line was added in, which its churn is credited to
	// birthDays is the unix day the line was added, and cohort its birth month as year*12+month.
	birthDays int
	cohort    int
}

// fileLines is a file as the walk sees it: one tag per current line.
type fileLines []tag

// cohortOf buckets a date by month so survival curves line up by age.
func cohortOf(t time.Time) int {
	t = t.UTC()

	return t.Year()*12 + int(t.Month())
}

func cohortLabel(c int) string {
	return fmt.Sprintf("%04d-%02d", c/monthsPerYear, c%monthsPerYear)
}

func days(t time.Time) int {
	return int(t.UTC().Unix() / 86400) //nolint:mnd // seconds per day
}

// ageDays is the days from a birth to a death or a walk's end. Committer dates
// can run backwards across a rebase, so a negative span reads as age zero.
func ageDays(birthDay, endDay int) int {
	return max(endDay-birthDay, 0)
}

// death is a removed line's record: when it died and how old it was.
type death struct {
	klass  string
	cohort int
	age    int
	fix    bool
}

// Survival is one cohort's Kaplan-Meier summary over the walked range. Cohort
// months are the pinned unit: at-risk is approximated at month granularity,
// which the cohort docs should name before quoting.
type Survival struct {
	S30         *float64    `json:"s_30d"`
	S90         *float64    `json:"s_90d"`
	S30CI       *[2]float64 `json:"s30_ci,omitempty"`
	S90CI       *[2]float64 `json:"s90_ci,omitempty"`
	Cohort      string      `json:"cohort"`
	Added       int         `json:"added"`
	Surviving   int         `json:"surviving"`
	Provisional bool        `json:"provisional,omitempty"`
}

// DirStats is a directory's aggregate over the range.
type DirStats struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
	Churn14 int `json:"churn_14d"` // added lines killed within 14 days
	Churn30 int `json:"churn_30d"` // within 30
	Rework  int `json:"rework"`    // young lines killed by fix commits
	Touches int `json:"touches"`
}

// TestSurvival is one author class's share of test files deleted within 30
// and 90 days of being added. Only adds observed inside the walked range
// count; a test file older than the range has no measurable birth.
type TestSurvival struct {
	S30         *float64 `json:"s_30d"`
	S90         *float64 `json:"s_90d"`
	Class       string   `json:"class"`
	Added       int      `json:"added"`
	Dead30      int      `json:"dead_30d"`
	Dead90      int      `json:"dead_90d"`
	Provisional bool     `json:"provisional,omitempty"`
}

// Result is the lineage walk's output.
type Result struct {
	Dirs          map[string]*DirStats `json:"dirs"`
	Concentration map[string]*float64  `json:"concentration"`
	Survival      []Survival           `json:"survival"`
	TestSurvival  []TestSurvival       `json:"test_survival"`
	Coupling      [][2]string          `json:"coupling"`
	Reverts       int                  `json:"reverts"`
	Commits       int                  `json:"commits"`
	Added30       int                  `json:"added_30d_window"`
	Churned30     int                  `json:"churned_30d"`
}

// testBirth is an observed test-file addition.
type testBirth struct {
	klass string
	day   int
}

// testDeath records a tracked test file's deletion age.
type testDeath struct {
	klass string
	age   int
}

// Walker carries the mutable state of one pass.
type Walker struct {
	files      map[string]fileLines
	births     map[int]int
	class      func(author, committer, body string) string
	couples    map[[2]string]int
	testBirths map[string]testBirth
	testDeaths []testDeath
	res        *Result
	deaths     []death
	lastDay    int
}

// NewWalker builds a Walker; class classifies a commit's author class.
func NewWalker(class func(author, committer, body string) string) *Walker {
	return &Walker{
		files:      map[string]fileLines{},
		births:     map[int]int{},
		class:      class,
		couples:    map[[2]string]int{},
		testBirths: map[string]testBirth{},
		res:        &Result{Dirs: map[string]*DirStats{}},
	}
}

// DirOf names a file's directory bucket.
func DirOf(path string) string {
	if i := strings.LastIndex(path, "/"); i > 0 {
		return path[:i]
	}

	return "."
}

// moves carries one commit's moved-line accounting: the tag each added line
// takes from the removed line it moved from, and the old positions whose
// removal is a move rather than a death.
type moves struct {
	tags   map[addedLine]tag
	exempt map[string]map[int]bool
}

// addedLine names an added line by its file and its index in that file's Added list.
type addedLine struct {
	path string
	idx  int
}

// moveTextFloor is the fewest trimmed bytes a line needs before a text match
// reads as a move; shorter lines like braces match by coincidence.
const moveTextFloor = 8

// removedRun is where one hunk's removed lines start: the old-file position of
// the first line, and the path its tags are stored under before the commit.
type removedRun struct {
	path, tagPath string
	pos           int
}

// addedRun is where one hunk's added lines start in the file's Added list.
type addedRun struct {
	path string
	idx  int
}

// collectMoves matches the commit's removed and added lines, one hunk at a time,
// with diff.MatchMoves. A removed line matched to an added line keeps its tag:
// it moves rather than dying and being reborn as new code.
func (w *Walker) collectMoves(rec vcs.CommitRecord) *moves {
	var removed, added [][]string
	var fromRemoved []removedRun
	var toAdded []addedRun

	for i := range rec.Files {
		f := &rec.Files[i]
		tagPath := f.Path
		if f.Status == vcs.StatusRenamed && f.OldPath != "" {
			tagPath = f.OldPath
		}

		removedAt, addedAt := 0, 0
		for _, h := range f.Hunks {
			if lines := hunkSlice(f.Removed, removedAt, h.OldLines); len(lines) > 0 {
				removed = append(removed, lines)
				fromRemoved = append(fromRemoved, removedRun{f.Path, tagPath, h.OldStart - 1})
			}

			if lines := hunkSlice(f.Added, addedAt, h.NewLines); len(lines) > 0 {
				added = append(added, lines)
				toAdded = append(toAdded, addedRun{f.Path, addedAt})
			}

			removedAt += h.OldLines
			addedAt += h.NewLines
		}
	}

	mv := &moves{tags: map[addedLine]tag{}, exempt: map[string]map[int]bool{}}
	pairs := diff.MatchMoves(removed, added, diff.MoveOptions{MinRun: 1, TextFloor: moveTextFloor})

	for r, a := range pairs {
		from, to := fromRemoved[r.Seq], toAdded[a.Seq]
		pos := from.pos + r.Line
		lines := w.files[from.tagPath]
		if pos < 0 || pos >= len(lines) {
			continue
		}

		mv.tags[addedLine{to.path, to.idx + a.Line}] = lines[pos]
		if mv.exempt[from.path] == nil {
			mv.exempt[from.path] = map[int]bool{}
		}
		mv.exempt[from.path][pos] = true
	}

	return mv
}

// hunkSlice returns the n lines from index from, clipped to the end of the list.
func hunkSlice(all []string, from, n int) []string {
	if from >= len(all) {
		return nil
	}

	return all[from:min(from+n, len(all))]
}

// Commit applies one commit's record to the line state.
func (w *Walker) Commit(rec vcs.CommitRecord, fixIntent, revert bool) {
	w.res.Commits++
	if revert {
		w.res.Reverts++
	}
	klass := w.class(rec.AuthorName+" <"+rec.AuthorEmail+">", rec.CommitterName+" <"+rec.CommitterEmail+">", rec.Body)
	cohort := cohortOf(rec.CommitTime)
	birth := days(rec.CommitTime)
	if birth > w.lastDay {
		w.lastDay = birth
	}
	mv := w.collectMoves(rec)

	touched := map[string]bool{}
	for i := range rec.Files {
		f := &rec.Files[i]
		d := DirOf(f.Path)
		if w.res.Dirs[d] == nil {
			w.res.Dirs[d] = &DirStats{}
		}
		ds := w.res.Dirs[d]
		ds.Touches++
		touched[f.Path] = true

		switch f.Status {
		case vcs.StatusAdded:
			w.addFile(f, klass, cohort, birth, rec, ds, mv)
		case vcs.StatusDeleted:
			w.removeFile(f.Path, birth, ds, rec, fixIntent, mv)
		default:
			if f.Status == vcs.StatusRenamed && f.OldPath != "" {
				w.renameFile(f)
			}
			w.applyHunks(f, cohort, birth, klass, rec, ds, fixIntent, mv)
		}
	}
	w.countCouples(touched)
}

// addFile records a whole new file's lines as births at this commit, keeping
// any tags the commit moved in from elsewhere.
func (w *Walker) addFile(f *vcs.CommitFile, klass string, cohort, birth int,
	rec vcs.CommitRecord, ds *DirStats, mv *moves,
) {
	n := f.LinesAdded()
	lines := make(fileLines, 0, n)
	births := 0
	for i := range n {
		t := tag{class: klass, author: rec.AuthorEmail, birthDays: birth, cohort: cohort, dir: DirOf(f.Path)}
		kept := false
		if t2, ok := mv.take(f.Path, i); ok {
			t = t2
			kept = true
		}
		if !kept {
			births++
		}
		lines = append(lines, t)
	}
	w.files[f.Path] = lines
	w.births[cohort] += births
	ds.Added += n
	if lang.IsTestPath(f.Path) {
		w.testBirths[f.Path] = testBirth{day: birth, klass: klass}
	}
}

// removeFile records a deletion: a test death when the file was being
// tracked, plus every live line killed.
func (w *Walker) removeFile(path string, birth int, ds *DirStats, rec vcs.CommitRecord, fix bool, mv *moves) {
	if tb, ok := w.testBirths[path]; ok {
		w.testDeaths = append(w.testDeaths, testDeath{age: ageDays(tb.day, birth), klass: tb.klass})
		delete(w.testBirths, path)
	}
	w.killAll(path, ds, rec, birth, fix, mv)
}

// renameFile moves a file's line tags and test-birth record to its new path.
func (w *Walker) renameFile(f *vcs.CommitFile) {
	w.files[f.Path] = w.files[f.OldPath]
	delete(w.files, f.OldPath)
	if tb, ok := w.testBirths[f.OldPath]; ok {
		w.testBirths[f.Path] = tb
		delete(w.testBirths, f.OldPath)
	}
}

// take returns the tag a moved added line brings, if its removed line was matched.
func (mv *moves) take(path string, idx int) (tag, bool) {
	t, ok := mv.tags[addedLine{path, idx}]

	return t, ok
}

// killAll records every live line of a deleted file as a death.
func (w *Walker) killAll(path string, ds *DirStats, _ vcs.CommitRecord, deathDay int, fix bool, mv *moves) {
	for i, t := range w.files[path] {
		if !mv.exempt[path][i] {
			w.recordDeath(t, ds, deathDay, fix)
		}
	}
	delete(w.files, path)
}

// hunkEdit pairs a hunk with where its added lines start in the file's Added
// list, before the hunks get applied last-first.
type hunkEdit struct {
	path  string
	first int
	h     vcs.DiffHunk
}

// applyHunks rewrites a file's line tags through its hunks, last hunk first so
// earlier offsets stay valid.
func (w *Walker) applyHunks(f *vcs.CommitFile, cohort, birth int, klass string,
	rec vcs.CommitRecord, ds *DirStats, fix bool, mv *moves,
) {
	lines := w.files[f.Path]
	edits := make([]hunkEdit, len(f.Hunks))
	cur := 0
	for i, h := range f.Hunks {
		edits[i] = hunkEdit{f.Path, cur, h}
		cur += h.NewLines
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].h.OldStart > edits[j].h.OldStart })
	exempt := mv.exempt[f.Path]
	for _, edit := range edits {
		h := edit.h
		idx := max(h.OldStart-1, 0)
		lines = w.killRange(lines, idx, h.OldLines, exempt, ds, birth, fix)
		mid, births := insertTags(edit, cohort, birth, klass, rec, mv)
		out := make(fileLines, 0, len(lines)-h.OldLines+h.NewLines)
		out = append(out, lines[:idx]...)
		out = append(out, mid...)
		out = append(out, lines[min(idx+h.OldLines, len(lines)):]...)
		lines = out
		ds.Added += h.NewLines
		w.births[cohort] += births
	}
	w.files[f.Path] = lines
}

// killRange records deaths over [idx, idx+oldLines), padding pre-range files
// whose true state the walk never saw. It answers the padded line state.
func (w *Walker) killRange(lines fileLines, idx, oldLines int, exempt map[int]bool,
	ds *DirStats, birth int, fix bool,
) fileLines {
	if need := idx + oldLines; need > len(lines) {
		// The file predates the walked range, so its line provenance is
		// unknown: grow the state with pre-range tags. Their deaths count
		// as removed lines, but a negative birth keeps them out of the
		// young-churn windows, and a negative cohort out of survival.
		pad := make(fileLines, need-len(lines))
		for i := range pad {
			pad[i] = tag{birthDays: -1, cohort: -1, class: "pre-range"}
		}
		lines = append(lines, pad...)
	}
	end := min(idx+oldLines, len(lines))
	for p, t := range lines[idx:end] {
		if !exempt[idx+p] {
			w.recordDeath(t, ds, birth, fix)
		}
	}

	return lines
}

// insertTags builds the line tags a hunk adds: a moved line claims its stashed
// tag, anything else is a birth at this commit. It answers the tags and the
// true birth count for the cohort tally.
func insertTags(edit hunkEdit, cohort, birth int, klass string,
	rec vcs.CommitRecord, mv *moves,
) (fileLines, int) {
	mid := make(fileLines, edit.h.NewLines)
	births := 0
	for i := range mid {
		mid[i] = tag{class: klass, author: rec.AuthorEmail, birthDays: birth, cohort: cohort, dir: DirOf(edit.path)}
		if kept, ok := mv.take(edit.path, edit.first+i); ok {
			mid[i] = kept

			continue
		}
		births++
	}

	return mid, births
}

// recordDeath logs one line's removal with its provenance. Churn goes to the
// directory the line was added in, so it never exceeds that directory's added
// lines, while removal and rework stay with the directory it died in.
func (w *Walker) recordDeath(t tag, ds *DirStats, deathDay int, fix bool) {
	ds.Removed++
	age := ageDays(t.birthDays, deathDay)
	w.deaths = append(w.deaths, death{cohort: t.cohort, age: age, klass: t.class, fix: fix})
	if age <= churn30Days {
		born := w.dirStats(t.dir)
		if age <= churn14Days {
			born.Churn14++
		}
		born.Churn30++
		w.res.Churned30++
	}
	if fix && age <= churn30Days {
		ds.Rework++
	}
}

func (w *Walker) dirStats(d string) *DirStats {
	if w.res.Dirs[d] == nil {
		w.res.Dirs[d] = &DirStats{}
	}

	return w.res.Dirs[d]
}

// countCouples counts same-commit file pairs, skipping very large commits.
func (w *Walker) countCouples(touched map[string]bool) {
	if len(touched) > bigCommit || len(touched) < minPairFiles {
		return
	}
	paths := make([]string, 0, len(touched))
	for p := range touched {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for i := range paths {
		for j := i + 1; j < len(paths); j++ {
			w.couples[[2]string{paths[i], paths[j]}]++
		}
	}
}

// Finish rolls the walk up into a Result.
func (w *Walker) Finish() *Result {
	res := w.res
	res.Survival = w.survival()
	res.TestSurvival = w.testSurvival()
	res.Concentration = w.concentration()
	res.Coupling = w.coupling()
	res.Added30 = w.totalAdded()

	return res
}

// obs is one line's outcome for the survival estimate: the age it died at, or
// the exposure it had when the walk ended (censored).
type obs struct {
	age  int
	dead bool
}

// survival computes the Kaplan-Meier per cohort: events are line deaths at an
// age in days, and lines still alive at the walk's end are censored at their
// exposure, so a line born recently is at-risk only for the days it lived.
func (w *Walker) survival() []Survival {
	obsByCohort := map[int][]obs{}
	for _, d := range w.deaths {
		obsByCohort[d.cohort] = append(obsByCohort[d.cohort], obs{age: d.age, dead: true})
	}
	survivingByCohort := map[int]int{}
	for _, lines := range w.files {
		for _, t := range lines {
			survivingByCohort[t.cohort]++
			if t.cohort >= 0 {
				obsByCohort[t.cohort] = append(obsByCohort[t.cohort], obs{age: ageDays(t.birthDays, w.lastDay)})
			}
		}
	}
	cohorts := make([]int, 0, len(w.births))
	for c := range w.births {
		cohorts = append(cohorts, c)
	}
	sort.Ints(cohorts)

	out := make([]Survival, 0, len(cohorts))
	for _, c := range cohorts {
		s := Survival{Cohort: cohortLabel(c), Added: w.births[c], Surviving: survivingByCohort[c]}
		ages := obsByCohort[c]
		sort.Slice(ages, func(i, j int) bool { return ages[i].age < ages[j].age })
		s.S30, s.S30CI = km(ages, horizon30Days)
		s.S90, s.S90CI = km(ages, horizon90Days)
		s.Provisional = s.Added < ProvisionalLines
		out = append(out, s)
	}

	return out
}

// testSurvival scores each author class's test files against the 30- and
// 90-day horizons. Deaths also count toward the denominator of what was added.
func (w *Walker) testSurvival() []TestSurvival {
	byClass := map[string]*TestSurvival{}
	obsByClass := map[string][]obs{}
	for _, tb := range w.testBirths {
		if byClass[tb.klass] == nil {
			byClass[tb.klass] = &TestSurvival{Class: tb.klass}
		}
		byClass[tb.klass].Added++
		obsByClass[tb.klass] = append(obsByClass[tb.klass], obs{age: ageDays(tb.day, w.lastDay)})
	}
	for _, td := range w.testDeaths {
		if byClass[td.klass] == nil {
			byClass[td.klass] = &TestSurvival{Class: td.klass}
		}
		ts := byClass[td.klass]
		if td.age <= horizon30Days {
			ts.Dead30++
		}
		if td.age <= horizon90Days {
			ts.Dead90++
		}
		ts.Added++
		obsByClass[td.klass] = append(obsByClass[td.klass], obs{age: td.age, dead: true})
	}
	out := make([]TestSurvival, 0, len(byClass))
	for _, c := range byClass {
		c.S30 = testShare(obsByClass[c.Class], horizon30Days)
		c.S90 = testShare(obsByClass[c.Class], horizon90Days)
		c.Provisional = c.Added < ProvisionalTests
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Class < out[j].Class })

	return out
}

// testShare is one minus the share of tests deleted within the horizon, among
// tests observed for at least that long. A live test younger than the horizon
// is censored out of the denominator. It answers nil when no test is at risk.
func testShare(observed []obs, horizon int) *float64 {
	atRisk, deleted := 0, 0
	for _, o := range observed {
		if !o.dead && o.age < horizon {
			continue
		}
		atRisk++
		if o.dead && o.age <= horizon {
			deleted++
		}
	}
	if atRisk == 0 {
		return nil
	}
	share := 1 - float64(deleted)/float64(atRisk)

	return &share
}

// concentration finds each directory's largest single-author share of its
// live lines. A directory with no live lines maps to nil.
func (w *Walker) concentration() map[string]*float64 {
	byDir := map[string]map[string]int{}
	for path, lines := range w.files {
		d := DirOf(path)
		if byDir[d] == nil {
			byDir[d] = map[string]int{}
		}
		for _, t := range lines {
			byDir[d][t.author]++
		}
	}
	out := make(map[string]*float64, len(w.res.Dirs))
	for d := range w.res.Dirs {
		out[d] = nil
	}
	for d, authors := range byDir {
		total, top := 0, 0
		for _, n := range authors {
			total += n
			top = max(top, n)
		}
		if total > 0 {
			share := float64(top) / float64(total)
			out[d] = &share
		}
	}

	return out
}

// coupling lists the file pairs that co-changed at least minCouples times.
func (w *Walker) coupling() [][2]string {
	var out [][2]string
	for pair, n := range w.couples {
		if n >= minCouples {
			out = append(out, pair)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })

	return out
}

// totalAdded sums every cohort's births.
func (w *Walker) totalAdded() int {
	total := 0
	for _, b := range w.births {
		total += b
	}

	return total
}

// km is the Kaplan-Meier product-limit estimate at horizon: at each distinct
// death age the at-risk set is every observation that reached it, dead or
// still alive (censored observations drop out as their exposure ends). The
// ages slice arrives sorted ascending. Its 95% interval is Greenwood's, on the
// survival scale and clipped to [0, 1]. It answers nil for both when fewer than
// minAtRisk observations reached the horizon.
func km(ages []obs, horizon int) (*float64, *[2]float64) {
	reached := 0
	for _, o := range ages {
		if o.age >= horizon {
			reached++
		}
	}
	if reached < minAtRisk {
		return nil, nil
	}
	surv, greenwood := 1.0, 0.0
	for i := 0; i < len(ages); {
		age := ages[i].age
		if age > horizon {
			break
		}
		risk := len(ages) - i
		died := 0
		for i < len(ages) && ages[i].age == age {
			if ages[i].dead {
				died++
			}
			i++
		}
		if died > 0 {
			surv *= 1 - float64(died)/float64(risk)
			if risk > died {
				greenwood += float64(died) / float64(risk*(risk-died))
			}
		}
	}
	se := surv * math.Sqrt(greenwood)
	ci := [2]float64{max(surv-zNormal95*se, 0), min(surv+zNormal95*se, 1)}

	return &surv, &ci
}

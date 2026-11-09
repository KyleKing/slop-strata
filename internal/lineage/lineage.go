// Package lineage walks a commit range oldest-first and tracks which cohort
// every live line belongs to, so survival, churn, rework, hotspots, coupling,
// and concentration all come off one pass over the diffs.
package lineage

import (
	"fmt"
	"sort"
	"strings"
	"time"

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
)

// tag is one live line's provenance.
type tag struct {
	class     string
	author    string
	birthDays int // unix days when the line was added
	cohort    int // birth month as year*12+month
}

// fileLines is a file as the walk sees it: one tag per current line.
type fileLines []tag

// cohortOf buckets a date by month so survival curves line up by age.
func cohortOf(t time.Time) int {
	return t.Year()*12 + int(t.Month())
}

func cohortLabel(c int) string {
	return fmt.Sprintf("%04d-%02d", c/monthsPerYear, c%monthsPerYear)
}

func days(t time.Time) int {
	return int(t.Unix() / 86400) //nolint:mnd // seconds per day
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
	Cohort    string  `json:"cohort"`
	Added     int     `json:"added"`
	Surviving int     `json:"surviving"`
	S30       float64 `json:"s_30d"`
	S90       float64 `json:"s_90d"`
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
	Class  string  `json:"class"`
	Added  int     `json:"added"`
	Dead30 int     `json:"dead_30d"`
	Dead90 int     `json:"dead_90d"`
	S30    float64 `json:"s_30d"`
	S90    float64 `json:"s_90d"`
}

// Result is the lineage walk's output.
type Result struct {
	Dirs          map[string]*DirStats `json:"dirs"`
	Concentration map[string]float64   `json:"concentration"`
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

// Commit applies one commit's record to the line state.
func (w *Walker) Commit(rec vcs.CommitRecord, fixIntent, revert bool) {
	w.res.Commits++
	if revert {
		w.res.Reverts++
	}
	klass := w.class(rec.AuthorName+" <"+rec.AuthorEmail+">", rec.CommitterName+" <"+rec.CommitterEmail+">", rec.Body)
	cohort := cohortOf(rec.AuthorTime)
	birth := days(rec.AuthorTime)

	touched := map[string]bool{}
	for _, f := range rec.Files {
		d := DirOf(f.Path)
		if w.res.Dirs[d] == nil {
			w.res.Dirs[d] = &DirStats{}
		}
		ds := w.res.Dirs[d]
		ds.Touches++
		touched[f.Path] = true

		switch f.Status {
		case vcs.StatusAdded:
			w.addFile(f, klass, cohort, birth, rec, ds)
		case vcs.StatusDeleted:
			w.removeFile(f.Path, birth, ds, rec, fixIntent)
		default:
			if f.Status == vcs.StatusRenamed && f.OldPath != "" {
				w.renameFile(f)
			}
			w.applyHunks(f.Path, f.Hunks, cohort, birth, klass, rec, ds, fixIntent)
		}
	}
	w.countCouples(touched)
}

// addFile records a whole new file's lines as births at this commit.
func (w *Walker) addFile(f vcs.CommitFile, klass string, cohort, birth int,
	rec vcs.CommitRecord, ds *DirStats,
) {
	n := f.LinesAdded()
	lines := make(fileLines, n)
	for i := range lines {
		lines[i] = tag{class: klass, author: rec.AuthorEmail, birthDays: birth, cohort: cohort}
	}
	w.files[f.Path] = lines
	w.births[cohort] += n
	ds.Added += n
	if lang.IsTestPath(f.Path) {
		w.testBirths[f.Path] = testBirth{day: birth, klass: klass}
	}
}

// removeFile records a deletion: a test death when the file was being
// tracked, plus every live line killed.
func (w *Walker) removeFile(path string, birth int, ds *DirStats, rec vcs.CommitRecord, fix bool) {
	if tb, ok := w.testBirths[path]; ok {
		w.testDeaths = append(w.testDeaths, testDeath{age: birth - tb.day, klass: tb.klass})
		delete(w.testBirths, path)
	}
	w.killAll(path, ds, rec, birth, fix)
}

// renameFile moves a file's line tags and test-birth record to its new path.
func (w *Walker) renameFile(f vcs.CommitFile) {
	w.files[f.Path] = w.files[f.OldPath]
	delete(w.files, f.OldPath)
	if tb, ok := w.testBirths[f.OldPath]; ok {
		w.testBirths[f.Path] = tb
		delete(w.testBirths, f.OldPath)
	}
}

// killAll records every live line of a deleted file as a death.
func (w *Walker) killAll(path string, ds *DirStats, _ vcs.CommitRecord, deathDay int, fix bool) {
	for _, t := range w.files[path] {
		w.recordDeath(t, ds, deathDay, fix)
	}
	delete(w.files, path)
}

// applyHunks rewrites a file's line tags through its hunks, last hunk first so
// earlier offsets stay valid.
func (w *Walker) applyHunks(path string, hunks []vcs.DiffHunk, cohort, birth int, klass string,
	rec vcs.CommitRecord, ds *DirStats, fix bool,
) {
	lines := w.files[path]
	sorted := append([]vcs.DiffHunk(nil), hunks...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].OldStart > sorted[j].OldStart })
	for _, h := range sorted {
		idx := max(h.OldStart-1, 0)
		if need := idx + h.OldLines; need > len(lines) {
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
		end := min(idx+h.OldLines, len(lines))
		for _, t := range lines[idx:end] {
			w.recordDeath(t, ds, birth, fix)
		}
		mid := make(fileLines, h.NewLines)
		for i := range mid {
			mid[i] = tag{class: klass, author: rec.AuthorEmail, birthDays: birth, cohort: cohort}
		}
		out := make(fileLines, 0, len(lines)-h.OldLines+h.NewLines)
		out = append(out, lines[:idx]...)
		out = append(out, mid...)
		out = append(out, lines[end:]...)
		lines = out
		ds.Added += h.NewLines
		w.births[cohort] += h.NewLines
	}
	w.files[path] = lines
}

// recordDeath logs one line's removal with its provenance.
func (w *Walker) recordDeath(t tag, ds *DirStats, deathDay int, fix bool) {
	ds.Removed++
	age := deathDay - t.birthDays
	w.deaths = append(w.deaths, death{cohort: t.cohort, age: age, klass: t.class, fix: fix})
	if age <= churn14Days {
		ds.Churn14++
	}
	if age <= churn30Days {
		ds.Churn30++
		w.res.Churned30++
	}
	if fix && age <= churn30Days {
		ds.Rework++
	}
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

// survival computes the Kaplan-Meier per cohort: events are line deaths at an
// age in days; at-risk is approximated at month granularity (every birth taken
// as day 1 of its cohort).
func (w *Walker) survival() []Survival {
	agesByCohort := map[int][]int{}
	for _, d := range w.deaths {
		agesByCohort[d.cohort] = append(agesByCohort[d.cohort], d.age)
	}
	survivingByCohort := map[int]int{}
	for _, lines := range w.files {
		for _, t := range lines {
			survivingByCohort[t.cohort]++
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
		ages := agesByCohort[c]
		sort.Ints(ages)
		s.S30 = km(ages, s.Added, horizon30Days)
		s.S90 = km(ages, s.Added, horizon90Days)
		out = append(out, s)
	}

	return out
}

// testSurvival shares each author class's test files deleted within the 30-
// and 90-day horizons. Deaths also count toward the denominator of what was
// added.
func (w *Walker) testSurvival() []TestSurvival {
	byClass := map[string]*TestSurvival{}
	for _, tb := range w.testBirths {
		if byClass[tb.klass] == nil {
			byClass[tb.klass] = &TestSurvival{Class: tb.klass}
		}
		byClass[tb.klass].Added++
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
	}
	out := make([]TestSurvival, 0, len(byClass))
	for _, c := range byClass {
		if c.Added > 0 {
			c.S30 = float64(c.Dead30) / float64(c.Added)
			c.S90 = float64(c.Dead90) / float64(c.Added)
		}
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Class < out[j].Class })

	return out
}

// concentration finds each directory's largest single-author share of its
// live lines.
func (w *Walker) concentration() map[string]float64 {
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
	out := map[string]float64{}
	for d, authors := range byDir {
		total, top := 0, 0
		for _, n := range authors {
			total += n
			top = max(top, n)
		}
		if total > 0 {
			out[d] = float64(top) / float64(total)
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

// km computes the Kaplan-Meier survival share at horizon days: the product of
// (1 - deaths/at-risk) over every event inside the horizon. At-risk counts
// all cohort births still alive before each event.
func km(ages []int, added, horizon int) float64 {
	if added == 0 {
		return 0
	}
	surv := 1.0
	risk := added
	for i := 0; i < len(ages); {
		j := i
		for j < len(ages) && ages[j] == ages[i] {
			j++
		}
		if ages[i] > horizon {
			break
		}
		died := j - i
		surv *= 1 - float64(died)/float64(risk)
		risk -= died
		i = j
	}
	if risk <= 0 {
		return 0
	}

	return surv
}

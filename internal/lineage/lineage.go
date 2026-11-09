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

// tag is one live line's provenance.
type tag struct {
	birthDays int // unix days when the line was added
	cohort    int // birth month as year*12+month
	class     string
	author    string
}

// fileLines is a file as the walk sees it: one tag per current line.
type fileLines []tag

// cohortOf buckets a date by month so survival curves line up by age.
func cohortOf(t time.Time) int {
	return t.Year()*12 + int(t.Month())
}

func cohortLabel(c int) string {
	return fmt.Sprintf("%04d-%02d", c/12, c%12)
}

func days(t time.Time) int {
	return int(t.Unix() / 86400) //nolint:mnd // seconds per day
}

// death is a removed line's record: when it died and how old it was.
type death struct {
	cohort, age int
	klass       string
	fix         bool
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
	Survival      []Survival           `json:"survival"`
	TestSurvival  []TestSurvival       `json:"test_survival"`
	Dirs          map[string]*DirStats `json:"dirs"`
	Reverts       int                  `json:"reverts"`
	Commits       int                  `json:"commits"`
	Concentration map[string]float64   `json:"concentration"`
	Coupling      [][2]string          `json:"coupling"`
	Added30       int                  `json:"added_30d_window"`
	Churned30     int                  `json:"churned_30d"`
}

// testBirth is an observed test-file addition.
type testBirth struct {
	day   int
	klass string
}

// testDeath records a tracked test file's deletion age.
type testDeath struct {
	age   int
	klass string
}

// Walker carries the mutable state of one pass.
type Walker struct {
	files      map[string]fileLines
	deaths     []death
	births     map[int]int
	class      func(author, committer, body string) string
	couples    map[[2]string]int
	testBirths map[string]testBirth
	testDeaths []testDeath
	res        *Result
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
			n := f.LinesAdded()
			lines := make(fileLines, n)
			for i := range lines {
				lines[i] = tag{birth, cohort, klass, rec.AuthorEmail}
			}
			w.files[f.Path] = lines
			w.births[cohort] += n
			ds.Added += n
			if lang.IsTestPath(f.Path) {
				w.testBirths[f.Path] = testBirth{day: birth, klass: klass}
			}
		case vcs.StatusDeleted:
			if tb, ok := w.testBirths[f.Path]; ok {
				w.testDeaths = append(w.testDeaths, testDeath{age: birth - tb.day, klass: tb.klass})
				delete(w.testBirths, f.Path)
			}
			w.killAll(f.Path, ds, rec, birth, fixIntent)
		default:
			if f.Status == vcs.StatusRenamed && f.OldPath != "" {
				w.files[f.Path] = w.files[f.OldPath]
				delete(w.files, f.OldPath)
				if tb, ok := w.testBirths[f.OldPath]; ok {
					w.testBirths[f.Path] = tb
					delete(w.testBirths, f.OldPath)
				}
			}
			w.applyHunks(f.Path, f.Hunks, cohort, birth, klass, rec, ds, fixIntent)
		}
	}
	w.countCouples(touched)
}

// killAll records every live line of a deleted file as a death.
func (w *Walker) killAll(path string, ds *DirStats, rec vcs.CommitRecord, deathDay int, fix bool) {
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
		end := min(idx+h.OldLines, len(lines))
		for _, t := range lines[idx:end] {
			w.recordDeath(t, ds, birth, fix)
		}
		mid := make(fileLines, h.NewLines)
		for i := range mid {
			mid[i] = tag{birth, cohort, klass, rec.AuthorEmail}
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
	if age <= 14 {
		ds.Churn14++
	}
	if age <= 30 {
		ds.Churn30++
		w.res.Churned30++
	}
	if fix && age <= 30 {
		ds.Rework++
	}
}

// countCouples counts same-commit file pairs, skipping very large commits.
func (w *Walker) countCouples(touched map[string]bool) {
	if len(touched) > 30 || len(touched) < 2 { //nolint:mnd // code-maat's "big commit" cut
		return
	}
	paths := make([]string, 0, len(touched))
	for p := range touched {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for i := 0; i < len(paths); i++ {
		for j := i + 1; j < len(paths); j++ {
			w.couples[[2]string{paths[i], paths[j]}]++
		}
	}
}

// Finish rolls the walk up into a Result.
func (w *Walker) Finish() *Result {
	res := w.res

	// Kaplan-Meier per cohort: events are line deaths at an age in days;
	// at-risk is approximated at month granularity (every birth taken as
	// day 1 of its cohort).
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
	cohortSet := map[int]bool{}
	for c := range w.births {
		cohortSet[c] = true
	}
	var cohorts []int
	for c := range cohortSet {
		cohorts = append(cohorts, c)
	}
	sort.Ints(cohorts)
	for _, c := range cohorts {
		s := Survival{Cohort: cohortLabel(c), Added: w.births[c], Surviving: survivingByCohort[c]}
		ages := agesByCohort[c]
		sort.Ints(ages)
		s.S30 = km(ages, s.Added, 30) //nolint:mnd // 30-day mark
		s.S90 = km(ages, s.Added, 90) //nolint:mnd // 90-day mark
		res.Survival = append(res.Survival, s)
	}

	// Test survival by adder class: the share of test files deleted within 30
	// and 90 days of their observed addition.
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
		if td.age <= 30 {
			ts.Dead30++
		}
		if td.age <= 90 {
			ts.Dead90++
		}
	}
	// Deaths also count toward the denominator of what was added.
	for _, td := range w.testDeaths {
		byClass[td.klass].Added++
	}
	for _, c := range byClass {
		if c.Added > 0 {
			c.S30 = float64(c.Dead30) / float64(c.Added)
			c.S90 = float64(c.Dead90) / float64(c.Added)
		}
		res.TestSurvival = append(res.TestSurvival, *c)
	}
	sort.Slice(res.TestSurvival, func(i, j int) bool { return res.TestSurvival[i].Class < res.TestSurvival[j].Class })

	res.Concentration = map[string]float64{}
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
	for d, authors := range byDir {
		total, top := 0, 0
		for _, n := range authors {
			total += n
			top = max(top, n)
		}
		if total > 0 {
			res.Concentration[d] = float64(top) / float64(total)
		}
	}

	for pair, n := range w.couples {
		if n >= 3 {
			res.Coupling = append(res.Coupling, pair)
		}
	}
	sort.Slice(res.Coupling, func(i, j int) bool { return res.Coupling[i][0] < res.Coupling[j][0] })

	totalAdded := 0
	for _, b := range w.births {
		totalAdded += b
	}
	res.Added30 = totalAdded

	return res
}

// km computes the Kaplan-Meier survival share at horizon days: the product of
// (1 - deaths/at-risk) over every event inside the horizon. at-risk counts
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

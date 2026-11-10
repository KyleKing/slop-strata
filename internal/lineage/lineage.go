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
	S30       *float64 `json:"s_30d"`
	S90       *float64 `json:"s_90d"`
	Cohort    string   `json:"cohort"`
	Added     int      `json:"added"`
	Surviving int      `json:"surviving"`
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
	S30    *float64 `json:"s_30d"`
	S90    *float64 `json:"s_90d"`
	Class  string   `json:"class"`
	Added  int      `json:"added"`
	Dead30 int      `json:"dead_30d"`
	Dead90 int      `json:"dead_90d"`
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

// moves carries one commit's moved-line accounting: removed-line tags keyed
// by text for the added side to reuse, and the old positions whose removal
// is a move rather than a death.
type moves struct {
	tags   map[string][]tag
	exempt map[string]map[int]bool
}

// moveTextFloor is the fewest trimmed bytes a line needs before a text match
// reads as a move; shorter lines like braces match by coincidence.
const moveTextFloor = 8

// moveTextKey normalizes a line for move matching.
func moveTextKey(text string) string {
	if key := strings.TrimSpace(text); len(key) >= moveTextFloor {
		return key
	}

	return ""
}

// collectMoves pairs the commit's removed and added line texts. Each removed
// line whose text also arrives (up to the number of copies arriving) keeps its
// tag: it moves rather than dying and being reborn as new code.
func (w *Walker) collectMoves(rec vcs.CommitRecord) *moves {
	addCount := map[string]int{}
	for i := range rec.Files {
		for _, text := range rec.Files[i].Added {
			if key := moveTextKey(text); key != "" {
				addCount[key]++
			}
		}
	}
	mv := &moves{tags: map[string][]tag{}, exempt: map[string]map[int]bool{}}
	for i := range rec.Files {
		w.stashRemoved(&rec.Files[i], addCount, mv)
	}

	return mv
}

// stashRemoved banks the tags of one file's removed lines that arrive
// elsewhere in the same commit, keyed by text for the added side to claim.
func (w *Walker) stashRemoved(f *vcs.CommitFile, addCount map[string]int, mv *moves) {
	tagPath := f.Path
	if f.Status == vcs.StatusRenamed && f.OldPath != "" {
		tagPath = f.OldPath
	}
	lines := w.files[tagPath]
	cur := 0
	for _, h := range f.Hunks {
		for i := range h.OldLines {
			pos := h.OldStart - 1 + i
			key := ""
			if cur < len(f.Removed) {
				key = moveTextKey(f.Removed[cur])
			}
			cur++
			if key == "" || pos < 0 || pos >= len(lines) || len(mv.tags[key]) >= addCount[key] {
				continue
			}
			mv.tags[key] = append(mv.tags[key], lines[pos])
			if mv.exempt[f.Path] == nil {
				mv.exempt[f.Path] = map[int]bool{}
			}
			mv.exempt[f.Path][pos] = true
		}
	}
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
		t := tag{class: klass, author: rec.AuthorEmail, birthDays: birth, cohort: cohort}
		kept := false
		if i < len(f.Added) {
			if t2, ok := mv.take(f.Added[i]); ok {
				t = t2
				kept = true
			}
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
		w.testDeaths = append(w.testDeaths, testDeath{age: birth - tb.day, klass: tb.klass})
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

// take claims the tag a moved line brings, if one was stashed for its text.
func (mv *moves) take(text string) (tag, bool) {
	key := moveTextKey(text)
	if key == "" || len(mv.tags[key]) == 0 {
		return tag{}, false
	}
	t := mv.tags[key][0]
	mv.tags[key] = mv.tags[key][1:]

	return t, true
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

// hunkEdit pairs a hunk with the text it adds, split out of the file-level
// Added list in patch order before the hunks get applied last-first.
type hunkEdit struct {
	add []string
	h   vcs.DiffHunk
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
		n := min(h.NewLines, max(len(f.Added)-cur, 0))
		edits[i] = hunkEdit{f.Added[cur : cur+n], h}
		cur += n
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
		mid[i] = tag{class: klass, author: rec.AuthorEmail, birthDays: birth, cohort: cohort}
		if i < len(edit.add) {
			if kept, ok := mv.take(edit.add[i]); ok {
				mid[i] = kept

				continue
			}
		}
		births++
	}

	return mid, births
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
				obsByCohort[t.cohort] = append(obsByCohort[t.cohort], obs{age: w.lastDay - t.birthDays})
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
		s.S30 = km(ages, horizon30Days)
		s.S90 = km(ages, horizon90Days)
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
		obsByClass[tb.klass] = append(obsByClass[tb.klass], obs{age: w.lastDay - tb.day})
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

// km is the Kaplan-Meier product-limit estimate at horizon: at each distinct
// death age the at-risk set is every observation that reached it, dead or
// still alive (censored observations drop out as their exposure ends). The
// ages slice arrives sorted ascending. It answers nil when no observation ran
// as long as the horizon, because the estimate would extrapolate past the data.
func km(ages []obs, horizon int) *float64 {
	if len(ages) == 0 || ages[len(ages)-1].age < horizon {
		return nil
	}
	surv := 1.0
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
		}
	}

	return &surv
}

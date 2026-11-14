package main

import (
	"cmp"
	"slices"
	"sort"

	"github.com/kyleking/aragonite/vcs"

	"github.com/kyleking/slop-strata/internal/engine"
	"github.com/kyleking/slop-strata/internal/lang"
	"github.com/kyleking/slop-strata/internal/lineage"
	"github.com/kyleking/slop-strata/internal/structure"
)

// A function oscillates when its body changes in oscillationChanges commits
// within oscillationDays days.
const (
	oscillationChanges = 3
	oscillationDays    = 14
)

// oscillationTop is how many production functions the JSON names.
const oscillationTop = 10

// oscillationResult counts the functions whose bodies changed in at least three
// commits inside 14 days. Dirs lists only directories that have one, of either kind.
type oscillationResult struct {
	Dirs      map[string]oscillationDir `json:"dirs"`
	Top       []oscillatingFunc         `json:"top"`
	Funcs     int                       `json:"oscillating_funcs"`
	TestFuncs int                       `json:"oscillating_test_funcs"`
}

// oscillationDir is one directory's oscillating functions, tests apart.
type oscillationDir struct {
	Funcs     int `json:"oscillating_funcs"`
	TestFuncs int `json:"oscillating_test_funcs"`
}

// oscillatingFunc names an oscillating production function, with the busiest
// stretch of its changes.
type oscillatingFunc struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Line     int    `json:"line"`
	Changes  int    `json:"changes"`
	SpanDays int    `json:"span_days"`
}

// funcState is one function's change days within the last oscillationDays of
// its latest change, and the most it changed in any such stretch.
type funcState struct {
	recent []int
	best   int
	span   int
	line   int
}

// oscillation follows each function, keyed by path and name, through the walk.
type oscillation struct {
	files map[string]map[string]*funcState
}

func newOscillation() *oscillation {
	return &oscillation{files: map[string]map[string]*funcState{}}
}

// rename moves a file's functions to its new path, so a renamed file keeps its
// history.
func (o *oscillation) rename(oldPath, path string) {
	if fns, ok := o.files[oldPath]; ok {
		o.files[path] = fns
		delete(o.files, oldPath)
	}
}

// observe records the day on every function of a modified file whose lines a
// hunk touched and that the before side already had, once per name however
// many same-named functions the file holds.
func (o *oscillation) observe(f *vcs.CommitFile, before, after *engine.FileReport, day int) {
	if before == nil || before.Skipped != "" || len(f.Hunks) == 0 {
		return
	}
	var had map[string]bool
	for _, fn := range after.Metrics.Funcs {
		if fn.Name == lang.AnonymousName || !touched(f.Hunks, fn) {
			continue
		}
		if had == nil {
			had = funcNames(before.Metrics.Funcs)
		}
		if had[fn.Name] {
			o.record(f.Path, fn, day)
			had[fn.Name] = false
		}
	}
}

func funcNames(fns []structure.FuncMetrics) map[string]bool {
	names := make(map[string]bool, len(fns))
	for _, fn := range fns {
		names[fn.Name] = true
	}

	return names
}

// touched reports whether a hunk adds a line inside the function or removes one
// from between its lines. A hunk with no new lines sits between lines NewStart-1
// and NewStart. Hunks arrive in file order.
func touched(hunks []vcs.DiffHunk, fn structure.FuncMetrics) bool {
	last := func(h vcs.DiffHunk) int { return h.NewStart + max(h.NewLines, 1) - 1 }
	i := sort.Search(len(hunks), func(i int) bool { return last(hunks[i]) >= fn.StartLine })
	for _, h := range hunks[i:] {
		if h.NewStart > fn.EndLine {
			return false
		}
		if h.NewLines > 0 || h.NewStart > fn.StartLine {
			return true
		}
	}

	return false
}

func (o *oscillation) record(path string, fn structure.FuncMetrics, day int) {
	if o.files[path] == nil {
		o.files[path] = map[string]*funcState{}
	}
	st := o.files[path][fn.Name]
	if st == nil {
		st = &funcState{}
		o.files[path][fn.Name] = st
	}
	keep := slices.IndexFunc(st.recent, func(d int) bool { return day-d <= oscillationDays })
	if keep < 0 {
		keep = len(st.recent)
	}
	st.recent = append(st.recent[keep:], day)
	st.line = fn.StartLine
	if n := len(st.recent); n > st.best || (n == st.best && day-st.recent[0] < st.span) {
		st.best, st.span = n, day-st.recent[0]
	}
}

// result counts the oscillating functions and names the busiest production ones.
func (o *oscillation) result() *oscillationResult {
	res := &oscillationResult{Dirs: map[string]oscillationDir{}, Top: []oscillatingFunc{}}
	var all []oscillatingFunc
	for path, fns := range o.files {
		test := lang.IsTestPath(path)
		for name, st := range fns {
			if st.best < oscillationChanges {
				continue
			}
			dir := res.Dirs[lineage.DirOf(path)]
			if test {
				res.TestFuncs++
				dir.TestFuncs++
			} else {
				res.Funcs++
				dir.Funcs++
				all = append(all, oscillatingFunc{
					Path: path, Name: name, Line: st.line, Changes: st.best, SpanDays: st.span,
				})
			}
			res.Dirs[lineage.DirOf(path)] = dir
		}
	}
	slices.SortFunc(all, func(a, b oscillatingFunc) int {
		return cmp.Or(
			cmp.Compare(b.Changes, a.Changes), cmp.Compare(a.SpanDays, b.SpanDays),
			cmp.Compare(a.Path, b.Path), cmp.Compare(a.Name, b.Name),
		)
	})
	res.Top = append(res.Top, all[:min(oscillationTop, len(all))]...)

	return res
}

package main

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/kyleking/slop-strata/internal/lang"
	"github.com/kyleking/slop-strata/internal/report"
	"github.com/kyleking/slop-strata/internal/structure"
)

// topFunctionLimit caps the top_functions list of a change.
const topFunctionLimit = 10

// massEpsilon is the mass difference below which two functions measure the same,
// so float rounding in CC x sqrt(SLOC) never reads as growth.
const massEpsilon = 1e-9

type funcSides struct {
	before *structure.FileMetrics
	after  *structure.FileMetrics
	path   string
}

type funcKind int

const (
	funcMatched funcKind = iota
	funcAdded
	funcMoved
	funcRemoved
)

type funcChange struct {
	before *structure.FuncMetrics
	after  *structure.FuncMetrics
	path   string
	kind   funcKind
}

type topFunc struct {
	File       string  `json:"file"`
	Name       string  `json:"name"`
	Line       int     `json:"line"`
	CCBefore   int     `json:"cc_before"`
	CCAfter    int     `json:"cc_after"`
	MassBefore float64 `json:"mass_before"`
	MassAfter  float64 `json:"mass_after"`
	MassGrown  float64 `json:"mass_grown"`
	Test       bool    `json:"test"`
	endLine    int
}

type massTotals struct {
	grown     float64
	removed   float64
	overLimit float64
}

func (t massTotals) net() float64 {
	return t.grown - t.removed
}

type funcCounts struct {
	added   int
	touched int
	gone    int
}

// funcKey identifies a moved function exactly: a move keeps its name and
// shape, so (name, complexity, size) matches it on the other side.
type funcKey struct {
	name string
	cc   int
	sloc int
}

func keyOf(fn structure.FuncMetrics) funcKey {
	return funcKey{fn.Name, fn.CC, fn.SLOC}
}

func uniqueKeys(fns []structure.FuncMetrics) []string {
	keys := funcKeys(fns)
	seen := map[string]int{}
	for i, k := range keys {
		seen[k]++
		if n := seen[k]; n > 1 {
			keys[i] = fmt.Sprintf("%s~%d", k, n)
		}
	}

	return keys
}

func funcsOf(m *structure.FileMetrics) []structure.FuncMetrics {
	if m == nil {
		return nil
	}

	return m.Funcs
}

// pairFuncs pairs each before-side function at most once, so grown minus removed equals the net mass change.
func pairFuncs(files []funcSides) []funcChange {
	var changes, orphans, removed []funcChange
	for _, f := range files {
		bf, af := funcsOf(f.before), funcsOf(f.after)
		beforeAt := map[string]int{}
		for i, k := range uniqueKeys(bf) {
			beforeAt[k] = i
		}
		used := make([]bool, len(bf))
		for i, k := range uniqueKeys(af) {
			j, ok := beforeAt[k]
			if !ok {
				orphans = append(orphans, funcChange{path: f.path, kind: funcAdded, after: &af[i]})

				continue
			}
			used[j] = true
			changes = append(changes, funcChange{path: f.path, kind: funcMatched, before: &bf[j], after: &af[i]})
		}
		for j := range bf {
			if !used[j] {
				removed = append(removed, funcChange{path: f.path, kind: funcRemoved, before: &bf[j]})
			}
		}
	}

	return append(changes, pairMoves(orphans, removed)...)
}

func pairMoves(orphans, removed []funcChange) []funcChange {
	pool := map[funcKey][]int{}
	for i, r := range removed {
		k := keyOf(*r.before)
		pool[k] = append(pool[k], i)
	}
	taken := make([]bool, len(removed))
	out := make([]funcChange, 0, len(orphans)+len(removed))
	for _, o := range orphans {
		k := keyOf(*o.after)
		if idx := pool[k]; len(idx) > 0 {
			o.kind = funcMoved
			o.before = removed[idx[0]].before
			taken[idx[0]] = true
			pool[k] = idx[1:]
		}
		out = append(out, o)
	}
	for i, r := range removed {
		if !taken[i] {
			out = append(out, r)
		}
	}

	return out
}

func (c funcChange) grown() float64 {
	if c.after == nil {
		return 0
	}
	g := structure.Mass(*c.after)
	if c.before != nil {
		g -= structure.Mass(*c.before)
	}

	if g < massEpsilon {
		return 0
	}

	return g
}

func (c funcChange) shrunk() float64 {
	if c.before == nil {
		return 0
	}
	s := structure.Mass(*c.before)
	if c.after != nil {
		s -= structure.Mass(*c.after)
	}

	if s < massEpsilon {
		return 0
	}

	return s
}

func (c funcChange) overLimit() bool {
	return c.after != nil && c.after.CC > structure.CCLimit
}

func totalsByPath(changes []funcChange) (map[string]massTotals, massTotals, massTotals) {
	byPath := map[string]massTotals{}
	var prod, tests massTotals
	for _, c := range changes {
		t := byPath[c.path]
		grown := c.grown()
		t.grown += grown
		t.removed += c.shrunk()
		if c.overLimit() {
			t.overLimit += grown
		}
		byPath[c.path] = t
	}
	for _, path := range report.SortedKeys(byPath) {
		t := byPath[path]
		sum := &prod
		if lang.IsTestPath(path) {
			sum = &tests
		}
		sum.grown += t.grown
		sum.removed += t.removed
		sum.overLimit += t.overLimit
	}

	return byPath, prod, tests
}

// highFuncChanges counts production functions over the CC limit that were added, touched, or gone. A move is none.
func highFuncChanges(changes []funcChange) funcCounts {
	var c funcCounts
	for _, ch := range changes {
		if lang.IsTestPath(ch.path) {
			continue
		}
		switch {
		case ch.kind == funcAdded && ch.overLimit():
			c.added++
		case ch.kind == funcMatched && ch.overLimit() && !sameShape(*ch.before, *ch.after):
			c.touched++
		case ch.kind == funcRemoved && ch.before.CC > structure.CCLimit:
			c.gone++
		}
	}

	return c
}

// sameShape reports whether two versions of a function measure the same.
func sameShape(a, b structure.FuncMetrics) bool {
	return a.CC == b.CC && a.SLOC == b.SLOC && a.Cognitive == b.Cognitive &&
		a.Nesting == b.Nesting && a.Params == b.Params && a.Comments == b.Comments
}

func rankFunctions(changes []funcChange) []topFunc {
	out := []topFunc{}
	for _, c := range changes {
		grown := c.grown()
		if grown <= 0 {
			continue
		}
		tf := topFunc{
			File: c.path, Name: c.after.Name, Line: c.after.StartLine, CCAfter: c.after.CC,
			MassAfter: structure.Mass(*c.after), MassGrown: grown, Test: lang.IsTestPath(c.path),
			endLine: c.after.EndLine,
		}
		if c.before != nil {
			tf.CCBefore = c.before.CC
			tf.MassBefore = structure.Mass(*c.before)
		}
		out = append(out, tf)
	}
	slices.SortFunc(out, func(a, b topFunc) int {
		return cmp.Or(
			cmp.Compare(b.MassGrown, a.MassGrown),
			strings.Compare(a.File, b.File),
			cmp.Compare(a.Line, b.Line),
			strings.Compare(a.Name, b.Name),
		)
	})

	return out
}

func leadHighFunc(ranked []topFunc) string {
	for _, f := range ranked {
		if !f.Test && f.CCAfter > structure.CCLimit {
			return fmt.Sprintf("`%s` (%s:%d)", f.Name, f.File, f.Line)
		}
	}

	return ""
}

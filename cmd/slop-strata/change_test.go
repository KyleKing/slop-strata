package main

import (
	"math"
	"testing"

	"github.com/kyleking/slop-strata/internal/lang"
	"github.com/kyleking/slop-strata/internal/structure"
)

const pyTwoRuns = `class A:
    def run(self):
        return 1


class B:
    def run(self):
        if self.x:
            return 1
        return 2
`

const pyTwoRunsAChanged = `class A:
    def run(self):
        if self.x:
            return 1
        if self.y:
            return 2
        return 3


class B:
    def run(self):
        if self.x:
            return 1
        return 2
`

const pyTwoRunsBChanged = `class A:
    def run(self):
        return 1


class B:
    def run(self):
        if self.x:
            return 1
        if self.y:
            return 2
        return 3
`

const pyClosures = `def f(xs):
    a = lambda: 1
    b = lambda y: y
    return a, b
`

const pyClosureChanged = `def f(xs):
    a = lambda: 1
    b = lambda y: y if y else 0
    return a, b
`

func analyzePython(t *testing.T, src string) structure.FileMetrics {
	t.Helper()
	tree := lang.NewPool().Parse(lang.Python, []byte(src))
	t.Cleanup(tree.Close)

	return structure.Analyze(lang.Python, tree, []byte(src))
}

func massByKey(t *testing.T, fm structure.FileMetrics, key string) float64 {
	t.Helper()
	for i, k := range funcKeys(fm.Funcs) {
		if k == key {
			return structure.Mass(fm.Funcs[i])
		}
	}
	t.Fatalf("no function keyed %q in %v", key, funcKeys(fm.Funcs))

	return 0
}

func TestFuncGrowthMatchesSameNamedFunctionsByOwner(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		before string
		after  string
		grew   string // key of the one function the change grew; empty for none
	}{
		{name: "method of the first class grows", before: pyTwoRuns, after: pyTwoRunsAChanged, grew: "A.run"},
		{name: "method of the second class grows", before: pyTwoRuns, after: pyTwoRunsBChanged, grew: "B.run"},
		{name: "unchanged file grows nothing", before: pyTwoRuns, after: pyTwoRuns},
		{name: "closure matched by its position", before: pyClosures, after: pyClosureChanged, grew: "f/<anonymous>#2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			before := analyzePython(t, tc.before)
			after := analyzePython(t, tc.after)

			want := 0.0
			if tc.grew != "" {
				want = massByKey(t, after, tc.grew) - massByKey(t, before, tc.grew)
			}
			got, _ := funcGrowth(&before, &after, map[funcKey]float64{})
			if math.Abs(got-want) > 1e-9 {
				t.Fatalf("grown = %.3f, want %.3f", got, want)
			}
		})
	}
}

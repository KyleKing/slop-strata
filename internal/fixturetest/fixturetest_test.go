package fixturetest_test

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/kyleking/slop-strata/internal/fixturetest"
)

func TestFilesParsesAnArchive(t *testing.T) {
	t.Parallel()
	cases := []struct {
		want    fixturetest.Tree
		name    string
		archive string
	}{
		{name: "empty", archive: "", want: fixturetest.Tree{}},
		{
			name: "text before the first marker is dropped", archive: "a comment\n-- a.py --\nx = 1\n",
			want: fixturetest.Tree{"a.py": "x = 1\n"},
		},
		{
			name: "two files, indented markers", archive: "\t-- pkg/a.py --\nimport b\n\n  -- pkg/b.py --\n",
			want: fixturetest.Tree{"pkg/a.py": "import b\n\n", "pkg/b.py": ""},
		},
		{name: "a bare marker is content", archive: "-- a.txt --\n-- --\n", want: fixturetest.Tree{"a.txt": "-- --\n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := fixturetest.Files(tc.archive); !maps.Equal(got, tc.want) {
				t.Errorf("Files() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestVariantApplyOverlaysWithoutTouchingTheBase(t *testing.T) {
	t.Parallel()
	base := fixturetest.Tree{"a": "1", "b": "2"}
	v := fixturetest.Variant{Files: fixturetest.Tree{"a": "changed", "c": "3"}, Remove: []string{"b"}}
	got := v.Apply(base)
	if want := (fixturetest.Tree{"a": "changed", "c": "3"}); !maps.Equal(got, want) {
		t.Errorf("Apply() = %v, want %v", got, want)
	}
	if want := (fixturetest.Tree{"a": "1", "b": "2"}); !maps.Equal(base, want) {
		t.Errorf("Apply changed its base: %v", base)
	}
	if got := (fixturetest.Variant{Files: fixturetest.Tree{"x": "1"}}).Apply(nil); len(got) != 1 {
		t.Errorf("Apply(nil) = %v, want the overlay alone", got)
	}
}

// recorder collects what Check reports.
type recorder struct{ failures []string }

func (*recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

// expect fails the test unless the failures recorded contain each wanted
// text, in order, and nothing more.
func (r *recorder) expect(t *testing.T, want []string) {
	t.Helper()
	if len(r.failures) != len(want) {
		t.Fatalf("failures = %q, want %d matching %q", r.failures, len(want), want)
	}
	for i, text := range want {
		if !strings.Contains(r.failures[i], text) {
			t.Errorf("failure %d = %q, want it to contain %q", i, r.failures[i], text)
		}
	}
}

// markerIDs is a stand-in rule: one finding per file holding "BAD", named for the file.
func markerIDs(tree fixturetest.Tree) []string {
	var ids []string
	for p, body := range tree {
		if strings.Contains(body, "BAD") {
			ids = append(ids, p)
		}
	}

	return ids
}

func TestCheckReportsEachBrokenExpectation(t *testing.T) {
	t.Parallel()
	base := fixturetest.Tree{"a": "BAD", "b": "fine"}
	cases := []struct {
		name string
		c    fixturetest.Case
		want []string
	}{
		{
			name: "holding dodge and working fix pass",
			c: fixturetest.Case{
				Base:   base,
				Dodges: []fixturetest.Variant{{Name: "comment", Files: fixturetest.Tree{"a": "# note\nBAD"}}},
				Fixes:  []fixturetest.Variant{{Name: "repair", Files: fixturetest.Tree{"a": "good"}}},
			},
		},
		{
			name: "a base with no finding fails once and skips its variants",
			c: fixturetest.Case{
				Base:  fixturetest.Tree{"a": "fine"},
				Fixes: []fixturetest.Variant{{Name: "repair", Files: fixturetest.Tree{"a": "BAD"}}},
			},
			want: []string{"produced no finding"},
		},
		{
			name: "a dodge that moves the finding fails",
			c: fixturetest.Case{
				Base: base,
				Dodges: []fixturetest.Variant{
					{Name: "rename", Files: fixturetest.Tree{"z": "BAD"}, Remove: []string{"a"}},
					{Name: "hide", Files: fixturetest.Tree{"a": "fine"}},
				},
			},
			want: []string{`dodge "rename" changed`, `dodge "hide" changed`},
		},
		{
			name: "a fix that leaves the finding fails",
			c: fixturetest.Case{
				Base:  base,
				Fixes: []fixturetest.Variant{{Name: "cosmetic", Files: fixturetest.Tree{"b": "still fine"}}},
			},
			want: []string{`fix "cosmetic" still produced`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &recorder{}
			fixturetest.Check(rec, tc.c, markerIDs)
			rec.expect(t, tc.want)
		})
	}
}

func TestRunChecksEveryCase(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	seen := map[string]bool{}
	cases := []fixturetest.Case{
		{Name: "first", Base: fixturetest.Tree{"a": "BAD"}},
		{Name: "second", Base: fixturetest.Tree{"b": "BAD"}},
	}
	// Cleanups run once the parallel subtests have finished.
	t.Cleanup(func() {
		if got := slices.Sorted(maps.Keys(seen)); !slices.Equal(got, []string{"a", "b"}) {
			t.Errorf("Run evaluated %v, want both cases", got)
		}
	})
	fixturetest.Run(t, cases, func(tree fixturetest.Tree) []string {
		ids := markerIDs(tree)
		mu.Lock()
		defer mu.Unlock()
		for _, id := range ids {
			seen[id] = true
		}

		return ids
	})
}

// markerCount is a stand-in metric: how many files hold "BAD".
func markerCount(tree fixturetest.Tree) string {
	return strconv.Itoa(len(markerIDs(tree)))
}

func TestCheckMetricReportsEachBrokenExpectation(t *testing.T) {
	t.Parallel()
	base := fixturetest.Tree{"a": "BAD", "b": "fine"}
	cases := []struct {
		name string
		c    fixturetest.MetricCase
		want []string
	}{
		{
			name: "a cosmetic edit that holds and a real change that moves pass",
			c: fixturetest.MetricCase{
				Base: base,
				Same: []fixturetest.Variant{
					{Name: "rename", Files: fixturetest.Tree{"z": "BAD"}, Remove: []string{"a"}},
				},
				Moved: []fixturetest.Variant{{Name: "second", Files: fixturetest.Tree{"b": "BAD"}}},
			},
		},
		{
			name: "a cosmetic edit that moves the metric fails",
			c: fixturetest.MetricCase{
				Base: base, Same: []fixturetest.Variant{{Name: "second", Files: fixturetest.Tree{"b": "BAD"}}},
			},
			want: []string{`"second" moved the metric: got 2, want 1`},
		},
		{
			name: "a real change the metric ignores fails",
			c: fixturetest.MetricCase{
				Base: base, Moved: []fixturetest.Variant{{Name: "comment", Files: fixturetest.Tree{"b": "still fine"}}},
			},
			want: []string{`"comment" left the metric at 1`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &recorder{}
			fixturetest.CheckMetric(rec, tc.c, markerCount)
			rec.expect(t, tc.want)
		})
	}
}

func TestRunMetricChecksEveryCase(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	seen := 0
	t.Cleanup(func() {
		if seen != 2 {
			t.Errorf("RunMetric read the metric %d times, want once per case", seen)
		}
	})
	cases := []fixturetest.MetricCase{
		{Name: "first", Base: fixturetest.Tree{"a": "BAD"}},
		{Name: "second", Base: fixturetest.Tree{"b": "BAD"}},
	}
	fixturetest.RunMetric(t, cases, func(tree fixturetest.Tree) string {
		mu.Lock()
		defer mu.Unlock()
		seen++

		return markerCount(tree)
	})
}

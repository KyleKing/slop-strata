package main

import (
	"bytes"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/kyleking/aragonite/vcs"

	"github.com/kyleking/slop-strata/internal/engine"
	"github.com/kyleking/slop-strata/internal/structure"
)

func TestHotspotTableRanksByScore(t *testing.T) {
	t.Parallel()
	res := &historyResult{Hotspots: map[string]float64{"a": 5353, "b": 35565, "c": 35565, "d": 9}}
	var out bytes.Buffer
	printHotspots(&out, res)

	var order []string
	for _, line := range strings.Split(out.String(), "\n") {
		name, _, _ := strings.Cut(line, " ")
		if slices.Contains([]string{"a", "b", "c", "d"}, name) {
			order = append(order, name)
		}
	}
	if want := []string{"b", "c", "a", "d"}; !slices.Equal(order, want) {
		t.Fatalf("hotspot order = %v, want %v (score descending, then name)", order, want)
	}
}

func TestMarkNoiseFlagsWeeksWithinOneMAD(t *testing.T) {
	t.Parallel()
	res := &historyResult{Weeks: []weekRow{{MassNet: 1}, {MassNet: 2}, {MassNet: 3}, {MassNet: 4}, {MassNet: 100}}}
	markNoise(res)

	if res.NoiseFloor != 1 {
		t.Fatalf("noise floor = %v, want the median absolute deviation 1", res.NoiseFloor)
	}
	got := make([]bool, len(res.Weeks))
	for i, wk := range res.Weeks {
		got[i] = wk.WithinNoise
	}
	if want := []bool{false, true, true, true, false}; !slices.Equal(got, want) {
		t.Fatalf("within noise = %v, want %v", got, want)
	}
}

type bodyEdit struct {
	path       string
	renameFrom string
	hunk       vcs.DiffHunk
	day        int
	unseen     bool
}

var nestedFuncs = []structure.FuncMetrics{
	{Name: "outer1", StartLine: 1, EndLine: 20},
	{Name: "wrapper", StartLine: 5, EndLine: 10},
	{Name: "outer2", StartLine: 21, EndLine: 40},
	{Name: "wrapper", StartLine: 25, EndLine: 30},
}
var inFirstWrapper, inSecondWrapper = vcs.DiffHunk{NewStart: 6, NewLines: 1}, vcs.DiffHunk{NewStart: 26, NewLines: 1}

func TestOscillationCountsBodyChangesInAnyTwoWeeks(t *testing.T) {
	t.Parallel()
	inF := vcs.DiffHunk{NewStart: 12, NewLines: 1}
	inG := vcs.DiffHunk{NewStart: 35, NewLines: 1}
	deleteInF := vcs.DiffHunk{OldStart: 12, OldLines: 1, NewStart: 15}
	three := func(hunks ...vcs.DiffHunk) []bodyEdit {
		return []bodyEdit{
			{path: "a.go", day: 0, hunk: hunks[0]},
			{path: "a.go", day: 6, hunk: hunks[1]},
			{path: "a.go", day: 14, hunk: hunks[2]},
		}
	}
	cases := []struct {
		name  string
		funcs []structure.FuncMetrics
		edits []bodyEdit
		want  oscillationResult
	}{
		{
			name: "same-named nested functions of different parents stay apart", funcs: nestedFuncs, edits: three(
				inFirstWrapper, inFirstWrapper, inSecondWrapper,
			),
		},
		{
			name: "a nested function is reported under its parent", funcs: nestedFuncs, edits: three(
				inSecondWrapper, inSecondWrapper, inSecondWrapper,
			),
			want: oscillationResult{
				Funcs: 2, Dirs: map[string]oscillationDir{".": {Funcs: 2}},
				Top: []oscillatingFunc{
					{Path: "a.go", Name: "outer2", Line: 21, Changes: 3, SpanDays: 14},
					{Path: "a.go", Name: "outer2/wrapper", Line: 25, Changes: 3, SpanDays: 14},
				},
			},
		},
		{
			name: "three edits in 14 days, one a pure deletion", edits: three(inF, deleteInF, inF),
			want: oscillationResult{
				Funcs: 1, Dirs: map[string]oscillationDir{".": {Funcs: 1}},
				Top: []oscillatingFunc{{Path: "a.go", Name: "f", Line: 10, Changes: 3, SpanDays: 14}},
			},
		},
		{name: "an edit outside the function", edits: three(inF, inG, inF)},
		{
			name: "a function new in the commit", edits: []bodyEdit{
				{path: "a.go", day: 0, hunk: inF},
				{path: "a.go", day: 1, hunk: inF},
				{path: "a.go", day: 2, hunk: inF, unseen: true},
			},
		},
		{
			name: "three edits over 15 days", edits: []bodyEdit{
				{path: "a.go", day: 0, hunk: inF},
				{path: "a.go", day: 8, hunk: inF},
				{path: "a.go", day: 15, hunk: inF},
			},
		},
		{
			name: "a rename keeps the history and a test path counts apart", edits: []bodyEdit{
				{path: "a_test.go", day: 0, hunk: inF},
				{path: "a_test.go", day: 1, hunk: inF},
				{path: "b_test.go", renameFrom: "a_test.go", day: 2, hunk: inF},
			},
			want: oscillationResult{
				TestFuncs: 1, Dirs: map[string]oscillationDir{".": {TestFuncs: 1}}, Top: []oscillatingFunc{},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			osc := newOscillation()
			funcs := tc.funcs
			if funcs == nil {
				funcs = []structure.FuncMetrics{
					{Name: "f", StartLine: 10, EndLine: 20}, {Name: "g", StartLine: 30, EndLine: 40},
				}
			}
			after := &engine.FileReport{Metrics: structure.FileMetrics{Funcs: funcs}}
			for _, e := range tc.edits {
				before := after
				if e.unseen {
					before = &engine.FileReport{}
				}
				osc.rename(e.renameFrom, e.path)
				osc.observe(&vcs.CommitFile{Path: e.path, Hunks: []vcs.DiffHunk{e.hunk}}, before, after, e.day)
			}
			got := osc.result()
			if tc.want.Dirs == nil {
				tc.want.Dirs, tc.want.Top = map[string]oscillationDir{}, []oscillatingFunc{}
			}
			if got.Funcs != tc.want.Funcs || got.TestFuncs != tc.want.TestFuncs ||
				!slices.Equal(got.Top, tc.want.Top) || len(got.Dirs) != len(tc.want.Dirs) {
				t.Fatalf("oscillation = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func BenchmarkHistoryWalk(b *testing.B) {
	repo := deterministicRepo(b)
	argv := []string{"history", "--json", "--rev", "HEAD", "--since", "2025-12-01", "--repo", repo}
	for b.Loop() {
		if err := run(b.Context(), argv, io.Discard, io.Discard); err != nil {
			b.Fatalf("history: %v", err)
		}
	}
}

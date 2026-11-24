package archtrend_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/archtrend"
)

func TestContinuesNeedsTwoSharedFiles(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		a, b []string
		want bool
	}{
		{name: "the same cycle", a: []string{"a", "b"}, b: []string{"a", "b"}, want: true},
		{name: "grown", a: []string{"a", "b"}, b: []string{"a", "b", "c"}, want: true},
		{name: "shrunk", a: []string{"a", "b", "c"}, b: []string{"b", "c"}, want: true},
		{name: "one file left and one joined", a: []string{"a", "b", "c"}, b: []string{"b", "c", "d"}, want: true},
		{name: "one file shared", a: []string{"a", "b"}, b: []string{"b", "c"}, want: false},
		{name: "nothing shared", a: []string{"a", "b"}, b: []string{"c", "d"}, want: false},
		{name: "empty", a: nil, b: []string{"a", "b"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := archtrend.Continues(tc.a, tc.b); got != tc.want {
				t.Errorf("Continues(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
			if got := archtrend.Continues(tc.b, tc.a); got != tc.want {
				t.Errorf("Continues(%v, %v) = %v, want %v", tc.b, tc.a, got, tc.want)
			}
		})
	}
}

// step is one sample of a tracking case: the renames since the sample before,
// then the cycles of the sample.
type step struct {
	renames [][2]string
	cycles  [][]string
}

// describe renders a sample's tracks as "members@first/firstSize".
func describe(tracks []*archtrend.Track) string {
	parts := make([]string, 0, len(tracks))
	for _, tr := range tracks {
		parts = append(parts, strings.Join(tr.Members, "+")+"@"+strconv.Itoa(tr.First)+"/"+
			strconv.Itoa(len(tr.FirstMembers)))
	}

	return strings.Join(parts, " ")
}

func TestObserveFollowsACycleAcrossSamples(t *testing.T) {
	t.Parallel()
	ab, cd := []string{"a", "b"}, []string{"c", "d"}
	cases := []struct {
		name  string
		want  string
		steps []step
	}{
		{name: "unchanged keeps its first sample", steps: []step{
			{cycles: [][]string{ab}}, {cycles: [][]string{ab}},
		}, want: "a+b@0/2"},
		{name: "grown keeps its first sample and size", steps: []step{
			{cycles: [][]string{ab}}, {cycles: [][]string{{"a", "b", "c"}}},
		}, want: "a+b+c@0/2"},
		{name: "shrunk keeps its first sample", steps: []step{
			{cycles: [][]string{{"a", "b", "c"}}}, {cycles: [][]string{ab}},
		}, want: "a+b@0/3"},
		{name: "a new cycle starts at its sample", steps: []step{
			{cycles: [][]string{ab}}, {cycles: [][]string{ab, cd}},
		}, want: "a+b@0/2 c+d@1/2"},
		{name: "a merge inherits the older cycle", steps: []step{
			{cycles: [][]string{cd}}, {cycles: [][]string{ab, cd}}, {cycles: [][]string{{"a", "b", "c", "d"}}},
		}, want: "a+b+c+d@0/2"},
		{name: "a split gives both halves the first sample", steps: []step{
			{cycles: [][]string{{"a", "b", "c", "d"}}}, {cycles: [][]string{ab, cd}},
		}, want: "a+b@0/4 c+d@0/4"},
		{name: "gone and back starts again", steps: []step{
			{cycles: [][]string{ab}}, {cycles: nil}, {cycles: [][]string{ab}},
		}, want: "a+b@2/2"},
		{name: "a renamed member keeps the cycle", steps: []step{
			{cycles: [][]string{ab}}, {renames: [][2]string{{"b", "z"}}, cycles: [][]string{{"a", "z"}}},
		}, want: "a+z@0/2"},
		{name: "both members renamed keeps the cycle", steps: []step{
			{cycles: [][]string{ab}},
			{renames: [][2]string{{"a", "y"}, {"b", "z"}}, cycles: [][]string{{"y", "z"}}},
		}, want: "y+z@0/2"},
		{name: "without the rename it is a new cycle", steps: []step{
			{cycles: [][]string{ab}}, {cycles: [][]string{{"a", "z"}}},
		}, want: "a+z@1/2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var tracker archtrend.Cycles
			var last []*archtrend.Track
			for i, s := range tc.steps {
				for _, r := range s.renames {
					tracker.Rename(r[0], r[1])
				}
				last = tracker.Observe(i, s.cycles)
			}
			if got := describe(last); got != tc.want {
				t.Errorf("tracks = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestObserveNumbersAnOriginOnceAndKeepsIt(t *testing.T) {
	t.Parallel()
	var tracker archtrend.Cycles
	first := tracker.Observe(0, [][]string{{"a", "b"}, {"c", "d"}})
	next := tracker.Observe(1, [][]string{{"a", "b", "e"}, {"c", "d"}, {"x", "y"}})
	origins := []int{next[0].Origin, next[1].Origin, next[2].Origin}
	if first[0].Origin == first[1].Origin || origins[0] != first[0].Origin || origins[1] != first[1].Origin ||
		slices.Contains(origins[:2], origins[2]) {
		t.Errorf("origins = %v after %d and %d, want the first two kept and a new third",
			origins, first[0].Origin, first[1].Origin)
	}
}

func TestRenamedKeepsMembersSortedAndUnshared(t *testing.T) {
	t.Parallel()
	members := []string{"a", "m", "z"}
	if got := archtrend.Renamed(members, "a", "q"); !slices.Equal(got, []string{"m", "q", "z"}) {
		t.Errorf("Renamed = %v, want [m q z]", got)
	}
	if !slices.Equal(members, []string{"a", "m", "z"}) {
		t.Errorf("Renamed changed its argument to %v", members)
	}
	if got := archtrend.Renamed(members, "b", "q"); !slices.Equal(got, members) {
		t.Errorf("Renamed of a non-member = %v, want %v", got, members)
	}
}

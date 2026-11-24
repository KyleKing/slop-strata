package depgraph_test

import (
	"path"
	"reflect"
	"slices"
	"testing"

	"github.com/kyleking/slop-strata/internal/depgraph"
)

// edge builds a runtime edge importing the given names.
func edge(from, to string, names ...string) depgraph.Edge {
	return depgraph.Edge{From: from, To: to, Kind: depgraph.Runtime, Names: names, Line: 1}
}

func TestNewMergesEdgesAndDropsSelfImports(t *testing.T) {
	t.Parallel()
	g := depgraph.New([]string{"lonely"}, []depgraph.Edge{
		{From: "b", To: "a", Kind: depgraph.Runtime, Names: []string{"Y", "X"}, Line: 9},
		{From: "b", To: "a", Kind: depgraph.Runtime, Names: []string{"X", "Z"}, Line: 3},
		{From: "b", To: "a", Kind: depgraph.TypeOnly, Names: []string{"T"}, Line: 5},
		{From: "a", To: "a", Kind: depgraph.Runtime, Line: 1},
	})
	if want := []string{"a", "b", "lonely"}; !slices.Equal(g.Nodes(), want) {
		t.Errorf("Nodes() = %v, want %v", g.Nodes(), want)
	}
	want := []depgraph.Edge{
		{From: "b", To: "a", Kind: depgraph.Runtime, Names: []string{"X", "Y", "Z"}, Line: 3},
		{From: "b", To: "a", Kind: depgraph.TypeOnly, Names: []string{"T"}, Line: 5},
	}
	if !reflect.DeepEqual(g.Edges(), want) {
		t.Errorf("Edges() = %+v, want %+v", g.Edges(), want)
	}
}

func TestEdgeCostCountsNamesWithAFloorOfOne(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		names []string
		want  int
	}{
		{name: "side-effect import", want: 1},
		{name: "one name", names: []string{"A"}, want: 1},
		{name: "three names", names: []string{"A", "B", "C"}, want: 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := edge("a", "b", tc.names...).Cost(); got != tc.want {
				t.Errorf("Cost() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestSCCs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		edges []depgraph.Edge
		want  [][]string
	}{
		{name: "no edges"},
		{name: "a chain has none", edges: []depgraph.Edge{edge("a", "b"), edge("b", "c")}},
		{name: "a pair", edges: []depgraph.Edge{edge("a", "b"), edge("b", "a")}, want: [][]string{{"a", "b"}}},
		{
			name:  "a triangle with a tail",
			edges: []depgraph.Edge{edge("a", "b"), edge("b", "c"), edge("c", "a"), edge("c", "d")},
			want:  [][]string{{"a", "b", "c"}},
		},
		{
			name: "two components joined one way stay apart, ordered by first member",
			edges: []depgraph.Edge{
				edge("x", "y"), edge("y", "x"), edge("y", "b"), edge("b", "c"), edge("c", "b"),
			},
			want: [][]string{{"b", "c"}, {"x", "y"}},
		},
		{
			name: "two cycles sharing a file are one component",
			edges: []depgraph.Edge{
				edge("hub", "a"), edge("a", "hub"), edge("hub", "b"), edge("b", "hub"),
			},
			want: [][]string{{"a", "b", "hub"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := depgraph.New(nil, tc.edges).SCCs()
			if len(got) != len(tc.want) || (len(got) > 0 && !reflect.DeepEqual(got, tc.want)) {
				t.Errorf("SCCs() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestShortestCycle(t *testing.T) {
	t.Parallel()
	// a -> b -> c -> a is the long way round, and b -> a closes a short one.
	g := depgraph.New(nil, []depgraph.Edge{
		edge("a", "b"), edge("b", "c"), edge("c", "a"), edge("b", "a"), edge("c", "d"),
	})
	cases := []struct {
		name     string
		from, to string
		want     []string
	}{
		{name: "through the short back edge", from: "b", to: "a", want: []string{"a", "b"}},
		{name: "through an edge only the long cycle uses", from: "c", to: "a", want: []string{"a", "b", "c"}},
		{name: "through a shared edge takes the short cycle", from: "a", to: "b", want: []string{"b", "a"}},
		{name: "an edge out of the component closes nothing", from: "c", to: "d"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := g.ShortestCycle(tc.from, tc.to); !slices.Equal(got, tc.want) {
				t.Errorf("ShortestCycle(%s, %s) = %v, want %v", tc.from, tc.to, got, tc.want)
			}
		})
	}
}

func TestCyclesPickTheCheapestCut(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		edges    []depgraph.Edge
		wantCut  [2]string
		wantPath []string
		members  int
	}{
		{
			name:    "the edge importing fewest names",
			edges:   []depgraph.Edge{edge("a", "b", "One", "Two", "Three"), edge("b", "a", "Foo")},
			wantCut: [2]string{"b", "a"}, wantPath: []string{"a", "b"}, members: 2,
		},
		{
			name:    "a tie goes to the first edge by importer",
			edges:   []depgraph.Edge{edge("b", "a", "Foo"), edge("a", "b", "Bar")},
			wantCut: [2]string{"a", "b"}, wantPath: []string{"b", "a"}, members: 2,
		},
		{
			name: "a large component reports the short cycle through the cut",
			edges: []depgraph.Edge{
				edge("a", "b", "p", "q"), edge("b", "c", "p", "q"), edge("c", "d", "p", "q"), edge("d", "a", "p", "q"),
				edge("c", "b", "only"),
			},
			wantCut: [2]string{"c", "b"}, wantPath: []string{"b", "c"}, members: 4,
		},
		{
			name: "an edge leaving the component is never the cut",
			edges: []depgraph.Edge{
				edge("a", "b", "p", "q"), edge("b", "a", "p", "q"), edge("a", "out", "x"),
			},
			wantCut: [2]string{"a", "b"}, wantPath: []string{"b", "a"}, members: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cycles := depgraph.New(nil, tc.edges).Cycles()
			if len(cycles) != 1 {
				t.Fatalf("Cycles() found %d, want 1: %+v", len(cycles), cycles)
			}
			c := cycles[0]
			if got := [2]string{c.Cut.From, c.Cut.To}; got != tc.wantCut {
				t.Errorf("cut = %v, want %v", got, tc.wantCut)
			}
			if !slices.Equal(c.Path, tc.wantPath) || len(c.Members) != tc.members {
				t.Errorf("path = %v with %d members, want %v with %d", c.Path, len(c.Members), tc.wantPath, tc.members)
			}
		})
	}
}

func TestFilterKeepsTheFilesAndDropsEdges(t *testing.T) {
	t.Parallel()
	g := depgraph.New(nil, []depgraph.Edge{
		edge("a", "b"), {From: "b", To: "a", Kind: depgraph.Deferred, Line: 2},
	})
	runtime := g.Filter(func(e depgraph.Edge) bool { return e.Kind == depgraph.Runtime })
	if len(g.SCCs()) != 1 || len(runtime.SCCs()) != 0 {
		t.Errorf("a deferred back edge must close a cycle only before the filter: %v, %v", g.SCCs(), runtime.SCCs())
	}
	if !slices.Equal(runtime.Nodes(), g.Nodes()) {
		t.Errorf("Filter dropped files: %v", runtime.Nodes())
	}
}

func TestFansCountDistinctOtherGroups(t *testing.T) {
	t.Parallel()
	g := depgraph.New([]string{"docs/alone.py"}, []depgraph.Edge{
		edge("cmd/main.go", "internal/a/x.go"), edge("cmd/main.go", "internal/a/y.go"),
		edge("cmd/main.go", "internal/b/z.go"), edge("internal/b/z.go", "internal/a/x.go"),
		edge("internal/a/x.go", "internal/a/y.go"),
	})
	want := map[string]depgraph.Fan{
		"cmd":        {In: 0, Out: 2},
		"internal/a": {In: 2, Out: 0},
		"internal/b": {In: 1, Out: 1},
		"docs":       {},
	}
	if got := g.Fans(path.Dir); !reflect.DeepEqual(got, want) {
		t.Errorf("Fans() = %+v, want %+v", got, want)
	}
}

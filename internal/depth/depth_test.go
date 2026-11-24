package depth_test

import (
	"path"
	"reflect"
	"slices"
	"testing"

	"github.com/kyleking/slop-strata/internal/depgraph"
	"github.com/kyleking/slop-strata/internal/depth"
)

// want is what a test expects of one module: counts, masses, and depth, with
// -1 standing for no depth.
type want struct {
	widest     string
	shown      int
	byName     int
	hidden     int
	fwd        int
	shownMass  float64
	hiddenMass float64
	depth      float64
	byMass     bool
}

func check(t *testing.T, got *depth.Module, w want) {
	t.Helper()
	if got == nil {
		t.Fatalf("no module, want %+v", w)
	}
	ratio := -1.0
	if got.Depth != nil {
		ratio = *got.Depth
	}
	have := want{
		widest: got.WidestFile, shown: got.Shown, byName: got.ShownByName, hidden: got.Hidden, fwd: got.Forwarders,
		shownMass: got.ShownMass, hiddenMass: got.HiddenMass, depth: ratio, byMass: got.ByMass,
	}
	if have != w {
		t.Errorf("module = %+v, want %+v", have, w)
	}
}

func use(from, to, name string) depgraph.Use { return depgraph.Use{From: from, To: to, Name: name} }

func TestComputeCountsWhatOtherGroupsUse(t *testing.T) {
	t.Parallel()
	engine := depth.File{Path: "lib/engine.go", Units: []depth.Unit{
		{Name: "Run", Mass: 2}, {Name: "step", Mass: 6}, {Name: "Limit"}, {Mass: 1.5},
	}}
	cases := []struct {
		want  map[string]want
		name  string
		files []depth.File
		uses  []depgraph.Use
	}{
		{
			name:  "a use from another group shows the name, by mass",
			files: []depth.File{engine, {Path: "app/main.go"}},
			uses:  []depgraph.Use{use("app/main.go", "lib/engine.go", "Run")},
			want: map[string]want{
				"lib": {
					shown: 1, hidden: 2, shownMass: 2, hiddenMass: 7.5, depth: 0.7895, byMass: true,
					widest: "lib/engine.go",
				},
				"app": {depth: -1},
			},
		},
		{
			name:  "a use inside the group shows nothing",
			files: []depth.File{engine, {Path: "lib/api.go"}},
			uses:  []depgraph.Use{use("lib/api.go", "lib/engine.go", "Run")},
			want:  map[string]want{"lib": {hidden: 3, hiddenMass: 9.5, depth: -1}},
		},
		{
			name:  "a use from a file that is not listed, such as a test, shows nothing",
			files: []depth.File{engine},
			uses:  []depgraph.Use{use("app/main_test.go", "lib/engine.go", "Run")},
			want:  map[string]want{"lib": {hidden: 3, hiddenMass: 9.5, depth: -1}},
		},
		{
			name: "a parent and a subdirectory are other groups",
			files: []depth.File{
				{Path: "lib/a.go", Units: []depth.Unit{{Name: "A", Mass: 1}}},
				{Path: "lib/sub/b.go", Units: []depth.Unit{{Name: "B", Mass: 1}, {Name: "b", Mass: 3}}},
			},
			uses: []depgraph.Use{use("lib/a.go", "lib/sub/b.go", "B"), use("lib/sub/b.go", "lib/a.go", "A")},
			want: map[string]want{
				"lib": {shown: 1, shownMass: 1, depth: 0, byMass: true, widest: "lib/a.go"},
				"lib/sub": {
					shown: 1, hidden: 1, shownMass: 1, hiddenMass: 3, depth: 0.75, byMass: true, widest: "lib/sub/b.go",
				},
			},
		},
		{
			name: "names with no mass on either side compare by count",
			files: []depth.File{
				{Path: "lib/k.go", Units: []depth.Unit{{Name: "A"}, {Name: "B"}, {Name: "c"}, {Name: "d"}}},
				{Path: "app/main.go"},
			},
			uses: []depgraph.Use{use("app/main.go", "lib/k.go", "A")},
			want: map[string]want{"lib": {shown: 1, hidden: 3, depth: 0.75, widest: "lib/k.go"}},
		},
		{
			name: "the widest file is the one with the most shown names",
			files: []depth.File{
				{Path: "lib/a.go", Units: []depth.Unit{{Name: "A", Mass: 1}}},
				{Path: "lib/b.go", Units: []depth.Unit{
					{Name: "B", Mass: 1}, {Name: "C", Mass: 1}, {Name: "d", Mass: 1},
				}},
				{Path: "app/main.go"},
			},
			uses: []depgraph.Use{
				use("app/main.go", "lib/a.go", "A"), use("app/main.go", "lib/b.go", "B"),
				use("app/main.go", "lib/b.go", "C"),
			},
			want: map[string]want{
				"lib": {
					shown: 3, hidden: 1, shownMass: 3, hiddenMass: 1, depth: 0.25, byMass: true, widest: "lib/b.go",
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := depth.Compute(tc.files, tc.uses, nil, path.Dir)
			for dir, w := range tc.want {
				check(t, got[dir], w)
			}
		})
	}
}

func TestComputeShowsAMethodByNameOnceTheGroupIsImported(t *testing.T) {
	t.Parallel()
	store := depth.File{Path: "lib/store.go", Members: []string{"flush"}, Units: []depth.Unit{
		{Name: "Store", Mass: 1},
		{Name: "Store.Get", Mass: 2},
		{Name: "Store.flush", Mass: 4, Sealed: true},
		{Name: "Store.Size", Mass: 1},
	}}
	caller := depth.File{Path: "app/main.go", Members: []string{"Get", "flush"}}
	edge := []depgraph.Edge{{From: "app/main.go", To: "lib/store.go"}}

	guessed := depth.Compute([]depth.File{store, caller}, nil, edge, path.Dir)["lib"]
	check(t, guessed, want{
		shown: 1, byName: 1, hidden: 3, shownMass: 2, hiddenMass: 6, depth: 0.75, byMass: true, widest: "lib/store.go",
	})
	if len(guessed.ShownNames) != 0 || !slices.Equal(guessed.NameMatched, []string{"Store.Get"}) {
		t.Errorf("attributed %v, by name %v, want only Store.Get, by name", guessed.ShownNames, guessed.NameMatched)
	}
	if guessed.DepthAttributed != nil || guessed.ShownMassByName != 2 {
		t.Errorf("module = %+v, want no attributed depth and the method's mass by name", guessed)
	}

	uses := []depgraph.Use{use("app/main.go", "lib/store.go", "Store")}
	named := depth.Compute([]depth.File{store, caller}, uses, edge, path.Dir)["lib"]
	check(t, named, want{
		shown: 2, byName: 1, hidden: 2, shownMass: 3, hiddenMass: 5, depth: 0.625, byMass: true, widest: "lib/store.go",
	})
	if named.DepthAttributed == nil || *named.DepthAttributed != 0.875 {
		t.Errorf("attributed depth = %v, want 0.875: the method counted as hidden", named.DepthAttributed)
	}

	unimported := depth.Compute([]depth.File{store, caller}, nil, nil, path.Dir)
	check(t, unimported["lib"], want{hidden: 4, hiddenMass: 8, depth: -1})
}

func TestComputeLeavesForwardersOutAndFollowsThem(t *testing.T) {
	t.Parallel()
	api := depth.File{Path: "lib/api.go", Units: []depth.Unit{
		{Name: "Load", Forwards: "load", Mass: 1},
		{Name: "load", Forwards: "read", Mass: 1},
		{Name: "read", Mass: 5},
		{Name: "Elsewhere", Forwards: "*", Mass: 1},
		{Name: "T.Get", Forwards: ".get", Mass: 1},
		{Name: "T.get", Mass: 3},
		{Name: "U.get", Mass: 2},
		{Name: "Stray", Forwards: ".get", Mass: 1},
		{Name: "unused", Forwards: "read", Mass: 1},
	}}
	caller := depth.File{Path: "app/main.go", Members: []string{"Get"}}
	uses := []depgraph.Use{
		use("app/main.go", "lib/api.go", "Load"), use("app/main.go", "lib/api.go", "Elsewhere"),
		use("app/main.go", "lib/api.go", "Stray"),
	}
	got := depth.Compute([]depth.File{api, caller}, uses, nil, path.Dir)["lib"]
	check(t, got, want{
		shown: 2, byName: 1, hidden: 1, fwd: 6, shownMass: 8, hiddenMass: 2, depth: 0.2, byMass: true,
		widest: "lib/api.go",
	})
	if !slices.Equal(got.ShownNames, []string{"read"}) || !slices.Equal(got.NameMatched, []string{"T.get"}) {
		t.Errorf("attributed %v, by name %v, want read attributed and T.get by name", got.ShownNames, got.NameMatched)
	}
}

func TestComputeMergesRepeatedDeclarationsOfOneName(t *testing.T) {
	t.Parallel()
	file := depth.File{Path: "lib/a.py", Units: []depth.Unit{
		{Name: "C.value", Forwards: "*", Mass: 1},
		{Name: "C.value", Mass: 2},
		{Name: "over", Mass: 1},
		{Name: "over", Mass: 1},
	}}
	uses := []depgraph.Use{use("app/b.py", "lib/a.py", "over")}
	got := depth.Compute([]depth.File{file, {Path: "app/b.py"}}, uses, nil, path.Dir)
	check(t, got["lib"], want{
		shown: 1, hidden: 1, shownMass: 2, hiddenMass: 3, depth: 0.6, byMass: true, widest: "lib/a.py",
	})
}

func TestComputeMarksAThinModuleProvisional(t *testing.T) {
	t.Parallel()
	uses := []depgraph.Use{use("app/main.go", "lib/a.go", "A")}
	for names, want := range map[int]bool{4: true, 5: false} {
		units := make([]depth.Unit, 0, names)
		for _, name := range []string{"A", "b", "c", "d", "e"}[:names] {
			units = append(units, depth.Unit{Name: name, Mass: 1})
		}
		files := []depth.File{{Path: "lib/a.go", Units: units}, {Path: "app/main.go"}}
		if got := depth.Compute(files, uses, nil, path.Dir)["lib"]; got.Provisional != want {
			t.Errorf("%d names: provisional = %v, want %v", names, got.Provisional, want)
		}
	}
}

func ratio(v float64) *float64 { return &v }

func TestWidenedListsGrownSurfaces(t *testing.T) {
	t.Parallel()
	before := map[string]*depth.Module{
		"lib":     {Shown: 1, ShownNames: []string{"A"}, DepthAttributed: ratio(0.9), Depth: ratio(0.8)},
		"renamed": {Shown: 1, ShownNames: []string{"Old"}},
		"shrunk":  {Shown: 2, ShownNames: []string{"A", "B"}},
		"wide":    {Shown: 0},
		"guessed": {Shown: 1, ShownNames: []string{"T"}},
	}
	after := map[string]*depth.Module{
		"lib": {
			Shown: 4, ShownByName: 2, ShownNames: []string{"A", "B"}, NameMatched: []string{"T.get", "T.put"},
			DepthAttributed: ratio(0.7), Depth: ratio(0.5),
		},
		"renamed": {Shown: 1, ShownNames: []string{"New"}},
		"shrunk":  {Shown: 1, ShownNames: []string{"A"}},
		"created": {Shown: 3, ShownNames: []string{"X", "Y", "Z"}},
		"wide":    {Shown: 7, ShownNames: []string{"a", "b", "c", "d", "e", "f", "g"}, DepthAttributed: ratio(0.1)},
		"guessed": {Shown: 3, ShownByName: 2, ShownNames: []string{"T"}, NameMatched: []string{"T.get", "T.put"}},
	}
	want := []depth.Widening{
		{
			Dir: "lib", NewlyShown: []string{"B"}, NewlyShownCount: 1, NewlyShownByName: 2,
			ShownBefore: 1, ShownAfter: 2, DepthBefore: ratio(0.9), DepthAfter: ratio(0.7),
		},
		{
			Dir: "wide", NewlyShown: []string{"a", "b", "c", "d", "e"}, NewlyShownCount: 7, ShownAfter: 7,
			DepthAfter: ratio(0.1),
		},
	}
	if got := depth.Widened(before, after); !reflect.DeepEqual(got, want) {
		t.Errorf("widened = %+v, want lib and wide: names matched by name open no row and are only counted", got)
	}
	if got := depth.Widened(before, before); got == nil || len(got) != 0 {
		t.Errorf("an unchanged tree widened %v, want an empty list", got)
	}
}

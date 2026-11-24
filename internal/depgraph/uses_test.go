package depgraph_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/depgraph"
)

// declares answers a set of declared names, each visible to other files
// unless it starts with an underscore.
func declares(names ...string) map[string]bool {
	out := map[string]bool{}
	for _, n := range names {
		out[n] = !strings.HasPrefix(n, "_")
	}

	return out
}

// usesOf renders a graph's uses as "from -> to name" lines.
func usesOf(g *depgraph.Graph) []string {
	out := make([]string, 0, len(g.Uses()))
	for _, u := range g.Uses() {
		out = append(out, u.From+" -> "+u.To+" "+u.Name)
	}

	return out
}

type usesCase struct {
	name  string
	files []depgraph.File
	want  []string
}

func runUsesCases(t *testing.T, cases []usesCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := usesOf(depgraph.Build(tc.files, nil))
			if !slices.Equal(got, tc.want) {
				t.Errorf("uses =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

func TestUsesAttributePythonNamesToTheDeclaringFile(t *testing.T) {
	t.Parallel()
	impl := depgraph.File{Path: "pkg/impl.py", Declared: declares("helper", "_private", "other")}
	runUsesCases(t, []usesCase{
		{
			name: "a named import is a use, by any visibility, unless the importer never uses it",
			files: []depgraph.File{
				file("pkg/__init__.py"), impl,
				{
					Path: "app/main.py", Passed: map[string]bool{"other": true},
					Imports: []depgraph.Import{imp("pkg.impl", "helper", "_private", "other", "missing")},
				},
			},
			want: []string{"app/main.py -> pkg/impl.py _private", "app/main.py -> pkg/impl.py helper"},
		},
		{
			name: "an alias is checked by its local name and reported by the declared one",
			files: []depgraph.File{
				file("pkg/__init__.py"), impl,
				{
					Path: "app/main.py", Passed: map[string]bool{"helper": true, "o": true},
					Imports: []depgraph.Import{{
						Spec: "pkg.impl", Names: []string{"helper", "other"},
						Aliases: map[string]string{"h": "helper", "o": "other"},
					}},
				},
			},
			want: []string{"app/main.py -> pkg/impl.py helper"},
		},
		{
			name: "a re-export is followed to the declaration, and is no use by itself",
			files: []depgraph.File{
				{
					Path: "pkg/__init__.py", Passed: map[string]bool{"aid": true, "other": true},
					Imports: []depgraph.Import{{
						Spec: ".impl", Names: []string{"helper", "other"}, Aliases: map[string]string{"aid": "helper"},
					}},
				},
				impl,
				{Path: "app/main.py", Imports: []depgraph.Import{imp("pkg", "aid")}},
			},
			want: []string{"app/main.py -> pkg/impl.py helper"},
		},
		{
			name: "a wildcard re-export is followed, and a wildcard import uses the visible names it mentions",
			files: []depgraph.File{
				{Path: "pkg/__init__.py", Imports: []depgraph.Import{imp(".impl", "*")}},
				impl,
				{Path: "app/main.py", Imports: []depgraph.Import{imp("pkg", "helper")}},
				{
					Path: "app/star.py", Referenced: map[string]int{"other": 1, "_private": 1},
					Imports: []depgraph.Import{imp("pkg.impl", "*")},
				},
			},
			want: []string{"app/main.py -> pkg/impl.py helper", "app/star.py -> pkg/impl.py other"},
		},
		{
			name: "a bound module is used through the names read off it",
			files: []depgraph.File{
				file("pkg/__init__.py"), impl,
				{
					Path: "app/a.py", Selected: map[string][]string{"pkg": {"_private", "impl"}},
					Referenced: map[string]int{"helper": 3},
					Imports:    []depgraph.Import{{Spec: "pkg.impl", Local: "pkg"}},
				},
				{
					Path: "app/b.py", Selected: map[string][]string{"m": {"helper"}},
					Imports: []depgraph.Import{
						{Spec: "pkg", Names: []string{"impl"}, Aliases: map[string]string{"m": "impl"}},
					},
				},
				{
					Path: "app/c.py", Referenced: map[string]int{"helper": 1},
					Imports: []depgraph.Import{{Spec: "pkg.impl", Local: "pkg"}},
				},
			},
			want: []string{"app/a.py -> pkg/impl.py _private", "app/b.py -> pkg/impl.py helper"},
		},
	})
}

func TestUsesAttributeTypeScriptNamesToTheDeclaringFile(t *testing.T) {
	t.Parallel()
	engine := depgraph.File{Path: "src/store/engine.ts", Declared: declares("parse", "clean"), Default: "parse"}
	runUsesCases(t, []usesCase{
		{
			name: "a default import names the default export",
			files: []depgraph.File{engine, {
				Path: "src/app.ts", Imports: []depgraph.Import{{
					Spec: "./store/engine", Names: []string{"default"}, Aliases: map[string]string{"run": "default"},
				}},
			}},
			want: []string{"src/app.ts -> src/store/engine.ts parse"},
		},
		{
			name: "a namespace import is used through the names read off it",
			files: []depgraph.File{engine, {
				Path: "src/app.ts", Selected: map[string][]string{"engine": {"clean"}},
				Imports: []depgraph.Import{{Spec: "./store/engine", Names: []string{"*"}, Local: "engine"}},
			}},
			want: []string{"src/app.ts -> src/store/engine.ts clean"},
		},
		{
			name: "a barrel is followed through a star, a renamed re-export, and an export alias",
			files: []depgraph.File{
				engine,
				{Path: "src/store/text.ts", Declared: declares("trim"), ExportAs: map[string]string{"strip": "trim"}},
				{
					Path: "src/store/index.ts", Passed: map[string]bool{"tidy": true, "strip": true},
					Imports: []depgraph.Import{
						imp("./engine", "*"),
						{Spec: "./engine", Names: []string{"clean"}, Aliases: map[string]string{"tidy": "clean"}},
						imp("./text", "strip"),
					},
				},
				{Path: "src/app.ts", Imports: []depgraph.Import{imp("./store", "parse", "tidy", "strip", "absent")}},
			},
			want: []string{
				"src/app.ts -> src/store/engine.ts clean", "src/app.ts -> src/store/engine.ts parse",
				"src/app.ts -> src/store/text.ts trim",
			},
		},
		{
			name: "a side-effect import and a barrel nobody consumes use nothing",
			files: []depgraph.File{
				engine,
				{Path: "src/index.ts", Imports: []depgraph.Import{imp("./store/engine", "*")}},
				{
					Path: "src/app.ts", Referenced: map[string]int{"parse": 2},
					Imports: []depgraph.Import{imp("./store/engine")},
				},
			},
		},
		{
			name: "re-exports that point at each other end",
			files: []depgraph.File{
				{Path: "src/a.ts", Imports: []depgraph.Import{imp("./b", "*")}},
				{Path: "src/b.ts", Imports: []depgraph.Import{imp("./a", "*")}},
				{Path: "src/app.ts", Imports: []depgraph.Import{imp("./a", "lost")}},
			},
		},
	})
}

func TestUsesAttributeGoNamesToTheDeclaringFile(t *testing.T) {
	t.Parallel()
	mod := depgraph.File{Path: "go.mod", GoModule: "example.com/app"}
	api := depgraph.File{Path: "store/api.go", Declared: map[string]bool{"Load": true, "Kind": true, "parse": false}}
	other := depgraph.File{Path: "cache/cache.go", Declared: map[string]bool{"Load": true, "New": true}}
	runUsesCases(t, []usesCase{
		{
			name: "a selector through the package name picks the package, not a same-named symbol elsewhere",
			files: []depgraph.File{mod, api, other, {
				Path: "cmd/app/main.go", Referenced: map[string]int{"Load": 2, "New": 1, "Kind": 1},
				Selected: map[string][]string{"store": {"Load"}, "c": {"New"}},
				Imports: []depgraph.Import{
					{Spec: "example.com/app/store", Local: "store"}, {Spec: "example.com/app/cache", Local: "c"},
				},
			}},
			want: []string{"cmd/app/main.go -> cache/cache.go New", "cmd/app/main.go -> store/api.go Load"},
		},
		{
			name: "a dot import and a package named unlike its directory fall back to visible names mentioned",
			files: []depgraph.File{mod, api, other, {
				Path: "cmd/app/main.go", Referenced: map[string]int{"Load": 1, "parse": 1, "New": 1},
				Selected: map[string][]string{"kv": {"New"}},
				Imports: []depgraph.Import{
					{Spec: "example.com/app/store", Local: "."}, {Spec: "example.com/app/cache", Local: "cache"},
				},
			}},
			want: []string{
				"cmd/app/main.go -> cache/cache.go Load", "cmd/app/main.go -> cache/cache.go New",
				"cmd/app/main.go -> store/api.go Load",
			},
		},
		{
			name: "a blank import uses nothing",
			files: []depgraph.File{mod, api, {
				Path: "cmd/app/main.go", Referenced: map[string]int{"Load": 1},
				Imports: []depgraph.Import{{Spec: "example.com/app/store", Local: "_"}},
			}},
		},
	})
}

func TestUsesAreEmptyOnAGraphBuiltByHand(t *testing.T) {
	t.Parallel()
	g := depgraph.New([]string{"a", "b"}, []depgraph.Edge{{From: "a", To: "b", Kind: depgraph.Runtime}})
	if got := g.Uses(); len(got) != 0 {
		t.Errorf("Uses() = %v, want none", got)
	}
}

func TestStarredFilesAreTheOnesBoundWholeAndUnnamed(t *testing.T) {
	t.Parallel()
	star := []string{"*"}
	cases := []struct {
		name     string
		importer depgraph.File
		want     bool
	}{
		{name: "a python star import", want: true, importer: depgraph.File{
			Path: "pkg/__init__.py", Imports: []depgraph.Import{{Spec: ".impl", Names: star}},
		}},
		{name: "a typescript re-export of everything", want: true, importer: depgraph.File{
			Path: "pkg/index.ts", Imports: []depgraph.Import{{Spec: "./impl", Names: star}},
		}},
		{name: "a namespace import keeps the names behind its own", importer: depgraph.File{
			Path: "pkg/index.ts", Imports: []depgraph.Import{{Spec: "./impl", Names: star, Local: "impl"}},
		}},
		{name: "a named import", importer: depgraph.File{
			Path: "pkg/index.ts", Imports: []depgraph.Import{{Spec: "./impl", Names: []string{"helper"}}},
		}},
		{name: "a module import", importer: depgraph.File{
			Path: "pkg/__init__.py", Imports: []depgraph.Import{{Spec: "pkg.impl", Local: "pkg"}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			target := "pkg/impl.py"
			if strings.HasSuffix(tc.importer.Path, ".ts") {
				target = "pkg/impl.ts"
			}
			g := depgraph.Build([]depgraph.File{tc.importer, {Path: target, Declared: declares("helper")}}, nil)
			if got := g.Starred(target); got != tc.want {
				t.Errorf("Starred(%s) = %v, want %v", target, got, tc.want)
			}
			if g.Starred(tc.importer.Path) {
				t.Errorf("the importer %s reads as starred", tc.importer.Path)
			}
		})
	}
}

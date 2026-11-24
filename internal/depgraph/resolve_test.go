package depgraph_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/depgraph"
)

// imp is a runtime import of names from a specifier.
func imp(spec string, names ...string) depgraph.Import {
	return depgraph.Import{Spec: spec, Kind: depgraph.Runtime, Names: names, Line: 1}
}

// file is a tree file with its imports.
func file(path string, imports ...depgraph.Import) depgraph.File {
	return depgraph.File{Path: path, Imports: imports}
}

// edgesOf renders a graph's edges as "from -> to kind [names]" lines.
func edgesOf(g *depgraph.Graph) []string {
	out := make([]string, 0, len(g.Edges()))
	for _, e := range g.Edges() {
		line := fmt.Sprintf("%s -> %s %s", e.From, e.To, e.Kind)
		if len(e.Names) > 0 {
			line += " [" + strings.Join(e.Names, ",") + "]"
		}
		out = append(out, line)
	}

	return out
}

type resolveCase struct {
	name  string
	files []depgraph.File
	extra []string // tree paths with no fingerprint
	want  []string
}

func runResolveCases(t *testing.T, cases []resolveCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := edgesOf(depgraph.Build(tc.files, tc.extra))
			if !slices.Equal(got, tc.want) {
				t.Errorf("edges =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

func TestBuildResolvesPythonImports(t *testing.T) {
	t.Parallel()
	runResolveCases(t, []resolveCase{
		{
			name: "absolute import of a module and of a name from a package",
			files: []depgraph.File{
				file("pkg/__init__.py"), file("pkg/b.py"),
				file("pkg/a.py", imp("pkg.b"), imp("pkg", "VERSION")),
			},
			want: []string{"pkg/a.py -> pkg/__init__.py runtime [VERSION]", "pkg/a.py -> pkg/b.py runtime"},
		},
		{
			name: "a name imported from a package is a submodule when a file answers to it",
			files: []depgraph.File{
				file("pkg/__init__.py"), file("pkg/b.py"), file("pkg/sub/__init__.py"),
				file("pkg/a.py", imp("pkg", "b", "sub", "helper")),
			},
			want: []string{
				"pkg/a.py -> pkg/__init__.py runtime [helper]",
				"pkg/a.py -> pkg/b.py runtime",
				"pkg/a.py -> pkg/sub/__init__.py runtime",
			},
		},
		{
			name: "relative imports climb one directory per extra dot",
			files: []depgraph.File{
				file("pkg/__init__.py"), file("pkg/util.py"), file("pkg/sub/__init__.py"), file("pkg/sub/c.py"),
				file("pkg/sub/d.py",
					imp(".", "c"), imp(".c", "X"), imp("..util", "Y"), imp("..", "util"), imp("...gone", "Z")),
			},
			want: []string{"pkg/sub/d.py -> pkg/sub/c.py runtime [X]", "pkg/sub/d.py -> pkg/util.py runtime [Y]"},
		},
		{
			name: "a src layout resolves by module path suffix",
			files: []depgraph.File{
				file("src/app/__init__.py"), file("src/app/core.py"),
				file("src/app/cli.py", imp("app.core", "run")),
				file("tests/test_cli.py", imp("app.cli", "main")),
			},
			want: []string{
				"src/app/cli.py -> src/app/core.py runtime [run]", "tests/test_cli.py -> src/app/cli.py runtime [main]",
			},
		},
		{
			name: "two roots: the importer's own root wins, and an outsider's import is dropped as ambiguous",
			files: []depgraph.File{
				file("one/app/__init__.py"), file("one/app/core.py"), file("one/app/cli.py", imp("app.core", "run")),
				file("two/app/__init__.py"), file("two/app/core.py"), file("two/app/cli.py", imp("app.core", "run")),
				file("scripts/tool.py", imp("app.core", "run")),
			},
			want: []string{
				"one/app/cli.py -> one/app/core.py runtime [run]", "two/app/cli.py -> two/app/core.py runtime [run]",
			},
		},
		{
			name: "a module inside a package never answers to a bare top-level name",
			files: []depgraph.File{
				file("pkg/__init__.py"), file("pkg/logging.py"),
				file("pkg/a.py", imp("logging"), imp("logging", "getLogger")),
			},
		},
		{
			name: "a lone module under a foreign root does not capture a top-level import",
			files: []depgraph.File{
				file("tools/json.py"), file("src/app/__init__.py"), file("src/app/a.py", imp("json")),
				file("tools/run.py", imp("json")),
			},
			want: []string{"tools/run.py -> tools/json.py runtime"},
		},
	})
}

func TestBuildResolvesPythonStubsAndModuleImports(t *testing.T) {
	t.Parallel()
	runResolveCases(t, []resolveCase{
		{
			name: "a stub is a type edge in both directions, and loses to a source file",
			files: []depgraph.File{
				file("pkg/__init__.py", imp("._native", "Thing"), imp(".real", "R")),
				file("pkg/_native.pyi", imp("pkg", "Other")),
				file("pkg/real.py"), file("pkg/real.pyi"),
			},
			want: []string{
				"pkg/__init__.py -> pkg/_native.pyi type [Thing]",
				"pkg/__init__.py -> pkg/real.py runtime [R]",
				"pkg/_native.pyi -> pkg/__init__.py type [Other]",
			},
		},
		{
			name: "an __init__ with no fingerprint still makes its directory a package",
			files: []depgraph.File{
				file("pkg/logging.py"), file("pkg/a.py", imp("logging")),
			},
			extra: []string{"pkg/__init__.py"},
		},
		{
			name: "a wildcard and a module import are priced by the names the importer uses",
			files: []depgraph.File{
				{Path: "lib.py", Exported: map[string]int{"alpha": 1, "beta": 2, "gamma": 3}},
				{
					Path: "star.py", Imports: []depgraph.Import{imp("lib", "*")},
					Referenced: map[string]int{"alpha": 2, "gamma": 1},
				},
				{Path: "mod.py", Imports: []depgraph.Import{imp("lib")}, Referenced: map[string]int{"beta": 1}},
				{Path: "unused.py", Imports: []depgraph.Import{imp("lib", "*")}},
			},
			want: []string{
				"mod.py -> lib.py runtime [beta]", "star.py -> lib.py runtime [alpha,gamma]",
				"unused.py -> lib.py runtime [*]",
			},
		},
	})
}

func TestBuildResolvesTypeScriptImports(t *testing.T) {
	t.Parallel()
	runResolveCases(t, []resolveCase{
		{
			name: "extension and index resolution",
			files: []depgraph.File{
				file("src/a.ts", imp("./b", "B"), imp("./ui", "View"), imp("./lib", "L"), imp("..", "Root")),
				file("src/b.ts"), file("src/ui.tsx"), file("src/lib/index.ts"), file("index.ts"),
			},
			want: []string{
				"src/a.ts -> index.ts runtime [Root]", "src/a.ts -> src/b.ts runtime [B]",
				"src/a.ts -> src/lib/index.ts runtime [L]", "src/a.ts -> src/ui.tsx runtime [View]",
			},
		},
		{
			name: "a .js specifier names the .ts source, and a file beats a directory",
			files: []depgraph.File{
				file("src/a.ts", imp("./b.js", "B"), imp("./view.jsx", "V"), imp("./m.mjs", "M"), imp("./both", "X")),
				file("src/b.ts"), file("src/view.tsx"), file("src/m.mts"), file("src/both.ts"),
				file("src/both/index.ts"),
			},
			want: []string{
				"src/a.ts -> src/b.ts runtime [B]", "src/a.ts -> src/both.ts runtime [X]",
				"src/a.ts -> src/m.mts runtime [M]", "src/a.ts -> src/view.tsx runtime [V]",
			},
		},
		{
			name: "bare specifiers, aliases, assets, and declaration files add no edge",
			files: []depgraph.File{
				file("src/a.ts", imp("react", "useState"), imp("@/b", "B"), imp("~/b", "B"), imp("./style.css"),
					imp("./types", "T"), imp("./types.d", "T"), imp("./missing", "M")),
				file("src/b.ts"), file("src/types.d.ts"),
			},
		},
		{
			name: "every import of a declaration file's own is a type edge",
			files: []depgraph.File{
				file("src/env.d.ts", imp("./b", "B")), file("src/b.ts"),
			},
			want: []string{"src/env.d.ts -> src/b.ts type [B]"},
		},
		{
			name: "kinds stay apart on one pair of files",
			files: []depgraph.File{
				file("a.ts", imp("./b", "run"),
					depgraph.Import{Spec: "./b", Kind: depgraph.TypeOnly, Names: []string{"T"}, Line: 2},
					depgraph.Import{Spec: "./b", Kind: depgraph.Deferred, Line: 9}),
				file("b.ts"),
			},
			want: []string{"a.ts -> b.ts deferred", "a.ts -> b.ts runtime [run]", "a.ts -> b.ts type [T]"},
		},
	})
}

func TestBuildResolvesGoImports(t *testing.T) {
	t.Parallel()
	goMod := func(path, module string) depgraph.File { return depgraph.File{Path: path, GoModule: module} }
	runResolveCases(t, []resolveCase{
		{
			name: "an import path maps to a directory through the module path",
			files: []depgraph.File{
				goMod("go.mod", "example.com/app"),
				{
					Path: "cmd/app/main.go",
					Imports: []depgraph.Import{
						imp("example.com/app/internal/store"), imp("fmt"), imp("example.com/other/pkg"),
					},
					Referenced: map[string]int{"Open": 1},
				},
				{Path: "internal/store/store.go", Exported: map[string]int{"Open": 3}},
				{Path: "internal/store/rows.go", Exported: map[string]int{"Rows": 3}},
				{Path: "internal/store/store_test.go", Exported: map[string]int{"Open": 3}},
			},
			want: []string{"cmd/app/main.go -> internal/store/store.go runtime [Open]"},
		},
		{
			name: "an import that uses no name the tool can place goes to every file of the package",
			files: []depgraph.File{
				goMod("go.mod", "example.com/app"),
				file("main.go", imp("example.com/app/drivers")),
				file("drivers/a.go"), file("drivers/b.go"),
			},
			want: []string{"main.go -> drivers/a.go runtime", "main.go -> drivers/b.go runtime"},
		},
		{
			name: "a nested module wins over its parent, and the module root is a package",
			files: []depgraph.File{
				goMod("go.mod", "example.com/app"), goMod("tools/go.mod", "example.com/app/tools"),
				file("root.go"), file("tools/gen/gen.go"),
				file("tools/main.go", imp("example.com/app/tools/gen"), imp("example.com/app")),
			},
			want: []string{"tools/main.go -> root.go runtime", "tools/main.go -> tools/gen/gen.go runtime"},
		},
		{
			name:  "without a go.mod nothing resolves",
			files: []depgraph.File{file("main.go", imp("example.com/app/pkg")), file("pkg/p.go")},
		},
	})
}

func TestBuildListsEveryCodeFileAsANode(t *testing.T) {
	t.Parallel()
	g := depgraph.Build([]depgraph.File{
		file("a.py"), file("b.ts"), file("c.go"), file("README.md"), {Path: "go.mod", GoModule: "example.com/x"},
	}, nil)
	if want := []string{"a.py", "b.ts", "c.go"}; !slices.Equal(g.Nodes(), want) {
		t.Errorf("Nodes() = %v, want %v", g.Nodes(), want)
	}
}

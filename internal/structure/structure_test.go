package structure_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/kyleking/slop-strata/internal/lang"
	"github.com/kyleking/slop-strata/internal/structure"
)

// funcWant is the per-function profile a snippet must produce, in tree order.
type funcWant struct {
	name      string
	cc        int
	sloc      int
	nesting   int
	cognitive int
	params    int
}

type funcCase struct {
	name string
	path string
	src  string
	want []funcWant
}

func pythonFuncCases() []funcCase {
	return []funcCase{
		{
			name: "python nested def scores alone",
			path: "outer.py",
			src: "def outer():\n" +
				"  def inner(x):\n" +
				"    if x: return 1\n" +
				"  return inner\n",
			want: []funcWant{
				{name: "outer", cc: 1, sloc: 2, nesting: 0, cognitive: 0, params: 0},
				{name: "inner", cc: 2, sloc: 2, nesting: 1, cognitive: 1, params: 1},
			},
		},
		{
			name: "python else clause counts cognitive",
			path: "sign.py",
			src: "def sign(x):\n" +
				"    if x > 0:\n" +
				"        return 1\n" +
				"    else:\n" +
				"        return 0\n",
			want: []funcWant{
				{name: "sign", cc: 2, sloc: 5, nesting: 1, cognitive: 2, params: 1},
			},
		},
	}
}

func goFuncCases() []funcCase {
	return []funcCase{
		{
			name: "go closure scores alone",
			path: "outer.go",
			src: "package p\n" +
				"\n" +
				"func outer() int {\n" +
				"\tf := func(x int) int {\n" +
				"\t\tif x > 0 {\n" +
				"\t\t\treturn 1\n" +
				"\t\t}\n" +
				"\t\treturn 0\n" +
				"\t}\n" +
				"\treturn f(1)\n" +
				"}\n",
			want: []funcWant{
				{name: "outer", cc: 1, sloc: 4, nesting: 0, cognitive: 0, params: 0},
				{name: "<anonymous>", cc: 2, sloc: 6, nesting: 1, cognitive: 1, params: 1},
			},
		},
		{
			name: "go switch nests its body",
			path: "route.go",
			src: "package p\n" +
				"\n" +
				"func route(v any, x int) int {\n" +
				"\tswitch x {\n" +
				"\tcase 1:\n" +
				"\t\tswitch v.(type) {\n" +
				"\t\tcase int:\n" +
				"\t\t\treturn 1\n" +
				"\t\t}\n" +
				"\t}\n" +
				"\treturn 0\n" +
				"}\n",
			want: []funcWant{
				{name: "route", cc: 3, sloc: 10, nesting: 2, cognitive: 3, params: 2},
			},
		},
		{
			name: "go else if and else count cognitive",
			path: "sign.go",
			src: "package p\n" +
				"\n" +
				"func sign(x int) int {\n" +
				"\tif x > 0 {\n" +
				"\t\treturn 1\n" +
				"\t} else if x < 0 {\n" +
				"\t\treturn -1\n" +
				"\t} else {\n" +
				"\t\treturn 0\n" +
				"\t}\n" +
				"}\n",
			want: []funcWant{
				{name: "sign", cc: 3, sloc: 9, nesting: 2, cognitive: 5, params: 1},
			},
		},
		{
			name: "go params ignore the result list",
			path: "pair.go",
			src: "package p\n" +
				"\n" +
				"func pair(x int) (int, error) {\n" +
				"\treturn x, nil\n" +
				"}\n",
			want: []funcWant{
				{name: "pair", cc: 1, sloc: 3, nesting: 0, cognitive: 0, params: 1},
			},
		},
	}
}

func tsFuncCases() []funcCase {
	return []funcCase{
		{
			name: "typescript nested declaration scores alone",
			path: "outer.ts",
			src: "function outer(): number {\n" +
				"  function inner(x: number): number {\n" +
				"    if (x) return 1;\n" +
				"    return 0;\n" +
				"  }\n" +
				"  return inner(1);\n" +
				"}\n",
			want: []funcWant{
				{name: "outer", cc: 1, sloc: 3, nesting: 0, cognitive: 0, params: 0},
				{name: "inner", cc: 2, sloc: 4, nesting: 1, cognitive: 1, params: 1},
			},
		},
		{
			name: "typescript else clause counts cognitive",
			path: "sign.ts",
			src: "function sign(x: number): number {\n" +
				"  if (x > 0) {\n" +
				"    return 1;\n" +
				"  } else {\n" +
				"    return 0;\n" +
				"  }\n" +
				"}\n",
			want: []funcWant{
				{name: "sign", cc: 2, sloc: 7, nesting: 1, cognitive: 2, params: 1},
			},
		},
	}
}

func TestAnalyzeFunctions(t *testing.T) {
	t.Parallel()
	cases := slices.Concat(pythonFuncCases(), goFuncCases(), tsFuncCases())
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := lang.ByPath(tc.path)
			src := []byte(tc.src)
			tree := lang.NewPool().Parse(l, src)
			defer tree.Close()

			fm := structure.Analyze(l, tree, src)
			got := make([]funcWant, 0, len(fm.Funcs))
			for _, f := range fm.Funcs {
				got = append(got, funcWant{
					name: f.Name, cc: f.CC, sloc: f.SLOC, nesting: f.Nesting,
					cognitive: f.Cognitive, params: f.Params,
				})
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("funcs = %+v\nwant   %+v", got, tc.want)
			}
		})
	}
}

func TestAnalyzeParseError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		path string
		src  string
		want bool
	}{
		{name: "python missing close paren", path: "broken.py", src: "def f(:\n    pass\n", want: true},
		{name: "typescript unclosed block", path: "broken.ts", src: "function a() {\n  if (x) {\n", want: true},
		{name: "python clean file", path: "ok.py", src: "def f(x):\n    return x\n", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := lang.ByPath(tc.path)
			src := []byte(tc.src)
			tree := lang.NewPool().Parse(l, src)
			defer tree.Close()

			if got := structure.Analyze(l, tree, src).ParseError; got != tc.want {
				t.Errorf("ParseError = %v, want %v", got, tc.want)
			}
		})
	}
}

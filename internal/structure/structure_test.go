package structure_test

import (
	"reflect"
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

func TestAnalyzeFunctions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		path string
		src  string
		want []funcWant
	}{
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
	}
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

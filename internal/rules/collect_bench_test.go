package rules_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/lang"
	"github.com/kyleking/slop-strata/internal/rules"
)

// benchSources builds one file per language: twenty imports, then functions
// enough to make the imports a small share of the tree, as in real files.
// The tsx file is the ts one with three components of markup after it.
func benchSources() map[string]string {
	var py, ts, goSrc strings.Builder
	goSrc.WriteString("package bench\n\nimport (\n")
	for i := range 20 {
		fmt.Fprintf(&py, "from pkg.mod%d import name%d, other%d\n", i, i, i)
		fmt.Fprintf(&ts, "import { name%d, type Other%d } from './mod%d'\n", i, i, i)
		fmt.Fprintf(&goSrc, "\t\"example.com/app/pkg%d\"\n", i)
	}
	goSrc.WriteString(")\n")
	for i := range 60 {
		n := i % 20
		fmt.Fprintf(&py, "\n\ndef f%d(x):\n    if x > %d:\n        return name%d(x)\n    return other%d(x)\n",
			i, i, n, n)
		fmt.Fprintf(&ts, "\nexport function f%d(x: number): number {\n  return x > %d ? name%d(x) : x\n}\n", i, i, n)
		fmt.Fprintf(&goSrc, "\nfunc f%d(x int) int {\n\tif x > %d {\n\t\treturn pkg%d.Do(x)\n\t}\n\n\treturn x\n}\n",
			i, i, n)
	}

	return map[string]string{
		"bench.py": py.String(), "bench.ts": ts.String(), "bench.go": goSrc.String(),
		"bench.tsx": ts.String() + jsxBenchSource(0) + jsxBenchSource(1) + jsxBenchSource(2),
	}
}

// BenchmarkCollect measures fingerprinting one parsed file, the pass that
// reads imports along with everything else the cross-file rules join on.
func BenchmarkCollect(b *testing.B) {
	for path, src := range benchSources() {
		l := lang.ByPath(path)
		content := []byte(src)
		tree := lang.Parse(l, content)
		ctx := &rules.Ctx{Lang: l, Root: tree.RootNode(), Src: content, Path: path}
		b.Run(l.ID, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				rules.Collect(ctx)
			}
		})
		tree.Close()
	}
}

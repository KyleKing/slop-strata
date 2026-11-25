package rules_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/lang"
	"github.com/kyleking/slop-strata/internal/rules"
)

// literalBenchSource writes one Go file that tests one of 40 states, hands
// another of them to a call, and writes twenty strings no other file holds.
func literalBenchSource(f int) string {
	var src strings.Builder
	src.WriteString("package bench\n\n")
	fmt.Fprintf(&src, "func ready%d(j job) bool {\n\treturn j.state == \"state-%02d-ready\"\n}\n\n", f, f%40)
	fmt.Fprintf(&src, "func mark%d(j job) {\n\tj.send(1, \"state-%02d-ready\")\n", f, (f+7)%40)
	for i := range 20 {
		fmt.Fprintf(&src, "\tj.log(\"file %d step %d done\")\n", f, i)
	}
	src.WriteString("}\n")

	return src.String()
}

// literalBenchTree fingerprints 400 files: 40 states that 10 files test and
// 10 others write, and 8,000 strings of one file each.
func literalBenchTree() map[string]*rules.Fingerprint {
	tree := map[string]*rules.Fingerprint{}
	for f := range 400 {
		path := fmt.Sprintf("jobs/area%d/job%d.go", f%20, f)
		content := []byte(literalBenchSource(f))
		parsed := lang.Parse(lang.Go, content)
		tree[path] = rules.Collect(&rules.Ctx{Lang: lang.Go, Root: parsed.RootNode(), Src: content, Path: path})
		parsed.Close()
	}

	return tree
}

// BenchmarkLiteralGroups measures the snapshot's literal pass over a tree of
// 400 fingerprinted files and 8,800 recorded strings: adding each up over the
// files, dropping the ones under the floors, and ranking the rest.
func BenchmarkLiteralGroups(b *testing.B) {
	tree := literalBenchTree()
	b.ReportAllocs()
	for b.Loop() {
		if got := rules.LiteralGroups(tree); len(got) != 40 {
			b.Fatalf("got %d groups, want one per state", len(got))
		}
	}
}

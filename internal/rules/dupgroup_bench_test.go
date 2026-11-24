package rules_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/lang"
	"github.com/kyleking/slop-strata/internal/rules"
)

// benchTree fingerprints 200 files of ten functions each. Twelve of the files
// hold a loader copied under another table name, and every other function is
// alike in shape only as far as an idiom is.
func benchTree(b *testing.B) map[string]*rules.Fingerprint {
	b.Helper()
	tree := map[string]*rules.Fingerprint{}
	for f := range 200 {
		var src strings.Builder
		src.WriteString("package bench\n")
		for i := range 10 {
			if i == 0 && f%17 == 0 {
				body := tallySrc(goTally, fmt.Sprintf("parse%d", f), fmt.Sprintf("table%d", f%7))
				src.WriteString(strings.TrimPrefix(body, "package store\n"))

				continue
			}
			fmt.Fprintf(&src, "\nfunc f%dx%d(x int) int {\n\tif x > %d {\n", f, i, i)
			fmt.Fprintf(&src, "\t\treturn pkg%d.Do(x, %d)\n\t}\n\n\treturn x\n}\n", f, i)
		}
		path := fmt.Sprintf("pkg%d/bench%d.go", f%20, f)
		content := []byte(src.String())
		parsed := lang.Parse(lang.Go, content)
		tree[path] = rules.Collect(&rules.Ctx{Lang: lang.Go, Root: parsed.RootNode(), Src: content, Path: path})
		parsed.Close()
	}

	return tree
}

// BenchmarkDuplicateGroups measures the snapshot's duplicate pass over a tree
// of 200 fingerprinted files: every pair of statements of one shape, grown,
// scored, and grouped.
func BenchmarkDuplicateGroups(b *testing.B) {
	tree := benchTree(b)
	b.ReportAllocs()
	for b.Loop() {
		if got := rules.DuplicateGroups(tree); len(got) == 0 {
			b.Fatal("no group in a tree of copies")
		}
	}
}

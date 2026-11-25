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
		tree[path] = benchPrint(path, src.String())
	}

	return tree
}

// benchPrint fingerprints one Go source of a benchmark's tree.
func benchPrint(path, src string) *rules.Fingerprint {
	content := []byte(src)
	parsed := lang.Parse(lang.Go, content)
	defer parsed.Close()

	return rules.Collect(&rules.Ctx{Lang: lang.Go, Root: parsed.RootNode(), Src: content, Path: path})
}

// longCopies fingerprints one long file copied under so many names: funcs
// functions of eight statements, every statement of a shape of its own, so
// each copy lines up with each other copy from its first line to its last.
// That is the case the pass must not read once per statement: a vendored or
// generated file kept in several places.
func longCopies(copies, funcs int) map[string]*rules.Fingerprint {
	ops := []string{"+", "-", "*", "/"}
	var body strings.Builder
	for f := range funcs {
		fmt.Fprintf(&body, "\nfunc step%d(a, b, c, d, e, f int) int {\n", f)
		for k := range 8 {
			n := f*8 + k
			fmt.Fprintf(&body, "\tv%d := a", k)
			for _, arg := range []string{"b", "c", "d", "e", "f"} {
				body.WriteString(" " + ops[n%len(ops)] + " " + arg)
				n /= len(ops)
			}
			body.WriteString("\n")
		}
		body.WriteString("\n\treturn v0\n}\n")
	}
	tree := map[string]*rules.Fingerprint{}
	for c := range copies {
		path := fmt.Sprintf("vendor%d/steps%d.go", c, c)
		tree[path] = benchPrint(path, "package steps\n"+body.String())
	}

	return tree
}

// BenchmarkDuplicateGroupsLongCopies measures the pass over a long file held
// in several places, at two lengths. Its cost per statement must not rise
// with the length of the file: every two copies are one run, read once.
func BenchmarkDuplicateGroupsLongCopies(b *testing.B) {
	for _, funcs := range []int{40, 160} {
		tree := longCopies(16, funcs)
		b.Run(fmt.Sprintf("statements=%d", funcs*8), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if got := rules.DuplicateGroups(tree); len(got) == 0 {
					b.Fatal("no group in a tree of copies")
				}
			}
		})
	}
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

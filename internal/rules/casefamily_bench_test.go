package rules_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/rules"
)

// caseBenchTree fingerprints 400 Go files of three places each: a switch over
// one of 40 enums of eight members, a table over another, and an if chain
// over strings every tenth file shares. Each enum is enumerated in 20 places,
// so the pass has 40 families to join and 800 places that share one case of
// a common vocabulary without being one.
func caseBenchTree() map[string]*rules.Fingerprint {
	tree := map[string]*rules.Fingerprint{}
	for f := range 400 {
		var src strings.Builder
		src.WriteString("package bench\n")
		fmt.Fprintf(&src, "\nfunc area%d(k Kind) int {\n\tswitch k {\n", f)
		for m := range 8 {
			fmt.Fprintf(&src, "\tcase Enum%dMember%d:\n\t\treturn %d\n", f%40, m, m)
		}
		src.WriteString("\tcase Shared:\n\t\treturn 9\n\t}\n\n\treturn 0\n}\n")
		fmt.Fprintf(&src, "\nvar names%d = map[Kind]string{\n", f)
		for m := range 8 {
			fmt.Fprintf(&src, "\tEnum%dMember%d: \"%d\",\n", (f+20)%40, m, m)
		}
		src.WriteString("}\n")
		fmt.Fprintf(&src, "\nfunc mode%d(s string) int {\n\tif s == \"mode-%d-a\" {\n\t\treturn 1\n", f, f%10)
		fmt.Fprintf(&src, "\t} else if s == \"mode-%d-b\" {\n\t\treturn 2\n", f%10)
		fmt.Fprintf(&src, "\t} else if s == \"mode-%d-c\" {\n\t\treturn 3\n\t}\n\n\treturn 0\n}\n", f%10)
		path := fmt.Sprintf("pkg%d/bench%d.go", f%20, f)
		tree[path] = benchPrint(path, src.String())
	}

	return tree
}

// BenchmarkCaseFamilies measures the snapshot's case pass over a tree of 400
// fingerprinted files and 1,200 places: decoding them, counting the places
// that hold each case, joining every two that share enough, and clustering.
func BenchmarkCaseFamilies(b *testing.B) {
	tree := caseBenchTree()
	b.ReportAllocs()
	for b.Loop() {
		if got := rules.CaseFamilies(tree); len(got) != 50 {
			b.Fatalf("got %d families, want 40 of an enum and 10 of a mode", len(got))
		}
	}
}

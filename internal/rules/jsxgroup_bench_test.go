package rules_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/lang"
	"github.com/kyleking/slop-strata/internal/rules"
)

// jsxBenchSource writes one component file of three blocks: a card of a
// layout 10 files share, with the file's own texts; a table every file
// writes its own way, by the number of its columns and their attributes; and
// a list under the card's layout again, so every card repeats inside the
// file too.
func jsxBenchSource(f int) string {
	var src strings.Builder
	fmt.Fprintf(&src, "export function Page%d({ rows }: Props) {\n  return (\n    <main>\n", f)
	card := func(n int) {
		fmt.Fprintf(&src, "      <Card%d className=\"card\" data-id=\"%d\">\n        <header>\n", f%30, n)
		fmt.Fprintf(&src, "          <h2>Title %d</h2>\n          <span>{count%d}</span>\n        </header>\n", n, n)
		src.WriteString("        <ul>\n          <li>one</li>\n          <li>two</li>\n        </ul>\n")
		fmt.Fprintf(&src, "      </Card%d>\n", f%30)
	}
	card(f)
	src.WriteString("      <table>\n        <tbody>\n          {rows.map(row => (\n            <tr key={row.id}>\n")
	for col := range 4 + f%7 {
		fmt.Fprintf(&src, "              <td data-col%d-%d=\"x\">{row.c%d}</td>\n", f, col, col)
	}
	src.WriteString("            </tr>\n          ))}\n        </tbody>\n      </table>\n")
	card(f + 1000)
	src.WriteString("    </main>\n  )\n}\n")

	return src.String()
}

// jsxBenchTree fingerprints 300 component files: 30 card layouts in 20
// places each, and 300 tables no other file repeats.
func jsxBenchTree() map[string]*rules.Fingerprint {
	tree := map[string]*rules.Fingerprint{}
	for f := range 300 {
		path := fmt.Sprintf("app/area%d/page%d.tsx", f%15, f)
		content := []byte(jsxBenchSource(f))
		parsed := lang.Parse(lang.TSX, content)
		tree[path] = rules.Collect(&rules.Ctx{Lang: lang.TSX, Root: parsed.RootNode(), Src: content, Path: path})
		parsed.Close()
	}

	return tree
}

// BenchmarkJSXGroups measures the snapshot's JSX pass over a tree of 300
// fingerprinted files and 1,200 recorded elements: decoding them, grouping
// them by shape, counting the holes the sites of a shape differ in, and
// dropping the blocks a larger group already lists.
func BenchmarkJSXGroups(b *testing.B) {
	tree := jsxBenchTree()
	b.ReportAllocs()
	for b.Loop() {
		if got := rules.JSXGroups(tree); len(got) != 30 {
			b.Fatalf("got %d groups, want one per card layout", len(got))
		}
	}
}

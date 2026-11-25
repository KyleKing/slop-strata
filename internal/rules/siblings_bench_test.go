package rules_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/rules"
)

// siblingBenchTree fingerprints 400 Go files of one type each: 40 designs in
// 10 files apiece. A type has the two methods of its design, written alike
// in every file of the design but for one literal, a Describe every type of
// the tree has, which is too common a name to pair anything, and a method of
// its own.
func siblingBenchTree() map[string]*rules.Fingerprint {
	tree := map[string]*rules.Fingerprint{}
	for f := range 400 {
		d := f % 40
		var src strings.Builder
		fmt.Fprintf(&src, "package bench\n\ntype Store%d struct{ db *DB }\n", f)
		fmt.Fprintf(&src, "\nfunc (s *Store%d) Load%d(key string) ([]Row, error) {\n", f, d)
		fmt.Fprintf(&src, "\trows, err := s.db.Query(\"select * from t%d where id = ?\", key)\n", f)
		src.WriteString("\tif err != nil {\n\t\treturn nil, fmt.Errorf(\"loading: %w\", err)\n\t}\n")
		src.WriteString("\tout := make([]Row, 0, len(rows))\n\tfor _, row := range rows {\n")
		src.WriteString("\t\tout = append(out, convert(row))\n\t}\n\n\treturn out, nil\n}\n")
		fmt.Fprintf(&src, "\nfunc (s *Store%d) Save%d(key string, value Row) error {\n", f, d)
		src.WriteString("\tpayload, err := encode(value)\n")
		src.WriteString("\tif err != nil {\n\t\treturn fmt.Errorf(\"encoding: %w\", err)\n\t}\n")
		fmt.Fprintf(&src, "\tif err := s.db.Exec(\"update t%d set body = ? where id = ?\", payload, key); ", f)
		src.WriteString("err != nil {\n")
		src.WriteString("\t\treturn fmt.Errorf(\"saving: %w\", err)\n\t}\n\ts.cache[key] = value\n\n\treturn nil\n}\n")
		fmt.Fprintf(&src, "\nfunc (s *Store%d) Describe() string {\n", f)
		src.WriteString("\tparts := []string{s.db.Name(), s.db.Host()}\n\tif s.db.ReadOnly() {\n")
		src.WriteString("\t\tparts = append(parts, \"read-only\")\n\t}\n\n\treturn strings.Join(parts, \" \")\n}\n")
		fmt.Fprintf(&src, "\nfunc (s *Store%d) Own%d(limit int) int {\n\ttotal := 0\n", f, f)
		src.WriteString("\tfor i := range limit {\n\t\tif s.db.Has(i) {\n\t\t\ttotal += s.db.Size(i) * 2\n\t\t}\n\t}\n")
		src.WriteString("\n\treturn total\n}\n")
		path := fmt.Sprintf("pkg%d/store%d.go", f%20, f)
		tree[path] = benchPrint(path, src.String())
	}

	return tree
}

// BenchmarkSiblingSets measures the snapshot's sibling pass over a tree of
// 400 fingerprinted files and 1,600 methods: listing the containers off the
// units, proposing the 1,800 pairs that share two rare names, reading those
// bodies off the stored statements, aligning them, and clustering.
func BenchmarkSiblingSets(b *testing.B) {
	tree := siblingBenchTree()
	b.ReportAllocs()
	for b.Loop() {
		if got := rules.SiblingSets(tree); len(got) != 40 {
			b.Fatalf("got %d sets, want one per design", len(got))
		}
	}
}

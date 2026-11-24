// Package fixturetest checks a rule against adversarial variants of one
// fixture tree. A case is a base tree a rule must flag, plus overlays of two
// sorts: a dodge is a cosmetic change that must leave the finding IDs exactly
// as they were, and a fix is a real repair that must leave none. A rule that
// passes its dodges keeps its baseline entries and labels through edits that
// change nothing, and a rule that passes its fixes stops reporting once the
// problem is gone.
//
// Trees are written inline in the archive format of Tree, so a case reads top
// to bottom in the test that owns it and no fixture file sits where a
// formatter or a linter would rewrite it.
package fixturetest

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

// Tree is a set of files: slash-separated path to content.
type Tree map[string]string

// Files parses an archive into a Tree. A line of the form "-- path --" starts
// a file, and the lines up to the next marker are its content. Text before
// the first marker is ignored, so an archive may open with a comment.
func Files(archive string) Tree {
	tree := Tree{}
	name := ""
	var body strings.Builder
	flush := func() {
		if name != "" {
			tree[name] = body.String()
		}
		body.Reset()
	}
	for _, line := range strings.SplitAfter(archive, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "-- ") && strings.HasSuffix(trimmed, " --") && len(trimmed) > len("-- --") {
			flush()
			name = strings.TrimSpace(trimmed[len("-- ") : len(trimmed)-len(" --")])

			continue
		}
		body.WriteString(line)
	}
	flush()

	return tree
}

// Variant is one overlay on a case's base tree: Files replace or add, and
// Remove deletes.
type Variant struct {
	Files  Tree
	Name   string
	Remove []string
}

// Apply answers a copy of base with the variant laid over it.
func (v Variant) Apply(base Tree) Tree {
	out := maps.Clone(base)
	if out == nil {
		out = Tree{}
	}
	for _, p := range v.Remove {
		delete(out, p)
	}
	maps.Copy(out, v.Files)

	return out
}

// Case is one fixture with its variants.
type Case struct {
	// Base is the tree the rule must flag.
	Base Tree
	Name string
	// Dodges must each produce exactly the base's finding IDs.
	Dodges []Variant
	// Fixes must each produce no finding.
	Fixes []Variant
}

// Eval runs the rule under test over a tree and answers its finding IDs, in
// any order.
type Eval func(tree Tree) []string

// Reporter is where Check reports a failed expectation. A *testing.T is one.
type Reporter interface {
	Helper()
	Errorf(format string, args ...any)
}

// Check evaluates one case: the base must produce a finding, every dodge the
// same IDs, and every fix none.
func Check(r Reporter, c Case, eval Eval) {
	r.Helper()
	want := sorted(eval(c.Base))
	if len(want) == 0 {
		r.Errorf("%s: the base tree produced no finding, so its variants prove nothing", c.Name)

		return
	}
	for _, v := range c.Dodges {
		if got := sorted(eval(v.Apply(c.Base))); !slices.Equal(got, want) {
			r.Errorf("%s: dodge %q changed the finding IDs: got %v, want %v", c.Name, v.Name, got, want)
		}
	}
	for _, v := range c.Fixes {
		if got := sorted(eval(v.Apply(c.Base))); len(got) != 0 {
			r.Errorf("%s: fix %q still produced findings %v", c.Name, v.Name, got)
		}
	}
}

// Run checks every case as a parallel subtest named for it.
func Run(t *testing.T, cases []Case, eval Eval) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			Check(t, c, eval)
		})
	}
}

func sorted(ids []string) []string {
	out := slices.Clone(ids)
	slices.Sort(out)

	return out
}

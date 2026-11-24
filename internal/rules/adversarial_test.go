package rules_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/fixturetest"
	"github.com/kyleking/slop-strata/internal/lang"
	"github.com/kyleking/slop-strata/internal/rules"
)

// advCase is one rule's fixture in one language. Rules holds more than one id
// where two rules split one problem between them, as x/dead-export and
// x/could-be-private do. Config is the config_paths setting x/stale-config
// reads.
type advCase struct {
	rules  []string
	config []string
	fixturetest.Case
}

// treeFindings scores a tree as a snapshot does: every per-blob rule, the
// rules that read a path, and the cross-file passes, all with the
// experimental rules on, then inline ignores applied.
func treeFindings(t *testing.T, tree fixturetest.Tree, config []string) map[string][]rules.Finding {
	t.Helper()
	blobs := make([]rules.BlobPrint, 0, len(tree))
	paths := make([]string, 0, len(tree))
	found := map[string][]rules.Finding{}
	for file, src := range tree {
		paths = append(paths, file)
		blobs = append(blobs, rules.BlobPrint{Path: file, Print: printOf(t, file, src)})
		test := lang.IsTestPath(file)
		if l := lang.ByPath(file); l != nil {
			test = l.IsTestFile(file)
			found[file] = findingsOf(t, file, src, true)
		}
		found[file] = append(found[file], rules.PathFindings(file, test, true)...)
	}
	for file, fs := range rules.CrossFile(blobs, paths, nil, config, true) {
		found[file] = append(found[file], fs...)
	}
	sup := rules.NewSuppressor(rules.Baseline{}, rules.Labels{})
	for i := range blobs {
		found[blobs[i].Path] = sup.Apply(blobs[i].Path, blobs[i].Print, found[blobs[i].Path])
	}

	return found
}

// ruleIDs answers the ids of the findings a tree has under any of the case's
// rules. An id is hashed over its file, so a finding that moves changes id.
func ruleIDs(t *testing.T, c *advCase) fixturetest.Eval {
	t.Helper()

	return func(tree fixturetest.Tree) []string {
		var ids []string
		for _, fs := range treeFindings(t, tree, c.config) {
			for i := range fs {
				if slices.Contains(c.rules, fs[i].Rule) {
					ids = append(ids, fs[i].ID)
				}
			}
		}

		return ids
	}
}

// adversarialCases lists every fixture, grouped by the file that defines it.
func adversarialCases() []advCase {
	return slices.Concat(goAdversarial(), pythonAdversarial(), typescriptAdversarial(), sharedAdversarial(),
		crossAdversarial())
}

// TestRulesHoldUnderAdversarialEdits runs each rule against the edits someone
// makes to get past it: a dodge keeps the finding and its id, a persisting
// edit keeps the finding, a gap is a loss the rule is known to take, and a
// fix clears it.
func TestRulesHoldUnderAdversarialEdits(t *testing.T) {
	t.Parallel()
	for _, c := range adversarialCases() {
		t.Run(strings.Join(c.rules, "+")+"/"+c.Name, func(t *testing.T) {
			t.Parallel()
			fixturetest.Check(t, c.Case, ruleIDs(t, &c))
		})
	}
}

// changeOnlyRules are tested where a change can be built: x/last-ref reads
// two trees, so its fixtures run the command in cmd/slop-strata.
var changeOnlyRules = []string{"x/last-ref"} //nolint:gochecknoglobals // a fixed list

// TestEveryRuleHasAnAdversarialCase fails when a rule ships with no fixture,
// or with one that tries fewer than two dodges and one fix.
func TestEveryRuleHasAnAdversarialCase(t *testing.T) {
	t.Parallel()
	covered := map[string]bool{ruleImportCycle: true}
	for _, id := range changeOnlyRules {
		covered[id] = true
	}
	for _, c := range adversarialCases() {
		if n := len(c.Dodges) + len(c.Persists) + len(c.Gaps); n < 2 || len(c.Fixes) < 1 {
			t.Errorf("%v %s: %d dodges and %d fixes, want at least 2 and 1", c.rules, c.Name, n, len(c.Fixes))
		}
		for _, id := range c.rules {
			covered[id] = true
		}
	}
	for _, r := range rules.Catalog() {
		if !covered[r.ID] {
			t.Errorf("%s has no adversarial case", r.ID)
		}
	}
}

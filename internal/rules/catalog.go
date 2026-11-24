package rules

import (
	"slices"
	"strings"
)

// RuleInfo names a rule and whether it runs only with --experimental.
type RuleInfo struct {
	ID           string
	Experimental bool
}

// Cross-file and path-only rule IDs that a tier gates.
const (
	ruleXCloneFunction  = "x/clone-function"
	ruleXCouldBePrivate = "x/could-be-private"
	ruleXDeadExport     = "x/dead-export"
	ruleXDupLiteral     = "x/dup-literal"
	ruleXDupTestCase    = "x/dup-test-case"
	ruleXLastRef        = "x/last-ref"
	ruleXSpentTest      = "x/spent-test"
)

// crossRules lists the cross-file and path-only rules with their tier.
// CrossFile and PathFindings gate on it through crossEnabled.
var crossRules = []RuleInfo{ //nolint:gochecknoglobals // a fixed vocabulary
	{ID: ruleXCloneFunction, Experimental: true},
	{ID: ruleXCouldBePrivate, Experimental: true},
	{ID: ruleXDeadExport, Experimental: true},
	{ID: ruleXDupLiteral, Experimental: true},
	{ID: ruleXDupTestCase, Experimental: true},
	{ID: ruleXImportCycle, Experimental: true},
	{ID: ruleXLastRef},
	{ID: "x/single-impl-iface"},
	{ID: ruleXSpentTest, Experimental: true},
	{ID: "x/stale-config"},
	{ID: ruleXStaleProse, Experimental: true},
	{ID: "x/unused-option"},
}

// crossEnabled reports whether a cross-file or path-only rule runs: always for
// a default rule, and only with experimental set for an experimental one.
func crossEnabled(id string, experimental bool) bool {
	i := slices.IndexFunc(crossRules, func(r RuleInfo) bool { return r.ID == id })

	return i >= 0 && (experimental || !crossRules[i].Experimental)
}

// Catalog lists every rule that ships, ordered by ID.
func Catalog() []RuleInfo {
	out := slices.Clone(crossRules)
	for i := range all {
		out = append(out, RuleInfo{ID: all[i].ID, Experimental: all[i].Experimental})
	}
	slices.SortFunc(out, func(a, b RuleInfo) int { return strings.Compare(a.ID, b.ID) })

	return out
}

// SnapshotRule reports whether a snapshot with the given experimental setting
// runs a built-in rule. Rules that only a change can report, and rules from
// ast-grep packs, are not among them.
func SnapshotRule(id string, experimental bool) bool {
	if id == ruleXLastRef {
		return false
	}
	for _, r := range Catalog() {
		if r.ID == id {
			return experimental || !r.Experimental
		}
	}

	return false
}

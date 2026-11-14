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

// crossRules lists the rules the cross-file passes and the path-only rules
// emit, with the tier CrossFile and PathFindings give them. Per-blob rules
// come from the registry.
var crossRules = []RuleInfo{ //nolint:gochecknoglobals // a fixed vocabulary
	{ID: "x/clone-function", Experimental: true},
	{ID: "x/could-be-private", Experimental: true},
	{ID: "x/dead-export", Experimental: true},
	{ID: "x/dup-literal", Experimental: true},
	{ID: "x/dup-test-case", Experimental: true},
	{ID: "x/last-ref"},
	{ID: "x/single-impl-iface"},
	{ID: "x/spent-test", Experimental: true},
	{ID: "x/stale-config"},
	{ID: ruleXStaleProse, Experimental: true},
	{ID: "x/unused-option"},
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

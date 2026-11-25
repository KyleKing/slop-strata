package rules_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/kyleking/slop-strata/internal/rules"
)

// TestCompareFindingsIsATotalOrder pins that findings come out in one order
// whatever order the passes handed them over in: two hits of one rule on one
// line, which a cross-file pass appends in map order, differ in their id
// alone.
func TestCompareFindingsIsATotalOrder(t *testing.T) {
	t.Parallel()
	want := []rules.Finding{
		{Line: 3, Rule: "x/new-duplicate", Match: "a.go load", ID: "b"},
		{Line: 7, Rule: "dup-literal", Match: `"select"`, ID: "1f"},
		{Line: 7, Rule: "dup-literal", Match: `"select"`, ID: "9c"},
		{Line: 7, Rule: "dup-literal", Match: `"update"`, ID: "00"},
		{Line: 7, Rule: "x/dead-export", Match: "Load", Scope: "A", ID: "c"},
		{Line: 7, Rule: "x/dead-export", Match: "Load", Scope: "B", ID: "a"},
		{Line: 7, Rule: "x/dead-export", Match: "Load", Scope: "B", Anchor: "1", ID: "a"},
		{Line: 7, Rule: "x/dead-export", Match: "Load", Scope: "B", Anchor: "1", Note: "n", ID: "a"},
		{Line: 9, Rule: "dup-literal", Match: `"select"`, ID: "00"},
	}
	orders := [][]int{
		{0, 1, 2, 3, 4, 5, 6, 7, 8},
		{8, 7, 6, 5, 4, 3, 2, 1, 0},
		{2, 1, 8, 0, 7, 3, 6, 4, 5},
		{5, 4, 6, 3, 7, 0, 8, 1, 2},
	}
	for _, order := range orders {
		got := make([]rules.Finding, 0, len(want))
		for _, i := range order {
			got = append(got, want[i])
		}
		slices.SortFunc(got, rules.CompareFindings)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("sorted from order %v:\n got %+v\nwant %+v", order, got, want)
		}
	}
}

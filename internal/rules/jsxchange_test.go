package rules_test

import (
	"reflect"
	"testing"

	"github.com/kyleking/slop-strata/internal/rules"
)

func TestDeferDuplicatesDropsWhatAMarkupFindingCovers(t *testing.T) {
	t.Parallel()
	block := rules.Finding{Rule: rules.RuleNewJSXShape, Line: 10, End: 30}
	dup := func(line, end int) rules.Finding {
		return rules.Finding{Rule: rules.RuleNewDuplicate, Line: line, End: end}
	}
	cases := []struct {
		dups, shapes, want map[string][]rules.Finding
		name               string
	}{
		{
			name:   "no markup finding",
			dups:   map[string][]rules.Finding{"a.tsx": {dup(12, 14)}},
			shapes: nil,
			want:   map[string][]rules.Finding{"a.tsx": {dup(12, 14)}},
		},
		{
			name:   "a duplicate inside the block, and an expression on its first line",
			dups:   map[string][]rules.Finding{"a.tsx": {dup(10, 10), dup(12, 14), dup(30, 30)}},
			shapes: map[string][]rules.Finding{"a.tsx": {block}},
			want:   map[string][]rules.Finding{},
		},
		{
			name:   "a duplicate that starts above the block or ends below it stays",
			dups:   map[string][]rules.Finding{"a.tsx": {dup(8, 12), dup(12, 14), dup(28, 33), dup(40, 41)}},
			shapes: map[string][]rules.Finding{"a.tsx": {block}},
			want:   map[string][]rules.Finding{"a.tsx": {dup(8, 12), dup(28, 33), dup(40, 41)}},
		},
		{
			name:   "the block is in another file",
			dups:   map[string][]rules.Finding{"a.tsx": {dup(12, 14)}},
			shapes: map[string][]rules.Finding{"b.tsx": {block}},
			want:   map[string][]rules.Finding{"a.tsx": {dup(12, 14)}},
		},
		{
			name:   "a finding with no last line is read as one line",
			dups:   map[string][]rules.Finding{"a.tsx": {dup(12, 0), dup(31, 0)}},
			shapes: map[string][]rules.Finding{"a.tsx": {block}},
			want:   map[string][]rules.Finding{"a.tsx": {dup(31, 0)}},
		},
		{
			name:   "no duplicate at all",
			dups:   nil,
			shapes: map[string][]rules.Finding{"a.tsx": {block}},
			want:   nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := rules.DeferDuplicates(tc.dups, tc.shapes); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("kept %+v, want %+v", got, tc.want)
			}
		})
	}
}

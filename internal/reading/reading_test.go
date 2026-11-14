package reading_test

import (
	"testing"

	"github.com/kyleking/slop-strata/internal/intent"
	"github.com/kyleking/slop-strata/internal/reading"
)

func TestSentenceLeadsWithNetAndCountsFunctionsByHowTheyChanged(t *testing.T) {
	t.Parallel()
	cases := []struct {
		want   string
		name   string
		fields reading.Fields
	}{
		{
			name: "fix that edits functions over the limit",
			fields: reading.Fields{
				Intent:          intent.Intent{Kind: intent.Fix},
				MassGrown:       15.6,
				FuncHighTouched: 4,
			},
			want: "Fix: net +15.6 mass, 15.6 grown; touched 4 functions over CC 10",
		},
		{
			name: "feature that introduces functions over the limit",
			fields: reading.Fields{
				Intent:        intent.Intent{Kind: intent.Feat},
				MassGrown:     40,
				FuncHighAdded: 2,
			},
			want: "Feat: net +40.0 mass, 40.0 grown; added 2 functions over CC 10",
		},
		{
			name: "refactor that removes functions over the limit",
			fields: reading.Fields{
				Intent:       intent.Intent{Kind: intent.Refactor},
				MassRemoved:  55,
				FuncHighGone: 2,
			},
			want: "Refactor: net -55.0 mass, 0.0 grown; removed 2 functions over CC 10",
		},
		{
			name: "one touched function reads singular",
			fields: reading.Fields{
				Intent:          intent.Intent{Kind: intent.Fix},
				MassGrown:       1,
				FuncHighTouched: 1,
			},
			want: "Fix: net +1.0 mass, 1.0 grown; touched 1 function over CC 10",
		},
		{
			name: "names the directory with the largest net mass and tags it when it is a hotspot",
			fields: reading.Fields{
				Intent: intent.Intent{Kind: intent.Feat}, MassGrown: 164,
				Dirs: []reading.Dir{
					{Name: ".", Hotspot: true},
					{Name: "cmd/x", MassNet: 163.8, MassGrown: 163.8, Hotspot: true},
					{Name: "internal/y", MassNet: -20, MassGrown: 1},
				},
			},
			want: "Feat in `cmd/x` (a hotspot): net +164.0 mass, 164.0 grown",
		},
		{
			name: "equal net mass goes to the larger growth, then the name",
			fields: reading.Fields{
				Intent: intent.Intent{Kind: intent.Fix}, MassGrown: 1, MassRemoved: 1,
				Dirs: []reading.Dir{
					{Name: "b", MassNet: 5, MassGrown: 5},
					{Name: "a", MassNet: -5, MassGrown: 5},
					{Name: "c", MassNet: 5, MassGrown: 2},
				},
			},
			want: "Fix in `a`: net +0.0 mass, 1.0 grown",
		},
		{
			name: "no directory moved mass reads without a hotspot tag",
			fields: reading.Fields{
				Intent: intent.Intent{Kind: intent.Docs},
				Dirs:   []reading.Dir{{Name: "docs", Hotspot: true}, {Name: "."}},
			},
			want: "Docs in `.`: no complexity mass moved",
		},
		{
			name:   "no mass moved",
			fields: reading.Fields{Intent: intent.Intent{Kind: intent.Chore}},
			want:   "Chore: no complexity mass moved",
		},
		{
			name:   "unknown intent reads as a plain change",
			fields: reading.Fields{Intent: intent.Intent{Kind: intent.Unknown}, MassGrown: 3, MassRemoved: 1},
			want:   "Change: net +2.0 mass, 3.0 grown",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.fields.Sentence(); got != tc.want {
				t.Fatalf("Sentence() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSentenceNamesTheLeadFunctionAndSaysWhenOnlyTestsMoved(t *testing.T) {
	t.Parallel()
	cases := []struct {
		want   string
		name   string
		fields reading.Fields
	}{
		{
			name: "names the function over the limit that grew most",
			fields: reading.Fields{
				Intent: intent.Intent{Kind: intent.Feat}, MassGrown: 41.2, FuncHighTouched: 1,
				LeadHighFunc: "`run` (a/b.go:12)",
			},
			want: "Feat: net +41.2 mass, 41.2 grown; touched 1 function over CC 10; " +
				"most grown over CC 10: `run` (a/b.go:12)",
		},
		{
			name: "tests only is said and never reads as production mass",
			fields: reading.Fields{
				Intent: intent.Intent{Kind: intent.Test}, MassGrown: 30, TestsOnly: true,
			},
			want: "Test: tests only, net +30.0 mass, 30.0 grown",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.fields.Sentence(); got != tc.want {
				t.Fatalf("Sentence() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDisagreementNamesTheNumbersAgainstTheIntent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		want   string
		fields reading.Fields
	}{
		{
			name:   "refactor that adds mass",
			fields: reading.Fields{Intent: intent.Intent{Kind: intent.Refactor}, MassGrown: 10, MassRemoved: 5},
			want:   "a refactor that adds mass",
		},
		{
			name: "refactor that raises erosion",
			fields: reading.Fields{
				Intent: intent.Intent{Kind: intent.Refactor}, MassGrown: 5, MassRemoved: 5,
				ErosionBefore: 0.2, ErosionAfter: 0.3,
			},
			want: "a refactor that raises erosion",
		},
		{
			name:   "fix that adds far more than it touches",
			fields: reading.Fields{Intent: intent.Intent{Kind: intent.Fix}, MassGrown: 100, MassRemoved: 20},
			want:   "a fix that adds far more than it touches",
		},
		{
			name:   "test change that adds production mass",
			fields: reading.Fields{Intent: intent.Intent{Kind: intent.Test}, MassGrown: 3},
			want:   "a test change that adds production mass",
		},
		{
			name:   "docs never disagrees",
			fields: reading.Fields{Intent: intent.Intent{Kind: intent.Docs}, MassGrown: 50},
			want:   "",
		},
		{
			name:   "perf may add mass",
			fields: reading.Fields{Intent: intent.Intent{Kind: intent.Perf}, MassGrown: 50, ErosionAfter: 0.4},
			want:   "",
		},
		{
			name:   "feature never disagrees with itself",
			fields: reading.Fields{Intent: intent.Intent{Kind: intent.Feat}, MassGrown: 100},
			want:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.fields.Disagreement(); got != tc.want {
				t.Fatalf("Disagreement() = %q, want %q", got, tc.want)
			}
		})
	}
}

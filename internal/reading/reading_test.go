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
				Intent:    intent.Intent{Kind: intent.Fix},
				MassAdded: 1663.7, MassRemoved: 1648.1, MassGrown: 15.6,
				FuncHighTouched: 4,
			},
			want: "Fix: net +15.6 mass, 15.6 grown; touched 4 functions over CC 10",
		},
		{
			name: "feature that introduces functions over the limit",
			fields: reading.Fields{
				Intent:    intent.Intent{Kind: intent.Feat},
				MassAdded: 40, MassRemoved: 0, MassGrown: 40,
				FuncHighAdded: 2,
			},
			want: "Feat: net +40.0 mass, 40.0 grown; added 2 functions over CC 10",
		},
		{
			name: "refactor that removes functions over the limit",
			fields: reading.Fields{
				Intent:    intent.Intent{Kind: intent.Refactor},
				MassAdded: 5, MassRemoved: 60, MassGrown: 0,
				FuncHighGone: 2,
			},
			want: "Refactor: net -55.0 mass, 0.0 grown; removed 2 functions over CC 10",
		},
		{
			name:   "no mass moved",
			fields: reading.Fields{Intent: intent.Intent{Kind: intent.Chore}},
			want:   "Chore: no complexity mass moved",
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

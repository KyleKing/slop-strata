package intent_test

import (
	"testing"

	"github.com/kyleking/slop-strata/internal/intent"
)

func TestForChangeTakesTheMajorityKindAndBreaksTiesByCommitOrder(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		want    intent.Kind
		intents []intent.Intent
	}{
		{
			name: "majority wins",
			intents: []intent.Intent{
				{Kind: intent.Feat}, {Kind: intent.Feat}, {Kind: intent.Fix},
			},
			want: intent.Feat,
		},
		{
			name:    "unknown commits do not vote",
			intents: []intent.Intent{{Kind: intent.Unknown}, {Kind: intent.Unknown}, {Kind: intent.Fix}},
			want:    intent.Fix,
		},
		{
			name:    "tie goes to the kind of the earlier commit",
			intents: []intent.Intent{{Kind: intent.Fix}, {Kind: intent.Feat}},
			want:    intent.Fix,
		},
		{
			name:    "tie in the other order goes the other way",
			intents: []intent.Intent{{Kind: intent.Feat}, {Kind: intent.Fix}},
			want:    intent.Feat,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for range 20 {
				if got := intent.ForChange(tc.intents).Kind; got != tc.want {
					t.Fatalf("ForChange() kind = %q, want %q", got, tc.want)
				}
			}
		})
	}
}

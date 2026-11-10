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

func TestOfReadsSubjectThenBranchThenRevert(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		subject string
		branch  string
		body    string
		want    intent.Intent
	}{
		{
			name:    "conventional subject with scope and breaking mark",
			subject: "feat(api)!: add export",
			want:    intent.Intent{Kind: intent.Feat, Scope: "api", Summary: "add export"},
		},
		{
			name:    "perf reads as refactor",
			subject: "perf: tighten the loop", want: intent.Intent{Kind: intent.Refactor, Summary: "tighten the loop"},
		},
		{
			name:    "branch decides when the subject does not",
			subject: "Merge pull request #4", branch: "fix/login-loop",
			want: intent.Intent{Kind: intent.Fix, Summary: "Merge pull request #4"},
		},
		{
			name:    "revert in the subject",
			subject: `Revert "feat: add export"`,
			want:    intent.Intent{Kind: intent.Unknown, Summary: `Revert "feat: add export"`, Revert: true},
		},
		{
			name:    "revert in the body",
			subject: "tidy", body: "This reverts commit 1234abcd.",
			want: intent.Intent{Kind: intent.Unknown, Summary: "tidy", Revert: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := intent.Of(tc.subject, tc.branch, tc.body); got != tc.want {
				t.Fatalf("Of() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

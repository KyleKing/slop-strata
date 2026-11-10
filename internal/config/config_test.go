package config_test

import (
	"testing"

	"github.com/kyleking/slop-strata/internal/config"
)

const dependabot = "dependabot[bot] <49699333+dependabot[bot]@users.noreply.github.com>"

func TestAuthorClassMatchesLoginByNameOrEmail(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		login     string
		author    string
		committer string
		want      bool
	}{
		{name: "name", login: "dependabot[bot]", author: dependabot, want: true},
		{name: "email", login: "49699333+dependabot[bot]@users.noreply.github.com", author: dependabot, want: true},
		{name: "case insensitive", login: "Dependabot[BOT]", author: dependabot, want: true},
		{name: "committer", login: "dependabot[bot]", author: "Dev <dev@x>", committer: dependabot, want: true},
		{name: "partial name", login: "dependabot", author: dependabot, want: false},
		{name: "other bot", login: "copilot[bot]", author: dependabot, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			class := config.AuthorClass{Name: "bot", Logins: []string{tc.login}}
			if got := class.Matches(tc.author, tc.committer, ""); got != tc.want {
				t.Fatalf("Matches(%q, %q) with login %q = %t, want %t", tc.author, tc.committer, tc.login, got, tc.want)
			}
		})
	}
}

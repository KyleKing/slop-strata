package config_test

import (
	"os"
	"path/filepath"
	"strings"
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
		{name: "suffix on name", login: "*[bot]", author: dependabot, want: true},
		{
			name: "suffix on email local part", login: "*[bot]",
			author: "Dev <49699333+dependabot[bot]@users.noreply.github.com>", want: true,
		},
		{name: "suffix ignores people", login: "*[bot]", author: "Dev <dev@x>", want: false},
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

func TestClassifyBuiltInAndRepositoryClasses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		file   string
		author string
		body   string
		want   string
	}{
		{
			name:   "co-author trailer of a coding agent",
			author: "Dev <dev@x>", body: "Fix parser\n\nCo-Authored-By: Copilot <c@x>", want: "agent",
		},
		{
			name:   "app login ending in [bot]",
			author: dependabot, want: "bot",
		},
		{
			name:   "person",
			author: "Dev <dev@x>", body: "Fix parser", want: "person",
		},
		{
			name:   "repository class is tried before the built-ins",
			file:   "[[author_class]]\nname = \"ci\"\nlogins = [\"dependabot[bot]\"]\n",
			author: dependabot, want: "ci",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if tc.file != "" {
				if err := os.WriteFile(filepath.Join(root, config.FileName), []byte(tc.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := config.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Classify(tc.author, "", tc.body); got != tc.want {
				t.Fatalf("Classify(%q) = %q, want %q", tc.author, got, tc.want)
			}
		})
	}
}

func TestLoadReadsSettingsOverDefaults(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		file     string
		skip     []string
		maxBytes int64
		wantErr  bool
		expt     bool
	}{
		{name: "no file keeps defaults", maxBytes: config.Default().MaxFileBytes},
		{
			name:     "settings replace defaults and the rest stay",
			file:     "max_file_bytes = 4096\nexperimental = true\nskip = [\"vendor/**\"]\n",
			maxBytes: 4096, expt: true, skip: []string{"vendor/**"},
		},
		{name: "unknown key is an error", file: "max_fil_bytes = 4096\n", wantErr: true},
		{
			name:    "bad trailer pattern is an error",
			file:    "[[author_class]]\nname = \"ci\"\ntrailers = [\"(\"]\n",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if tc.file != "" {
				if err := os.WriteFile(filepath.Join(root, config.FileName), []byte(tc.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := config.Load(root)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Load() error = %v, want error %t", err, tc.wantErr)
			}
			if !tc.wantErr {
				checkSettings(t, cfg, tc.maxBytes, tc.expt, tc.skip)
			}
		})
	}
}

// checkSettings asserts the fields a config file sets, and that the minified cap
// keeps its default.
func checkSettings(t *testing.T, cfg *config.Config, maxBytes int64, expt bool, skip []string) {
	t.Helper()
	if cfg.MaxFileBytes != maxBytes || cfg.Experimental != expt {
		t.Errorf("max_file_bytes = %d, experimental = %t; want %d, %t",
			cfg.MaxFileBytes, cfg.Experimental, maxBytes, expt)
	}
	if cfg.MinifiedAvgLine != config.Default().MinifiedAvgLine {
		t.Errorf("minified_avg_line = %d, want the default", cfg.MinifiedAvgLine)
	}
	if strings.Join(cfg.Skip, ",") != strings.Join(skip, ",") {
		t.Errorf("Skip = %v, want %v", cfg.Skip, skip)
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestUsageErrorsAreCheckedBeforeTheRepository(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		argv []string
	}{
		{name: "unknown subcommand", argv: []string{"bogus-cmd", "--repo", "/no/such/dir"}},
		{name: "unknown flag", argv: []string{"change", "--bogus"}},
		{name: "empty inline value", argv: []string{"change", "--rev=", "HEAD~1"}},
		{name: "value flag at the end", argv: []string{"change", "--rev"}},
		{name: "flag the command does not take", argv: []string{"init", "--pairs", "x"}},
		{name: "flag before the subcommand", argv: []string{"--json", "change"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			err := run(context.Background(), tc.argv, &out, io.Discard)
			if !errors.Is(err, errUsage) {
				t.Fatalf("run(%v) = %v, want a usage error", tc.argv, err)
			}
			if strings.Contains(err.Error(), "repository") {
				t.Errorf("usage error reached repository discovery: %v", err)
			}
		})
	}
}

func TestCommandHelpListsOnlyItsFlags(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := run(context.Background(), []string{"history", "--help"}, &out, io.Discard); err != nil {
		t.Fatalf("history --help: %v", err)
	}
	help := out.String()
	if !strings.Contains(help, "--since") {
		t.Errorf("history help misses --since:\n%s", help)
	}
	for _, foreign := range []string{"--pairs", "--write-baseline"} {
		if strings.Contains(help, foreign) {
			t.Errorf("history help lists %s, which history does not take:\n%s", foreign, help)
		}
	}
}

func TestVersionJSONNamesTheCacheFile(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--version", "--json"}, &out, io.Discard); err != nil {
		t.Fatalf("--version --json: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("parsing %q: %v", out.String(), err)
	}
	if got["version"] != version || !strings.HasPrefix(got["cache"], "blobs-") || len(got) != 2 {
		t.Errorf("version --json = %v", got)
	}
}

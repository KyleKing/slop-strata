package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// update rewrites the golden files from the current output instead of comparing.
var update = flag.Bool("update", false, "rewrite golden files") //nolint:gochecknoglobals // a test flag

// goldenCommit is one commit of the deterministic fixture: its fixed date, its
// message, and the files it writes.
type goldenCommit struct {
	files map[string]string
	date  string
	msg   string
}

// goldenHistory is the fixture every golden runs against. Its dates are fixed
// and its identities invented, so commit ids and every number derived from them
// repeat on every run.
var goldenHistory = []goldenCommit{
	{
		date: "2026-01-05T10:00:00Z", msg: "feat: add the tally",
		files: map[string]string{"tally.go": `package tally

import "fmt"

// Sum adds the parts that match the filter.
func Sum(parts []int, keep func(int) bool) int {
	total := 0
	for _, p := range parts {
		if keep(p) {
			if p > 0 {
				total += p
			} else {
				fmt.Println("negative", p)
			}
		}
	}

	return total
}
`},
	},
	{
		date: "2026-01-12T10:00:00Z", msg: "feat: read the config",
		files: map[string]string{"config.py": `def load(path):
    try:
        return open(path).read()
    except Exception:
        pass
`},
	},
	{
		date: "2026-01-20T10:00:00Z", msg: "refactor: name the filter",
		files: map[string]string{"tally.go": `package tally

import "fmt"

// Sum adds the parts that match the filter.
func Sum(parts []int, keep func(int) bool) int {
	total := 0
	for _, p := range parts {
		if keep(p) {
			total += clamp(p)
		}
	}

	return total
}

// clamp no longer lets a negative part through.
func clamp(p int) int {
	if p < 0 {
		fmt.Println("negative", p)

		return 0
	}

	return p
}
`},
	},
}

// deterministicRepo builds goldenHistory in a fresh repository.
func deterministicRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "--initial-branch=main")
	for _, c := range goldenHistory {
		for name, body := range c.files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		gitIn(t, dir, "add", "-A")
		cmd := exec.CommandContext(t.Context(), "git", "commit", "-m", c.msg) //nolint:gosec // args are test literals
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com",
			"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com",
			"GIT_AUTHOR_DATE="+c.date, "GIT_COMMITTER_DATE="+c.date)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git commit: %v\n%s", err, out)
		}
	}

	return dir
}

// normalize drops what varies per run (the envelope's time and the temporary
// repository path) and re-encodes with sorted keys, so a golden is stable.
func normalize(t *testing.T, out string) []byte {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parsing JSON: %v\n%s", err, out)
	}
	env["at"] = ""
	env["repo"] = "<repo>"
	body, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("encoding envelope: %v", err)
	}

	return append(body, '\n')
}

// checkGolden compares got with testdata/<name>.golden, or rewrites it under -update.
func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}

		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // path is built from a test name
	if err != nil {
		t.Fatalf("reading golden (run go test -update): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from its golden; review the output and run go test -update\n%s", path, got)
	}
}

func TestGoldenSubcommands(t *testing.T) {
	t.Parallel()
	repo := deterministicRepo(t)
	cases := []struct {
		name string
		argv []string
	}{
		{name: "change", argv: []string{"change", "--json", "--base", "HEAD~1", "--rev", "HEAD", "--experimental"}},
		{name: "snapshot", argv: []string{"snapshot", "--json", "--rev", "HEAD", "--experimental"}},
		{name: "history", argv: []string{"history", "--json", "--rev", "HEAD", "--since", "2025-12-01"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := runCLI(t, repo, tc.argv...)
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			checkGolden(t, tc.name, normalize(t, out))
		})
	}
}

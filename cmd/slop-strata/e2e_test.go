package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain points the user cache at a throwaway directory, so a run never reads
// or writes the developer's blob cache and a stale entry cannot pass a test.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "slop-strata-cmd-test")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("HOME", dir); err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_CACHE_HOME", dir); err != nil {
		panic(err)
	}
	code := m.Run()
	if err := os.RemoveAll(dir); err != nil {
		panic(err)
	}
	os.Exit(code)
}

// fixtureRepo builds a small repo exercising every pipeline stage: a complex
// function, a silent error, a timing-hack test, a rename, and a worktree edit.
func fixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...) //nolint:gosec // args are test literals
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	run("init", "--initial-branch=main")
	write("main.go", `package main

import "fmt"

// now uses a plain loop
func big(a, b, c int) int {
	if a > 0 {
		for i := 0; i < a; i++ {
			if b > 0 && c > 0 {
				fmt.Println(i)
			}
		}
	}
	return a + b + c
}
`)
	write("util_test.go", `package main

import (
	"testing"
	"time"
)

func TestWaits(t *testing.T) { time.Sleep(1) }
`)
	run("add", "-A")
	run("commit", "-m", "feat: init")
	write("helper.py", "def f(x):\n    try:\n        return x / 0\n    except Exception:\n        pass\n")
	run("add", "-A")
	run("commit", "-m", "feat: python helper")

	return dir
}

func runCLI(t *testing.T, repo string, argv ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := run(context.Background(), append(argv, "--repo", repo), &out)

	return out.String(), err
}

func TestSnapshotJSON(t *testing.T) {
	t.Parallel()
	repo := fixtureRepo(t)
	out, err := runCLI(t, repo, "snapshot", "--json", "--experimental")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	var env struct {
		Command string `json:"command"`
		Result  struct {
			Findings map[string][]struct {
				Rule string `json:"rule"`
			} `json:"findings"`
			Total struct {
				Erosion   float64 `json:"erosion"`
				FuncCount int     `json:"func_count"`
			} `json:"total"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parsing JSON: %v\n%s", err, out)
	}
	if env.Command != "snapshot" || env.Result.Total.FuncCount != 3 {
		t.Fatalf("unexpected snapshot: %s", out)
	}
	var found []string
	for _, fs := range env.Result.Findings {
		for _, f := range fs {
			found = append(found, f.Rule)
		}
	}
	for _, want := range []string{"go/timing-hack", "py/silent-except", "x/narrating-comment"} {
		ok := false
		for _, r := range found {
			if r == want {
				ok = true
			}
		}
		if !ok {
			t.Fatalf("want rule %s in findings, got %v", want, found)
		}
	}
}

func TestChangeWorktree(t *testing.T) {
	t.Parallel()
	repo := fixtureRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "extra.go"),
		[]byte("package main\n\nfunc sneaky() { _ = g() }\n\nfunc g() error { return nil }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, repo, "change", "--json", "--experimental")
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if !strings.Contains(out, `"mass_added"`) || !strings.Contains(out, "go/silent-error") {
		t.Fatalf("unexpected change output: %s", out)
	}
}

func TestHistoryWalk(t *testing.T) {
	t.Parallel()
	repo := fixtureRepo(t)
	out, err := runCLI(t, repo, "history", "--json")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	var env struct {
		Result struct {
			Lineage struct {
				Survival []struct {
					Cohort string `json:"cohort"`
					Added  int    `json:"added"`
				} `json:"survival"`
			} `json:"lineage"`
			Changes int `json:"changes"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parsing JSON: %v\n%s", err, out)
	}
	if env.Result.Changes != 2 || len(env.Result.Lineage.Survival) == 0 {
		t.Fatalf("unexpected history: %s", out)
	}
}

func TestSnapshotStaleRules(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...) //nolint:gosec // args are test literals
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	run("init", "--initial-branch=main")
	write("main.go", `package main

import "fmt"

func usedFn() {}

func main() { fmt.Println(usedFn, "timeout") }
`)
	write("README.md", "Call usedFn() here, or phantomFn for the old path.\n")
	write("settings.toml", "timeout = 30\nghost_key = 1\n")
	write(".slop-strata.toml", "config_paths = [\"*.toml\"]\n")
	run("add", "-A")
	run("commit", "-m", "feat: init")

	out, err := runCLI(t, dir, "snapshot", "--json", "--experimental")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	var env struct {
		Result struct {
			Findings map[string][]struct {
				Rule  string `json:"rule"`
				Match string `json:"match"`
			} `json:"findings"`
			Total struct {
				Files int `json:"files"`
			} `json:"total"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parsing JSON: %v\n%s", err, out)
	}
	if env.Result.Total.Files != 1 {
		t.Fatalf("text files leaked into the file count: %s", out)
	}
	has := func(path, rule, match string) bool {
		for _, f := range env.Result.Findings[path] {
			if f.Rule == rule && f.Match == match {
				return true
			}
		}

		return false
	}
	if !has("README.md", "x/stale-prose", "phantomFn") {
		t.Fatalf("phantomFn not flagged in README.md: %s", out)
	}
	if has("README.md", "x/stale-prose", "usedFn") {
		t.Fatalf("usedFn flagged though it resolves: %s", out)
	}
	if !has("settings.toml", "x/stale-config", "ghost_key") {
		t.Fatalf("ghost_key not flagged in settings.toml: %s", out)
	}
	if has("settings.toml", "x/stale-config", "timeout") {
		t.Fatalf("timeout flagged though code reads it: %s", out)
	}
}

func TestChangeLastRef(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...) //nolint:gosec // args are test literals
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	run("init", "--initial-branch=main")
	write("a.go", "package main\n\nfunc helper() {}\n\nfunc main() { helper() }\n")
	write("b.go", "package main\n\nfunc other() { helper() }\n")
	run("add", "-A")
	run("commit", "-m", "feat: init")
	write("b.go", "package main\n\nfunc other() {}\n")
	run("add", "-A")
	run("commit", "-m", "fix: drop the call")

	out, err := runCLI(t, dir, "change", "--base", "HEAD~1", "--rev", "HEAD", "--json")
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	var env struct {
		Result struct {
			Findings []struct {
				Path  string `json:"path"`
				Rule  string `json:"rule"`
				Match string `json:"match"`
			} `json:"findings"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parsing JSON: %v\n%s", err, out)
	}
	found := false
	for _, f := range env.Result.Findings {
		if f.Rule == "x/last-ref" && f.Path == "b.go" && f.Match == "helper" {
			found = true
		}
	}
	if !found {
		t.Fatalf("last use of helper not flagged: %s", out)
	}
}

func TestExplain(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--explain", "erosion"}, &out); err != nil {
		t.Fatalf("explain: %v", err)
	}
	if !strings.Contains(out.String(), "complexity mass") {
		t.Fatalf("explain output: %s", out.String())
	}
}

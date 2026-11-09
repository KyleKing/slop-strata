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

// fixtureRepo builds a small repo exercising every pipeline stage: a complex
// function, a silent error, a timing-hack test, a rename, and a worktree edit.
func fixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
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

// was a stub
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
	repo := fixtureRepo(t)
	out, err := runCLI(t, repo, "snapshot", "--json")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	var env struct {
		Command string `json:"command"`
		Result  struct {
			Total struct {
				Erosion   float64 `json:"erosion"`
				FuncCount int     `json:"func_count"`
			} `json:"total"`
			Findings map[string][]struct {
				Rule string `json:"rule"`
			} `json:"findings"`
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
	repo := fixtureRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "extra.go"),
		[]byte("package main\n\nfunc sneaky() { _ = g() }\n\nfunc g() error { return nil }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, repo, "change", "--json")
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if !strings.Contains(out, `"mass_added"`) || !strings.Contains(out, "go/silent-error") {
		t.Fatalf("unexpected change output: %s", out)
	}
}

func TestHistoryWalk(t *testing.T) {
	repo := fixtureRepo(t)
	out, err := runCLI(t, repo, "history", "--json")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	var env struct {
		Result struct {
			Changes int `json:"changes"`
			Lineage struct {
				Survival []struct {
					Cohort string `json:"cohort"`
					Added  int    `json:"added"`
				} `json:"survival"`
			} `json:"lineage"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parsing JSON: %v\n%s", err, out)
	}
	if env.Result.Changes != 2 || len(env.Result.Lineage.Survival) == 0 {
		t.Fatalf("unexpected history: %s", out)
	}
}

func TestExplain(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--explain", "erosion"}, &out); err != nil {
		t.Fatalf("explain: %v", err)
	}
	if !strings.Contains(out.String(), "complexity mass") {
		t.Fatalf("explain output: %s", out.String())
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/report"
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

// gitIn runs git in dir with a fixed identity.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...) //nolint:gosec // args are test literals
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func runCLI(t *testing.T, repo string, argv ...string) (string, error) {
	t.Helper()
	out, _, err := runCLIStreams(t, repo, argv...)

	return out, err
}

// runCLIStreams runs the CLI against repo and returns what it wrote to stdout
// and stderr.
func runCLIStreams(t *testing.T, repo string, argv ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := run(context.Background(), append(argv, "--repo", repo), &out, &errOut)

	return out.String(), errOut.String(), err
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

// changeEnvelope is the part of a change envelope the pairs test reads.
type changeEnvelope struct {
	Result *struct {
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	} `json:"result"`
	Error string `json:"error"`
}

func TestChangePairsEmitsOneEnvelopePerPair(t *testing.T) {
	t.Parallel()
	repo := fixtureRepo(t)
	pairs := filepath.Join(t.TempDir(), "pairs.txt")
	lines := "HEAD~1 HEAD\nno-such-rev HEAD\n# comment\n\nHEAD~0 HEAD\n"
	if err := os.WriteFile(pairs, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, repo, "change", "--json", "--pairs", pairs)
	if err == nil {
		t.Fatal("want an error for the failed pair")
	}
	envs := make([]changeEnvelope, 0, 3)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var env changeEnvelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("line is not one envelope: %v\n%s", err, line)
		}
		envs = append(envs, env)
	}
	if len(envs) != 3 {
		t.Fatalf("want 3 envelopes, got %d:\n%s", len(envs), out)
	}
	if envs[0].Result == nil || len(envs[0].Result.Files) != 1 || envs[0].Result.Files[0].Path != "helper.py" {
		t.Errorf("first pair should change helper.py only: %+v", envs[0])
	}
	if envs[1].Error == "" || envs[1].Result != nil {
		t.Errorf("second pair should carry an error and no result: %+v", envs[1])
	}
	if envs[2].Error != "" || envs[2].Result == nil {
		t.Errorf("third pair should score cleanly: %+v", envs[2])
	}
}

func TestChangeScoresPureRename(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	gitIn(t, repo, "init", "--initial-branch=main")
	body := "def helper():\n    return 1\n\n\ndef main():\n    return helper()\n"
	if err := os.WriteFile(filepath.Join(repo, "old.py"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-m", "feat: init")
	gitIn(t, repo, "mv", "old.py", "new.py")
	gitIn(t, repo, "commit", "-m", "refactor: rename")

	out, err := runCLI(t, repo, "change", "--json", "--base", "HEAD~1", "--rev", "HEAD")
	if err != nil {
		t.Fatalf("change: %v\n%s", err, out)
	}
	var env struct {
		Result *struct {
			Files []struct {
				Path string `json:"path"`
			} `json:"files"`
			Errors []skippedRow `json:"errors"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parsing JSON: %v\n%s", err, out)
	}
	if env.Result == nil || len(env.Result.Errors) != 0 {
		t.Fatalf("pure rename should score without errors: %s", out)
	}
	if len(env.Result.Files) != 1 || env.Result.Files[0].Path != "new.py" {
		t.Errorf("want new.py as the one changed file: %s", out)
	}
}

func TestCountsNameUnscoredFiles(t *testing.T) {
	t.Parallel()
	repo := fixtureRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "blob.go"), []byte("package p\x00\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-m", "feat: binary blob")
	out, _, err := runCLIStreams(t, repo, "snapshot", "--json")
	if err != nil {
		t.Fatalf("snapshot --json: %v", err)
	}
	var env struct {
		Counts report.Counts `json:"counts"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parsing JSON: %v", err)
	}
	if env.Counts != (report.Counts{Skipped: 1}) {
		t.Errorf("counts = %+v, want one skipped file", env.Counts)
	}
	_, errOut, err := runCLIStreams(t, repo, "snapshot")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !strings.Contains(errOut, "1 skipped, 0 with parse errors") {
		t.Errorf("text mode stderr = %q, want the skipped warning", errOut)
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

func TestSnapshotMarksThinDirsProvisional(t *testing.T) {
	t.Parallel()
	repo := fixtureRepo(t)
	out, err := runCLI(t, repo, "snapshot", "--json")
	if err != nil {
		t.Fatalf("snapshot --json: %v", err)
	}
	var env struct {
		Result struct {
			Dirs []struct {
				Dir         string `json:"dir"`
				Provisional bool   `json:"provisional"`
			} `json:"dirs"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parsing JSON: %v\n%s", err, out)
	}
	if len(env.Result.Dirs) == 0 || !env.Result.Dirs[0].Provisional {
		t.Errorf("fixture directories under the function floor are not provisional: %s", out)
	}
	text, err := runCLI(t, repo, "snapshot")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !strings.Contains(text, "* fewer than 5 samples") || !strings.Contains(text, "0.00*") {
		t.Errorf("snapshot text lacks the provisional mark and footnote:\n%s", text)
	}
}

func TestHistoryTextTrendColumns(t *testing.T) {
	t.Parallel()
	repo := fixtureRepo(t)
	out, err := runCLI(t, repo, "history")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	header := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, colDir+" ") {
			header = line

			break
		}
	}
	for _, col := range []string{"churn_14d", "churn_30d", "mass_8w"} {
		if !strings.Contains(header, col) {
			t.Errorf("dir table header %q lacks %s", header, col)
		}
	}
	if !regexp.MustCompile("[▁▂▃▄▅▆▇█]{8}").MatchString(out) {
		t.Errorf("history text has no 8-tick mass sparkline:\n%s", out)
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

func TestChangeOutputRepeats(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const n = 20
	var decls, calls strings.Builder
	decls.WriteString("package main\n\n")
	calls.WriteString("package main\n\nfunc other() {\n")
	for i := range n {
		fmt.Fprintf(&decls, "func helper%02d() {}\n", i)
		fmt.Fprintf(&calls, "\thelper%02d()\n", i)
	}
	calls.WriteString("}\n")

	gitIn(t, dir, "init", "--initial-branch=main")
	write("a.go", decls.String())
	write("b.go", calls.String())
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-m", "feat: add helpers")
	write("b.go", "package main\n\nfunc other() {}\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-m", "fix: drop the calls")

	result := func() []byte {
		t.Helper()
		out, err := runCLI(t, dir, "change", "--base", "HEAD~1", "--rev", "HEAD", "--json")
		if err != nil {
			t.Fatalf("change: %v", err)
		}
		var env struct {
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("parsing JSON: %v\n%s", err, out)
		}

		return env.Result
	}
	first, second := result(), result()
	if got := strings.Count(string(first), `"x/last-ref"`); got != n {
		t.Fatalf("want %d last-ref findings, got %d", n, got)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("two runs of the same change differ:\n%s\n---\n%s", first, second)
	}
}

func TestChangeReportsProseNamingRemovedNames(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gitIn(t, dir, "init", "--initial-branch=main")
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("lib.go", "package main\n\nfunc old_helper() int { return 1 }\n\nfunc main() { _ = old_helper() }\n")
	write("README.md", "# Fixture\n\nCall `old_helper` first.\n\nThe client wraps `requests.Session`.\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-m", "feat: add helper")
	write("lib.go", "package main\n\nfunc new_helper() int { return 1 }\n\nfunc main() { _ = new_helper() }\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-m", "refactor: rename helper")

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
				Line  int    `json:"line"`
			} `json:"findings"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parsing JSON: %v\n%s", err, out)
	}
	var proseHits []string
	for _, f := range env.Result.Findings {
		if f.Rule == "x/stale-prose" {
			proseHits = append(proseHits, fmt.Sprintf("%s:%d %s", f.Path, f.Line, f.Match))
		}
	}
	if want := []string{"README.md:3 old_helper"}; !slices.Equal(proseHits, want) {
		t.Fatalf("stale prose = %v, want %v", proseHits, want)
	}
}

func TestExplain(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--explain", "erosion"}, &out, io.Discard); err != nil {
		t.Fatalf("explain: %v", err)
	}
	if !strings.Contains(out.String(), "complexity mass") {
		t.Fatalf("explain output: %s", out.String())
	}
}

// metricHeaders are the text table columns that name a metric. Each needs the
// same --explain entry as the JSON docs map.
var metricHeaders = []string{ //nolint:gochecknoglobals // the header vocabulary is fixed
	"churn_14d", "churn_30d", "concentration", "erosion", "hotspot", "mass", "mass_8w",
	"over_50", "rework", "rule_hits", "s_30d", "s_90d",
}

func TestExplainCoversEveryMetric(t *testing.T) {
	t.Parallel()
	var listing bytes.Buffer
	if err := run(context.Background(), []string{"--explain"}, &listing, io.Discard); err != nil {
		t.Fatalf("--explain with no name: %v", err)
	}
	names := slices.Concat(report.Names(), metricHeaders)
	for _, name := range names {
		if !strings.Contains(listing.String(), name+"\n") {
			t.Errorf("--explain with no name does not list %s", name)
		}
	}
	for name := range report.Docs {
		checkExplain(t, name)
	}
	for _, name := range metricHeaders {
		checkExplain(t, name)
	}
}

func checkExplain(t *testing.T, name string) {
	t.Helper()
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--explain", name}, &out, io.Discard); err != nil {
		t.Errorf("--explain %s: %v", name, err)

		return
	}
	if !strings.HasPrefix(out.String(), name+"\n") || len(strings.TrimSpace(out.String())) <= len(name) {
		t.Errorf("--explain %s has no explain text: %q", name, out.String())
	}
}

package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/lang"
	"github.com/kyleking/slop-strata/internal/structure"
)

const pyTwoRuns = `class A:
    def run(self):
        return 1


class B:
    def run(self):
        if self.x:
            return 1
        return 2
`

const pyTwoRunsAChanged = `class A:
    def run(self):
        if self.x:
            return 1
        if self.y:
            return 2
        return 3


class B:
    def run(self):
        if self.x:
            return 1
        return 2
`

const pyTwoRunsBChanged = `class A:
    def run(self):
        return 1


class B:
    def run(self):
        if self.x:
            return 1
        if self.y:
            return 2
        return 3
`

const pyClosures = `def f(xs):
    a = lambda: 1
    b = lambda y: y
    return a, b
`

const pyClosureChanged = `def f(xs):
    a = lambda: 1
    b = lambda y: y if y else 0
    return a, b
`

func analyzePython(t *testing.T, src string) structure.FileMetrics {
	t.Helper()
	tree := lang.NewPool().Parse(lang.Python, []byte(src))
	t.Cleanup(tree.Close)

	return structure.Analyze(lang.Python, tree, []byte(src))
}

func massByKey(t *testing.T, fm structure.FileMetrics, key string) float64 {
	t.Helper()
	for i, k := range funcKeys(fm.Funcs) {
		if k == key {
			return structure.Mass(fm.Funcs[i])
		}
	}
	t.Fatalf("no function keyed %q in %v", key, funcKeys(fm.Funcs))

	return 0
}

func TestFuncGrowthMatchesSameNamedFunctionsByOwner(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		before string
		after  string
		grew   string // key of the one function the change grew; empty for none
	}{
		{name: "method of the first class grows", before: pyTwoRuns, after: pyTwoRunsAChanged, grew: "A.run"},
		{name: "method of the second class grows", before: pyTwoRuns, after: pyTwoRunsBChanged, grew: "B.run"},
		{name: "unchanged file grows nothing", before: pyTwoRuns, after: pyTwoRuns},
		{name: "closure matched by its position", before: pyClosures, after: pyClosureChanged, grew: "f/<anonymous>#2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			before := analyzePython(t, tc.before)
			after := analyzePython(t, tc.after)

			want := 0.0
			if tc.grew != "" {
				want = massByKey(t, after, tc.grew) - massByKey(t, before, tc.grew)
			}
			got, _ := funcGrowth(&before, &after, map[funcKey]float64{})
			if math.Abs(got-want) > 1e-9 {
				t.Fatalf("grown = %.3f, want %.3f", got, want)
			}
		})
	}
}

const svcBefore = `def load(path):
    try:
        return open(path).read()
    except Exception:
        pass
    return ""


def store(path, data):
    try:
        return len(data)
    except Exception:
        pass
    return 0
`

const svcAfter = `def load(path):
    try:
        return open(path).read()
    except Exception:
        pass
    return ""


def store(path, data):
    try:
        return len(data)
    except OSError:
        pass
    return 0


def flush(path):
    try:
        return 1
    except ValueError:
        pass
    return 0
`

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...) //nolint:gosec // args are test literals
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestChangeFindingsAreNewOnlyOnAddedLines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gitRun(t, dir, "init", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(dir, "svc.py"), []byte(svcBefore), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "feat: add store")
	if err := os.WriteFile(filepath.Join(dir, "svc.py"), []byte(svcAfter), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "fix: narrow store's except and add flush")

	out, err := runCLI(t, dir, "change", "--base", "HEAD~1", "--rev", "HEAD", "--json")
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	var env struct {
		Result struct {
			Findings []struct {
				Rule string `json:"rule"`
				Line int    `json:"line"`
				New  bool   `json:"new"`
			} `json:"findings"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parsing JSON: %v\n%s", err, out)
	}
	got := map[int]bool{}
	for _, f := range env.Result.Findings {
		if f.Rule == "py/silent-except" {
			got[f.Line] = f.New
		}
	}
	want := map[int]bool{4: false, 12: false, 20: true}
	if len(got) != len(want) {
		t.Fatalf("silent-except findings = %v, want %v", got, want)
	}
	for line, isNew := range want {
		if got[line] != isNew {
			t.Fatalf("line %d new = %t, want %t (all: %v)", line, got[line], isNew, got)
		}
	}
}

func TestChangeRootCommitDiffsFromEmptyTree(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gitRun(t, dir, "init", "--initial-branch=main")
	body := "package main\n\nfunc run(x int) int {\n\tif x > 0 {\n\t\treturn x\n\t}\n\treturn 0\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "feat: init")
	head := exec.CommandContext(t.Context(), "git", "rev-parse", "HEAD")
	head.Dir = dir
	raw, err := head.Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(string(raw))

	out, err := runCLI(t, dir, "change", "--base", sha+"^", "--rev", sha, "--json")
	if err != nil {
		t.Fatalf("change from a root commit: %v\n%s", err, out)
	}
	var env struct {
		Result struct {
			Delta struct {
				MassAdded float64 `json:"mass_added"`
			} `json:"delta"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parsing JSON: %v\n%s", err, out)
	}
	if env.Result.Delta.MassAdded == 0 {
		t.Fatalf("root commit's function counted no mass: %s", out)
	}
}

func TestChangeDefaultBaseFollowsUpstream(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	remote := filepath.Join(t.TempDir(), "origin.git")
	gitRun(t, dir, "init", "--initial-branch=main")
	gitRun(t, dir, "init", "--bare", "--initial-branch=main", remote)
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc a() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "feat: init")
	gitRun(t, dir, "remote", "add", "origin", remote)
	gitRun(t, dir, "push", "-u", "origin", "main")

	lib := "package main\n\nfunc b(x int) int {\n\tif x > 0 {\n\t\treturn x\n\t}\n\treturn 0\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "lib.go"), []byte(lib), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "feat: unpushed")
	edited := "package main\n\nfunc a() {}\n\nfunc c() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, dir, "change", "--json")
	if err != nil {
		t.Fatalf("change: %v\n%s", err, out)
	}
	var env struct {
		Result struct {
			Files []struct {
				Path string `json:"path"`
			} `json:"files"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parsing JSON: %v\n%s", err, out)
	}
	paths := make([]string, 0, len(env.Result.Files))
	for _, f := range env.Result.Files {
		paths = append(paths, f.Path)
	}
	if !slices.Contains(paths, "lib.go") {
		t.Fatalf("unpushed commit left out of the change: files = %v", paths)
	}
}

func TestChangeCountsUntrackedLines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gitRun(t, dir, "init", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "feat: init")
	if err := os.WriteFile(filepath.Join(dir, "fresh.go"),
		[]byte("package main\n\nfunc fresh() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, dir, "change", "--json")
	if err != nil {
		t.Fatalf("change: %v\n%s", err, out)
	}
	var env struct {
		Result struct {
			Files []struct {
				Path  string `json:"path"`
				Added int    `json:"added"`
			} `json:"files"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parsing JSON: %v\n%s", err, out)
	}
	for _, f := range env.Result.Files {
		if f.Path == "fresh.go" {
			if f.Added != 3 {
				t.Fatalf("untracked fresh.go added = %d, want 3", f.Added)
			}

			return
		}
	}
	t.Fatalf("untracked fresh.go missing from files: %s", out)
}

func TestHotspotCountsCommitsNotFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gitRun(t, dir, "init", "--initial-branch=main")
	if err := os.Mkdir(filepath.Join(dir, "pkg"), 0o750); err != nil {
		t.Fatal(err)
	}
	const commits = 6
	for i := range commits {
		body := fmt.Sprintf("package pkg\n\nvar n%d = %d\n", i, i)
		for _, name := range []string{"a.go", "b.go"} {
			if err := os.WriteFile(filepath.Join(dir, "pkg", name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		gitRun(t, dir, "add", "-A")
		gitRun(t, dir, "commit", "-m", fmt.Sprintf("chore: step %d", i))
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg", "a.go"),
		[]byte("package pkg\n\nvar n = 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, dir, "change", "--json")
	if err != nil {
		t.Fatalf("change: %v\n%s", err, out)
	}
	var env struct {
		Result struct {
			Dirs map[string]struct {
				Hotspot bool `json:"hotspot"`
			} `json:"dirs"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parsing JSON: %v\n%s", err, out)
	}
	pkg, ok := env.Result.Dirs["pkg"]
	if !ok {
		t.Fatalf("pkg missing from dirs: %s", out)
	}
	if pkg.Hotspot {
		t.Fatalf("pkg is %d commits touched, not a hotspot at 10: %s", commits, out)
	}
}

func TestParseErrorRowDescribesMissingNodes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		src  string
		want string
	}{
		{name: "missing node covers no bytes", src: "def f(:\n", want: "tree-sitter parse error (missing node)"},
		{name: "error node covers bytes", src: "x = = 1\n", want: "tree-sitter parse error (12.5% of bytes)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fm := analyzePython(t, tc.src)
			if got := parseErrorRow("p.py", fm).Reason; got != tc.want {
				t.Fatalf("reason = %q, want %q", got, tc.want)
			}
		})
	}
}

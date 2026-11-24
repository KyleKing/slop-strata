package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/report"
	"github.com/kyleking/slop-strata/internal/rules"
)

const ruleImportCycle = "x/import-cycle"

// commitFiles writes files into a repository, creating directories, and commits them.
func commitFiles(t *testing.T, repo, msg string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-m", msg)
}

// cycleRepo holds three Python modules that import each other in a ring, a
// test that imports one of them, and a Go command importing a Go package.
func cycleRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitIn(t, repo, "init", "--initial-branch=main")
	commitFiles(t, repo, "feat: init", map[string]string{
		"app/__init__.py":      "",
		"app/a.py":             "from app.b import b_one, b_two\n\na_one = b_one\na_two = b_two\n",
		"app/b.py":             "from app.core.c import c_one, c_two\n\nb_one = c_one\nb_two = c_two\n",
		"app/core/__init__.py": "",
		"app/core/c.py":        "import os\n\nfrom app.a import a_one\n\nc_one = c_two = os.sep\nprint(a_one)\n",
		"tests/test_a.py":      "from app.a import a_one\n\n\ndef test_a():\n    assert a_one\n",
		"go.mod":               "module example.com/fixture\n\ngo 1.26\n",
		"cmd/tool/main.go": "package main\n\nimport \"example.com/fixture/internal/store\"\n\n" +
			"func main() { store.Open() }\n",
		"internal/store/s.go": "package store\n\n// Open opens.\nfunc Open() {}\n",
	})

	return repo
}

// importsEnvelope is the part of a snapshot envelope the import tests read.
type importsEnvelope struct {
	Result struct {
		Findings map[string][]rules.Finding `json:"findings"`
		Dirs     []dirSnapshot              `json:"dirs"`
		TestDirs []dirSnapshot              `json:"test_dirs"`
		Imports  importSummary              `json:"imports"`
	} `json:"result"`
	Counts report.Counts `json:"counts"`
}

func snapshotImports(t *testing.T, repo string, args ...string) importsEnvelope {
	t.Helper()
	out, err := runCLI(t, repo, append([]string{"snapshot", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	var env importsEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parsing JSON: %v\n%s", err, out)
	}

	return env
}

// cycles lists the import-cycle findings of a snapshot.
func (e importsEnvelope) cycles() []rules.Finding {
	var out []rules.Finding
	for _, fs := range e.Result.Findings {
		for i := range fs {
			if fs[i].Rule == ruleImportCycle {
				out = append(out, fs[i])
			}
		}
	}

	return out
}

// fanOf answers a directory's fan-in and fan-out from a snapshot's rows.
func fanOf(t *testing.T, dirs []dirSnapshot, dir string) [2]int {
	t.Helper()
	for _, d := range dirs {
		if d.Dir == dir {
			return [2]int{d.FanIn, d.FanOut}
		}
	}
	t.Fatalf("no directory %s in %+v", dir, dirs)

	return [2]int{}
}

func TestSnapshotCountsTheImportGraph(t *testing.T) {
	t.Parallel()
	env := snapshotImports(t, cycleRepo(t))
	if want := (importSummary{Edges: 5, Cycles: 1, LargestCycle: 3}); env.Result.Imports != want {
		t.Errorf("imports = %+v, want %+v", env.Result.Imports, want)
	}
	if got := env.cycles(); len(got) != 0 {
		t.Errorf("an experimental rule reported without the flag: %+v", got)
	}
	wantFans := map[string][2]int{"app": {1, 1}, "app/core": {1, 1}, "cmd/tool": {0, 1}, "internal/store": {1, 0}}
	for dir, want := range wantFans {
		if got := fanOf(t, env.Result.Dirs, dir); got != want {
			t.Errorf("%s fan in/out = %v, want %v", dir, got, want)
		}
	}
	if got := fanOf(t, env.Result.TestDirs, "tests"); got != [2]int{0, 1} {
		t.Errorf("tests fan in/out = %v, want [0 1]", got)
	}
}

func TestSnapshotReportsAnImportCycleWithItsCheapestCut(t *testing.T) {
	t.Parallel()
	repo := cycleRepo(t)
	found := snapshotImports(t, repo, "--experimental").cycles()
	if len(found) != 1 {
		t.Fatalf("import cycles = %+v, want one", found)
	}
	wantNote := "a.py -> b.py -> core/c.py -> a.py; cheapest cut: core/c.py imports 1 name (a_one) from a.py; " +
		"3 files import each other in app"
	if found[0].Note != wantNote || found[0].Line != 3 || len(found[0].Members) != 3 {
		t.Errorf("finding = %+v\nwant note %q on line 3", found[0], wantNote)
	}

	text, stderr, err := runCLIStreams(t, repo, "snapshot", "--experimental", "--all")
	if err != nil {
		t.Fatalf("snapshot text: %v", err)
	}
	for _, want := range []string{
		"imports: 5 edges · 1 import_cycles · largest 3 files", "fan_in", "fan_out",
		"app/core/c.py:3  " + found[0].ID + "  " + ruleImportCycle,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("snapshot text lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(stderr, "baseline entries") {
		t.Errorf("no baseline, yet stderr speaks of stale entries: %s", stderr)
	}
}

// baselinedRepo is cycleRepo with its ring of three accepted in the baseline.
func baselinedRepo(t *testing.T) string {
	t.Helper()
	repo := cycleRepo(t)
	if _, err := runCLI(t, repo, "snapshot", "--experimental", "--write-baseline"); err != nil {
		t.Fatalf("write baseline: %v", err)
	}

	return repo
}

// shrinkCycle makes b.py import a.py back and nothing else: the ring of three becomes a pair.
func shrinkCycle(t *testing.T, repo string) {
	t.Helper()
	commitFiles(t, repo, "refactor: shrink the cycle", map[string]string{
		"app/b.py":      "from app.a import a_one\n\nb_one = b_two = a_one\n",
		"app/core/c.py": "import os\n\nc_one = c_two = os.sep\n",
	})
}

func TestBaselineStoresACyclesMembersAndCoversIt(t *testing.T) {
	t.Parallel()
	repo := baselinedRepo(t)
	data, err := os.ReadFile(filepath.Join(repo, ".slop-strata-baseline.json")) //nolint:gosec // a test-owned temp path
	if err != nil || !strings.Contains(string(data), `"members"`) || !strings.Contains(string(data), `"version": 3`) {
		t.Fatalf("baseline lacks the cycle's members: %v\n%s", err, data)
	}
	env := snapshotImports(t, repo, "--experimental")
	if len(env.cycles()) != 0 || env.Counts.StaleBaseline != 0 || env.Counts.Suppressed.Baseline == 0 {
		t.Errorf("a fresh baseline must cover the cycle with nothing stale: %+v, %+v", env.cycles(), env.Counts)
	}
	if env.Result.Imports.Cycles != 1 {
		t.Errorf("imports.cycles = %d, want 1: the count is taken before the baseline", env.Result.Imports.Cycles)
	}
}

func TestBaselineCoversAShrunkCycleAndCountsItStale(t *testing.T) {
	t.Parallel()
	repo := baselinedRepo(t)
	shrinkCycle(t, repo)
	shrunk := snapshotImports(t, repo, "--experimental")
	if len(shrunk.cycles()) != 0 || shrunk.Result.Imports.LargestCycle != 2 {
		t.Errorf("a shrunk cycle read as new: %+v, imports %+v", shrunk.cycles(), shrunk.Result.Imports)
	}
	if shrunk.Counts.StaleBaseline != 1 {
		t.Errorf("stale_baseline = %d, want 1", shrunk.Counts.StaleBaseline)
	}
	if plain := snapshotImports(t, repo); plain.Counts.StaleBaseline != 0 {
		t.Errorf("stale_baseline = %d without --experimental, want 0: the cycle rule did not run",
			plain.Counts.StaleBaseline)
	}
	_, stderr, err := runCLIStreams(t, repo, "snapshot", "--experimental")
	if err != nil {
		t.Fatalf("a stale baseline must not fail the run: %v", err)
	}
	for _, want := range []string{"1 baseline entries match no finding now", "snapshot --write-baseline"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %s", want, stderr)
		}
	}
}

func TestBaselineDoesNotCoverAGrownCycle(t *testing.T) {
	t.Parallel()
	repo := baselinedRepo(t)
	shrinkCycle(t, repo)
	commitFiles(t, repo, "feat: grow the cycle", map[string]string{
		"app/b.py": "from app.d import d_one\n\nb_one = b_two = d_one\n",
		"app/d.py": "from app.a import a_one\n\nd_one = a_one\n",
	})
	if grown := snapshotImports(t, repo, "--experimental").cycles(); len(grown) != 1 {
		t.Errorf("a cycle with a member the baseline never held must be reported: %+v", grown)
	}
}

// loopRepo has three commits: two TypeScript files with a one-way import, the
// commit that closes the loop, and one that only moves the import down a line.
func loopRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitIn(t, repo, "init", "--initial-branch=main")
	commitFiles(t, repo, "feat: init", map[string]string{
		"src/a.ts": "import { helper } from './b'\n\nexport const a = helper()\n",
		"src/b.ts": "export function helper(): number {\n  return 1\n}\n",
	})
	commitFiles(t, repo, "feat: close the loop", map[string]string{
		"src/b.ts": "import { a } from './a'\n\nexport function helper(): number {\n  return a\n}\n",
	})
	commitFiles(t, repo, "docs: comment the loop", map[string]string{
		"src/b.ts": "// reads a\nimport { a } from './a'\n\nexport function helper(): number {\n  return a\n}\n",
	})

	return repo
}

func TestChangeBriefSpeaksOfANewImportCycle(t *testing.T) {
	t.Parallel()
	repo := loopRepo(t)
	cases := []struct {
		name     string
		rev      string
		wantNext string
		args     []string
	}{
		{
			name: "the change that closes the loop", rev: "HEAD~1", args: []string{"--experimental"},
			wantNext: "next: src/a.ts:1 " + ruleImportCycle,
		},
		{name: "the rule is experimental", rev: "HEAD~1"},
		{name: "a change that only moves the import", rev: "HEAD", args: []string{"--experimental"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			argv := []string{"change", "--brief", "--exit-code", "--base", tc.rev + "~1", "--rev", tc.rev}
			argv = append(argv, tc.args...)
			out, err := runCLI(t, repo, argv...)
			if tc.wantNext == "" {
				if err != nil || out != "" {
					t.Errorf("want a quiet change, got %v\n%q", err, out)
				}

				return
			}
			if !errors.Is(err, errWorthALook) || !strings.Contains(out, tc.wantNext) {
				t.Errorf("want worth a look with %q, got %v\n%q", tc.wantNext, err, out)
			}
		})
	}
}

func TestChangeJSONCarriesTheCyclesCut(t *testing.T) {
	t.Parallel()
	out, err := runCLI(t, loopRepo(t), "change", "--json", "--experimental", "--base", "HEAD~2", "--rev", "HEAD~1")
	if err != nil {
		t.Fatalf("change --json: %v", err)
	}
	var env struct {
		Result struct {
			Findings []findingRow `json:"findings"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	const wantCut = "cheapest cut: a.ts imports 1 name (helper) from b.ts"
	for _, f := range env.Result.Findings {
		if f.Rule == ruleImportCycle && f.New && strings.Contains(f.Note, wantCut) {
			return
		}
	}
	t.Errorf("change findings = %+v, want a new import cycle noting %q", env.Result.Findings, wantCut)
}

func TestChangeIsQuietOnceTheBaselineHoldsTheCycle(t *testing.T) {
	t.Parallel()
	repo := loopRepo(t)
	gitIn(t, repo, "checkout", "--quiet", "HEAD~1")
	if _, err := runCLI(t, repo, "snapshot", "--experimental", "--write-baseline"); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	out, err := runCLI(t, repo,
		"change", "--brief", "--exit-code", "--experimental", "--base", "HEAD~1", "--rev", "HEAD")
	if err != nil || out != "" {
		t.Errorf("a baselined cycle must not make the change worth a look: %v\n%q", err, out)
	}
}

package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kyleking/slop-strata/internal/archtrend"
	"github.com/kyleking/slop-strata/internal/depth"
	"github.com/kyleking/slop-strata/internal/fixturetest"
	"github.com/kyleking/slop-strata/internal/report"
	"github.com/kyleking/slop-strata/internal/rules"
)

// removed is the content that deletes a file in a written history.
const removed = "<removed>\n"

// writtenCommit is one commit of a history written as a fixturetest.Tree.
type writtenCommit struct {
	files   map[string]string
	subject string
	week    int
	order   int
}

// writtenHistory reads a history out of a tree whose keys are
// "<week>.<n> <subject>/<path>": commit n of a week writes path. A subject
// ending in "@agent" carries a coding-agent trailer and one ending in "@bot" a
// bot's login, and the content removed deletes the file. Laying a variant over
// such a tree adds a commit, or a file to one.
func writtenHistory(t *testing.T, tree fixturetest.Tree) []writtenCommit {
	t.Helper()
	byLabel := map[string]*writtenCommit{}
	for key, body := range tree {
		label, file, ok := strings.Cut(key, "/")
		stamp, subject, _ := strings.Cut(label, " ")
		c := byLabel[label]
		if c == nil {
			c = &writtenCommit{subject: subject, files: map[string]string{}}
			if _, err := fmt.Sscanf(stamp, "%d.%d", &c.week, &c.order); err != nil || !ok {
				t.Fatalf("history key %q is not \"<week>.<n> <subject>/<path>\"", key)
			}
			byLabel[label] = c
		}
		c.files[file] = body
	}
	commits := make([]writtenCommit, 0, len(byLabel))
	for _, c := range byLabel {
		commits = append(commits, *c)
	}
	slices.SortFunc(commits, func(a, b writtenCommit) int {
		return cmp.Or(cmp.Compare(a.week, b.week), cmp.Compare(a.order, b.order), strings.Compare(a.subject, b.subject))
	})

	return commits
}

// historyRepo builds a written history in a fresh repository. Week 1 starts on
// Monday 2026-01-05, and each commit of a week is an hour after the one before.
func historyRepo(t *testing.T, tree fixturetest.Tree) string {
	t.Helper()
	repo := t.TempDir()
	gitIn(t, repo, "init", "--initial-branch=main")
	start := time.Date(2026, time.January, 5, 9, 0, 0, 0, time.UTC)
	for _, c := range writtenHistory(t, tree) {
		for name, body := range c.files {
			path := filepath.Join(repo, name)
			if body == removed {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}

				continue
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		gitIn(t, repo, "add", "-A")
		commitWritten(t, repo, c, start.AddDate(0, 0, daysPerWeek*(c.week-1)).Add(time.Duration(c.order)*time.Hour))
	}

	return repo
}

// commitWritten commits the staged files as one written commit at a fixed time.
func commitWritten(t *testing.T, repo string, c writtenCommit, at time.Time) {
	t.Helper()
	author, message := "Fixture", c.subject
	if subject, ok := strings.CutSuffix(c.subject, " @agent"); ok {
		message = subject + "\n\nCo-Authored-By: Claude <noreply@example.com>"
	}
	if subject, ok := strings.CutSuffix(c.subject, " @bot"); ok {
		author, message = "renovate[bot]", subject
	}
	cmd := exec.CommandContext(t.Context(), "git", "commit", "-m", message) //nolint:gosec // args are test literals
	cmd.Dir = repo
	stamp := at.Format(time.RFC3339)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME="+author, "GIT_AUTHOR_EMAIL=fixture@example.com",
		"GIT_COMMITTER_NAME="+author, "GIT_COMMITTER_EMAIL=fixture@example.com",
		"GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
}

// historyArchitecture runs history over a repository and answers its
// architecture result.
func historyArchitecture(t *testing.T, repo string, args ...string) *architecture {
	t.Helper()
	out, err := runCLI(t, repo, append([]string{"history", "--json", "--since", "2025-12-01"}, args...)...)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	var env struct {
		Result struct {
			Architecture *architecture `json:"architecture"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parsing JSON: %v\n%s", err, out)
	}
	if env.Result.Architecture == nil {
		t.Fatalf("history has no architecture:\n%s", out)
	}

	return env.Result.Architecture
}

// subjectOf answers the subject of a commit.
func subjectOf(t *testing.T, repo, hash string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", "log", "-1", "--format=%s", hash) //nolint:gosec // a commit id
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log %s: %v", hash, err)
	}

	return strings.TrimSpace(string(out))
}

// cycleOrigins renders where each cycle of a written history came from: its
// first week and size, and the subject, author class, and intent of the
// commit that introduced it. The id and the present size are left out,
// because a rename and a new member rightly change them.
func cycleOrigins(t *testing.T) fixturetest.Metric {
	t.Helper()

	return func(tree fixturetest.Tree) string {
		repo := historyRepo(t, tree)
		cycles := historyArchitecture(t, repo).Cycles
		rows := make([]string, 0, len(cycles))
		for _, c := range cycles {
			row := fmt.Sprintf("first=%s then=%d before=%v", c.FirstWeek, c.FirstSize, c.BeforeWindow)
			if in := c.Introduced; in != nil {
				row += fmt.Sprintf(" by=%q %s %s", subjectOf(t, repo, in.Commit), in.AuthorClass, in.Intent)
			}
			rows = append(rows, row)
		}

		return strings.Join(rows, "; ")
	}
}

// hiddenCoupling renders the hidden coupling pairs of a written history.
func hiddenCoupling(t *testing.T) fixturetest.Metric {
	t.Helper()

	return func(tree fixturetest.Tree) string {
		pairs := historyArchitecture(t, historyRepo(t, tree)).HiddenCoupling
		rows := make([]string, 0, len(pairs))
		for _, p := range pairs {
			rows = append(rows, fmt.Sprintf("%s|%s %d %d/%d %.2f %.2f %v",
				p.A, p.B, p.CoChanges, p.ChangesA, p.ChangesB, p.Support, p.Confidence, p.SharedImporter))
		}

		return strings.Join(rows, "; ")
	}
}

const (
	pyA       = "def fa():\n    return 1\n"
	pyAUsesB  = "from app.b import fb\n\n\ndef fa():\n    return fb()\n"
	pyB       = "def fb():\n    return 2\n"
	pyBUsesA  = "from app.a import fa\n\n\ndef fb():\n    return 2\n\n\ndef fb2():\n    return fa()\n"
	pyBUsesA2 = "import os\n\nfrom app.a import fa as first\n\n\ndef fb():\n    return 2\n\n\ndef fb2():\n" +
		"    return first() or os.sep\n"
	pyBUsesC  = "from app.c import fc\n\n\ndef fb():\n    return 2\n\n\ndef fb2():\n    return fc()\n"
	pyCUsesA  = "from app.a import fa\n\n\ndef fc():\n    return fa()\n"
	pyAUsesZ  = "from app.z import fb\n\n\ndef fa():\n    return fb()\n"
	pyDefer   = "def fb():\n    return 2\n\n\ndef fb2():\n    from app.a import fa\n\n    return fa()\n"
	pyOther   = "def other():\n    return 3\n"
	wiredWeek = "first=2026-01-12 then=2 before=false by=\"feat: call back\" agent feat"
)

// cycleHistory is two modules that come to import each other in week 2, in a
// commit an agent co-authored, between two commits that leave them alone.
func cycleHistory() fixturetest.Tree {
	return fixturetest.Tree{
		"1.0 feat: add the parts/app/__init__.py": "",
		"1.0 feat: add the parts/app/a.py":        pyAUsesB,
		"1.0 feat: add the parts/app/b.py":        pyB,
		"2.1 fix: unrelated/app/other.py":         pyOther + "\n\nEARLY = 1\n",
		"2.2 feat: call back @agent/app/b.py":     pyBUsesA,
		"2.3 fix: later/app/other.py":             pyOther + "\n\nLATER = 1\n",
		"3.1 fix: next week/app/other.py":         pyOther + "\n\nLATER = 2\n",
	}
}

func TestHistoryDatesACycleToTheCommitThatIntroducedIt(t *testing.T) {
	t.Parallel()
	if got := cycleOrigins(t)(cycleHistory()); got != wiredWeek {
		t.Fatalf("cycle origin = %s, want %s", got, wiredWeek)
	}
	fixturetest.RunMetric(t, []fixturetest.MetricCase{{
		Name: "cycle origin", Base: cycleHistory(),
		Same: []fixturetest.Variant{
			{Name: "a member renamed in a later week", Files: fixturetest.Tree{
				"4.1 refactor: rename b/app/b.py": removed,
				"4.1 refactor: rename b/app/z.py": pyBUsesA,
				"4.1 refactor: rename b/app/a.py": pyAUsesZ,
			}},
			{Name: "a member renamed later in the same week", Files: fixturetest.Tree{
				"2.4 refactor: rename b/app/b.py": removed,
				"2.4 refactor: rename b/app/z.py": pyBUsesA,
				"2.4 refactor: rename b/app/a.py": pyAUsesZ,
			}},
			{Name: "the cycle grows by a file", Files: fixturetest.Tree{
				"4.1 feat: route through c/app/c.py": pyCUsesA,
				"4.1 feat: route through c/app/b.py": pyBUsesC,
			}},
			{Name: "the import is rewritten", Files: fixturetest.Tree{"4.1 style: alias/app/b.py": pyBUsesA2}},
			{Name: "commits after it", Files: fixturetest.Tree{"5.1 fix: more/app/other.py": pyOther + "\nMORE = 1\n"}},
		},
		Moved: []fixturetest.Variant{
			{Name: "broken and made again", Files: fixturetest.Tree{
				"4.1 fix: defer the import/app/b.py": pyDefer,
				"5.1 feat: call back again/app/b.py": pyBUsesA,
			}},
			{Name: "introduced a commit later", Files: fixturetest.Tree{
				"2.2 feat: call back @agent/app/b.py": pyB + "\n\nSOON = 1\n",
				"2.3 fix: later/app/b.py":             pyBUsesA,
			}},
			{
				Name: "by a person", Remove: []string{"2.2 feat: call back @agent/app/b.py"},
				Files: fixturetest.Tree{"2.2 feat: call back/app/b.py": pyBUsesA},
			},
			{Name: "deferred into a function", Files: fixturetest.Tree{"2.2 feat: call back @agent/app/b.py": pyDefer}},
		},
	}}, cycleOrigins(t))
}

func TestHistoryNamesACycleAsSnapshotDoes(t *testing.T) {
	t.Parallel()
	history := cycleHistory()
	history["4.1 refactor: rename b/app/b.py"] = removed
	history["4.1 refactor: rename b/app/z.py"] = pyBUsesA
	history["4.1 refactor: rename b/app/a.py"] = pyAUsesZ
	repo := historyRepo(t, history)
	arch := historyArchitecture(t, repo)
	found := snapshotImports(t, repo, "--experimental").cycles()
	if len(arch.Cycles) != 1 || len(found) != 1 {
		t.Fatalf("history has %d cycles and snapshot %d findings, want 1 and 1", len(arch.Cycles), len(found))
	}
	got := arch.Cycles[0]
	if got.ID != found[0].ID || !slices.Equal(got.Members, []string{"app/a.py", "app/z.py"}) || got.Size != 2 {
		t.Errorf("cycle = %+v, want the id %s of the snapshot's finding over a.py and z.py", got, found[0].ID)
	}
	if got.ID == rules.SetID(ruleImportCycle, []string{"app/a.py", "app/b.py"}) {
		t.Errorf("the id %s did not follow the rename", got.ID)
	}
}

func TestHistoryMarksACycleOlderThanTheWindow(t *testing.T) {
	t.Parallel()
	repo := historyRepo(t, cycleHistory())
	got := historyArchitecture(t, repo, "--since", "2026-01-15").Cycles
	if len(got) != 1 || !got[0].BeforeWindow || got[0].Introduced != nil || got[0].FirstWeek != "" {
		t.Errorf("cycles = %+v, want one marked before the window with no commit", got)
	}
}

func TestHistoryDatesACycleOfTheRootCommitToIt(t *testing.T) {
	t.Parallel()
	got := cycleOrigins(t)(fixturetest.Tree{
		"1.0 feat: start tangled/app/__init__.py": "",
		"1.0 feat: start tangled/app/a.py":        pyAUsesB,
		"1.0 feat: start tangled/app/b.py":        pyBUsesA,
		"2.1 fix: later/app/other.py":             pyOther,
	})
	if want := "first=2026-01-05 then=2 before=false by=\"feat: start tangled\" person feat"; got != want {
		t.Errorf("cycle origin = %s, want %s", got, want)
	}
}

func TestHistoryCannotDateACycleAtAShallowBoundary(t *testing.T) {
	t.Parallel()
	origin := historyRepo(t, cycleHistory())
	clone := filepath.Join(t.TempDir(), "clone")
	gitIn(t, origin, "clone", "--quiet", "--depth", "2", "file://"+origin, clone)
	got := historyArchitecture(t, clone).Cycles
	if len(got) != 1 || !got[0].BeforeWindow || got[0].Introduced != nil {
		t.Errorf("cycles = %+v, want one marked before the window: the clone's oldest commit already holds it", got)
	}
}

func TestHistorySamplesTheGraphEachWeek(t *testing.T) {
	t.Parallel()
	history := cycleHistory()
	history["4.1 fix: defer the import/app/b.py"] = pyDefer
	arch := historyArchitecture(t, historyRepo(t, history))
	got := make([]string, 0, len(arch.Periods))
	for _, p := range arch.Periods {
		got = append(got, fmt.Sprintf("%s %d/%d/%d", p.Week, p.Cycles, p.LargestCycle, p.FilesInCycles))
	}
	want := []string{"2026-01-05 0/0/0", "2026-01-12 1/2/2", "2026-01-19 1/2/2", "2026-01-26 0/0/0"}
	if !slices.Equal(got, want) {
		t.Errorf("periods = %v, want %v", got, want)
	}
	if len(arch.Cycles) != 0 {
		t.Errorf("a cycle that was broken is still listed: %+v", arch.Cycles)
	}
}

// coupledHistory changes two modules with no import between them in each of
// six commits, two of them alone together.
func coupledHistory() fixturetest.Tree {
	tree := fixturetest.Tree{
		"1.0 feat: add the parts/app/__init__.py": "",
		"1.0 feat: add the parts/app/other.py":    pyOther,
	}
	for i := 1; i <= 6; i++ {
		label := fmt.Sprintf("%d.1 feat: step %d", i, i)
		tree[label+"/app/read.py"] = fmt.Sprintf("def read():\n    return %d\n", i)
		tree[label+"/app/write.py"] = fmt.Sprintf("def write():\n    return %d\n", i)
	}

	return tree
}

// stepFiles adds one file to each of the six steps of coupledHistory.
func stepFiles(file string, body func(step int) string) fixturetest.Tree {
	tree := fixturetest.Tree{}
	for i := 1; i <= 6; i++ {
		tree[fmt.Sprintf("%d.1 feat: step %d/%s", i, i, file)] = body(i)
	}

	return tree
}

// bulkCommit is one commit that changes read.py, write.py, and enough other
// files to pass the bulk cut.
func bulkCommit(label string) fixturetest.Tree {
	tree := fixturetest.Tree{
		label + "/app/read.py":  "def read():\n    return 60\n",
		label + "/app/write.py": "def write():\n    return 60\n",
	}
	for i := range archtrend.MaxCommitFiles {
		tree[fmt.Sprintf("%s/app/bulk%02d.py", label, i)] = fmt.Sprintf("BULK = %d\n", i)
	}

	return tree
}

func TestHistoryListsFilesThatChangeTogetherWithNoImport(t *testing.T) {
	t.Parallel()
	const want = "app/read.py|app/write.py 6 6/6 6.00 1.00 false"
	if got := hiddenCoupling(t)(coupledHistory()); got != want {
		t.Fatalf("hidden coupling = %s, want %s", got, want)
	}
	numbered := func(format string) func(int) string {
		return func(step int) string { return fmt.Sprintf(format, step) }
	}
	fixturetest.RunMetric(t, []fixturetest.MetricCase{{
		Name: "hidden coupling", Base: coupledHistory(),
		Same: []fixturetest.Variant{
			{Name: "a bulk reformat", Files: bulkCommit("7.1 feat: reformat")},
			{Name: "a test that changes with its subject", Files: stepFiles("tests/test_read.py",
				numbered("from app.read import read\n\n\ndef test_read():\n    assert read() == %d\n"))},
			{Name: "a lockfile that changes with them", Files: stepFiles("uv.lock", numbered("version = %d\n"))},
			{Name: "a generated file that changes with them", Files: stepFiles("app/schema_gen.py",
				numbered("# Code generated by tool. DO NOT EDIT.\nSCHEMA = %d\n"))},
			{Name: "a bot that changes them", Files: fixturetest.Tree{
				"7.1 feat: bump @bot/app/read.py":  "def read():\n    return 70\n",
				"7.1 feat: bump @bot/app/write.py": "def write():\n    return 70\n",
			}},
			{Name: "a docs commit that changes them", Files: fixturetest.Tree{
				"7.1 docs: comment/app/read.py":  "# Reads.\ndef read():\n    return 6\n",
				"7.1 docs: comment/app/write.py": "# Writes.\ndef write():\n    return 6\n",
			}},
		},
		Moved: []fixturetest.Variant{
			{Name: "one imports the other at the end", Files: fixturetest.Tree{
				"7.1 feat: join/app/read.py": "from app.write import write\n\n\ndef read():\n    return write()\n",
			}},
			{Name: "one imports the other inside a function", Files: fixturetest.Tree{
				"7.1 feat: join/app/read.py": "def read():\n    from app.write import write\n\n    return write()\n",
			}},
			{Name: "a seventh change together", Files: fixturetest.Tree{
				"7.1 feat: step 7/app/read.py":  "def read():\n    return 7\n",
				"7.1 feat: step 7/app/write.py": "def write():\n    return 7\n",
			}},
			{Name: "one is renamed", Files: fixturetest.Tree{
				"7.1 refactor: rename/app/write.py": removed,
				"7.1 refactor: rename/app/store.py": "def write():\n    return 6\n",
			}},
			{Name: "a third file changes with them", Files: stepFiles("app/third.py", numbered("THIRD = %d\n"))},
		},
	}}, hiddenCoupling(t))
}

func TestHistoryKeepsAPairThroughARename(t *testing.T) {
	t.Parallel()
	history := coupledHistory()
	history["7.1 refactor: rename/app/write.py"] = removed
	history["7.1 refactor: rename/app/store.py"] = "def write():\n    return 6\n"
	const want = "app/read.py|app/store.py 6 6/6 6.00 1.00 false"
	if got := hiddenCoupling(t)(history); got != want {
		t.Errorf("hidden coupling = %s, want %s", got, want)
	}
}

func TestHistoryLeavesOutAChangeAnImportExplains(t *testing.T) {
	t.Parallel()
	const usesShared = "from app.shared import SHARED\n\n\ndef %s():\n    return %d + SHARED\n"
	history := coupledHistory()
	for i := 1; i <= 6; i++ {
		label := fmt.Sprintf("%d.1 feat: step %d", i, i)
		history[label+"/app/shared.py"] = fmt.Sprintf("SHARED = %d\n", i)
		history[label+"/app/read.py"] = fmt.Sprintf(usesShared, "read", i)
		history[label+"/app/write.py"] = fmt.Sprintf(usesShared, "write", i)
	}
	if got := hiddenCoupling(t)(history); got != "" {
		t.Errorf("hidden coupling = %s, want none: each commit changed the module both import", got)
	}
}

func TestHistoryTextListsTheArchitecture(t *testing.T) {
	t.Parallel()
	history := coupledHistory()
	for key, body := range cycleHistory() {
		history[key] = body
	}
	text, err := runCLI(t, historyRepo(t, history), "history", "--since", "2025-12-01")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	_, section, found := strings.Cut(text, "\narchitecture ")
	if !found {
		t.Fatalf("history text has no architecture table:\n%s", text)
	}
	for _, want := range []string{
		"2026-01-05    0       0        0", "2026-01-12    1       2        2", "cycle with", "app/a.py",
		"2026-01-12  2", "agent feat", "hidden_coupling", "app/read.py", "app/write.py", "6.00",
		"weeks with the cycle counts of the week before not shown",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("architecture text lacks %q:\n%s", want, section)
		}
	}
	for _, line := range strings.Split(section, "\n") {
		if n := len([]rune(line)); n > tableWidth {
			t.Errorf("architecture line is %d columns wide: %q", n, line)
		}
	}
}

func TestEveryArchitectureNumberHasADoc(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"architecture", "files_in_cycles", "depth_dirs", "median_depth", "depth_moved", "cycle_origin",
		"hidden_coupling", "support", "confidence",
	} {
		if report.Docs[name] == "" {
			t.Errorf("no doc for %s", name)
		}
	}
}

func TestDepthMovesNeedBothBoundsToAgree(t *testing.T) {
	t.Parallel()
	mod := func(lower, upper float64, provisional bool) *depth.Module {
		return &depth.Module{Depth: &lower, DepthAttributed: &upper, Provisional: provisional}
	}
	first := map[string]*depth.Module{
		"both": mod(0.2, 0.4, false), "split": mod(0.5, 0.5, false), "thin": mod(0.1, 0.1, true),
		"small": mod(0.5, 0.5, false), "down": mod(0.9, 0.9, false), "gone": mod(0.1, 0.1, false),
	}
	last := map[string]*depth.Module{
		"both": mod(0.5, 0.9, false), "split": mod(0.2, 0.8, false), "thin": mod(0.9, 0.9, false),
		"small": mod(0.52, 0.53, false), "down": mod(0.3, 0.5, false), "new": mod(0.1, 0.1, false),
	}
	moves := depthMoves(first, last)
	got := make([]string, 0, len(moves))
	for _, m := range moves {
		got = append(got, fmt.Sprintf("%s %+.2f", m.Dir, m.Moved))
	}
	if want := []string{"down -0.40", "both +0.30"}; !slices.Equal(got, want) {
		t.Errorf("depth moves = %v, want %v", got, want)
	}
}

func TestHistoryOfAnEmptyWindowListsEmptyArrays(t *testing.T) {
	t.Parallel()
	out, err := runCLI(t, historyRepo(t, cycleHistory()), "history", "--json", "--since", "2030-01-01")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	for _, want := range []string{
		`"periods": []`, `"depth_moved": []`, `"cycles": []`, `"hidden_coupling": []`, `"weeks": []`,
		`"virality": []`, `"pairing": []`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("a window with no commit lacks %s:\n%s", want, out[:min(len(out), 600)])
		}
	}
}

// workspaceHistory is a TypeScript workspace of two packages. One directory
// of package a declares six functions, and its caller comes to use five of
// them in week 5 where it used one. Three files change together in the first
// four weeks: two of package a and one of package b.
func workspaceHistory() fixturetest.Tree {
	var store, uses strings.Builder
	for i := 1; i <= 6; i++ {
		fmt.Fprintf(&store, "export function f%d(n: number) {\n  return n > %d ? n : %d\n}\n\n", i, i, i)
	}
	uses.WriteString("import { f1, f2, f3, f4, f5 } from '../store/store'\n\n")
	uses.WriteString("export const run = () => f1(1) + f2(2) + f3(3) + f4(4) + f5(5)\n")
	tree := fixturetest.Tree{
		"1.0 feat: start/packages/a/package.json":       "{\"name\": \"a\"}\n",
		"1.0 feat: start/packages/b/package.json":       "{\"name\": \"b\"}\n",
		"1.0 feat: start/packages/a/src/store/store.ts": store.String(),
		"1.0 feat: start/packages/a/src/app/main.ts": "import { f1 } from '../store/store'\n\n" +
			"export const run = () => f1(1)\n",
		"5.1 feat: use more/packages/a/src/app/main.ts": uses.String(),
	}
	for i := 1; i <= 4; i++ {
		label := fmt.Sprintf("%d.1 feat: step %d", i, i)
		tree[label+"/packages/a/src/x/left.ts"] = fmt.Sprintf("export const left = %d\n", i)
		tree[label+"/packages/a/src/y/right.ts"] = fmt.Sprintf("export const right = %d\n", i)
		tree[label+"/packages/b/src/peer.ts"] = fmt.Sprintf("export const peer = %d\n", i)
	}

	return tree
}

func TestHistoryReportsDepthOverTheWindowAndPairsWithinAPackage(t *testing.T) {
	t.Parallel()
	repo := historyRepo(t, workspaceHistory())
	arch := historyArchitecture(t, repo)
	first, last := arch.Periods[0], arch.Periods[len(arch.Periods)-1]
	if first.DepthDirs != 1 || first.MedianDepth == nil || last.MedianDepth == nil ||
		*last.MedianDepth >= *first.MedianDepth {
		t.Errorf("periods run from %+v to %+v, want one directory whose median depth fell", first, last)
	}
	moved := arch.DepthMoved
	if len(moved) != 1 || moved[0].Dir != "packages/a/src/store" || moved[0].Moved > -0.5 {
		t.Errorf("depth_moved = %+v, want packages/a/src/store falling by over a half", arch.DepthMoved)
	}
	got := make([]string, 0, len(arch.HiddenCoupling))
	for _, p := range arch.HiddenCoupling {
		got = append(got, fmt.Sprintf("%s|%s %d %.2f", p.A, p.B, p.CoChanges, p.Support))
	}
	if want := []string{"packages/a/src/x/left.ts|packages/a/src/y/right.ts 4 2.00"}; !slices.Equal(got, want) {
		t.Errorf("hidden coupling = %v, want %v: a pair across two package.json files is never listed", got, want)
	}
	text, err := runCLI(t, repo, "history", "--since", "2025-12-01")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	for _, want := range []string{"depth_moved", "packages/a/src/store", "0.83 → 0.17", "packages/a/src/x/left.ts"} {
		if !strings.Contains(text, want) {
			t.Errorf("history text lacks %q:\n%s", want, text)
		}
	}
}

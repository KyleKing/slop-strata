package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/depth"
	"github.com/kyleking/slop-strata/internal/report"
)

const depthAPI = `package store

// Load loads.
func Load(path string) []string {
	if path == "" {
		return nil
	}

	return Parse(path)
}

// Again loads.
func Again(path string) []string { return Load(path) }
`

const depthEngine = `package store

// Limit bounds a parse.
const Limit = 10

// Parse parses.
func Parse(path string) []string {
	var out []string
	for _, line := range []string{path} {
		if line != "" && len(out) < Limit {
			out = append(out, clean(line))
		}
	}

	return out
}

func clean(line string) string {
	if line == "#" {
		return ""
	}

	return line
}
`

const depthMain = `package main

import "example.com/fixture/internal/store"

func main() {
	store.Load("x")
}
`

// depthRepo has two commits: a store package whose one function a command
// uses, over a hidden engine, and then the command reaching into the engine.
func depthRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitIn(t, repo, "init", "--initial-branch=main")
	commitFiles(t, repo, "feat: init", map[string]string{
		"go.mod":                   "module example.com/fixture\n\ngo 1.26\n",
		"cmd/tool/main.go":         depthMain,
		"internal/store/api.go":    depthAPI,
		"internal/store/engine.go": depthEngine,
		"internal/store/api_test.go": "package store\n\nimport \"testing\"\n\n" +
			"func TestLoad(t *testing.T) { Load(\"\") }\n",
	})
	commitFiles(t, repo, "feat: parse in the command", map[string]string{
		"cmd/tool/main.go": strings.Replace(depthMain, "}\n", "\tstore.Parse(\"y\")\n}\n", 1),
	})

	return repo
}

func dirRow(t *testing.T, dirs []dirSnapshot, dir string) dirSnapshot {
	t.Helper()
	for i := range dirs {
		if dirs[i].Dir == dir {
			return dirs[i]
		}
	}
	t.Fatalf("no directory %s in %+v", dir, dirs)

	return dirSnapshot{}
}

func TestSnapshotReportsModuleDepth(t *testing.T) {
	t.Parallel()
	env := snapshotImports(t, depthRepo(t), "--rev", "HEAD~1")
	store := dirRow(t, env.Result.Dirs, "internal/store").Module
	if store.Depth == nil {
		t.Fatalf("store = %+v, want a depth: the command uses Load", store)
	}
	got := depth.Module{
		Shown: store.Shown, Hidden: store.Hidden, Forwarders: store.Forwarders, ByMass: store.ByMass,
		WidestFile: store.WidestFile, WidestShown: store.WidestShown, Provisional: store.Provisional,
	}
	want := depth.Module{
		Shown: 1, Hidden: 3, Forwarders: 1, ByMass: true,
		WidestFile: "internal/store/api.go", WidestShown: 1, Provisional: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("store = %+v, want Load shown, Limit, Parse, clean hidden, Again a forwarder: %+v", got, want)
	}
	if *store.Depth <= 0.5 || store.HiddenMass <= store.ShownMass {
		t.Errorf("store = %+v with depth %v, want mostly hidden", store, *store.Depth)
	}
	if tool := dirRow(t, env.Result.Dirs, "cmd/tool"); tool.Depth != nil || tool.Shown != 0 || tool.Hidden != 1 {
		t.Errorf("cmd/tool = %+v, want one hidden name and no depth", tool.Module)
	}
	if tests := dirRow(t, env.Result.TestDirs, "internal/store"); tests.Depth != nil || tests.Shown+tests.Hidden != 0 {
		t.Errorf("the test row carries a depth: %+v", tests.Module)
	}
}

func TestSnapshotTextListsModuleDepth(t *testing.T) {
	t.Parallel()
	repo := depthRepo(t)
	text, err := runCLI(t, repo, "snapshot", "--rev", "HEAD~1")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	for _, want := range []string{
		"module depth, shallowest first", "widest_surface", "api.go (1)", "* fewer than 5 samples",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("snapshot text lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(depthSection(text), "cmd/tool") {
		t.Errorf("a directory nothing uses is listed without --all:\n%s", text)
	}
	all, err := runCLI(t, repo, "snapshot", "--rev", "HEAD~1", "--all")
	if err != nil {
		t.Fatalf("snapshot --all: %v", err)
	}
	if section := depthSection(all); !strings.Contains(section, "cmd/tool") || !strings.Contains(section, " -") {
		t.Errorf("--all does not list the entry point with no depth:\n%s", all)
	}
}

// depthSection cuts the module depth table out of a snapshot's text.
func depthSection(text string) string {
	_, rest, _ := strings.Cut(text, "module depth")
	section, _, _ := strings.Cut(rest, "\n\n")

	return section
}

// surfaceEnvelope is the part of a change envelope the depth tests read.
type surfaceEnvelope struct {
	Result struct {
		Surface []depth.Widening `json:"surface_widened"`
		Next    []nextItem       `json:"next"`
	} `json:"result"`
}

func TestChangeReportsAWidenedSurfaceWithoutRaisingIt(t *testing.T) {
	t.Parallel()
	repo := depthRepo(t)
	out, err := runCLI(t, repo, "change", "--json", "--base", "HEAD~1", "--rev", "HEAD")
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	var env surfaceEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parsing JSON: %v\n%s", err, out)
	}
	if len(env.Result.Surface) != 1 {
		t.Fatalf("surface_widened = %+v, want one directory", env.Result.Surface)
	}
	got := env.Result.Surface[0]
	if got.DepthBefore == nil || got.DepthAfter == nil || *got.DepthAfter >= *got.DepthBefore {
		t.Errorf("surface_widened = %+v, want a depth on both sides, lower after", got)
	}
	got.DepthBefore, got.DepthAfter = nil, nil
	want := depth.Widening{
		Dir: "internal/store", NewlyShown: []string{"Parse"}, NewlyShownCount: 1, ShownBefore: 1, ShownAfter: 2,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("surface_widened = %+v, want %+v", got, want)
	}
	if len(env.Result.Next) != 0 {
		t.Errorf("a widened surface made the change worth a look: %+v", env.Result.Next)
	}
}

func TestEveryDepthNumberHasADoc(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"shown", "hidden", "shown_mass", "hidden_mass", "forwarders", "depth", "widest_surface", "surface_widened",
	} {
		if report.Docs[name] == "" {
			t.Errorf("no doc for %s", name)
		}
	}
}

func TestChangeTextListsAWidenedSurfaceAndBriefStaysQuiet(t *testing.T) {
	t.Parallel()
	repo := depthRepo(t)
	text, err := runCLI(t, repo, "change", "--base", "HEAD~1", "--rev", "HEAD")
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	for _, want := range []string{"surface_widened", "internal/store", "1 → 2", "Parse"} {
		if !strings.Contains(text, want) {
			t.Errorf("change text lacks %q:\n%s", want, text)
		}
	}
	brief, err := runCLI(t, repo, "change", "--brief", "--exit-code", "--base", "HEAD~1", "--rev", "HEAD")
	if err != nil || brief != "" {
		t.Errorf("--brief spoke of a widened surface: %q, %v", brief, err)
	}
}

func TestChangeWithNoScoredFileListsAnEmptyFilesArray(t *testing.T) {
	t.Parallel()
	repo := depthRepo(t)
	gitIn(t, repo, "commit", "--allow-empty", "-m", "chore: nothing")
	out, err := runCLI(t, repo, "change", "--json", "--base", "HEAD~1", "--rev", "HEAD")
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if !strings.Contains(out, `"files": []`) {
		t.Errorf("a change that touches no file must list files as an empty array:\n%s", out)
	}
}

func TestDepthTableFitsAHundredColumnsOnDeepPaths(t *testing.T) {
	t.Parallel()
	const deep = "packages/application/source/server/middlewares/handlers"
	const longName = "aVeryLongFileNameForOneHandlerModule"
	repo := t.TempDir()
	gitIn(t, repo, "init", "--initial-branch=main")
	commitFiles(t, repo, "feat: init", map[string]string{
		deep + "/" + longName + ".ts": "export function handle(a: number) {\n  return a ? 1 : 2\n}\n",
		"packages/application/source/client/entry/main.ts": "import { handle } from " +
			"'../../server/middlewares/handlers/" + longName + "'\n\nexport const run = () => handle(1) + 1\n",
	})
	text, err := runCLI(t, repo, "snapshot")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	section := depthSection(text)
	if !strings.Contains(section, "packages/…/") || !strings.Contains(section, "handlers") {
		t.Errorf("the deep directory is not shortened to its ends:\n%s", section)
	}
	for _, line := range strings.Split(section, "\n") {
		if n := len([]rune(line)); n > depthTableWidth {
			t.Errorf("depth table line is %d columns wide: %q", n, line)
		}
	}
}

func TestChangeListsNoSurfaceWhenNothingWidened(t *testing.T) {
	t.Parallel()
	repo := depthRepo(t)
	commitFiles(t, repo, "refactor: wrap and export", map[string]string{
		"internal/store/more.go": "package store\n\n// Clean cleans.\n" +
			"func Clean(line string) string { return clean(line) }\n",
	})
	out, err := runCLI(t, repo, "change", "--json", "--base", "HEAD~1", "--rev", "HEAD")
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if !strings.Contains(out, `"surface_widened": []`) {
		t.Errorf("a wrapper nobody uses widened a surface, or the list is not an empty array:\n%s", out)
	}
}

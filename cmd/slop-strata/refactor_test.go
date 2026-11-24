package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/rules"
	"github.com/kyleking/slop-strata/internal/structure"
)

// A function with two loops over one counter, complex enough to hold a block
// worth moving out: the branch inside its first loop.
const refactorTally = `package store

func tally(a, b []int, lim int) int {
	n := 0
	for _, x := range a {
		if x > lim {
			for _, y := range b {
				if y == x {
					n++
				}
			}
		}
	}
	for _, y := range b {
		if y > lim {
			if y%2 == 0 {
				n--
			}
		}
	}

	return n
}
`

// A loader two files hold with one name changed.
const refactorLoad = `package store

func NAME(raw string) (map[string]int, error) {
	counts := map[string]int{}
	for _, field := range strings.Split(raw, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(field), "=")
		if !ok {
			return nil, fmt.Errorf("TABLE: field %q has no value", field)
		}
		n, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", field, err)
		}
		counts[strings.ToLower(key)] += n
	}

	return counts, nil
}
`

// refactorRepo holds one function to split and one loader in two copies, and
// a copy of each in a test and in an example.
func refactorRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	load := func(name, table string) string {
		return strings.NewReplacer("NAME", name, "TABLE", table).Replace(refactorLoad)
	}
	files := map[string]string{
		"store/tally.go": refactorTally, "store/tally_test.go": refactorTally, "examples/tally/main.go": refactorTally,
		"store/users.go": load("loadUsers", "users"), "store/orders.go": load("loadOrders", "orders"),
		"examples/load/main.go": load("loadDemo", "demo"),
	}
	for name, body := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, dir, "init", "--initial-branch=main")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-m", "feat: init")

	return dir
}

// refactorLists is the part of a snapshot the two lists sit in.
type refactorLists struct {
	Result struct {
		Splits     *splitList     `json:"split_candidates"`
		Duplicates *duplicateList `json:"duplicate_groups"`
	} `json:"result"`
}

func snapshotLists(t *testing.T, repo string, argv ...string) refactorLists {
	t.Helper()
	out, err := runCLI(t, repo, append([]string{"snapshot", "--json"}, argv...)...)
	if err != nil {
		t.Fatalf("snapshot --json %v: %v", argv, err)
	}
	var env refactorLists
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parsing JSON: %v\n%s", err, out)
	}

	return env
}

func TestSnapshotListsSplitCandidates(t *testing.T) {
	t.Parallel()
	got := snapshotLists(t, refactorRepo(t)).Result
	if got.Duplicates != nil {
		t.Errorf("duplicate_groups = %+v without --experimental, want none", got.Duplicates)
	}
	// The test's copy and the example's copy of the function are left out.
	if got.Splits == nil || got.Splits.Total != 1 || len(got.Splits.Functions) != 1 {
		t.Fatalf("split_candidates = %+v, want the one production function", got.Splits)
	}
	want := splitFunc{
		File: "store/tally.go", Name: "tally", Line: 3, EndLine: 23, Cognitive: 16, CC: 8,
		Split: structure.Split{
			StartLine: 6, EndLine: 12, Statements: 1, Drop: 9, Cognitive: 6, CC: 4, Remainder: 7, RemainderCC: 5,
			Inputs: 4, Outputs: 1,
		},
	}
	if f := got.Splits.Functions[0]; f != want {
		t.Errorf("function = %+v, want %+v", f, want)
	}
}

func TestSnapshotListsDuplicateGroupsWhenExperimental(t *testing.T) {
	t.Parallel()
	got := snapshotLists(t, refactorRepo(t), "--experimental").Result
	if got.Splits == nil || got.Splits.Total != 1 {
		t.Errorf("split_candidates = %+v, want it with --experimental too", got.Splits)
	}
	d := got.Duplicates
	if d == nil || d.Total != 1 || len(d.Groups) != 1 {
		t.Fatalf("duplicate_groups = %+v, want the one group of the two loaders", d)
	}
	g := d.Groups[0]
	if len(g.Sites) != 1+1 || g.Sites[0].Path != "store/orders.go" || g.Sites[0].Unit != "loadOrders" ||
		g.Sites[1].Path != "store/users.go" {
		t.Errorf("sites = %+v, want orders and users, the example left out", g.Sites)
	}
	if len(g.Parameters) != 1 || g.Removable != g.Tokens-8 || d.Removable != g.Removable {
		t.Errorf("group = %+v of %+v, want one parameter and removable = tokens - 8", g, d)
	}
}

func TestSnapshotTextPrintsTheTwoLists(t *testing.T) {
	t.Parallel()
	repo := refactorRepo(t)
	plain, err := runCLI(t, repo, "snapshot")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	for _, want := range []string{
		"split candidates, 1 of 1 (cognitive: before → rest + block)",
		"store/tally.go:3  tally  6-12   16 → 7 + 6  4   1    0",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("snapshot text lacks %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "duplicate groups") {
		t.Errorf("snapshot text lists duplicate groups without --experimental:\n%s", plain)
	}
	gated, err := runCLI(t, repo, "snapshot", "--experimental")
	if err != nil {
		t.Fatalf("snapshot --experimental: %v", err)
	}
	for _, want := range []string{
		"duplicate groups, 1 of 1 (96 tokens removable in all)", "store/orders.go:3  loadOrders  96",
		"store/users.go:3   loadUsers",
	} {
		if !strings.Contains(gated, want) {
			t.Errorf("snapshot --experimental text lacks %q:\n%s", want, gated)
		}
	}
}

func TestSnapshotOmitsTheListsOfATreeWithNothingToList(t *testing.T) {
	t.Parallel()
	repo := deterministicRepo(t)
	got := snapshotLists(t, repo, "--experimental").Result
	if got.Splits == nil || got.Splits.Total != 0 || got.Splits.Functions == nil {
		t.Errorf("split_candidates = %+v, want an empty list", got.Splits)
	}
	if got.Duplicates == nil || got.Duplicates.Total != 0 || got.Duplicates.Groups == nil {
		t.Errorf("duplicate_groups = %+v, want an empty list", got.Duplicates)
	}
	text, err := runCLI(t, repo, "snapshot", "--experimental")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if strings.Contains(text, "split candidates") || strings.Contains(text, "duplicate groups") {
		t.Errorf("snapshot text prints an empty list:\n%s", text)
	}
}

func TestDuplicateRowsCapTheSitesAndTheNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, text, want string
		width            int
	}{
		{name: "short enough", text: "loadUsers", width: 9, want: "loadUsers"},
		{name: "cut", text: "loadUsersFromTheStore", width: 9, want: "loadUser…"},
		{name: "cut on a rune", text: "naïve/naïf", width: 5, want: "naïv…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := clip(tt.text, tt.width); got != tt.want {
				t.Errorf("clip(%q, %d) = %q, want %q", tt.text, tt.width, got, tt.want)
			}
		})
	}
}

func TestDuplicateRowsListThreeSitesAndCountTheRest(t *testing.T) {
	t.Parallel()
	g := rules.DupGroup{
		Tokens: 40, Removable: 152, Parameters: [][]string{{"a", "b", "c", "d", "e"}},
		Sites: []rules.DupGroupSite{
			{Path: "a.go", Unit: "one", Line: 3},
			{Path: "b.go", Unit: "two", Line: 4},
			{Path: "c.go", Unit: "three", Line: 5},
			{Path: "d.go", Unit: "four", Line: 6},
			{Path: "e.go", Unit: "five", Line: 7},
		},
	}
	got := duplicateRows(&g)
	want := [][]string{
		{"a.go:3", "one", "152", "5", "40", "a/b/c/d/e"},
		{"b.go:4", "two", "", "", "", ""},
		{"c.go:5", "three", "", "", "", ""},
		{"… and 2 more", "", "", "", "", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %q, want %q", got, want)
	}
}

func TestSnapshotTextListsEveryRowWithAll(t *testing.T) {
	t.Parallel()
	list := &splitList{Total: 7}
	for i := range 7 {
		list.Functions = append(list.Functions, splitFunc{File: "a.go", Name: "f", Line: 10 * (i + 1)})
	}
	for all, want := range map[bool]string{false: "split candidates, 5 of 7", true: "split candidates, 7 of 7"} {
		var out strings.Builder
		printSplits(&out, list, all)
		if !strings.Contains(out.String(), want) {
			t.Errorf("with all=%v the table lacks %q:\n%s", all, want, out.String())
		}
	}
}

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/fixturetest"
	"github.com/kyleking/slop-strata/internal/rules"
)

const ruleNewJSXShape = "x/new-jsx-shape"

// panel writes a component whose markup is a card of seven elements: NAME is
// the component, TITLE the heading, and COUNT what it counts.
func panel(name, title, count string) string {
	return strings.NewReplacer("NAME", name, "TITLE", title, "COUNT", count).Replace(panelSource)
}

const panelSource = `export function NAME({ COUNT }: Props) {
  return (
    <div className="card">
      <header>
        <h2>TITLE</h2>
        <span>{COUNT.length}</span>
      </header>
      <ul>
        <li>open</li>
        <li>closed</li>
      </ul>
    </div>
  )
}
`

// newJSXShapeCase is a change that adds a second component with the markup
// of one the tree already holds, under another title.
func newJSXShapeCase() fixturetest.Case {
	teams := func(body string) fixturetest.Tree { return fixturetest.Tree{"ui/teams.tsx": body} }
	whole := panel("Teams", "Teams", "teams")

	return fixturetest.Case{
		Name: "a change adds a second component with the same markup",
		Base: fixturetest.Tree{
			before + "ui/users.tsx": panel("Users", "Users", "users"),
			added + "ui/teams.tsx":  whole,
		},
		Dodges: []fixturetest.Variant{
			adds("write other text", teams(strings.ReplaceAll(whole, "open", "active"))),
			adds("set another class", teams(strings.Replace(whole, `"card"`, `"card wide"`, 1))),
			adds("add a comment above the component", teams("// Teams lists the teams.\n"+whole)),
			adds("indent the markup another way", teams(strings.ReplaceAll(whole, "\n      ", "\n\t"))),
		},
		Persists: []fixturetest.Variant{
			adds("give the component another name", teams(panel("Groups", "Teams", "teams"))),
			movesTo("ui/teams.tsx", "ui/groups.tsx", whole),
			adds("wrap the card in a section", teams(strings.NewReplacer(
				"    <div className=\"card\">", "    <section>\n    <div className=\"card\">",
				"    </div>\n  )", "    </div>\n    </section>\n  )").Replace(whole))),
		},
		Gaps: []fixturetest.Variant{
			adds("use another tag for the heading", teams(strings.ReplaceAll(whole, "h2", "h3"))),
			adds("add an attribute to one element", teams(strings.Replace(whole, "<ul>", `<ul role="list">`, 1))),
			adds("add a list item",
				teams(strings.Replace(whole, "<li>open</li>", "<li>open</li>\n        <li>new</li>", 1))),
		},
		Fixes: []fixturetest.Variant{
			adds("use a component for the card", teams("export function Teams({ teams }: Props) {\n"+
				"  return <Card title=\"Teams\" count={teams.length} />\n}\n")),
		},
	}
}

func TestNewJSXShapeHoldsUnderAdversarialEdits(t *testing.T) {
	t.Parallel()
	fixturetest.Run(t, []fixturetest.Case{newJSXShapeCase()}, changeRuleIDs(t, ruleNewJSXShape, "--experimental"))
}

func TestNewJSXShapeStaysQuiet(t *testing.T) {
	t.Parallel()
	users, teams := panel("Users", "Users", "users"), panel("Teams", "Teams", "teams")
	cases := []quietCase{
		{name: "both components are new", tree: fixturetest.Tree{
			before + "ui/index.ts": "export const version = 1\n",
			added + "ui/users.tsx": users, added + "ui/teams.tsx": teams,
		}},
		{name: "a component moves to another file", tree: fixturetest.Tree{
			before + "ui/users.tsx": users, before + "ui/teams.tsx": teams,
			added + "ui/teams.tsx": removed, added + "ui/groups.tsx": teams,
		}},
		{name: "the markup is indented under a fragment and its title edited", tree: fixturetest.Tree{
			before + "ui/users.tsx": users, before + "ui/teams.tsx": teams,
			added + "ui/teams.tsx": strings.NewReplacer(
				"\n    ", "\n      ", "  return (", "  return (\n    <>", "\n  )\n", "\n    </>\n  )\n",
				"<h2>Teams", "<h2>All teams",
			).Replace(teams),
		}},
		{name: "the copy is added to a test", tree: fixturetest.Tree{
			before + "ui/users.tsx": users, added + "ui/teams.test.tsx": teams,
		}},
		{name: "the copy is added to a story", tree: fixturetest.Tree{
			before + "ui/users.tsx": users, added + "ui/teams.stories.tsx": teams,
		}},
		{name: "the copy is added to an example", tree: fixturetest.Tree{
			before + "ui/users.tsx": users, added + "examples/teams/page.tsx": teams,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, f := range changeFindings(t, historyRepo(t, tc.tree), "--experimental") {
				if f.Rule == ruleNewJSXShape {
					t.Errorf("reported %s:%d: %s", f.Path, f.Line, f.Note)
				}
			}
		})
	}
}

// TestNewJSXShapeIsReadable pins what a person and an agent see of a finding:
// the note under the findings table, and the same note as the reason of a
// next line, so --brief names the place that already holds the markup.
func TestNewJSXShapeIsReadable(t *testing.T) {
	t.Parallel()
	repo := historyRepo(t, newJSXShapeCase().Base)
	const want = "repeats 7 elements of markup that 1 other place holds already: Users at ui/users.tsx:3. " +
		"The sites differ in 2 values. Make the block one component"
	for _, flags := range [][]string{{"--experimental"}, {"--experimental", "--brief"}} {
		out, err := runCLI(t, repo, append([]string{"change", "--base", "HEAD~1", "--rev", "HEAD"}, flags...)...)
		if err != nil {
			t.Fatalf("change %v: %v", flags, err)
		}
		if !strings.Contains(out, want) {
			t.Errorf("change %v does not name the place that holds the markup:\n%s", flags, out)
		}
	}
	plain, err := runCLI(t, repo, "change", "--base", "HEAD~1", "--rev", "HEAD")
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if strings.Contains(plain, ruleNewJSXShape) {
		t.Errorf("the rule reported without --experimental:\n%s", plain)
	}
}

// TestNewJSXShapeTakesAnInlineIgnore pins that a reasoned ignore sets the
// finding aside and counts it, written above the block as a comment of the
// language or on its first line as a comment of the markup.
func TestNewJSXShapeTakesAnInlineIgnore(t *testing.T) {
	t.Parallel()
	whole := panel("Teams", "Teams", "teams")
	const reason = "slop-strata:ignore x/new-jsx-shape the two cards part ways next release"
	for name, body := range map[string]string{
		"above the block": strings.Replace(whole, "  return (\n", "  return (\n    // "+reason+"\n", 1),
		"on its first line": strings.Replace(whole, `<div className="card">`,
			`<div className="card"> {/* `+reason+` */}`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tree := newJSXShapeCase().Base
			tree[added+"ui/teams.tsx"] = body
			argv := []string{"change", "--base", "HEAD~1", "--rev", "HEAD", "--json", "--experimental"}
			out, err := runCLI(t, historyRepo(t, tree), argv...)
			if err != nil {
				t.Fatalf("change: %v", err)
			}
			var env struct {
				Result struct {
					Findings []findingRow `json:"findings"`
				} `json:"result"`
				Counts struct {
					Suppressed struct {
						Ignored int `json:"ignored"`
					} `json:"suppressed"`
				} `json:"counts"`
			}
			if err := json.Unmarshal([]byte(out), &env); err != nil {
				t.Fatalf("parsing JSON: %v\n%s", err, out)
			}
			for _, row := range env.Result.Findings {
				if row.Rule == ruleNewJSXShape {
					t.Errorf("an ignored finding was reported: %+v", row)
				}
			}
			if env.Counts.Suppressed.Ignored != 1 {
				t.Errorf("counts.suppressed.ignored = %d, want 1", env.Counts.Suppressed.Ignored)
			}
		})
	}
}

// jsxGroupRepo holds one card in three components, and a copy of it in a
// test, in a story, and in an example.
func jsxGroupRepo(t *testing.T) string {
	t.Helper()
	files := map[string]string{
		"ui/users.tsx": panel("Users", "Users", "users"), "ui/teams.tsx": panel("Teams", "Teams", "teams"),
		"ui/roles.tsx":         panel("Roles", "Roles", "roles"),
		"ui/users.test.tsx":    panel("TestUsers", "Users", "users"),
		"ui/users.stories.tsx": panel("StoryUsers", "Users", "users"),
		"examples/ui/page.tsx": panel("Demo", "Users", "users"),
	}
	dir := t.TempDir()
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

// snapshotJSXGroups reads the JSX groups of a snapshot.
func snapshotJSXGroups(t *testing.T, repo string, argv ...string) *jsxGroupList {
	t.Helper()
	var list *jsxGroupList
	snapshotField(t, repo, "jsx_groups", &list, argv...)

	return list
}

func TestSnapshotListsJSXGroupsWhenExperimental(t *testing.T) {
	t.Parallel()
	repo := jsxGroupRepo(t)
	if got := snapshotJSXGroups(t, repo); got != nil {
		t.Errorf("jsx_groups = %+v without --experimental, want none", got)
	}
	got := snapshotJSXGroups(t, repo, "--experimental")
	// Three sites of seven elements that differ in the title and the count:
	// 7 x (3 - 1) - 2. The test's, the story's, and the example's are left out.
	want := &jsxGroupList{Total: 1, Groups: []rules.JSXGroup{{
		Blocks: 1, Elements: 7, Holes: 2, Rank: 12,
		Sites: []rules.JSXSite{
			{Path: "ui/roles.tsx", Unit: "Roles", Line: 3, EndLine: 12},
			{Path: "ui/teams.tsx", Unit: "Teams", Line: 3, EndLine: 12},
			{Path: "ui/users.tsx", Unit: "Users", Line: 3, EndLine: 12},
		},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("jsx_groups = %+v, want %+v", got, want)
	}
	if again := snapshotJSXGroups(t, repo, "--experimental"); !reflect.DeepEqual(again, got) {
		t.Errorf("a second run lists %+v, the first %+v", again, got)
	}
}

func TestSnapshotTextPrintsJSXGroups(t *testing.T) {
	t.Parallel()
	repo := jsxGroupRepo(t)
	plain, err := runCLI(t, repo, "snapshot")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if strings.Contains(plain, "JSX groups") {
		t.Errorf("snapshot text lists JSX groups without --experimental:\n%s", plain)
	}
	gated, err := runCLI(t, repo, "snapshot", "--experimental")
	if err != nil {
		t.Fatalf("snapshot --experimental: %v", err)
	}
	for _, want := range []string{
		"JSX groups, 1 of 1 (rank: elements × (sites − 1) − holes)",
		"ui/roles.tsx:3  Roles  12    3      1       7         2",
		"ui/teams.tsx:3  Teams",
		"ui/users.tsx:3  Users",
	} {
		if !strings.Contains(gated, want) {
			t.Errorf("snapshot --experimental text lacks %q:\n%s", want, gated)
		}
	}
	if strings.Index(gated, "JSX groups") < strings.Index(gated, "top functions") {
		t.Errorf("the JSX groups are printed before the functions:\n%s", gated)
	}
	empty, err := runCLI(t, deterministicRepo(t), "snapshot", "--experimental")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if strings.Contains(empty, "JSX groups") {
		t.Errorf("snapshot text prints an empty list:\n%s", empty)
	}
}

func TestJSXGroupRowsListThreeSitesAndCountTheRest(t *testing.T) {
	t.Parallel()
	g := rules.JSXGroup{
		Blocks: 2, Elements: 19, Holes: 3, Rank: 73,
		Sites: []rules.JSXSite{
			{Path: "a.tsx", Unit: "One", Line: 3},
			{Path: "b.tsx", Unit: "AComponentWithAVeryLongNameIndeed", Line: 4},
			{Path: "c.tsx", Unit: "Three", Line: 5},
			{Path: "d.tsx", Unit: "Four", Line: 6},
			{Path: "e.tsx", Unit: "Five", Line: 7},
		},
	}
	got := jsxGroupRows(&g)
	want := [][]string{
		{"a.tsx:3", "One", "73", "5", "2", "19", "3"},
		{"b.tsx:4", "AComponentWithAVeryLongName…", "", "", "", "", ""},
		{"c.tsx:5", "Three", "", "", "", "", ""},
		{"… and 2 more", "", "", "", "", "", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %q, want %q", got, want)
	}
}

func TestSnapshotTextListsEveryJSXGroupWithAll(t *testing.T) {
	t.Parallel()
	list := &jsxGroupList{Total: 7}
	for i := range 7 {
		list.Groups = append(list.Groups, rules.JSXGroup{
			Blocks: 1, Elements: 6,
			Sites: []rules.JSXSite{{Path: "a.tsx", Line: i + 1}, {Path: "b.tsx", Line: 1}},
		})
	}
	for all, want := range map[bool]string{false: "JSX groups, 5 of 7", true: "JSX groups, 7 of 7"} {
		var out strings.Builder
		printJSXGroups(&out, list, all)
		if !strings.Contains(out.String(), want) {
			t.Errorf("with all=%v the table lacks %q:\n%s", all, want, out.String())
		}
	}
}

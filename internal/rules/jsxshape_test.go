package rules_test

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/rules"
)

// jsxGroups fingerprints a tree and lists its JSX groups.
func jsxGroups(t *testing.T, tree map[string]string) []rules.JSXGroup {
	t.Helper()

	return rules.JSXGroups(printsOf(t, tree))
}

// component wraps markup in a component of a name.
func component(name, markup string) string {
	return "export function " + name + "(props: Props) {\n  return (\n" + markup + "  )\n}\n"
}

// card is a block of seven elements with three holes: a class, a title, and
// a count.
func card(class, title, count string) string {
	return `    <div className="` + class + `">
      <header>
        <h2>` + title + `</h2>
        <span>{` + count + `}</span>
      </header>
      <ul>
        <li>one</li>
        <li>two</li>
      </ul>
    </div>
`
}

// jsxSummary renders groups as sites, blocks, elements, holes, and rank.
func jsxSummary(groups []rules.JSXGroup) []string {
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		sites := make([]string, 0, len(g.Sites))
		for _, s := range g.Sites {
			sites = append(sites, fmt.Sprintf("%s:%d-%d %s", s.Path, s.Line, s.EndLine, s.Unit))
		}
		out = append(out, fmt.Sprintf("%s | blocks %d elements %d holes %d rank %d",
			strings.Join(sites, ", "), g.Blocks, g.Elements, g.Holes, g.Rank))
	}

	return out
}

// jsxGroupCase is a tree and the groups it lists.
type jsxGroupCase struct {
	tree map[string]string
	name string
	want []string
}

// row is a parent of five children, each with one run of text.
func row(parent, child, attrs string, texts ...string) string {
	var b strings.Builder
	b.WriteString("<" + parent + attrs + ">\n")
	for _, text := range texts {
		b.WriteString("<" + child + ">" + text + "</" + child + ">\n")
	}
	b.WriteString("</" + parent + ">\n")

	return b.String()
}

// drawing is an svg of six paths, the first drawn as d.
func drawing(d string) string {
	return `    <svg viewBox="0 0 24 24">
      <g>
        <path d="` + d + `" />
        <path d="M1 1" />
        <path d="M2 2" />
        <path d="M3 3" />
        <path d="M4 4" />
        <path d="M5 5" />
      </g>
    </svg>
`
}

// jsxShapeCases are trees that say what one shape is.
func jsxShapeCases() []jsxGroupCase {
	users := component("Users", card("card", "Users", "users.length"))
	teams := component("Teams", card("card", "Teams", "teams.length"))
	inner := card("c", "T", "n")

	return []jsxGroupCase{
		{
			name: "one block under other text is a group, a hole per value that differs",
			tree: map[string]string{"ui/users.tsx": users, "ui/teams.tsx": teams},
			want: []string{"ui/teams.tsx:3-12 Teams, ui/users.tsx:3-12 Users | blocks 1 elements 7 holes 2 rank 5"},
		},
		{
			name: "a copy indented another way with its attributes in another order is the same block",
			tree: map[string]string{
				"ui/a.tsx": component("A", "<section id=\"a\" className=\"x\">\n"+inner+"</section>\n"),
				"ui/b.jsx": component("B", "      <section\n className=\"x\"\n id=\"a\">\n"+
					strings.ReplaceAll(inner, "\n", "\n\t")+"</section>\n"),
			},
			want: []string{"ui/a.tsx:3-14 A, ui/b.jsx:3-16 B | blocks 1 elements 8 holes 0 rank 8"},
		},
		{
			name: "another tag is another shape",
			tree: map[string]string{"ui/users.tsx": users, "ui/teams.tsx": strings.ReplaceAll(teams, "h2", "h3")},
		},
		{
			name: "another attribute name is another shape",
			tree: map[string]string{
				"ui/users.tsx": users, "ui/teams.tsx": strings.Replace(teams, "className", "id", 1),
			},
		},
		{
			name: "a condition and a loop around one element are two shapes",
			tree: map[string]string{
				"ui/a.tsx": component("A", "<div>\n{open && (\n"+inner+")}\n</div>\n"),
				"ui/b.tsx": component("B", "<div>\n{rows.map(row => (\n"+inner+"))}\n</div>\n"),
			},
			want: []string{"ui/a.tsx:5-14 A, ui/b.tsx:5-14 B | blocks 1 elements 7 holes 0 rank 7"},
		},
		{
			name: "the condition around an element is a hole",
			tree: map[string]string{
				"ui/a.tsx": component("A", "<div>\n{open && (\n"+inner+")}\n</div>\n"),
				"ui/b.tsx": component("B", "<div>\n{shown && (\n"+inner+")}\n</div>\n"),
			},
			want: []string{"ui/a.tsx:3-16 A, ui/b.tsx:3-16 B | blocks 1 elements 8 holes 1 rank 7"},
		},
		{
			name: "an element handed over as an attribute is part of the shape",
			tree: map[string]string{
				"ui/a.tsx": component("A", "<Page title=\"a\" side={\n"+inner+"} />\n"),
				"ui/b.tsx": component("B", "<Page title=\"b\" side={\n"+inner+"} />\n"),
			},
			want: []string{"ui/a.tsx:3-14 A, ui/b.tsx:3-14 B | blocks 1 elements 8 holes 1 rank 7"},
		},
		{
			name: "a block inside a repeated block is not listed again",
			tree: map[string]string{
				"ui/a.tsx": component("A", "<main>\n"+inner+"</main>\n"),
				"ui/b.tsx": component("B", "<main>\n"+card("c", "U", "n")+"</main>\n"),
			},
			want: []string{"ui/a.tsx:3-14 A, ui/b.tsx:3-14 B | blocks 1 elements 8 holes 1 rank 7"},
		},
		{
			name: "an inner block with a site of its own is a group beside the outer one",
			tree: map[string]string{
				"ui/a.tsx": component("A", "<main>\n"+inner+"</main>\n"),
				"ui/b.tsx": component("B", "<main>\n"+inner+"</main>\n"),
				"ui/c.tsx": component("C", inner),
			},
			want: []string{
				"ui/a.tsx:4-13 A, ui/b.tsx:4-13 B, ui/c.tsx:3-12 C | blocks 1 elements 7 holes 0 rank 14",
				"ui/a.tsx:3-14 A, ui/b.tsx:3-14 B | blocks 1 elements 8 holes 0 rank 8",
			},
		},
		{
			name: "two blocks the same two units share are one group",
			tree: map[string]string{
				"ui/a.tsx": component("A", "<>\n"+inner+"<p>only here</p>\n"+
					row("Menu", "Item", "", "a", "b", "c", "d", "e")+"</>\n"),
				"ui/b.tsx": component("B", "<>\n"+card("c", "U", "n")+
					row("Menu", "Item", "", "a", "b", "c", "d", "e")+"</>\n"),
			},
			want: []string{"ui/a.tsx:4-13 A, ui/b.tsx:4-13 B | blocks 2 elements 13 holes 1 rank 12"},
		},
	}
}

// jsxGuardCases are trees that say what is no block and what is left out.
func jsxGuardCases() []jsxGroupCase {
	users := component("Users", card("card", "Users", "users.length"))
	teams := component("Teams", card("card", "Teams", "teams.length"))
	inner := card("c", "T", "n")
	paths := func(name string, ds ...string) string {
		var b strings.Builder
		b.WriteString("export const " + name + " = (\n<g>\n")
		for _, d := range ds {
			b.WriteString("<path d=\"" + d + "\" />\n")
		}
		b.WriteString("</g>\n)\n")

		return b.String()
	}

	return []jsxGroupCase{
		{
			name: "five elements are the use of a component, not a block",
			tree: map[string]string{
				"ui/a.tsx": component("A", row("Row", "Cell", "", "a", "b", "c", "d")),
				"ui/b.tsx": component("B", row("Row", "Cell", "", "a", "b", "c", "d")),
			},
		},
		{
			name: "more holes than elements is a skeleton the sites fill differently",
			tree: map[string]string{
				"ui/a.tsx": component("A", row("ul", "li", ` id="a" role="x"`, "a", "b", "c", "d", "e")),
				"ui/b.tsx": component("B", row("ul", "li", ` id="b" role="y"`, "f", "g", "h", "i", "j")),
			},
		},
		{
			name: "tests and stories are left out",
			tree: map[string]string{
				"ui/users.tsx": users, "ui/users.test.tsx": users, "ui/users.stories.tsx": teams,
				"examples/teams.tsx": teams,
			},
		},
		{
			name: "two drawings are data, not a block",
			tree: map[string]string{
				"ui/a.tsx": component("A", drawing("M0 0")), "ui/b.tsx": component("B", drawing("M9 9")),
				"ui/c.tsx": paths("c", "a", "b", "c", "d", "e", "f"),
				"ui/d.tsx": paths("d", "g", "h", "i", "j", "k", "l"),
			},
		},
		{
			name: "a drawing inside a block is one hole, whatever it draws",
			tree: map[string]string{
				"ui/a.tsx": component("A", "<main>\n"+drawing("M0 0")+inner+"</main>\n"),
				"ui/b.tsx": component("B", "<main>\n<svg><circle r=\"2\" /></svg>\n"+inner+"</main>\n"),
			},
			want: []string{"ui/a.tsx:3-24 A, ui/b.tsx:3-15 B | blocks 1 elements 9 holes 1 rank 8"},
		},
		{
			name: "a file with no markup has no block",
			tree: map[string]string{"ui/a.ts": "export const a = 1\n", "ui/b.go": "package ui\n"},
		},
	}
}

func TestJSXGroups(t *testing.T) {
	t.Parallel()
	for _, c := range append(jsxShapeCases(), jsxGuardCases()...) {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := jsxSummary(jsxGroups(t, c.tree))
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("groups:\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(c.want, "\n     "))
			}
		})
	}
}

// A payload cut short or filled with noise lists nothing and does not panic.
func TestJSXGroups_DamagedPayload(t *testing.T) {
	t.Parallel()
	whole := printOf(t, "ui/users.tsx", component("Users", card("card", "Users", "n")))
	if len(whole.JSX) == 0 {
		t.Fatal("the fixture recorded no markup")
	}
	for cut := range len(whole.JSX) {
		for _, fill := range []byte{0x00, 0xff} {
			damaged := append(rules.JSXShapes{}, whole.JSX[:cut]...)
			damaged = append(damaged, fill, fill, fill)
			tree := map[string]*rules.Fingerprint{
				"ui/a.tsx": whole, "ui/b.tsx": {JSX: damaged, Units: whole.Units},
			}
			for _, g := range rules.JSXGroups(tree) {
				if len(g.Sites) < 2 || g.Elements < 6 {
					t.Fatalf("cut at %d filled with %x: group %+v", cut, fill, g)
				}
			}
		}
	}
}

// jsxEdit is one file of a change: what it held, what it holds, and the lines
// the change added.
type jsxEdit struct {
	added         func(int) bool
	path          string
	before, after string
}

// jsxChangeCase is a tree, a change laid over it, and the findings.
type jsxChangeCase struct {
	base         map[string]string
	name         string
	changes      []jsxEdit
	want         []string
	experimental bool
}

func everyLine(int) bool { return true }

func noLine(int) bool { return false }

const jsxAdvice = "Make the block one component that takes what differs as props, or say why the sites stay apart"

// jsxReportedCases are changes that add a block the tree held.
func jsxReportedCases() []jsxChangeCase {
	users := component("Users", card("card", "Users", "users.length"))
	teams := component("Teams", card("card", "Teams", "teams.length"))
	roles := component("Roles", card("card", "Roles", "roles.length"))
	inner := card("c", "T", "n")

	return []jsxChangeCase{
		{
			name: "a new file that repeats a block of the tree", experimental: true,
			base:    map[string]string{"ui/users.tsx": users},
			changes: []jsxEdit{{path: "ui/teams.tsx", after: teams, added: everyLine}},
			want: []string{"ui/teams.tsx:3 Teams -> ui/users.tsx Users: repeats 7 elements of markup that 1 other " +
				"place holds already: Users at ui/users.tsx:3. The sites differ in 2 values. " + jsxAdvice},
		},
		{
			name: "two new copies of a block that was there are one finding", experimental: true,
			base: map[string]string{"ui/users.tsx": users},
			changes: []jsxEdit{
				{path: "ui/roles.tsx", after: roles, added: everyLine},
				{path: "ui/teams.tsx", after: teams, added: everyLine},
			},
			want: []string{"ui/roles.tsx:3 Roles -> ui/users.tsx Users: repeats 7 elements of markup that 1 other " +
				"place holds already: Users at ui/users.tsx:3. The sites differ in 2 values. " + jsxAdvice +
				". The change adds 2 such blocks, here and at Teams at ui/teams.tsx:3"},
		},
		{
			name: "a copy of a block and of the block inside it is reported once, on the outer one", experimental: true,
			base: map[string]string{
				"ui/a.tsx": component("A", "<main>\n"+inner+"</main>\n"), "ui/c.tsx": component("C", inner),
			},
			changes: []jsxEdit{
				{path: "ui/b.tsx", after: component("B", "<main>\n"+inner+"</main>\n"), added: everyLine},
			},
			want: []string{"ui/b.tsx:3 B -> ui/a.tsx A: repeats 8 elements of markup that 1 other place holds " +
				"already: A at ui/a.tsx:3. The sites differ in nothing. " + jsxAdvice},
		},
	}
}

// jsxQuietCases are changes that add no block a reader would call a copy.
func jsxQuietCases() []jsxChangeCase {
	users := component("Users", card("card", "Users", "users.length"))
	teams := component("Teams", card("card", "Teams", "teams.length"))
	held := map[string]string{"ui/users.tsx": users}

	return []jsxChangeCase{
		{
			name: "without --experimental nothing is reported", base: held,
			changes: []jsxEdit{{path: "ui/teams.tsx", after: teams, added: everyLine}},
		},
		{
			name: "two new files that repeat each other have nothing that was there", experimental: true,
			changes: []jsxEdit{
				{path: "ui/teams.tsx", after: teams, added: everyLine},
				{path: "ui/users.tsx", after: users, added: everyLine},
			},
		},
		{
			name: "a block moved to another file is not new", experimental: true, base: held,
			changes: []jsxEdit{
				{path: "ui/old.tsx", before: teams}, {path: "ui/teams.tsx", after: teams, added: everyLine},
			},
		},
		{
			name: "a block indented again with its text edited is not new", experimental: true, base: held,
			changes: []jsxEdit{{
				path: "ui/teams.tsx", before: teams, added: everyLine,
				after: component("Teams", strings.ReplaceAll(card("card", "All teams", "teams.length"), "\n", "\n  ")),
			}},
		},
		{
			name: "a block whose first line the change did not add is not new", experimental: true, base: held,
			changes: []jsxEdit{{path: "ui/teams.tsx", after: teams, added: noLine}},
		},
		{
			name: "a new block in a story is left out", experimental: true, base: held,
			changes: []jsxEdit{{path: "ui/teams.stories.tsx", after: teams, added: everyLine}},
		},
	}
}

// jsxFindings runs the change rule over a case and renders what it reports.
func jsxFindings(t *testing.T, c *jsxChangeCase) []string {
	t.Helper()
	rev := printsOf(t, c.base)
	changes := make([]rules.DupChange, 0, len(c.changes))
	for _, ch := range c.changes {
		dc := rules.DupChange{Path: ch.path, Added: ch.added}
		if ch.before != "" {
			dc.Before = printOf(t, ch.path, ch.before)
		}
		if ch.after != "" {
			dc.After = printOf(t, ch.path, ch.after)
			rev[ch.path] = dc.After
		}
		changes = append(changes, dc)
	}
	var got []string
	found := rules.NewJSXShapes(rev, changes, c.experimental)
	for _, path := range slices.Sorted(maps.Keys(found)) {
		for i := range found[path] {
			f := &found[path][i]
			got = append(got, fmt.Sprintf("%s:%d %s -> %s: %s", path, f.Line, f.Scope, f.Match, f.Note))
		}
	}

	return got
}

func TestNewJSXShapes(t *testing.T) {
	t.Parallel()
	cases := append(jsxReportedCases(), jsxQuietCases()...)
	for i := range cases {
		c := &cases[i]
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := jsxFindings(t, c)
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("findings:\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(c.want, "\n     "))
			}
		})
	}
}

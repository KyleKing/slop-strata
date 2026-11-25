package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/fixturetest"
	"github.com/kyleking/slop-strata/internal/rules"
)

const ruleNewCaseSet = "x/new-case-set"

// kindFixture writes, in one language, a function that enumerates the kinds
// of a shape: as a switch, as an if chain, as a table, or as a call of
// another function.
type kindFixture struct {
	name, ext string
	head      string // what a file opens with
	sw        string // FN and ARMS
	arm       string // KIND and N
	chain     string // FN and ARMS
	first     string // KIND and N
	link      string // KIND and N
	table     string // FN and ROWS
	row       string // KIND and N
	call      string // FN and CALLEE
	kinds     []string
}

func kindFixtures() []kindFixture {
	return []kindFixture{
		{
			name: "go", ext: ".go", kinds: []string{"KindCircle", "KindSquare", "KindTriangle", "KindHexagon"},
			head:  "package shape\n\n",
			sw:    "func FN(k Kind) int {\n\tswitch k {\nARMS\t}\n\n\treturn 0\n}\n",
			arm:   "\tcase KIND:\n\t\treturn N\n",
			chain: "func FN(k Kind) int {\nARMS\n\treturn 0\n}\n",
			first: "\tif k == KIND {\n\t\treturn N\n\t}", link: " else if k == KIND {\n\t\treturn N\n\t}",
			table: "var FN = map[Kind]func() int{\nROWS}\n", row: "\tKIND: sidesN,\n",
			call: "func FN(k Kind) int {\n\treturn CALLEE(k) + 1\n}\n",
		},
		{
			name: "python", ext: ".py",
			kinds: []string{"Kind.CIRCLE", "Kind.SQUARE", "Kind.TRIANGLE", "Kind.HEXAGON"},
			sw:    "def FN(kind):\n    match kind:\nARMS    return 0\n",
			arm:   "        case KIND:\n            return N\n",
			chain: "def FN(kind):\nARMS\n    return 0\n",
			first: "    if kind == KIND:\n        return N", link: "\n    elif kind == KIND:\n        return N",
			table: "FN = {\nROWS}\n", row: "    KIND: sides_N,\n",
			call: "def FN(kind):\n    return CALLEE(kind) + 1\n",
		},
		{
			name: "typescript", ext: ".ts",
			kinds: []string{"Kind.Circle", "Kind.Square", "Kind.Triangle", "Kind.Hexagon"},
			sw:    "export function FN(kind: Kind): number {\n  switch (kind) {\nARMS  }\n  return 0\n}\n",
			arm:   "    case KIND:\n      return N\n",
			chain: "export function FN(kind: Kind): number {\nARMS\n  return 0\n}\n",
			first: "  if (kind === KIND) {\n    return N\n  }", link: " else if (kind === KIND) {\n    return N\n  }",
			table: "export const FN: Record<Kind, () => number> = {\nROWS}\n", row: "  [KIND]: sidesN,\n",
			call: "export function FN(kind: Kind): number {\n  return CALLEE(kind) + 1\n}\n",
		},
	}
}

// fillKinds writes one piece per kind, numbered from base.
func fillKinds(tmpl string, kinds []string, base int) string {
	var b strings.Builder
	for i, kind := range kinds {
		b.WriteString(strings.NewReplacer("KIND", kind, "N", strconv.Itoa(base+i)).Replace(tmpl))
	}

	return b.String()
}

func (f *kindFixture) asSwitch(fn string, base int, kinds ...string) string {
	return f.head + strings.NewReplacer("FN", fn, "ARMS", fillKinds(f.arm, kinds, base)).Replace(f.sw)
}

func (f *kindFixture) asChain(fn string, kinds ...string) string {
	arms := fillKinds(f.first, kinds[:1], 1) + fillKinds(f.link, kinds[1:], 2)

	return f.head + strings.NewReplacer("FN", fn, "ARMS", arms).Replace(f.chain)
}

func (f *kindFixture) asTable(fn string, kinds ...string) string {
	return f.head + strings.NewReplacer("FN", fn, "ROWS", fillKinds(f.row, kinds, 1)).Replace(f.table)
}

func (f *kindFixture) asCall(fn, callee string) string {
	return f.head + strings.NewReplacer("FN", fn, "CALLEE", callee).Replace(f.call)
}

// newCaseSetCase is a change that adds a second switch over the kinds a
// switch of the tree already enumerates.
func newCaseSetCase(f *kindFixture) fixturetest.Case {
	sides := func(body string) fixturetest.Tree { return fixturetest.Tree{"shape/sides" + f.ext: body} }
	k := f.kinds
	whole := f.asSwitch("sides", 1, k...)

	return fixturetest.Case{
		Name: f.name + ": a change adds a second switch over the same cases",
		Base: fixturetest.Tree{
			before + "shape/area" + f.ext: f.asSwitch("area", 1, k...),
			added + "shape/sides" + f.ext: whole,
		},
		Dodges: []fixturetest.Variant{
			adds("return other values from the arms", sides(f.asSwitch("sides", 5, k...))),
			adds("list the cases in another order", sides(f.asSwitch("sides", 1, k[3], k[1], k[2], k[0]))),
			adds("add a comment above the function",
				sides(strings.Replace(whole, f.head, f.head+commentFor(f)+"\n", 1))),
		},
		Persists: []fixturetest.Variant{
			adds("give the function another name", sides(f.asSwitch("edges", 1, k...))),
			movesTo("shape/sides"+f.ext, "shape/edges"+f.ext, whole),
			adds("leave one case out", sides(f.asSwitch("sides", 1, k[:3]...))),
			adds("write it as an if chain", sides(f.asChain("sides", k...))),
			adds("write it as a table", sides(f.asTable("sides", k...))),
		},
		Gaps: []fixturetest.Variant{
			adds("split the switch over two functions", sides(
				f.asSwitch("sides", 1, k[:2]...)+strings.TrimPrefix(f.asSwitch("moreSides", 3, k[2:]...), f.head))),
		},
		Fixes: []fixturetest.Variant{
			adds("call the function that is there", sides(f.asCall("sides", "area"))),
			adds("enumerate other cases", sides(f.asSwitch("sides", 1, k[0]+"Big", k[1]+"Big", k[2]+"Big"))),
		},
	}
}

// commentFor is a comment line in the fixture's language.
func commentFor(f *kindFixture) string {
	if f.ext == ".py" {
		return "# sides counts the edges"
	}

	return "// sides counts the edges"
}

func TestNewCaseSetHoldsUnderAdversarialEdits(t *testing.T) {
	t.Parallel()
	fixtures := kindFixtures()
	cases := make([]fixturetest.Case, 0, len(fixtures))
	for i := range fixtures {
		cases = append(cases, newCaseSetCase(&fixtures[i]))
	}
	fixturetest.Run(t, cases, changeRuleIDs(t, ruleNewCaseSet, "--experimental"))
}

// caseQuietCases are changes that add no new place a reader would say
// enumerates the cases again.
func caseQuietCases() []quietCase {
	f := &kindFixtures()[0]
	k := f.kinds
	area, sides := f.asSwitch("area", 1, k...), f.asSwitch("sides", 1, k...)
	held := fixturetest.Tree{before + "shape/area.go": area, before + "shape/sides.go": sides}
	with := func(files fixturetest.Tree) fixturetest.Tree {
		out := fixturetest.Tree{}
		for name, body := range held {
			out[name] = body
		}
		for name, body := range files {
			out[added+name] = body
		}

		return out
	}
	wrapped := strings.Replace(strings.ReplaceAll(sides, "\n\t", "\n\t\t"),
		"func sides(k Kind) int {", "func sides(k Kind) int {\n\tif k != nil {", 1)
	wrapped = strings.Replace(wrapped, "\n\t\treturn 0\n}\n", "\n\t}\n\n\treturn 0\n}\n", 1)

	return []quietCase{
		{name: "the whole family is new", tree: fixturetest.Tree{
			before + "shape/doc.go": "package shape\n",
			added + "shape/area.go": area, added + "shape/sides.go": sides,
		}},
		{name: "a switch moves to another file", tree: with(fixturetest.Tree{
			"shape/sides.go": removed, "shape/edges.go": sides,
		})},
		{name: "a switch is indented under a condition", tree: with(fixturetest.Tree{"shape/sides.go": wrapped})},
		{name: "a switch that was there gains a case", tree: fixturetest.Tree{
			before + "shape/area.go": area, before + "shape/sides.go": f.asSwitch("sides", 1, k[:3]...),
			added + "shape/sides.go": strings.Replace(sides, "switch k {", "switch kind := k; kind {", 1),
		}},
		{name: "a switch is added to a test", tree: fixturetest.Tree{
			before + "shape/area.go": area, added + "shape/sides_test.go": sides,
		}},
		{name: "a switch is added to an example", tree: fixturetest.Tree{
			before + "shape/area.go": area, added + "examples/sides/main.go": sides,
		}},
		{name: "a switch over two of the cases", tree: fixturetest.Tree{
			before + "shape/area.go": area, added + "shape/sides.go": f.asSwitch("sides", 1, k[:2]...),
		}},
	}
}

func TestNewCaseSetStaysQuiet(t *testing.T) {
	t.Parallel()
	for _, tc := range caseQuietCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, f := range changeFindings(t, historyRepo(t, tc.tree), "--experimental") {
				if f.Rule == ruleNewCaseSet {
					t.Errorf("reported %s:%d: %s", f.Path, f.Line, f.Note)
				}
			}
		})
	}
}

// TestNewCaseSetIsReadable pins what a person and an agent see of a finding:
// the note under the findings table, and the same note as the reason of a
// next line, so --brief names the places that already enumerate the cases.
func TestNewCaseSetIsReadable(t *testing.T) {
	t.Parallel()
	repo := historyRepo(t, newCaseSetCase(&kindFixtures()[0]).Base)
	const want = "enumerates 4 cases (KindCircle, KindHexagon, KindSquare, and 1 more) that 1 other place " +
		"enumerates already: area at shape/area.go:4. A new case now needs 2 edits. Have the places read one table"
	for _, flags := range [][]string{{"--experimental"}, {"--experimental", "--brief"}} {
		out, err := runCLI(t, repo, append([]string{"change", "--base", "HEAD~1", "--rev", "HEAD"}, flags...)...)
		if err != nil {
			t.Fatalf("change %v: %v", flags, err)
		}
		if !strings.Contains(out, want) {
			t.Errorf("change %v does not name the places that hold the cases:\n%s", flags, out)
		}
	}
	plain, err := runCLI(t, repo, "change", "--base", "HEAD~1", "--rev", "HEAD")
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if strings.Contains(plain, ruleNewCaseSet) {
		t.Errorf("the rule reported without --experimental:\n%s", plain)
	}
}

// TestNewCaseSetCountsTheCasesTheNewPlaceNames pins that the note lists the
// family's cases the new place names, and not one it leaves out.
func TestNewCaseSetCountsTheCasesTheNewPlaceNames(t *testing.T) {
	t.Parallel()
	f := &kindFixtures()[0]
	found := changeFindings(t, historyRepo(t, fixturetest.Tree{
		before + "shape/area.go": f.asSwitch("area", 1, f.kinds...),
		added + "shape/sides.go": f.asSwitch("sides", 1, f.kinds[:3]...),
	}), "--experimental")
	const want = "enumerates 3 cases (KindCircle, KindSquare, KindTriangle) that 1 other place enumerates already: "
	for _, row := range found {
		if row.Rule == ruleNewCaseSet && strings.HasPrefix(row.Note, want) {
			return
		}
	}
	t.Errorf("findings = %+v, want a note that starts %q", found, want)
}

// TestNewCaseSetReportsAFamilyOnce pins that a change adding two places to a
// family reports the first and names the other.
func TestNewCaseSetReportsAFamilyOnce(t *testing.T) {
	t.Parallel()
	f := &kindFixtures()[1]
	found := changeFindings(t, historyRepo(t, fixturetest.Tree{
		before + "shape/area.py":  f.asSwitch("area", 1, f.kinds...),
		added + "shape/sides.py":  f.asTable("SIDES", f.kinds...),
		added + "shape/angles.py": f.asChain("angles", f.kinds...),
	}), "--experimental")
	var notes []string
	for _, row := range found {
		if row.Rule == ruleNewCaseSet {
			notes = append(notes, row.Path+": "+row.Note)
		}
	}
	if len(notes) != 1 || !strings.HasPrefix(notes[0], "shape/angles.py: ") ||
		!strings.Contains(notes[0], "A new case now needs 3 edits") ||
		!strings.HasSuffix(notes[0], "The change adds 2 such places, here and at SIDES at shape/sides.py:1") {
		t.Errorf("findings = %q, want one on the first new place that names the second", notes)
	}
}

// TestNewCaseSetTakesAnInlineIgnore pins that a reasoned ignore above the new
// switch sets the finding aside and counts it.
func TestNewCaseSetTakesAnInlineIgnore(t *testing.T) {
	t.Parallel()
	f := &kindFixtures()[0]
	tree := newCaseSetCase(f).Base
	tree[added+"shape/sides.go"] = strings.Replace(f.asSwitch("sides", 1, f.kinds...), "\tswitch k {",
		"\t// slop-strata:ignore x/new-case-set the two tables merge next release\n\tswitch k {", 1)
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
		if row.Rule == ruleNewCaseSet {
			t.Errorf("an ignored finding was reported: %+v", row)
		}
	}
	if env.Counts.Suppressed.Ignored != 1 {
		t.Errorf("counts.suppressed.ignored = %d, want 1", env.Counts.Suppressed.Ignored)
	}
}

// caseSetRepo holds a switch, an if chain, and a table over the kinds of a
// shape, and a copy of the switch in a test and in an example.
func caseSetRepo(t *testing.T) string {
	t.Helper()
	f := &kindFixtures()[0]
	k := f.kinds
	files := map[string]string{
		"shape/area.go": f.asSwitch("area", 1, k...), "shape/name.go": f.asChain("name", k[:3]...),
		"shape/sides.go":     f.asTable("sides", k...),
		"shape/area_test.go": f.asSwitch("testArea", 1, k...), "examples/area/main.go": f.asSwitch("demo", 1, k...),
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

// snapshotCaseSets reads the case sets of a snapshot.
func snapshotCaseSets(t *testing.T, repo string, argv ...string) *caseSetList {
	t.Helper()
	out, err := runCLI(t, repo, append([]string{"snapshot", "--json"}, argv...)...)
	if err != nil {
		t.Fatalf("snapshot --json %v: %v", argv, err)
	}
	var env struct {
		Result struct {
			CaseSets *caseSetList `json:"case_sets"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parsing JSON: %v\n%s", err, out)
	}

	return env.Result.CaseSets
}

func TestSnapshotListsCaseSetsWhenExperimental(t *testing.T) {
	t.Parallel()
	repo := caseSetRepo(t)
	if got := snapshotCaseSets(t, repo); got != nil {
		t.Errorf("case_sets = %+v without --experimental, want none", got)
	}
	got := snapshotCaseSets(t, repo, "--experimental")
	want := &caseSetList{Total: 1, Families: []rules.CaseFamily{{
		Cases: []string{"KindCircle", "KindHexagon", "KindSquare", "KindTriangle"}, Weight: 3, Rank: 8.99,
		Sites: []rules.CaseSite{
			{Path: "shape/area.go", Unit: "area", Kind: "switch", Line: 4, EndLine: 13, Cases: 4, Missing: []string{}},
			{
				Path: "shape/name.go", Unit: "name", Kind: "if", Line: 4, EndLine: 10, Cases: 3,
				Missing: []string{"KindHexagon"},
			},
			{Path: "shape/sides.go", Unit: "sides", Kind: "table", Line: 3, EndLine: 8, Cases: 4, Missing: []string{}},
		},
	}}}
	// The test's copy and the example's copy of the switch are left out.
	if !reflect.DeepEqual(got, want) {
		t.Errorf("case_sets = %+v, want %+v", got, want)
	}
}

func TestSnapshotTextPrintsCaseSets(t *testing.T) {
	t.Parallel()
	repo := caseSetRepo(t)
	plain, err := runCLI(t, repo, "snapshot")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if strings.Contains(plain, "case sets") {
		t.Errorf("snapshot text lists case sets without --experimental:\n%s", plain)
	}
	gated, err := runCLI(t, repo, "snapshot", "--experimental")
	if err != nil {
		t.Fatalf("snapshot --experimental: %v", err)
	}
	for _, want := range []string{
		"case sets, 1 of 1 (rank: sites × weight of the cases they share)",
		"shape/area.go:4   area   9.0   3      4: KindCircle, KindHe…",
		"shape/name.go:4   name                                        1: KindHexagon",
		"shape/sides.go:3  sides",
	} {
		if !strings.Contains(gated, want) {
			t.Errorf("snapshot --experimental text lacks %q:\n%s", want, gated)
		}
	}
	empty, err := runCLI(t, deterministicRepo(t), "snapshot", "--experimental")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if strings.Contains(empty, "case sets") {
		t.Errorf("snapshot text prints an empty list:\n%s", empty)
	}
}

func TestCaseSetRowsListThreeSitesAndCountTheRest(t *testing.T) {
	t.Parallel()
	f := rules.CaseFamily{
		Cases: []string{"KindCircle", "KindSquare", "KindTriangle"}, Weight: 4.2, Rank: 21,
		Sites: []rules.CaseSite{
			{Path: "a.go", Unit: "one", Line: 3},
			{
				Path: "b.go", Unit: "aFunctionWithAVeryLongNameIndeed", Line: 4,
				Missing: []string{"KindSquare", "KindTriangle"},
			},
			{Path: "c.go", Unit: "three", Line: 5},
			{Path: "d.go", Unit: "four", Line: 6},
			{Path: "e.go", Unit: "five", Line: 7},
		},
	}
	got := caseSetRows(&f)
	want := [][]string{
		{"a.go:3", "one", "21.0", "5", "3: KindCircle, KindSq…", ""},
		{"b.go:4", "aFunctionWithAVeryL…", "", "", "", "2: KindSquare, …"},
		{"c.go:5", "three", "", "", "", ""},
		{"… and 2 more", "", "", "", "", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %q, want %q", got, want)
	}
}

func TestSnapshotTextListsEveryCaseSetWithAll(t *testing.T) {
	t.Parallel()
	list := &caseSetList{Total: 7}
	for i := range 7 {
		list.Families = append(list.Families, rules.CaseFamily{
			Cases: []string{"A", "B", "C"},
			Sites: []rules.CaseSite{{Path: "a.go", Line: i + 1}, {Path: "b.go", Line: 1}},
		})
	}
	for all, want := range map[bool]string{false: "case sets, 5 of 7", true: "case sets, 7 of 7"} {
		var out strings.Builder
		printCaseSets(&out, list, all)
		if !strings.Contains(out.String(), want) {
			t.Errorf("with all=%v the table lacks %q:\n%s", all, want, out.String())
		}
	}
}

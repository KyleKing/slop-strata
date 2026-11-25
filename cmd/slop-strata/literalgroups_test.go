package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/fixturetest"
	"github.com/kyleking/slop-strata/internal/rules"
)

// literalFixture writes, in one language, a file that tests a value against
// a literal, one that hands it to a call, one that names it, and one that
// reads the name. FN is the function's name and LIT what it writes.
type literalFixture struct {
	name, ext string
	head      string
	test      string
	write     string
	named     string // declares the constant
	constant  string // its name
	other     string // the literal under another quote
	halves    string // the literal built from two parts
	comment   string
}

// The literal the fixtures repeat.
const (
	vendorValue = "application/vnd.app+json"
	vendorLit   = `"` + vendorValue + `"`
)

// vendorHalves builds the value from two parts.
const vendorHalves = `"application/" + "vnd.app+json"`

func literalFixtures() []literalFixture {
	return []literalFixture{
		{
			name: "go", ext: ".go", head: "package api\n\n",
			test:  "func FN(r req) bool {\n\treturn r.kind == LIT\n}\n",
			write: "func FN(r req) {\n\tr.send(1, LIT)\n}\n",
			named: "const vendorType = " + vendorLit + "\n\n", constant: "vendorType",
			other: "`" + vendorValue + "`", halves: vendorHalves, comment: "// put sends the body\n",
		},
		{
			name: "python", ext: ".py",
			test:  "def FN(r):\n    return r.kind == LIT\n",
			write: "def FN(r):\n    r.send(1, LIT)\n",
			named: "VENDOR_TYPE = " + vendorLit + "\n\n\n", constant: "VENDOR_TYPE",
			other: "'" + vendorValue + "'", halves: vendorHalves, comment: "# put sends the body\n",
		},
		{
			name: "typescript", ext: ".ts",
			test:  "export function FN(r: Req): boolean {\n  return r.kind === LIT\n}\n",
			write: "export function FN(r: Req) {\n  r.send(1, LIT)\n}\n",
			named: "export const VENDOR_TYPE = " + vendorLit + "\n\n", constant: "VENDOR_TYPE",
			other: "'" + vendorValue + "'", halves: vendorHalves, comment: "// put sends the body\n",
		},
	}
}

// fillLiteral writes one piece of a fixture with its function name and literal.
func fillLiteral(tmpl, fn, lit string) string {
	return strings.NewReplacer("FN", fn, "LIT", lit).Replace(tmpl)
}

// tests is a file that tests a value against the literal.
func (f *literalFixture) tests(fn string) string { return f.head + fillLiteral(f.test, fn, vendorLit) }

// writes is a file that hands an expression to a call.
func (f *literalFixture) writes(fn, expr string) string {
	return f.head + fillLiteral(f.write, fn, expr)
}

// held is a tree whose base commit tests the literal in two files and writes
// it out in a third.
func (f *literalFixture) held() fixturetest.Tree {
	return fixturetest.Tree{
		before + "api/accept" + f.ext: f.tests("accept"),
		before + "api/check" + f.ext:  f.tests("check"),
		before + "api/post" + f.ext:   f.writes("post", vendorLit),
	}
}

// dupLiteralCase is a change that writes out, in a fourth file, a literal
// three files of the tree already write and two of them test.
func dupLiteralCase(f *literalFixture) fixturetest.Case {
	put := func(body string) fixturetest.Tree { return fixturetest.Tree{"api/put" + f.ext: body} }
	whole := f.writes("put", vendorLit)
	base := f.held()
	base[added+"api/put"+f.ext] = whole

	return fixturetest.Case{
		Name: f.name + ": a change writes out a literal the tree tests elsewhere",
		Base: base,
		Dodges: []fixturetest.Variant{
			adds("add a comment above the function", put(f.head+f.comment+fillLiteral(f.write, "put", vendorLit))),
			adds("add a function that does not write it",
				put(whole+"\n"+fillLiteral(f.write, "patch", `"text/plain"`))),
		},
		Persists: []fixturetest.Variant{
			adds("write it under another quote", put(f.writes("put", f.other))),
			adds("give the function another name", put(f.writes("replace", vendorLit))),
			movesTo("api/put"+f.ext, "api/replace"+f.ext, whole),
			adds("write it twice", put(whole+"\n"+fillLiteral(f.write, "patch", vendorLit))),
			adds("test a value against it", put(f.tests("put"))),
		},
		Gaps: []fixturetest.Variant{
			adds("build it from two halves", put(f.writes("put", f.halves))),
		},
		Fixes: []fixturetest.Variant{
			adds("name it in the change and read the name",
				put(f.head+f.named+fillLiteral(f.write, "put", f.constant))),
			adds("write another value", put(f.writes("put", `"application/octet-stream"`))),
		},
	}
}

func TestDupLiteralHoldsUnderAdversarialEdits(t *testing.T) {
	t.Parallel()
	fixtures := literalFixtures()
	cases := make([]fixturetest.Case, 0, len(fixtures))
	for i := range fixtures {
		cases = append(cases, dupLiteralCase(&fixtures[i]))
	}
	fixturetest.Run(t, cases, changeRuleIDs(t, rules.RuleDupLiteral, "--experimental"))
}

// literalQuietCases are changes that write a literal out in no place a
// reader would call new.
func literalQuietCases() []quietCase {
	f := &literalFixtures()[0]
	put := f.writes("put", vendorLit)
	with := func(files fixturetest.Tree) fixturetest.Tree {
		out := f.held()
		for name, body := range files {
			out[name] = body
		}

		return out
	}

	return []quietCase{
		{name: "every place of the literal is new", tree: fixturetest.Tree{
			before + "api/doc.go":   "package api\n",
			added + "api/accept.go": f.tests("accept"), added + "api/check.go": f.tests("check"),
			added + "api/post.go": f.writes("post", vendorLit), added + "api/put.go": put,
		}},
		{name: "a place moves to another file", tree: with(fixturetest.Tree{
			before + "api/put.go": put, added + "api/put.go": removed, added + "api/replace.go": put,
		})},
		{name: "a place that was there is edited on its line", tree: with(fixturetest.Tree{
			before + "api/put.go": put, added + "api/put.go": strings.Replace(put, "r.send(1, ", "r.send(2, ", 1),
		})},
		{name: "the change names the literal", tree: with(fixturetest.Tree{
			before + "api/put.go": put, added + "api/names.go": f.head + f.named,
		})},
		{name: "the place is in a test", tree: with(fixturetest.Tree{added + "api/put_test.go": put})},
		{name: "the place is in an example", tree: with(fixturetest.Tree{added + "examples/put/main.go": put})},
		{name: "the tree tests the literal once", tree: fixturetest.Tree{
			before + "api/accept.go": f.tests("accept"), before + "api/check.go": f.writes("check", vendorLit),
			before + "api/post.go": f.writes("post", vendorLit), added + "api/put.go": put,
		}},
		{name: "the literal is under the floor of four places", tree: fixturetest.Tree{
			before + "api/accept.go": f.tests("accept"), before + "api/check.go": f.tests("check"),
			added + "api/put.go": put,
		}},
	}
}

func TestDupLiteralStaysQuiet(t *testing.T) {
	t.Parallel()
	for _, tc := range literalQuietCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, f := range changeFindings(t, historyRepo(t, tc.tree), "--experimental") {
				if f.Rule == rules.RuleDupLiteral {
					t.Errorf("reported %s:%d: %s", f.Path, f.Line, f.Note)
				}
			}
		})
	}
}

// TestDupLiteralIsReadable pins what a person and an agent see of a finding:
// the note under the findings table, and the same note as the reason of a
// next line, so --brief names the files that already write the literal.
func TestDupLiteralIsReadable(t *testing.T) {
	t.Parallel()
	repo := historyRepo(t, dupLiteralCase(&literalFixtures()[0]).Base)
	const want = "written out in 4 places of 4 files, 2 of them a test, a case, or a key: api/accept.go:4, " +
		"api/check.go:4, api/post.go:4. A slip in one spelling goes unseen. Name it once"
	for _, flags := range [][]string{{"--experimental"}, {"--experimental", "--brief"}} {
		out, err := runCLI(t, repo, append([]string{"change", "--base", "HEAD~1", "--rev", "HEAD"}, flags...)...)
		if err != nil {
			t.Fatalf("change %v: %v", flags, err)
		}
		if !strings.Contains(out, want) {
			t.Errorf("change %v does not name the files that write the literal:\n%s", flags, out)
		}
	}
	plain, err := runCLI(t, repo, "change", "--base", "HEAD~1", "--rev", "HEAD")
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if strings.Contains(plain, rules.RuleDupLiteral) {
		t.Errorf("the rule reported without --experimental:\n%s", plain)
	}
}

// TestDupLiteralNamesTheConstant pins that a change writing out a value a
// constant already names is pointed at the constant, in every language.
func TestDupLiteralNamesTheConstant(t *testing.T) {
	t.Parallel()
	for _, f := range literalFixtures() {
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			found := changeFindings(t, historyRepo(t, fixturetest.Tree{
				before + "api/names" + f.ext: f.head + f.named,
				before + "api/post" + f.ext:  f.writes("post", vendorLit),
				before + "api/put" + f.ext:   f.writes("put", vendorLit),
				added + "api/patch" + f.ext:  f.writes("patch", vendorLit),
			}), "--experimental")
			line := strings.Count(f.head, "\n") + 1
			want := f.constant + " at api/names" + f.ext + ":" + strconv.Itoa(line) + " already names it. Read"
			for _, row := range found {
				if row.Rule == rules.RuleDupLiteral && row.Path == "api/patch"+f.ext && row.Match == vendorLit &&
					strings.HasPrefix(row.Note, "written out in 4 places of 4 files: api/names"+f.ext) &&
					strings.Contains(row.Note, want) {
					return
				}
			}
			t.Errorf("findings = %+v, want one on api/patch%s whose note has %q", found, f.ext, want)
		})
	}
}

// TestDupLiteralReportsAValueOnce pins that a change writing a value out in
// two new files reports the first.
func TestDupLiteralReportsAValueOnce(t *testing.T) {
	t.Parallel()
	f := &literalFixtures()[1]
	tree := f.held()
	tree[added+"api/put.py"] = f.writes("put", vendorLit)
	tree[added+"api/patch.py"] = f.writes("patch", vendorLit)
	var at []string
	for _, row := range changeFindings(t, historyRepo(t, tree), "--experimental") {
		if row.Rule == rules.RuleDupLiteral {
			at = append(at, row.Path)
		}
	}
	if !reflect.DeepEqual(at, []string{"api/patch.py"}) {
		t.Errorf("findings at %q, want one at api/patch.py", at)
	}
}

// literalRepo holds a literal two files test and two write out, a copy in a
// test and in an example, and a value a constant names that two files write
// out all the same.
func literalRepo(t *testing.T) string {
	t.Helper()
	f := &literalFixtures()[0]
	files := map[string]string{
		"api/accept.go": f.tests("accept"), "api/check.go": f.tests("check"),
		"api/post.go": f.writes("post", vendorLit) + "\n" + fillLiteral(f.write, "postForm", `"sha256-base64"`),
		"api/put.go":  f.writes("put", vendorLit) + "\n" + fillLiteral(f.write, "putForm", `"sha256-base64"`),
		"api/sums.go": f.head + "const sumKind = \"sha256-base64\"\n\n" +
			fillLiteral(f.write, "sum", `"sha256-base64"`),
		"api/put_test.go": f.writes("testPut", vendorLit), "examples/put/main.go": f.writes("demo", vendorLit),
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

// snapshotLiteralGroups reads the literal groups of a snapshot.
func snapshotLiteralGroups(t *testing.T, repo string, argv ...string) *literalGroupList {
	t.Helper()
	var list *literalGroupList
	snapshotField(t, repo, "literal_groups", &list, argv...)

	return list
}

func TestSnapshotListsLiteralGroupsWhenExperimental(t *testing.T) {
	t.Parallel()
	repo := literalRepo(t)
	if got := snapshotLiteralGroups(t, repo); got != nil {
		t.Errorf("literal_groups = %+v without --experimental, want none", got)
	}
	got := snapshotLiteralGroups(t, repo, "--experimental")
	want := &literalGroupList{Total: 2, Groups: []rules.LiteralGroup{
		{
			Value: "sha256-base64", Files: 3, Written: 4, Uses: 0, Rank: 3,
			Constant: &rules.LiteralConstant{Name: "sumKind", Path: "api/sums.go", Line: 3},
			Sites: []rules.LiteralSite{
				{Path: "api/post.go", Line: 8, Written: 1},
				{Path: "api/put.go", Line: 8, Written: 1},
				{Path: "api/sums.go", Line: 3, Written: 2},
			},
		},
		{
			Value: vendorValue, Files: 4, Written: 4, Uses: 2, Rank: 2,
			Sites: []rules.LiteralSite{
				{Path: "api/accept.go", Line: 4, Written: 1, Uses: 1},
				{Path: "api/check.go", Line: 4, Written: 1, Uses: 1},
				{Path: "api/post.go", Line: 4, Written: 1},
				{Path: "api/put.go", Line: 4, Written: 1},
			},
		},
	}}
	// The test's copy and the example's copy are left out.
	if !reflect.DeepEqual(got, want) {
		t.Errorf("literal_groups = %+v, want %+v", got, want)
	}
	// The rule reports on a change and never per file of a snapshot.
	out, err := runCLI(t, repo, "snapshot", "--json", "--experimental")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if strings.Contains(out, rules.RuleDupLiteral+`"`) {
		t.Errorf("snapshot lists %s findings:\n%s", rules.RuleDupLiteral, out)
	}
}

// TestSnapshotTextPrintsLiteralGroups pins the table a person reads, and that
// it is printed only with the flag and only when a group is listed.
func TestSnapshotTextPrintsLiteralGroups(t *testing.T) {
	t.Parallel()
	repo := literalRepo(t)
	const title = "literal groups"
	for _, tc := range []struct {
		name, repo string
		argv, want []string
	}{
		{name: "without the flag", repo: repo, argv: []string{"snapshot"}},
		{name: "a tree with no group", repo: deterministicRepo(t), argv: []string{"snapshot", "--experimental"}},
		{name: "with the flag", repo: repo, argv: []string{"snapshot", "--experimental"}, want: []string{
			title + ", 2 of 2 (rank: files × share of places that test or key on the value)",
			`api/post.go:8    "sha256-base64"             3.0   3      4        0     sumKind`,
			`api/accept.go:4  "application/vnd.app+json"  2.0   4      4        2`,
			"… and 1 more",
		}},
	} {
		out, err := runCLI(t, tc.repo, tc.argv...)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(tc.want) == 0 && strings.Contains(out, title) {
			t.Errorf("%s: the text lists literal groups:\n%s", tc.name, out)
		}
		for _, want := range tc.want {
			if !strings.Contains(out, want) {
				t.Errorf("%s: the text lacks %q:\n%s", tc.name, want, out)
			}
		}
	}
}

func TestLiteralGroupRowsListThreeFilesAndCountTheRest(t *testing.T) {
	t.Parallel()
	g := rules.LiteralGroup{
		Value: "a value that runs well past the width of its column", Files: 9, Written: 12, Uses: 3, Rank: 2.25,
		Constant: &rules.LiteralConstant{Name: "aConstantWithAVeryLongNameIndeed", Path: "a.go", Line: 3},
		Sites: []rules.LiteralSite{
			{Path: "a.go", Line: 3}, {Path: "b.go", Line: 4}, {Path: "c.go", Line: 5}, {Path: "d.go", Line: 6},
		},
	}
	got := literalGroupRows(&g)
	want := [][]string{
		{"a.go:3", `"a value that runs well past th…`, "2.2", "9", "12", "3", "aConstantWithAVeryLongNameI…"},
		{"b.go:4", "", "", "", "", "", ""},
		{"c.go:5", "", "", "", "", "", ""},
		{"… and 6 more", "", "", "", "", "", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %q, want %q", got, want)
	}
}

func TestSnapshotTextListsEveryLiteralGroupWithAll(t *testing.T) {
	t.Parallel()
	list := &literalGroupList{Total: 7, Groups: make([]rules.LiteralGroup, 7)}
	for i := range list.Groups {
		list.Groups[i] = rules.LiteralGroup{
			Value: "value-" + strconv.Itoa(i), Files: 3, Sites: []rules.LiteralSite{{Path: "a.go", Line: i + 1}},
		}
	}
	var top, every strings.Builder
	printLiteralGroups(&top, list, false)
	printLiteralGroups(&every, list, true)
	if got := top.String(); !strings.Contains(got, "literal groups, 5 of 7") || strings.Contains(got, `"value-5"`) {
		t.Errorf("without --all the table does not stop at five groups:\n%s", got)
	}
	if got := every.String(); !strings.Contains(got, "literal groups, 7 of 7") || !strings.Contains(got, `"value-6"`) {
		t.Errorf("with --all the table does not list all seven groups:\n%s", got)
	}
}

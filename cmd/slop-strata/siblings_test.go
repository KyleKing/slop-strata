package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/fixturetest"
	"github.com/kyleking/slop-strata/internal/rules"
)

const ruleNewSibling = "x/new-sibling"

// tableStore writes a Python class of two methods over one table: NAME is
// the class and TABLE the table.
func tableStore(name, table string) string {
	return strings.NewReplacer("NAME", name, "TABLE", table).Replace(tableStoreSource)
}

const tableStoreSource = `class NAME(Base):
    def load(self, key):
        rows = self.db.query("select * from TABLE where id = ?", key)
        if not rows:
            raise KeyError(key)
        result = [self.convert(row) for row in rows]
        self.cache[key] = result
        return result

    def save(self, key, value):
        payload = self.encode(value)
        if payload is None:
            raise ValueError(value)
        self.db.execute("update TABLE set body = ? where id = ?", payload, key)
        self.cache[key] = value
        return payload
`

// newSiblingCase is a change that adds a second class written like one the
// tree already holds, over another table.
func newSiblingCase() fixturetest.Case {
	orders := func(body string) fixturetest.Tree { return fixturetest.Tree{"store/orders.py": body} }
	whole := tableStore("OrderStore", "orders")

	return fixturetest.Case{
		Name: "a change adds a second class with the same methods",
		Base: fixturetest.Tree{
			before + "store/users.py": tableStore("UserStore", "users"),
			added + "store/orders.py": whole,
		},
		Dodges: []fixturetest.Variant{
			adds("read another table", orders(tableStore("OrderStore", "purchases"))),
			adds("rename a local", orders(strings.ReplaceAll(whole, "payload", "encoded"))),
			adds("add a comment above the class", orders("# OrderStore keeps the orders.\n"+whole)),
			adds("add a statement to one method", orders(strings.Replace(whole,
				"        return result\n", "        self.audit(key)\n        return result\n", 1))),
			adds("add a method of its own", orders(whole+"\n    def purge(self, key):\n"+
				"        self.db.execute(\"delete from orders where id = ?\", key)\n"+
				"        self.cache.pop(key, None)\n")),
		},
		Persists: []fixturetest.Variant{
			adds("give the class another name", orders(tableStore("OrderRepo", "orders"))),
			movesTo("store/orders.py", "store/purchases.py", whole),
		},
		Gaps: []fixturetest.Variant{
			adds("rename one of the two methods", orders(strings.Replace(whole, "def save", "def store", 1))),
			adds("write the methods as functions of a module", orders(strings.NewReplacer(
				"class OrderStore(Base):\n", "", "\n    ", "\n", "    def load", "def load").Replace(whole))),
		},
		Fixes: []fixturetest.Variant{
			adds("inherit the methods from a shared base",
				orders("class OrderStore(TableStore):\n    table = \"orders\"\n")),
		},
	}
}

func TestNewSiblingHoldsUnderAdversarialEdits(t *testing.T) {
	t.Parallel()
	fixturetest.Run(t, []fixturetest.Case{newSiblingCase()}, changeRuleIDs(t, ruleNewSibling, "--experimental"))
}

func TestNewSiblingStaysQuiet(t *testing.T) {
	t.Parallel()
	users, orders := tableStore("UserStore", "users"), tableStore("OrderStore", "orders")
	half := orders[:strings.Index(orders, "\n    def save")+1]
	cases := []quietCase{
		{name: "both classes are new", tree: fixturetest.Tree{
			before + "store/base.py": "class Base:\n    pass\n",
			added + "store/users.py": users, added + "store/orders.py": orders,
		}},
		{name: "a class moves to another file", tree: fixturetest.Tree{
			before + "store/users.py": users, before + "store/orders.py": orders,
			added + "store/orders.py": removed, added + "store/purchases.py": orders,
		}},
		{name: "a class is renamed where it stands", tree: fixturetest.Tree{
			before + "store/users.py": users, before + "store/orders.py": orders,
			added + "store/orders.py": tableStore("OrderRepo", "orders"),
		}},
		{name: "a class that was there gains the second method", tree: fixturetest.Tree{
			before + "store/users.py": users, before + "store/orders.py": half,
			added + "store/orders.py": orders,
		}},
		{name: "a method of a sibling that was there is edited", tree: fixturetest.Tree{
			before + "store/users.py": users, before + "store/orders.py": orders,
			added + "store/orders.py": strings.Replace(orders, "raise KeyError(key)", "raise LookupError(key)", 1),
		}},
		{name: "the copy is added to a test", tree: fixturetest.Tree{
			before + "store/users.py": users, added + "store/test_orders.py": orders,
		}},
		{name: "the copy is added to an example", tree: fixturetest.Tree{
			before + "store/users.py": users, added + "examples/orders.py": orders,
		}},
		{name: "the copy is added to a migration", tree: fixturetest.Tree{
			before + "store/users.py": users, added + "migrations/orders.py": orders,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, f := range changeFindings(t, historyRepo(t, tc.tree), "--experimental") {
				if f.Rule == ruleNewSibling {
					t.Errorf("reported %s:%d: %s", f.Path, f.Line, f.Note)
				}
			}
		})
	}
}

// TestNewSiblingReportsASetOnce pins one finding per set: a change that adds
// two siblings of a class that was there reports the first and names the
// second.
func TestNewSiblingReportsASetOnce(t *testing.T) {
	t.Parallel()
	repo := historyRepo(t, fixturetest.Tree{
		before + "store/users.py": tableStore("UserStore", "users"),
		added + "store/orders.py": tableStore("OrderStore", "orders"),
		added + "store/teams.py":  tableStore("TeamStore", "teams"),
	})
	var got []findingRow
	for _, f := range changeFindings(t, repo, "--experimental") {
		if f.Rule == ruleNewSibling {
			got = append(got, f)
		}
	}
	if len(got) != 1 {
		t.Fatalf("findings = %+v, want one for the set", got)
	}
	f := got[0]
	const second = "The change adds 2 of them, here and at TeamStore at store/teams.py:1"
	if f.Path != "store/orders.py" || f.Line != 1 || f.Match != "store/users.py UserStore" ||
		!strings.Contains(f.Note, second) {
		t.Errorf("finding = %+v, want it on store/orders.py:1, matching store/users.py UserStore, and naming %q",
			f, second)
	}
}

// TestNewSiblingIsReadable pins what a person and an agent see of a finding:
// the note under the findings table, and the same note as the reason of a
// next line, so --brief names the sibling that was already there.
func TestNewSiblingIsReadable(t *testing.T) {
	t.Parallel()
	repo := historyRepo(t, newSiblingCase().Base)
	const want = "class OrderStore is written like UserStore at store/users.py:1: 2 members of one name " +
		"(load, save) share 12 statements in line. Extend what is there with a shared base, a generic, " +
		"or a table of what differs, or say why the copies stay apart"
	for _, flags := range [][]string{{"--experimental"}, {"--experimental", "--brief"}} {
		out, err := runCLI(t, repo, append([]string{"change", "--base", "HEAD~1", "--rev", "HEAD"}, flags...)...)
		if err != nil {
			t.Fatalf("change %v: %v", flags, err)
		}
		if !strings.Contains(out, want) {
			t.Errorf("change %v does not name the sibling that was there:\n%s", flags, out)
		}
	}
	plain, err := runCLI(t, repo, "change", "--base", "HEAD~1", "--rev", "HEAD")
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if strings.Contains(plain, ruleNewSibling) {
		t.Errorf("the rule reported without --experimental:\n%s", plain)
	}
}

// TestNewSiblingTakesAnInlineIgnore pins that a reasoned ignore above the
// class sets the finding aside.
func TestNewSiblingTakesAnInlineIgnore(t *testing.T) {
	t.Parallel()
	tree := newSiblingCase().Base
	tree[added+"store/orders.py"] = "# slop-strata:ignore x/new-sibling orders move to another service next quarter\n" +
		tableStore("OrderStore", "orders")
	for _, f := range changeFindings(t, historyRepo(t, tree), "--experimental") {
		if f.Rule == ruleNewSibling {
			t.Errorf("an ignored finding was reported: %+v", f)
		}
	}
}

// siblingRepo holds one store in three classes, a copy of it in a test and
// in an example, and two Go types written alike.
func siblingRepo(t *testing.T) string {
	t.Helper()
	const goType = "package repo\n\ntype NAME struct{ db *DB }\n\n" +
		"func (s *NAME) Load(key string) ([]Row, error) {\n" +
		"\trows, err := s.db.Query(\"select * from TABLE where id = ?\", key)\n" +
		"\tif err != nil {\n\t\treturn nil, fmt.Errorf(\"loading: %w\", err)\n\t}\n" +
		"\tout := make([]Row, 0, len(rows))\n" +
		"\tfor _, row := range rows {\n\t\tout = append(out, convert(row))\n\t}\n\n" +
		"\treturn out, nil\n}\n\n" +
		"func (s *NAME) Save(key string, value Row) error {\n\tpayload, err := encode(value)\n" +
		"\tif err != nil {\n\t\treturn fmt.Errorf(\"encoding: %w\", err)\n\t}\n" +
		"\tif err := s.db.Exec(\"update TABLE set body = ? where id = ?\", payload, key); err != nil {\n" +
		"\t\treturn fmt.Errorf(\"saving: %w\", err)\n\t}\n\ts.cache[key] = value\n\n\treturn nil\n}\n"
	goFile := func(name, table string) string {
		return strings.NewReplacer("NAME", name, "TABLE", table).Replace(goType)
	}
	files := map[string]string{
		"store/users.py": tableStore("UserStore", "users"), "store/orders.py": tableStore("OrderStore", "orders"),
		"store/teams.py":      tableStore("TeamStore", "teams"),
		"store/test_roles.py": tableStore("RoleStore", "roles"),
		"examples/roles.py":   tableStore("RoleStore", "roles"),
		"repo/users.go":       goFile("Users", "users"), "repo/orders.go": goFile("Orders", "orders"),
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

// snapshotSiblingSets reads the sibling sets of a snapshot.
func snapshotSiblingSets(t *testing.T, repo string, argv ...string) *siblingSetList {
	t.Helper()
	var list *siblingSetList
	snapshotField(t, repo, "sibling_sets", &list, argv...)

	return list
}

func TestSnapshotListsSiblingSetsWhenExperimental(t *testing.T) {
	t.Parallel()
	repo := siblingRepo(t)
	if got := snapshotSiblingSets(t, repo); got != nil {
		t.Errorf("sibling_sets = %+v without --experimental, want none", got)
	}
	got := snapshotSiblingSets(t, repo, "--experimental")
	if got == nil || got.Total != 2 || len(got.Sets) != 2 {
		t.Fatalf("sibling_sets = %+v, want the three classes and the two types", got)
	}
	// Three classes whose two methods line up whole: the tokens of the 12
	// statements, times the two copies a shared base would take out. The
	// test's and the example's copies are left out.
	classes := got.Sets[0]
	want := rules.SiblingSet{
		Kind: rules.SiblingClass, Members: []string{"load", "save"}, Statements: 12,
		Tokens: classes.Tokens, Rank: 2 * classes.Tokens,
		Sites: []rules.SiblingSite{
			{Path: "store/orders.py", Name: "OrderStore", Line: 1, EndLine: 16},
			{Path: "store/teams.py", Name: "TeamStore", Line: 1, EndLine: 16},
			{Path: "store/users.py", Name: "UserStore", Line: 1, EndLine: 16},
		},
	}
	if !reflect.DeepEqual(classes, want) || classes.Tokens < 60 {
		t.Errorf("first set = %+v, want %+v", classes, want)
	}
	types := got.Sets[1]
	if types.Kind != rules.SiblingType || len(types.Sites) != 2 || types.Sites[0].Name != "Orders" ||
		types.Rank != types.Tokens || types.Rank > classes.Rank {
		t.Errorf("second set = %+v, want the two Go types, ranked by their tokens once", types)
	}
	if again := snapshotSiblingSets(t, repo, "--experimental"); !reflect.DeepEqual(again, got) {
		t.Errorf("a second run lists %+v, the first %+v", again, got)
	}
}

func TestSnapshotTextPrintsSiblingSets(t *testing.T) {
	t.Parallel()
	repo := siblingRepo(t)
	plain, err := runCLI(t, repo, "snapshot")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if strings.Contains(plain, "sibling sets") {
		t.Errorf("snapshot text lists sibling sets without --experimental:\n%s", plain)
	}
	gated, err := runCLI(t, repo, "snapshot", "--experimental")
	if err != nil {
		t.Fatalf("snapshot --experimental: %v", err)
	}
	for _, want := range []string{
		"sibling sets, 2 of 2 (rank: tokens in line × (sites − 1), per shared member)",
		"store/orders.py:1  OrderStore",
		"store/teams.py:1   TeamStore",
		"store/users.py:1   UserStore",
		"repo/orders.go:3   Orders",
	} {
		if !strings.Contains(gated, want) {
			t.Errorf("snapshot --experimental text lacks %q:\n%s", want, gated)
		}
	}
	if strings.Index(gated, "sibling sets") < strings.Index(gated, "top functions") {
		t.Errorf("the sibling sets are printed before the functions:\n%s", gated)
	}
	empty, err := runCLI(t, deterministicRepo(t), "snapshot", "--experimental")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if strings.Contains(empty, "sibling sets") {
		t.Errorf("snapshot text prints an empty list:\n%s", empty)
	}
}

func TestSiblingSetRowsListThreeSitesAndCountTheRest(t *testing.T) {
	t.Parallel()
	s := rules.SiblingSet{
		Kind: rules.SiblingClass, Members: []string{"load", "save", "purge"},
		Statements: 14, Tokens: 120, Differing: 3, Rank: 480,
		Sites: []rules.SiblingSite{
			{Path: "a.py", Name: "One", Line: 3},
			{Path: "b.py", Name: "AClassWithAVeryLongNameIndeed", Line: 4},
			{Path: "c.py", Name: "Three", Line: 5},
			{Path: "d.py", Name: "Four", Line: 6},
			{Path: "e.py", Name: "Five", Line: 7},
		},
	}
	got := siblingSetRows(&s)
	want := [][]string{
		{"a.py:3", "One", "480", "5", "3: load, save, purge", "14", "3"},
		{"b.py:4", "AClassWithAVeryLongName…", "", "", "", "", ""},
		{"c.py:5", "Three", "", "", "", "", ""},
		{"… and 2 more", "", "", "", "", "", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %q, want %q", got, want)
	}
}

func TestSnapshotTextListsEverySiblingSetWithAll(t *testing.T) {
	t.Parallel()
	list := &siblingSetList{Total: 7}
	for i := range 7 {
		list.Sets = append(list.Sets, rules.SiblingSet{
			Kind: rules.SiblingModule, Members: []string{"load", "save"},
			Sites: []rules.SiblingSite{{Path: "a.py", Line: i + 1}, {Path: "b.py", Line: 1}},
		})
	}
	for all, want := range map[bool]string{false: "sibling sets, 5 of 7", true: "sibling sets, 7 of 7"} {
		var out strings.Builder
		printSiblingSets(&out, list, all)
		if !strings.Contains(out.String(), want) {
			t.Errorf("with all=%v the table lacks %q:\n%s", all, want, out.String())
		}
	}
}

// TestSortFindingsOrdersTwoHitsOfOneLineByID pins that a change prints the
// same rows however the passes handed them over: two hits of one rule on one
// line differ in their id alone.
func TestSortFindingsOrdersTwoHitsOfOneLineByID(t *testing.T) {
	t.Parallel()
	a := findingRow{Path: "a.go", Line: 4, Rule: "dup-literal", Match: `"x"`, ID: "1a"}
	b := findingRow{Path: "a.go", Line: 4, Rule: "dup-literal", Match: `"x"`, ID: "7c"}
	c := findingRow{Path: "a.go", Line: 4, Rule: "dup-literal", Match: `"x"`, Note: "n", ID: "00"}
	want := []findingRow{a, b, c}
	for _, order := range [][]findingRow{{a, b, c}, {c, b, a}, {b, c, a}} {
		sortFindings(order)
		if !reflect.DeepEqual(order, want) {
			t.Errorf("sorted = %+v, want %+v", order, want)
		}
	}
}

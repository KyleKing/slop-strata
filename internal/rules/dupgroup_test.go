package rules_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/rules"
)

// paramCost is what one parameter takes off a group's removable tokens.
const paramCost = 8

// duplicateGroups fingerprints a tree and lists its groups.
func duplicateGroups(t *testing.T, tree map[string]string) []rules.DupGroup {
	t.Helper()
	prints := map[string]*rules.Fingerprint{}
	for path, src := range tree {
		prints[path] = printOf(t, path, src)
	}

	return rules.DuplicateGroups(prints)
}

// sitePaths lists a group's sites as path and unit.
func sitePaths(g *rules.DupGroup) string {
	names := make([]string, 0, len(g.Sites))
	for _, s := range g.Sites {
		names = append(names, s.Path+" "+s.Unit)
	}

	return strings.Join(names, ", ")
}

func TestDuplicateGroupsListACopyInEachLanguage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, tmpl, ext string
	}{
		{"go", goTally, ".go"}, {"python", pyTally, ".py"}, {"typescript", tsTally, ".ts"}, {"tsx", tsTally, ".tsx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := duplicateGroups(t, map[string]string{
				"store/users" + tt.ext:  tallySrc(tt.tmpl, "parseUsers", "users"),
				"store/orders" + tt.ext: tallySrc(tt.tmpl, "parseOrders", "orders"),
				"store/items" + tt.ext:  tallySrc(tt.tmpl, "parseItems", "items"),
			})
			if len(got) != 1 {
				t.Fatalf("got %d groups, want one: %+v", len(got), got)
			}
			g := &got[0]
			wantSites := strings.ReplaceAll(
				"store/items.x parseItems, store/orders.x parseOrders, store/users.x parseUsers", ".x", tt.ext)
			if sitePaths(g) != wantSites {
				t.Errorf("sites = %s, want %s", sitePaths(g), wantSites)
			}
			// The table name is the one thing the three sites spell differently:
			// the function names sit in signatures, which count nothing.
			if tables := tableNames(g); tables != "items, orders, users" {
				t.Errorf("parameters = %q, want the table name per site, got %s", g.Parameters, tables)
			}
			checkWholeBody(t, g)
		})
	}
}

// tableNames reads the table each site of a group names, when the table's
// literal is the group's one parameter.
func tableNames(g *rules.DupGroup) string {
	if len(g.Parameters) != 1 {
		return ""
	}
	names := make([]string, 0, len(g.Parameters[0]))
	for _, text := range g.Parameters[0] {
		for _, table := range []string{"items", "orders", "users"} {
			if strings.Contains(text, table) {
				names = append(names, table)
			}
		}
	}

	return strings.Join(names, ", ")
}

// checkWholeBody checks the numbers of a group of three function bodies with
// one parameter.
func checkWholeBody(t *testing.T, g *rules.DupGroup) {
	t.Helper()
	if g.Expression || g.Statements < 3 || g.Tokens < 40 {
		t.Errorf("group = %+v, want the statements of a function body", g)
	}
	if want := g.Tokens*2 - paramCost; g.Removable != want {
		t.Errorf("removable = %d, want tokens x (sites - 1) - %d = %d", g.Removable, paramCost, want)
	}
	if g.Sites[0].Line < 1 || g.Sites[0].LastLine <= g.Sites[0].Line {
		t.Errorf("site = %+v, want a first and a later last line", g.Sites[0])
	}
}

func TestDuplicateGroupsListARepeatedExpression(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, total, quote, price, ext string
	}{
		{"go", goTotal, goQuote, goPrice, ".go"},
		{"python", pyTotal, pyQuote, pyPrice, ".py"},
		{"typescript", tsTotal, tsQuote, tsPrice, ".ts"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := duplicateGroups(t, map[string]string{
				"bill/total" + tt.ext: withExpr(tt.total, tt.price),
				"bill/quote" + tt.ext: withExpr(tt.quote, tt.price),
			})
			if len(got) != 1 {
				t.Fatalf("got %d groups, want one: %+v", len(got), got)
			}
			g := &got[0]
			want := rules.DupGroup{
				Tokens: 36, Removable: 36, Expression: true, Parameters: [][]string{}, Sites: g.Sites,
			}
			if fmt.Sprint(*g) != fmt.Sprint(want) {
				t.Errorf("group = %+v, want %+v", *g, want)
			}
			wantSites := fmt.Sprintf("bill/quote%[1]s quote, bill/total%[1]s total", tt.ext)
			if sitePaths(g) != wantSites {
				t.Errorf("sites = %s, want %s", sitePaths(g), wantSites)
			}
		})
	}
}

// goLedger is a function whose first half one other file repeats and whose
// whole body a third does.
func goLedger(name string, withTail bool) string {
	body := "\tbalance := opening(acct.ID, period.Start)\n" +
		"\tfor _, entry := range journal.Between(period.Start, period.End) {\n" +
		"\t\tbalance = balance.Add(entry.Debit).Sub(entry.Credit)\n" +
		"\t}\n" +
		"\tif balance.IsNegative() && !acct.Overdraft {\n" +
		"\t\treturn zero, fmt.Errorf(\"account %s overdrawn in %s\", acct.ID, period.Name)\n" +
		"\t}\n"
	if withTail {
		body += "\trounded := balance.Round(acct.Currency.Decimals)\n" +
			"\taudit.Record(acct.ID, period.Name, rounded.String(), clock.Now().UTC())\n" +
			"\tcache.Store(acct.ID+period.Name, rounded, cacheTTL)\n"
	}

	return "package ledger\n\nfunc " + name + "(acct Account, period Period) (Money, error) {\n" + body +
		"\n\treturn balance, nil\n}\n"
}

func TestDuplicateGroupsPutNoStatementInTwoGroups(t *testing.T) {
	t.Parallel()
	got := duplicateGroups(t, map[string]string{
		"ledger/close.go": goLedger("closePeriod", true), "ledger/reopen.go": goLedger("reopenPeriod", true),
		"ledger/peek.go": goLedger("peekBalance", false),
	})
	// The two whole copies pair first and claim their statements. The third
	// file shares only their first half, which is already held.
	if len(got) != 1 || sitePaths(&got[0]) != "ledger/close.go closePeriod, ledger/reopen.go reopenPeriod" {
		t.Fatalf("got %+v, want one group of the two whole copies", got)
	}
}

func TestDuplicateGroupsRankByRemovableTokens(t *testing.T) {
	t.Parallel()
	got := duplicateGroups(t, map[string]string{
		"store/users.go":  tallySrc(goTally, "parseUsers", "users"),
		"store/orders.go": tallySrc(goTally, "parseOrders", "orders"),
		"ledger/close.go": goLedger("closePeriod", true), "ledger/reopen.go": goLedger("reopenPeriod", true),
		"ledger/audit.go": goLedger("auditPeriod", true),
	})
	if len(got) != 1+1 {
		t.Fatalf("got %d groups, want two: %+v", len(got), got)
	}
	if len(got[0].Sites) != 3 || len(got[1].Sites) != 2 || got[0].Removable <= got[1].Removable {
		t.Errorf("got %+v, want the three ledger copies ahead of the two tallies", got)
	}
}

func TestDuplicateGroupsKeepAGroupWithinItsParameterBudget(t *testing.T) {
	t.Parallel()
	rename := func(name string, pairs ...string) string {
		return strings.NewReplacer(pairs...).Replace(tallySrc(goTally, name, "users"))
	}
	tree := map[string]string{
		"store/users.go":  tallySrc(goTally, "parseUsers", "users"),
		"store/orders.go": tallySrc(goTally, "parseOrders", "orders"),
		"store/items.go":  rename("parseItems", "key", "name", "ok", "found"),
		"store/notes.go":  rename("parseNotes", "counts", "totals", "err", "fail"),
		"store/wide.go":   rename("parseWide", "counts", "sums", "key", "id", "ok", "has", "err", "bad"),
	}
	got := duplicateGroups(t, tree)
	// Orders differs from users in one name and items in two more, which is
	// the budget of three. Notes would add two and stays out, though it is
	// within the budget against users alone. Wide renames four things, which
	// is over the budget against any one of those. The two left over then
	// pair with each other on the stretch they share within three names.
	want := "store/items.go parseItems, store/orders.go parseOrders, store/users.go parseUsers"
	if len(got) != 1+1 || sitePaths(&got[0]) != want {
		t.Fatalf("got %+v, want two groups, the first of %s", got, want)
	}
	if len(got[0].Parameters) != 3 || len(got[1].Parameters) != 3 {
		t.Errorf("parameters = %q and %q, want three each", got[0].Parameters, got[1].Parameters)
	}
	if left := "store/notes.go parseNotes, store/wide.go parseWide"; sitePaths(&got[1]) != left {
		t.Errorf("second group = %s, want %s", sitePaths(&got[1]), left)
	}
}

// goRates is a table of values two files hold alike: data, not code.
const goRates = `package rates

var NAME = [][]int{
	{1, 20, 300, 4000, 50000}, {2, 21, 301, 4001, 50001}, {3, 22, 302, 4002, 50002},
	{4, 23, 303, 4003, 50003}, {5, 24, 304, 4004, 50004}, {6, 25, 305, 4005, 50005},
	{7, 26, 306, 4006, 50006}, {8, 27, 307, 4007, 50007}, {9, 28, 308, 4008, 50008},
}
`

func TestDuplicateGroupsStayQuiet(t *testing.T) {
	t.Parallel()
	users := tallySrc(goTally, "parseUsers", "users")
	orders := tallySrc(goTally, "parseOrders", "orders")
	idiom := map[string]string{}
	for i := range 40 {
		idiom[fmt.Sprintf("store/t%02d.go", i)] = tallySrc(goTally, fmt.Sprintf("parse%02d", i), "rows")
	}
	tests := []struct {
		tree map[string]string
		name string
	}{
		{name: "one site", tree: map[string]string{"store/users.go": users}},
		{name: "two tests", tree: map[string]string{"store/users_test.go": users, "store/orders_test.go": orders}},
		{
			name: "a test and production code",
			tree: map[string]string{"store/users.go": users, "store/orders_test.go": orders},
		},
		{name: "two examples", tree: map[string]string{"examples/a/main.go": users, "examples/b/main.go": orders}},
		{
			name: "an example and production code",
			tree: map[string]string{"store/users.go": users, "demo/main.go": orders},
		},
		{
			name: "two scaffolding templates",
			tree: map[string]string{"create/template-react/app.go": users, "create/template-vue/app.go": orders},
		},
		{
			name: "two codemod fixtures",
			tree: map[string]string{
				"mod/__testfixtures__/a.input.go": users, "mod/__testfixtures__/a.output.go": users,
			},
		},
		{
			name: "a tree kept in step",
			tree: map[string]string{"src/store/parse.go": users, "mirror/store/parse.go": users},
		},
		{
			name: "one file written out under two extensions",
			tree: map[string]string{
				"src/index.mts": tallySrc(tsTally, "parseUsers", "users"),
				"src/index.cts": tallySrc(tsTally, "parseUsers", "users"),
			},
		},
		{name: "a shape the tree holds in forty places", tree: idiom},
		{
			name: "two arms of one switch, back to back",
			tree: map[string]string{"conv/widen.go": goArms},
		},
		{
			name: "two tables of values",
			tree: map[string]string{
				"rates/eu.go": strings.Replace(goRates, "NAME", "EU", 1),
				"rates/us.go": strings.Replace(goRates, "NAME", "US", 1),
			},
		},
		{
			name: "two overloads that share nothing but signatures",
			tree: map[string]string{
				"emit/a.ts": strings.ReplaceAll(tsOverloads, "NAME", "emitA") + tsBodyA,
				"emit/b.ts": strings.ReplaceAll(tsOverloads, "NAME", "emitB") + tsBodyB,
			},
		},
		{
			name: "a pair under the floor",
			tree: map[string]string{
				"a/a.go": "package a\n\nfunc first(x int) int {\n\ty := x + 1\n\n\treturn y\n}\n",
				"b/b.go": "package b\n\nfunc second(x int) int {\n\ty := x + 1\n\n\treturn y\n}\n",
			},
		},
		{
			name: "an expression twice in one function",
			tree: map[string]string{
				"bill/both.go": "package bill\n\nfunc both(o *Order, cfg *Config) (float64, bool) {\n" +
					"\tfirst := " + goPrice + "\n\tlog.Print(first)\n\n" +
					"\treturn first, " + goPrice + " > cfg.Floor\n}\n",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := duplicateGroups(t, tt.tree); len(got) != 0 {
				t.Errorf("got %+v, want no group", got)
			}
		})
	}
}

func TestAsideNamesTheFilesThatStandAlone(t *testing.T) {
	t.Parallel()
	tests := map[string]bool{
		"store/users.go": false, "store/users_test.go": true, "examples/a/main.go": true, "pkg/bench/run.py": true,
		"create/template-vue/src/app.ts": true, "create/templates/app.ts": true, "src/templater/render.go": false,
		"mod/__testfixtures__/a.input.ts": true, "tests/test_app.py": true,
	}
	for path, want := range tests {
		if got := rules.Aside(path); got != want {
			t.Errorf("Aside(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestDuplicateGroupsTolerateATruncatedPayload cuts a cached fingerprint at
// every byte: the pass reads what is whole and never panics.
func TestDuplicateGroupsTolerateATruncatedPayload(t *testing.T) {
	t.Parallel()
	sources := map[string]string{
		"store/users.go": tallySrc(goTally, "parseUsers", "users"), "bill/total.go": withExpr(goTotal, goPrice),
	}
	others := map[string]*rules.Fingerprint{
		"store/orders.go": printOf(t, "store/orders.go", tallySrc(goTally, "parseOrders", "orders")),
		"bill/quote.go":   printOf(t, "bill/quote.go", withExpr(goQuote, goPrice)),
	}
	for path, src := range sources {
		full := printOf(t, path, src)
		for cut := range len(full.Frags) {
			broken := roundTrip(t, full)
			broken.Frags = broken.Frags[:cut]
			tree := map[string]*rules.Fingerprint{path: broken}
			for name, fp := range others {
				tree[name] = fp
			}
			rules.DuplicateGroups(tree)
		}
	}
}

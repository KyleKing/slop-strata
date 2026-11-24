package rules_test

import (
	"strings"
	"testing"
)

// A function of five statements, none long enough to be reported alone. EXTRA
// lines stand between them in a copy, so no two of its statements line up in
// a row with the first function's.
const goSync = `func NAME(db *DB, api *Client, since time.Time) error {
	rows := api.Fetch("TABLE", db.Cursor(since))EXTRA1
	stale := db.Stale("TABLE", since.Add(-window))EXTRA2
	db.Upsert(rows, conflictPolicy(api.Version))EXTRA3
	db.Delete(stale, auditTrail(api.Actor))EXTRA4

	return db.Commit(len(rows), len(stale))
}
`

// goSyncFn fills the function in: a name, a table, and the lines to put
// after each of its first four statements.
func goSyncFn(name, table string, extra ...string) string {
	const gaps = 4
	pairs := make([]string, 0, 2*(gaps+1+1))
	pairs = append(pairs, "NAME", name, "TABLE", table)
	for i := range gaps {
		line := ""
		if i < len(extra) && extra[i] != "" {
			line = "\n\t" + extra[i]
		}
		pairs = append(pairs, "EXTRA"+string(rune('1'+i)), line)
	}

	return strings.NewReplacer(pairs...).Replace(goSync)
}

const goSyncHead = "package feed\n\n"

// goSpread are the lines a copy puts between the statements it took.
func goSpread() []string {
	const flush = "db.Flush()"

	return []string{`metrics.Count("fetched", len(rows))`, `metrics.Count("stale", len(stale))`, flush, flush}
}

func TestNewDuplicatesJoinsStretchesOfOneCopy(t *testing.T) {
	t.Parallel()
	users := goSyncHead + goSyncFn("syncUsers", "users")
	apart := goSyncFn("syncOrders", "orders", goSpread()...)
	const upsert = "\tdb.Upsert(rows, conflictPolicy(api.Version))\n"
	const drop = "\tdb.Delete(stale, auditTrail(api.Actor))\n"
	swapped := strings.NewReplacer(upsert, drop, drop, upsert).Replace(apart)
	tests := []struct {
		name, added string
	}{
		{name: "a statement added after each one", added: goSyncHead + apart},
		{name: "and two of them swapped", added: goSyncHead + swapped},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := newDuplicates(t, map[string]string{"feed/users.go": users},
				[]dupChange{{path: "feed/orders.go", after: tt.added}}, true)
			fs := got["feed/orders.go"]
			const want = "repeats syncUsers at feed/users.go:3, in the same directory: 6 statements in 5 stretches, " +
				`91 tokens, 1 difference ("users"→"orders"). Extend or parameterise that code instead of adding a copy`
			if len(fs) != 1 || fs[0].Note != want {
				t.Fatalf("got %+v, want one finding with the note %q", got, want)
			}
		})
	}
}

// TestNewDuplicatesJoinsAgainstOneFunction pins that stretches two functions
// of the tree both hold are reported once, with the second as one more site.
func TestNewDuplicatesJoinsAgainstOneFunction(t *testing.T) {
	t.Parallel()
	tree := map[string]string{
		"feed/users.go": goSyncHead + goSyncFn("syncUsers", "users"),
		"feed/teams.go": goSyncHead + goSyncFn("syncTeams", "teams"),
	}
	added := goSyncHead + goSyncFn("syncOrders", "orders", goSpread()...)
	got := newDuplicates(t, tree, []dupChange{{path: "feed/orders.go", after: added}}, true)
	fs := got["feed/orders.go"]
	const want = "repeats syncTeams at feed/teams.go:3, in the same directory, and syncUsers at feed/users.go:3: "
	if len(fs) != 1 || !strings.HasPrefix(fs[0].Note, want) {
		t.Errorf("got %+v, want one finding that starts %q", got, want)
	}
}

// TestNewDuplicatesNamesACopyOnce pins that the stretches around a run long
// enough to be reported alone are not reported beside it.
func TestNewDuplicatesNamesACopyOnce(t *testing.T) {
	t.Parallel()
	users := goSyncHead + goSyncFn("syncUsers", "users")
	added := goSyncHead + goSyncFn("syncOrders", "orders", "", "", "db.Flush()", "db.Flush()")
	got := newDuplicates(t, map[string]string{"feed/users.go": users},
		[]dupChange{{path: "feed/orders.go", after: added}}, true)
	fs := got["feed/orders.go"]
	if len(fs) != 1 || !strings.Contains(fs[0].Note, ": 4 statements, 63 tokens, 1 difference") {
		t.Errorf("got %+v, want one finding, for the run of four statements", got)
	}
}

func TestNewDuplicatesDoesNotJoin(t *testing.T) {
	t.Parallel()
	users := goSyncHead + goSyncFn("syncUsers", "users")
	arm := func(table string, extra ...string) string {
		body := goSyncFn("x", table, extra...)
		body = body[strings.Index(body, "\n")+1 : strings.LastIndex(body, "\n\n\treturn")]

		return strings.ReplaceAll(body, "\n\t", "\n\t\t")
	}
	tests := []struct {
		name  string
		tree  map[string]string
		added string
	}{
		{
			name: "stretches that each differ in more names than they are worth",
			tree: map[string]string{"feed/users.go": users},
			added: goSyncHead + strings.NewReplacer(
				"Fetch", "Pull", "window", "horizon", "conflictPolicy", "mergePolicy", "auditTrail", "ledger",
				"Commit", "Seal", "Upsert", "Put", "Delete", "Drop", "error {", "(int, error) {",
			).Replace(goSyncFn("syncOrders", "orders", goSpread()...)),
		},
		{
			name: "one short stretch, which is not a copy",
			tree: map[string]string{"feed/users.go": users},
			added: goSyncHead + "func purge(db *DB, api *Client, since time.Time) {\n" +
				"\tstale := db.Stale(\"users\", since.Add(-window))\n\tdb.Drop(stale)\n}\n",
		},
		{
			name: "two arms of one function",
			added: goSyncHead + "func syncBoth(db *DB, api *Client, since time.Time, full bool) {\n\tif full {\n\t" +
				arm("users") + "\n\t} else {\n\t" + arm("users", goSpread()...) + "\n\t}\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := newDuplicates(t, tt.tree, []dupChange{{path: "feed/orders.go", after: tt.added}}, true)
			if len(got) != 0 {
				t.Errorf("got %+v, want no finding", got)
			}
		})
	}
}

// TestNewDuplicatesReportsACopyOnce pins the grouping: a change that adds the
// same copy three times says so in one finding, on the first of them.
func TestNewDuplicatesReportsACopyOnce(t *testing.T) {
	t.Parallel()
	tree := map[string]string{"store/users.go": tallySrc(goTally, "parseUsers", "users")}
	got := newDuplicates(t, tree, []dupChange{
		{path: "store/a.go", after: tallySrc(goTally, "parseA", "users")},
		{path: "store/b.go", after: tallySrc(goTally, "parseB", "users")},
		{path: "store/c.go", after: tallySrc(goTally, "parseC", "users")},
	}, true)
	fs := got["store/a.go"]
	const want = ". The change adds this copy 3 times, here and at store/b.go:3, store/c.go:3"
	if len(got) != 1 || len(fs) != 1 || !strings.HasSuffix(fs[0].Note, want) {
		t.Errorf("got %+v, want one finding on store/a.go that ends %q", got, want)
	}
}

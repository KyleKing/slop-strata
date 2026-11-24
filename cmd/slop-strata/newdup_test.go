package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/fixturetest"
)

const ruleNewDuplicate = "x/new-duplicate"

// added is the commit a new-duplicate fixture scores: the change that adds a
// copy of code the first commit already holds.
const added = "1.1 feat: list orders/"

// adds is a variant that replaces or adds files of the scored change.
func adds(name string, files fixturetest.Tree) fixturetest.Variant {
	out := fixturetest.Tree{}
	for file, body := range files {
		out[added+file] = body
	}

	return fixturetest.Variant{Name: name, Files: out}
}

// movesTo is a variant whose change writes the copy to another file.
func movesTo(from, to, body string) fixturetest.Variant {
	return fixturetest.Variant{
		Name: "put the copy in another file", Remove: []string{added + from},
		Files: fixturetest.Tree{added + to: body},
	}
}

// goLoader is a Go function that reads rows of one table. Each argument
// rewrites one part of it, so a variant says only what it changes.
func goLoader(name, table, rows, capacity, comment string) string {
	body := `func NAME(db *DB, limit int) ([]row, error) {
	ROWS, err := db.Query("SELECT id, name FROM TABLE LIMIT ?", limit)
	if err != nil {
		return nil, fmt.Errorf("querying: %w", err)
	}
	defer ROWS.Close()COMMENT
	out := make([]row, CAP, limit)
	for ROWS.Next() {
		var r row
		if err := ROWS.Scan(&r.id, &r.name); err != nil {
			return nil, fmt.Errorf("scanning: %w", err)
		}
		out = append(out, r)
	}

	return out, ROWS.Err()
}
`
	if comment != "" {
		comment = "\n\t// " + comment
	}

	return strings.NewReplacer("NAME", name, "TABLE", table, "ROWS", rows, "CAP", capacity, "COMMENT", comment).
		Replace(body)
}

// goRenamed is the loader copied under other names throughout: more of them
// than a shared function would take as parameters.
const goRenamed = `func loadOrders(conn *DB, max int) ([]entry, error) {
	cursor, problem := conn.Run("SELECT key, label FROM orders LIMIT ?", max)
	if problem != nil {
		return nil, fmt.Errorf("running: %w", problem)
	}
	defer cursor.Done()
	found := make([]entry, 0, max)
	for cursor.More() {
		var item entry
		if problem := cursor.Read(&item.key, &item.label); problem != nil {
			return nil, fmt.Errorf("reading: %w", problem)
		}
		found = append(found, item)
	}

	return found, cursor.Failure()
}
`

// goCaller is a loader that only calls the shared one with its table.
func goCaller(name, table string) string {
	return "func " + name + "(db *DB, limit int) ([]row, error) {\n\treturn loadRows(db, \"" + table + "\", limit)\n}\n"
}

const goStoreHead = "package store\n\nimport \"fmt\"\n\n"

func goNewDuplicateCase() fixturetest.Case {
	users := goStoreHead + goLoader("loadUsers", "users", "rows", "0", "")
	orders := func(body string) fixturetest.Tree { return fixturetest.Tree{"store/orders.go": goStoreHead + body} }

	return fixturetest.Case{
		Name: "go: a change adds a copy of a function next to it",
		Base: fixturetest.Tree{
			before + "store/users.go": users,
			added + "store/orders.go": goStoreHead + goLoader("loadOrders", "orders", "rows", "0", ""),
		},
		Dodges: []fixturetest.Variant{
			adds("rename a variable in the copy", orders(goLoader("loadOrders", "orders", "found", "0", ""))),
			adds("change a literal in the copy", orders(goLoader("loadOrders", "orders", "rows", "8", ""))),
			adds("add a comment to the copy", orders(goLoader("loadOrders", "orders", "rows", "0", "freed on return"))),
		},
		Persists: []fixturetest.Variant{
			adds("give the copy another name", orders(goLoader("listOrders", "orders", "rows", "0", ""))),
			movesTo("store/orders.go", "store/sales.go", goStoreHead+goLoader("loadOrders", "orders", "rows", "0", "")),
			adds("swap two statements of the copy", orders(strings.Replace(
				goLoader("loadOrders", "orders", "rows", "0", ""),
				"\tdefer rows.Close()\n\tout := make([]row, 0, limit)\n",
				"\tout := make([]row, 0, limit)\n\tdefer rows.Close()\n", 1))),
		},
		Gaps: []fixturetest.Variant{
			adds("rename every name the copy uses", orders(goRenamed)),
		},
		Fixes: []fixturetest.Variant{
			adds("call the function that is there", orders(
				"func loadOrders(db *DB, limit int) ([]row, error) {\n\treturn loadUsers(db, limit)\n}\n")),
			adds("give the function a parameter and call it twice", fixturetest.Tree{
				"store/users.go": goStoreHead +
					strings.Replace(strings.Replace(goLoader("loadRows", "users", "rows", "0", ""),
						"limit int)", "table string, limit int)", 1),
						"\"SELECT id, name FROM users LIMIT ?\"", "\"SELECT id, name FROM \"+table+\" LIMIT ?\"", 1) +
					"\n" + goCaller("loadUsers", "users"),
				"store/orders.go": "package store\n\n" +
					goCaller("loadOrders", "orders"),
			}),
		},
	}
}

// pyLoader is the Python function of the fixture, with the same knobs.
func pyLoader(name, table, rows, column, comment string) string {
	body := `def NAME(conn, limit):
    cursor = conn.execute("SELECT id, name FROM TABLE LIMIT ?", (limit,))
    ROWS = []COMMENT
    for record in cursor.fetchall():
        if record["COLUMN"] is not None:
            continue
        ROWS.append({"id": record["id"], "name": record["name"].strip()})
    cursor.close()
    return sorted(ROWS, key=lambda row: row["id"])
`
	if comment != "" {
		comment = "\n    # " + comment
	}

	return strings.NewReplacer("NAME", name, "TABLE", table, "ROWS", rows, "COLUMN", column, "COMMENT", comment).
		Replace(body)
}

func pythonNewDuplicateCase() fixturetest.Case {
	orders := func(body string) fixturetest.Tree { return fixturetest.Tree{"app/orders.py": body} }

	return fixturetest.Case{
		Name: "python: a change adds a copy of a function next to it",
		Base: fixturetest.Tree{
			before + "app/users.py": pyLoader("load_users", "users", "rows", "deleted_at", ""),
			added + "app/orders.py": pyLoader("load_orders", "orders", "rows", "deleted_at", ""),
		},
		Dodges: []fixturetest.Variant{
			adds("rename a variable in the copy", orders(pyLoader("load_orders", "orders", "found", "deleted_at", ""))),
			adds("change a literal in the copy", orders(pyLoader("load_orders", "orders", "rows", "archived_at", ""))),
			adds("add a comment to the copy", orders(pyLoader("load_orders", "orders", "rows", "deleted_at", "kept"))),
		},
		Persists: []fixturetest.Variant{
			adds("give the copy another name", orders(pyLoader("list_orders", "orders", "rows", "deleted_at", ""))),
			movesTo("app/orders.py", "app/sales.py", pyLoader("load_orders", "orders", "rows", "deleted_at", "")),
			adds("write the loop as a comprehension", orders(`def load_orders(conn, limit):
    cursor = conn.execute("SELECT id, name FROM orders LIMIT ?", (limit,))
    rows = [
        {"id": record["id"], "name": record["name"].strip()}
        for record in cursor.fetchall()
        if record["deleted_at"] is None
    ]
    cursor.close()
    return sorted(rows, key=lambda row: row["id"])
`)),
		},
		Gaps: []fixturetest.Variant{
			adds("rename every name the copy uses", orders(`def load_orders(db, count):
    cur = db.run("SELECT key, label FROM orders LIMIT ?", (count,))
    found = []
    for item in cur.all():
        if item["removed_at"] is not None:
            continue
        found.append({"key": item["key"], "label": item["label"].rstrip()})
    cur.done()
    return ordered(found, by=lambda entry: entry["key"])
`)),
		},
		Fixes: []fixturetest.Variant{
			adds("call the function that is there", orders(
				"from app.users import load_users\n\n\n"+
					"def load_orders(conn, limit):\n    return load_users(conn, limit)\n")),
			adds("give the function a parameter and call it twice", fixturetest.Tree{
				"app/users.py": strings.Replace(strings.Replace(
					pyLoader("load_rows", "users", "rows", "deleted_at", ""),
					"(conn, limit)", "(conn, table, limit)", 1),
					"\"SELECT id, name FROM users LIMIT ?\"", "f\"SELECT id, name FROM {table} LIMIT ?\"", 1) +
					"\n\ndef load_users(conn, limit):\n    return load_rows(conn, \"users\", limit)\n",
				"app/orders.py": "from app.users import load_rows\n\n\n" +
					"def load_orders(conn, limit):\n    return load_rows(conn, \"orders\", limit)\n",
			}),
		},
	}
}

// tsLoader is the TypeScript function of the fixture, with the same knobs.
func tsLoader(name, table, rows, column, comment string) string {
	body := `export async function NAME(db: Db, limit: number): Promise<Row[]> {
  const result = await db.query("SELECT id, name FROM TABLE LIMIT $1", [limit])
  const ROWS: Row[] = []COMMENT
  for (const record of result.rows) {
    if (record.COLUMN !== null) {
      continue
    }
    ROWS.push({ id: record.id, name: record.name.trim() })
  }
  await result.release()
  return ROWS.sort((a, b) => a.id - b.id)
}
`
	if comment != "" {
		comment = "\n  // " + comment
	}

	return strings.NewReplacer("NAME", name, "TABLE", table, "ROWS", rows, "COLUMN", column, "COMMENT", comment).
		Replace(body)
}

func typescriptNewDuplicateCase() fixturetest.Case {
	orders := func(body string) fixturetest.Tree { return fixturetest.Tree{"src/orders.ts": body} }

	return fixturetest.Case{
		Name: "typescript: a change adds a copy of a function next to it",
		Base: fixturetest.Tree{
			before + "src/users.ts": tsLoader("loadUsers", "users", "rows", "deletedAt", ""),
			added + "src/orders.ts": tsLoader("loadOrders", "orders", "rows", "deletedAt", ""),
		},
		Dodges: []fixturetest.Variant{
			adds("rename a variable in the copy", orders(tsLoader("loadOrders", "orders", "found", "deletedAt", ""))),
			adds("change a property in the copy", orders(tsLoader("loadOrders", "orders", "rows", "archivedAt", ""))),
			adds("add a comment to the copy", orders(tsLoader("loadOrders", "orders", "rows", "deletedAt", "kept"))),
		},
		Persists: []fixturetest.Variant{
			adds("give the copy another name", orders(tsLoader("listOrders", "orders", "rows", "deletedAt", ""))),
			movesTo("src/orders.ts", "src/sales.ts", tsLoader("loadOrders", "orders", "rows", "deletedAt", "")),
			adds("write the copy as an arrow function", orders(strings.Replace(
				tsLoader("loadOrders", "orders", "rows", "deletedAt", ""),
				"export async function loadOrders(db: Db, limit: number): Promise<Row[]> {",
				"export const loadOrders = async (db: Db, limit: number): Promise<Row[]> => {", 1))),
			adds("write the loop as a chain of calls", orders(`export async function loadOrders(db: Db, limit: number) {
  const result = await db.query("SELECT id, name FROM orders LIMIT $1", [limit])
  const rows = result.rows
    .filter((record) => record.deletedAt === null)
    .map((record) => ({ id: record.id, name: record.name.trim() }))
  await result.release()
  return rows.sort((a, b) => a.id - b.id)
}
`)),
		},
		Gaps: []fixturetest.Variant{
			adds("rename every name the copy uses", orders(`export async function loadOrders(conn: Conn, max: number) {
  const found = await conn.run("SELECT key, label FROM orders LIMIT $1", [max])
  const entries: Entry[] = []
  for (const item of found.all) {
    if (item.removedAt !== null) {
      continue
    }
    entries.push({ key: item.key, label: item.label.trimEnd() })
  }
  await found.done()
  return entries.sort((x, y) => x.key - y.key)
}
`)),
		},
		Fixes: []fixturetest.Variant{
			adds("call the function that is there", orders("import { loadUsers } from \"./users\"\n\n"+
				"export const loadOrders = (db: Db, limit: number) => loadUsers(db, limit)\n")),
			adds("give the function a parameter and call it twice", fixturetest.Tree{
				"src/users.ts": strings.Replace(strings.Replace(
					tsLoader("loadRows", "users", "rows", "deletedAt", ""),
					"(db: Db, limit", "(db: Db, table: string, limit", 1),
					"\"SELECT id, name FROM users LIMIT $1\"", "`SELECT id, name FROM ${table} LIMIT $1`", 1) +
					"\nexport const loadUsers = (db: Db, limit: number) => loadRows(db, \"users\", limit)\n",
				"src/orders.ts": "import { loadRows } from \"./users\"\n\n" +
					"export const loadOrders = (db: Db, limit: number) => loadRows(db, \"orders\", limit)\n",
			}),
		},
	}
}

// TestNewDuplicateHoldsUnderAdversarialEdits is x/new-duplicate's share of the
// adversarial fixtures. The rule reads a change, so each variant is a
// two-commit history and the command scores its last commit.
func TestNewDuplicateHoldsUnderAdversarialEdits(t *testing.T) {
	t.Parallel()
	fixturetest.Run(t, []fixturetest.Case{
		goNewDuplicateCase(), pythonNewDuplicateCase(), typescriptNewDuplicateCase(),
	}, changeRuleIDs(t, ruleNewDuplicate, "--experimental"))
}

// goErrChain is a Go function of three calls, each followed by the error
// check every Go file has.
func goErrChain(name, open, read, parse string) string {
	return strings.NewReplacer("NAME", name, "OPEN", open, "READ", read, "PARSE", parse).Replace(
		`func NAME(path string) (*Doc, error) {
	file, err := OPEN(path)
	if err != nil {
		return nil, err
	}
	data, err := READ(file)
	if err != nil {
		return nil, err
	}
	doc, err := PARSE(data)
	if err != nil {
		return nil, err
	}

	return doc, nil
}
`)
}

// goErrChains is a file of such functions, enough of them that the error
// check is an idiom of the tree and not a block two functions share.
func goErrChains() string {
	var b strings.Builder
	b.WriteString("package doc\n")
	for i := range 12 {
		n := strconv.Itoa(i)
		b.WriteString("\n" + goErrChain("load"+n, "open"+n, "read"+n, "parse"+n))
	}

	return b.String()
}

// goFormat is a type that implements a three-method interface with one-line bodies.
func goFormat(typ, name, ext, mime string) string {
	return strings.NewReplacer("TYPE", typ, "NAME", name, "EXT", ext, "MIME", mime).Replace(
		`type TYPE struct{}

func (TYPE) Name() string { return "NAME" }

func (TYPE) Ext() string { return "EXT" }

func (TYPE) Mime() string { return "MIME" }
`)
}

// quietCase is a change that adds nothing a reader would call a new copy.
type quietCase struct {
	tree fixturetest.Tree
	name string
}

func goQuietCases() []quietCase {
	users := goStoreHead + goLoader("loadUsers", "users", "rows", "0", "")
	orders := goStoreHead + goLoader("loadOrders", "orders", "rows", "0", "")
	wrapped := strings.Replace(strings.ReplaceAll(orders, "\n\t", "\n\t\t"),
		"([]row, error) {", "([]row, error) {\n\tif db == nil {\n\t\treturn nil, nil\n\t}\n\t{", 1)
	wrapped = strings.Replace(wrapped, "\n}\n", "\n\t}\n}\n", 1)
	twins := fixturetest.Tree{before + "store/users.go": users, before + "store/legacy.go": orders}

	return []quietCase{
		{name: "go: a function moves to another file while its twin stays", tree: fixturetest.Variant{
			Files: fixturetest.Tree{added + "store/legacy.go": "package store\n", added + "store/orders.go": orders},
		}.Apply(twins)},
		{name: "go: a file is renamed and one line of it edited", tree: fixturetest.Variant{
			Files: fixturetest.Tree{
				added + "store/legacy.go": removed,
				added + "store/orders.go": strings.Replace(orders, "0, limit", "0, 2*limit", 1),
			},
		}.Apply(twins)},
		{name: "go: a twin is wrapped in a block, which re-indents every line", tree: fixturetest.Variant{
			Files: fixturetest.Tree{added + "store/legacy.go": wrapped},
		}.Apply(twins)},
		{name: "go: the shared part of two twins moves into a helper both call", tree: fixturetest.Variant{
			Files: fixturetest.Tree{
				added + "store/users.go": goStoreHead +
					goCaller("loadUsers", "users"),
				added + "store/legacy.go": "package store\n\n" +
					goCaller("loadOrders", "orders"),
				added + "store/rows.go": goStoreHead + strings.Replace(strings.Replace(
					goLoader("loadRows", "users", "rows", "0", ""), "limit int)", "table string, limit int)", 1),
					"\"SELECT id, name FROM users LIMIT ?\"", "\"SELECT id, name FROM \"+table+\" LIMIT ?\"", 1),
			},
		}.Apply(twins)},
		{name: "go: a function repeats only the error checks every function has", tree: fixturetest.Tree{
			before + "doc/yaml.go":   "package doc\n\n" + goErrChain("loadYAML", "openFile", "readAll", "parseYAML"),
			before + "doc/checks.go": goErrChains(),
			added + "doc/toml.go":    "package doc\n\n" + goErrChain("loadTOML", "openLocked", "readHead", "parseTOML"),
		}},
		{name: "go: a type implements an interface with the one-line bodies another has", tree: fixturetest.Tree{
			before + "format/csv.go": "package format\n\n" + goFormat("csvFormat", "csv", ".csv", "text/csv"),
			added + "format/json.go": "package format\n\n" +
				goFormat("jsonFormat", "json", ".json", "application/json"),
		}},
		{name: "go: a test repeats the setup of another test", tree: fixturetest.Tree{
			before + "store/users_test.go": users,
			added + "store/orders_test.go": orders,
		}},
		{name: "go: a generated file repeats a written one", tree: fixturetest.Tree{
			before + "store/users.go":     users,
			added + "store/orders.gen.go": "// Code generated by rowgen. DO NOT EDIT.\n\n" + orders,
		}},
	}
}

// tsSummary is a function unlike the loaders, for a change that adds new code.
const tsSummary = `export function summarize(rows: Row[]): Summary {
  const total = rows.reduce((sum, row) => sum + row.amount, 0)
  const largest = rows.reduce((max, row) => Math.max(max, row.amount), 0)
  return { total, largest, mean: rows.length === 0 ? 0 : total / rows.length }
}
`

func otherQuietCases() []quietCase {
	pyUsers := pyLoader("load_users", "users", "rows", "deleted_at", "")
	pyOrders := pyLoader("load_orders", "orders", "rows", "deleted_at", "")
	tsUsers := tsLoader("loadUsers", "users", "rows", "deletedAt", "")
	tsOrders := tsLoader("loadOrders", "orders", "rows", "deletedAt", "")

	return []quietCase{
		{name: "python: a function moves to another file while its twin stays", tree: fixturetest.Tree{
			before + "app/users.py": pyUsers, before + "app/legacy.py": pyOrders,
			added + "app/legacy.py": "\n", added + "app/orders.py": pyOrders,
		}},
		{name: "python: a test repeats the setup of another test", tree: fixturetest.Tree{
			before + "tests/test_users.py": pyUsers, added + "tests/test_orders.py": pyOrders,
		}},
		{name: "typescript: one change lands in a tree and in its mirror", tree: fixturetest.Tree{
			before + "src/users.ts": tsUsers, before + "deno/lib/users.ts": tsUsers,
			added + "src/users.ts": tsUsers + "\n" + tsSummary, added + "deno/lib/users.ts": tsUsers + "\n" + tsSummary,
		}},
		{name: "typescript: a function moves to another file while its twin stays", tree: fixturetest.Tree{
			before + "src/users.ts": tsUsers, before + "src/legacy.ts": tsOrders,
			added + "src/legacy.ts": "export {}\n", added + "src/orders.ts": tsOrders,
		}},
		{name: "typescript: an example app repeats another example app", tree: fixturetest.Tree{
			before + "examples/react/src/users.ts": tsUsers, added + "examples/vue/src/orders.ts": tsOrders,
		}},
		{name: "python: a benchmark repeats another benchmark", tree: fixturetest.Tree{
			before + "benchmarks/users.py": pyUsers, added + "benchmarks/orders.py": pyOrders,
		}},
		{name: "typescript: a test repeats the setup of another test", tree: fixturetest.Tree{
			before + "src/users.test.ts": tsUsers, added + "src/orders.test.ts": tsOrders,
		}},
	}
}

// TestNewDuplicateStaysQuiet pins the changes that look like a new copy to a
// diff and are not one: moved and re-indented code, a body pulled into a
// helper, the idioms every file repeats, tests, examples, benchmarks,
// generated files, and a tree kept in step with its mirror.
func TestNewDuplicateStaysQuiet(t *testing.T) {
	t.Parallel()
	for _, tc := range append(goQuietCases(), otherQuietCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, f := range changeFindings(t, historyRepo(t, tc.tree), "--experimental") {
				if f.Rule == ruleNewDuplicate {
					t.Errorf("reported %s:%d: %s", f.Path, f.Line, f.Note)
				}
			}
		})
	}
}

// TestNewDuplicateIsReadable pins what a person and an agent see of a finding:
// the note under the findings table, and the same note as the reason of a
// next line, so --brief says where the code to extend is.
func TestNewDuplicateIsReadable(t *testing.T) {
	t.Parallel()
	repo := historyRepo(t, goNewDuplicateCase().Base)
	const want = "repeats loadUsers at store/users.go:5, in the same directory: "
	for _, flags := range [][]string{{"--experimental"}, {"--experimental", "--brief"}} {
		out, err := runCLI(t, repo, append([]string{"change", "--base", "HEAD~1", "--rev", "HEAD"}, flags...)...)
		if err != nil {
			t.Fatalf("change %v: %v", flags, err)
		}
		if !strings.Contains(out, want) || !strings.Contains(out, "Extend or parameterise that code") {
			t.Errorf("change %v does not say where the repeated code is:\n%s", flags, out)
		}
	}
	plain, err := runCLI(t, repo, "change", "--base", "HEAD~1", "--rev", "HEAD")
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if strings.Contains(plain, ruleNewDuplicate) {
		t.Errorf("the rule reported without --experimental:\n%s", plain)
	}
}

// TestNewDuplicateTakesAnInlineIgnore pins that a reasoned ignore on the first
// line of the copy sets the finding aside and counts it.
func TestNewDuplicateTakesAnInlineIgnore(t *testing.T) {
	t.Parallel()
	tree := goNewDuplicateCase().Base
	tree[added+"store/orders.go"] = goStoreHead +
		"// slop-strata:ignore x/new-duplicate the two tables split next release\n" +
		goLoader("loadOrders", "orders", "rows", "0", "")
	repo := historyRepo(t, tree)
	out, err := runCLI(t, repo, "change", "--base", "HEAD~1", "--rev", "HEAD", "--json", "--experimental")
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
	for _, f := range env.Result.Findings {
		if f.Rule == ruleNewDuplicate {
			t.Errorf("an ignored finding was reported: %+v", f)
		}
	}
	if env.Counts.Suppressed.Ignored != 1 {
		t.Errorf("counts.suppressed.ignored = %d, want 1", env.Counts.Suppressed.Ignored)
	}
}

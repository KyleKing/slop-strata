package rules_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/rules"
)

// A function long enough to be worth sharing, in each language. %s is the
// function's name and %s after it a table name, the one literal that differs.
const (
	goTally = `package store

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
	pyTally = `def NAME(raw):
    counts = {}
    for field in raw.split(";"):
        key, _, value = field.strip().partition("=")
        if not value:
            raise ValueError(f"TABLE: field {field!r} has no value")
        counts[key.lower()] = counts.get(key.lower(), 0) + int(value)
    return counts
`
	tsTally = `export function NAME(raw: string): Record<string, number> {
  const counts: Record<string, number> = {}
  for (const field of raw.split(";")) {
    const [key, value] = field.trim().split("=")
    if (value === undefined) {
      throw new Error("TABLE: field " + field + " has no value")
    }
    counts[key.toLowerCase()] = (counts[key.toLowerCase()] ?? 0) + Number(value)
  }
  return counts
}
`
)

// tsScript is a script with no function: its statements sit at file scope.
const tsScript = `process.chdir(fileURLToPath(new URL("..", import.meta.url)))
for (const [name, range] of Object.entries(JSON.parse(await readFile("package.json", "utf8")).dependencies)) {
  await writeFile(join("vendor", name + ".json"), JSON.stringify({ name, range, state: "synced" }))
}
`

func tallySrc(tmpl, name, table string) string {
	return strings.NewReplacer("NAME", name, "TABLE", table).Replace(tmpl)
}

// dupChange is a file as a change leaves it. An empty before is a new file,
// whose every line the change added. A before the after starts with was
// appended to, and otherwise the lines the before side does not hold are the
// added ones.
type dupChange struct {
	path, before, after string
}

// newDuplicates fingerprints a tree and the files a change rewrote in it, and
// runs the rule the way change does.
func newDuplicates(t *testing.T, tree map[string]string, changed []dupChange, experimental bool,
) map[string][]rules.Finding {
	t.Helper()
	rev := map[string]*rules.Fingerprint{}
	for path, src := range tree {
		rev[path] = printOf(t, path, src)
	}
	changes := make([]rules.DupChange, 0, len(changed))
	for _, c := range changed {
		ch := rules.DupChange{Path: c.path}
		old := map[string]bool{}
		if c.before != "" {
			ch.Before = printOf(t, c.path, c.before)
			for _, line := range strings.Split(c.before, "\n") {
				old[line] = true
			}
		}
		if c.after != "" {
			ch.After = printOf(t, c.path, c.after)
			rev[c.path] = ch.After
			lines := strings.Split(c.after, "\n")
			kept := 0
			if strings.HasPrefix(c.after, c.before) {
				kept = strings.Count(c.before, "\n") // an append adds every line after the old ones
			}
			ch.Added = func(line int) bool {
				return line > kept && line <= len(lines) && (kept > 0 || !old[lines[line-1]])
			}
		} else {
			delete(rev, c.path)
		}
		changes = append(changes, ch)
	}

	return rules.NewDuplicates(rev, changes, experimental)
}

func TestNewDuplicatesReportsACopyInEachLanguage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, tmpl, old, added, wantMatch string
	}{
		{"go", goTally, "store/users.go", "store/orders.go", "store/users.go parseUsers"},
		{"python", pyTally, "app/users.py", "app/orders.py", "app/users.py parseUsers"},
		{"typescript", tsTally, "src/users.ts", "src/orders.ts", "src/users.ts parseUsers"},
		{"tsx", tsTally, "src/users.tsx", "src/orders.tsx", "src/users.tsx parseUsers"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tree := map[string]string{tt.old: tallySrc(tt.tmpl, "parseUsers", "users")}
			got := newDuplicates(t, tree, []dupChange{
				{path: tt.added, after: tallySrc(tt.tmpl, "parseOrders", "orders")},
			}, true)
			fs := got[tt.added]
			if len(fs) != 1 {
				t.Fatalf("got %+v, want one finding on %s", got, tt.added)
			}
			f := fs[0]
			if f.Rule != rules.RuleNewDuplicate || f.Match != tt.wantMatch || f.Scope != "parseOrders" {
				t.Errorf("finding = %+v, want rule %s, match %q, scope parseOrders",
					f, rules.RuleNewDuplicate, tt.wantMatch)
			}
			wants := []string{"repeats parseUsers at " + tt.old + ":", "in the same directory", "1 difference ("}
			for _, want := range wants {
				if !strings.Contains(f.Note, want) {
					t.Errorf("note %q lacks %q", f.Note, want)
				}
			}
		})
	}
}

func TestNewDuplicatesNote(t *testing.T) {
	t.Parallel()
	users := tallySrc(goTally, "parseUsers", "users")
	orders := strings.TrimPrefix(tallySrc(goTally, "parseOrders", "users"), "package store\n")
	tests := []struct {
		name    string
		tree    map[string]string
		changed []dupChange
		path    string
		want    []string
	}{
		{
			name:    "an identical copy in the same file",
			changed: []dupChange{{path: "store/users.go", before: users, after: users + orders}},
			path:    "store/users.go",
			want: []string{
				"repeats parseUsers at store/users.go:3, in this file", "identical", "Extend or parameterise",
			},
		},
		{
			name:    "a copy in another directory",
			tree:    map[string]string{"store/users.go": users},
			changed: []dupChange{{path: "api/orders.go", after: tallySrc(goTally, "parseOrders", "users")}},
			path:    "api/orders.go",
			want:    []string{"in another directory", "identical"},
		},
		{
			name: "two existing sites",
			tree: map[string]string{
				"store/users.go": users, "store/items.go": tallySrc(goTally, "parseItems", "users"),
			},
			changed: []dupChange{{path: "store/orders.go", after: tallySrc(goTally, "parseOrders", "users")}},
			path:    "store/orders.go",
			want:    []string{"repeats parseItems at store/items.go:3", ", and parseUsers at store/users.go:3"},
		},
		{
			name:    "the same code added twice to one file",
			changed: []dupChange{{path: "store/users.go", after: users + orders}},
			path:    "store/users.go",
			want:    []string{"repeats parseUsers at store/users.go:3, which this change also adds, in this file"},
		},
		{
			name: "more existing sites than a note lists",
			tree: map[string]string{
				"store/users.go": users, "store/items.go": tallySrc(goTally, "parseItems", "users"),
				"store/carts.go": tallySrc(goTally, "parseCarts", "users"),
				"store/bills.go": tallySrc(goTally, "parseBills", "users"),
			},
			changed: []dupChange{{path: "store/orders.go", after: tallySrc(goTally, "parseOrders", "users")}},
			path:    "store/orders.go",
			want: []string{
				"repeats parseBills at store/bills.go:3", ", and parseCarts at store/carts.go:3",
				", and parseItems at store/items.go:3, and 1 more place:",
			},
		},
		{
			name: "code at file scope, which no function holds",
			tree: map[string]string{"scripts/sync.ts": tsScript},
			changed: []dupChange{{
				path: "scripts/prune.ts", after: strings.Replace(tsScript, "synced", "pruned", 1),
			}},
			path: "scripts/prune.ts",
			want: []string{"repeats scripts/sync.ts:1, in the same directory", "1 difference (\"synced\"→\"pruned\")"},
		},
		{
			name: "the same code added twice",
			changed: []dupChange{
				{path: "store/users.go", after: users},
				{path: "store/orders.go", after: tallySrc(goTally, "parseOrders", "users")},
			},
			path: "store/users.go",
			want: []string{"repeats parseOrders at store/orders.go:3, which this change also adds"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := newDuplicates(t, tt.tree, tt.changed, true)
			total := 0
			for _, fs := range got {
				total += len(fs)
			}
			if total != 1 || len(got[tt.path]) != 1 {
				t.Fatalf("got %+v, want one finding, on %s", got, tt.path)
			}
			for _, want := range tt.want {
				if !strings.Contains(got[tt.path][0].Note, want) {
					t.Errorf("note %q lacks %q", got[tt.path][0].Note, want)
				}
			}
		})
	}
}

func TestNewDuplicatesStaysQuiet(t *testing.T) {
	t.Parallel()
	users := tallySrc(goTally, "parseUsers", "users")
	copied := tallySrc(goTally, "parseOrders", "orders")
	tests := []struct {
		name         string
		tree         map[string]string
		changed      []dupChange
		experimental bool
	}{
		{
			name:    "without --experimental",
			tree:    map[string]string{"store/users.go": users},
			changed: []dupChange{{path: "store/orders.go", after: copied}},
		},
		{
			name:         "a test file that repeats a test file",
			tree:         map[string]string{"store/users_test.go": users},
			changed:      []dupChange{{path: "store/orders_test.go", after: copied}},
			experimental: true,
		},
		{
			name:         "a test file that repeats production code",
			tree:         map[string]string{"store/users.go": users},
			changed:      []dupChange{{path: "store/orders_test.go", after: copied}},
			experimental: true,
		},
		{
			name: "a function moved to another file while a twin stays",
			tree: map[string]string{"store/users.go": users},
			changed: []dupChange{
				{path: "store/old.go", before: copied},
				{path: "store/orders.go", after: copied},
			},
			experimental: true,
		},
		{
			name: "a twin edited in place",
			tree: map[string]string{"store/users.go": users},
			changed: []dupChange{{
				path: "store/orders.go", before: copied, after: strings.ReplaceAll(copied, "field", "part"),
			}},
			experimental: true,
		},
		{
			name: "a copy kept in step under the same file name",
			tree: map[string]string{"src/store/parse.go": users},
			changed: []dupChange{
				{path: "src/store/parse.go", after: users},
				{path: "mirror/store/parse.go", after: users},
			},
			experimental: true,
		},
		{
			name:         "an example that repeats another example",
			tree:         map[string]string{"examples/users/load.go": users},
			changed:      []dupChange{{path: "examples/orders/list.go", after: copied}},
			experimental: true,
		},
		{
			name:         "a benchmark that repeats another benchmark",
			tree:         map[string]string{"bench/users.go": users},
			changed:      []dupChange{{path: "bench/orders.go", after: copied}},
			experimental: true,
		},
		{
			name:         "production code that repeats an example",
			tree:         map[string]string{"examples/users/main.go": users},
			changed:      []dupChange{{path: "store/orders.go", after: copied}},
			experimental: true,
		},
		{
			name:         "a file with no statement",
			tree:         map[string]string{"store/users.go": users},
			changed:      []dupChange{{path: "store/doc.go", after: "// Package store holds rows.\n"}},
			experimental: true,
		},
		{
			name:         "a deleted file",
			tree:         map[string]string{"store/users.go": users},
			changed:      []dupChange{{path: "store/orders.go", before: copied}},
			experimental: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := newDuplicates(t, tt.tree, tt.changed, tt.experimental); len(got) != 0 {
				t.Errorf("got %+v, want no finding", got)
			}
		})
	}
}

// TestNewDuplicatesJudgesALongRunWhole pins the path a copied file takes: a
// run too long to search for its best stretch is still found.
func TestNewDuplicatesJudgesALongRunWhole(t *testing.T) {
	t.Parallel()
	// 300 statements of 300 shapes, so none is an idiom of the tree.
	var body strings.Builder
	for i := range 300 {
		args := strings.TrimSuffix(strings.Repeat("row, ", i%20+1), ", ")
		fmt.Fprintf(&body, "\ttotal += weigh(%s)%s\n", args, strings.Repeat(" + shard", i/20))
	}
	file := func(name string) string {
		return "package rules\n\nfunc " + name + "(row Row, shard int) (total int) {\n" +
			body.String() + "\treturn\n}\n"
	}
	got := newDuplicates(t, map[string]string{"rules/v1.go": file("installV1")},
		[]dupChange{{path: "rules/v2.go", after: file("installV2")}}, true)
	fs := got["rules/v2.go"]
	if len(fs) != 1 || !strings.Contains(fs[0].Note, "302 statements") || !strings.Contains(fs[0].Note, "identical") {
		t.Errorf("got %+v, want one finding over the 302 statements of the copy", got)
	}
}

// TestNewDuplicatesReadsPastDocstrings pins that a docstring is no statement:
// a copy documented in other words is still an identical copy, from its
// first line.
func TestNewDuplicatesReadsPastDocstrings(t *testing.T) {
	t.Parallel()
	documented := func(name, doc string) string {
		src := tallySrc(pyTally, name, "users")
		head, body, _ := strings.Cut(src, "\n")

		return head + "\n    \"\"\"" + doc + "\"\"\"\n" + body
	}
	got := newDuplicates(t, map[string]string{"app/users.py": documented("parse_users", "Count the fields.")},
		[]dupChange{{path: "app/orders.py", after: documented("parse_orders", "Tally what each order holds.")}}, true)
	fs := got["app/orders.py"]
	if len(fs) != 1 || !strings.Contains(fs[0].Note, "identical") || fs[0].Line != 1 {
		t.Errorf("got %+v, want one finding on line 1 that calls the copy identical", got)
	}
}

// A condition long enough to be reported were it code, guarding two bodies
// that have nothing in common.
const (
	guardOpen  = "package gate\n\nfunc NAME(req *Request, cfg *Config) error {\n"
	guardCheck = "\tif req.User != nil && req.User.Active && cfg.Limits[req.User.Plan] > req.Used &&" +
		" !cfg.Frozen[req.Tenant] {\n"
	guardSame = "\t\treturn store.Reserve(req.Tenant, req.User.ID, cfg.Limits[req.User.Plan]-req.Used)\n"
)

func TestNewDuplicatesNeedsMoreThanASharedCondition(t *testing.T) {
	t.Parallel()
	admit := guardOpen + guardCheck + guardSame + "\t}\n\n\treturn errDenied\n}\n"
	tests := []struct {
		name string
		body string
		want int
	}{
		{
			name: "the condition and what it guards",
			body: guardCheck + guardSame + "\t}\n\n\treturn nil\n}\n", want: 1,
		},
		{name: "the condition alone", body: guardCheck + "\t\tlog.Print(req.Tenant)\n\t}\n\n\treturn nil\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tree := map[string]string{"gate/admit.go": strings.Replace(admit, "NAME", "admit", 1)}
			got := newDuplicates(t, tree, []dupChange{
				{path: "gate/extend.go", after: strings.Replace(guardOpen, "NAME", "extend", 1) + tt.body},
			}, true)
			if len(got["gate/extend.go"]) != tt.want {
				t.Errorf("got %+v, want %d findings", got, tt.want)
			}
		})
	}
}

// Two arms of one switch that do the same thing, as a type switch makes them.
const goArms = `package conv

func widen(v any) int64 {
	switch n := v.(type) {
	case int8:
		if n < 0 {
			return clamp(int64(n), limits.Low, limits.High) + offsets[kindOf(v)]
		}
		return int64(n)
	case int16:
		if n < 0 {
			return clamp(int64(n), limits.Low, limits.High) + offsets[kindOf(v)]
		}
		return int64(n)
	}

	return 0
}
`

// The same arm in a second function, away from the first.
const goArmElsewhere = `
func narrow(v any) int64 {
	n, ok := v.(int32)
	if !ok {
		return 0
	}
	if n < 0 {
		return clamp(int64(n), limits.Low, limits.High) + offsets[kindOf(v)]
	}
	return int64(n)
}
`

func TestNewDuplicatesLeavesBackToBackArmsAlone(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		want int
	}{
		{name: "two arms of one switch, one behind the other", src: goArms},
		{name: "the arm again in another function", src: goArms + goArmElsewhere, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := newDuplicates(t, nil, []dupChange{{path: "conv/widen.go", after: tt.src}}, true)
			if len(got["conv/widen.go"]) != tt.want {
				t.Errorf("got %+v, want %d findings", got, tt.want)
			}
		})
	}
}

// Overloads: the signatures of two functions line up word for word, and the
// bodies have nothing in common.
const (
	tsOverloads = `export function NAME(doc: Doc, ctx: Context, schema: SomeType, accessor: string): string;
export function NAME(doc: Doc, ctx: Context, schema: SomeType, accessor: string, keep: true): string | null;
export function NAME(doc: Doc, ctx: Context, schema: SomeType, accessor: string, keep?: boolean) {
`
	tsBodyA = "  return keep ? emit(doc, ctx.child(schema), accessor) : null\n}\n"
	tsBodyB = "  doc.write(accessor)\n  return ctx.failed ? null : render(schema)\n}\n"
)

func TestNewDuplicatesDoesNotCountSignatures(t *testing.T) {
	t.Parallel()
	tree := map[string]string{"src/child.ts": strings.ReplaceAll(tsOverloads, "NAME", "compileChild") + tsBodyA}
	got := newDuplicates(t, tree, []dupChange{
		{path: "src/check.ts", after: strings.ReplaceAll(tsOverloads, "NAME", "generateCheck") + tsBodyB},
	}, true)
	if len(got) != 0 {
		t.Errorf("got %+v, want no finding for two functions that share only their overloads", got)
	}
}

func TestNewDuplicatesCountsCodeAndNotItsWrapping(t *testing.T) {
	t.Parallel()
	const (
		pyImports = "from collections.abc import Iterable, Iterator, Mapping, MutableMapping, Sequence\n" +
			"from typing import Any, Callable, Generic, Literal, Protocol, TypeVar, overload\n\n"
		tsOptions = "export interface NAME<TInput extends Record<string, unknown>, TOutput = TInput," +
			" TError = Error> {\n"
		tsListen = "  server.on(\"request\", async (request: IncomingMessage, response: ServerResponse) => {\n" +
			"    response.end()\n  })\n"
	)
	tests := []struct {
		name, old, added, oldSrc, addedSrc string
		want                               int
	}{
		{
			name: "python: two files that share only their imports",
			old:  "app/a.py", oldSrc: pyImports + "def first(rows):\n    return rows[0]\n",
			added: "app/b.py", addedSrc: pyImports + "def size(rows):\n    return len(rows) + 1\n",
		},
		{
			name: "typescript: two interfaces that share the line that opens them and one short member",
			old:  "src/a.ts", oldSrc: strings.Replace(tsOptions, "NAME", "ReadOptions", 1) +
				"  signal?: AbortSignal | null\n  cursor: string\n}\n",
			added: "src/b.ts", addedSrc: strings.Replace(tsOptions, "NAME", "WriteOptions", 1) +
				"  signal?: AbortSignal | null\n  retries: number\n}\n",
		},
		{
			name: "typescript: a call that takes a callback counts with what the callback does",
			old:  "src/a.ts", oldSrc: "export function serve(server: Server) {\n" + tsListen + "}\n",
			added: "src/b.ts", addedSrc: "export function mirror(server: Server) {\n" + tsListen + "}\n",
			want: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := newDuplicates(t, map[string]string{tt.old: tt.oldSrc},
				[]dupChange{{path: tt.added, after: tt.addedSrc}}, true)
			if len(got[tt.added]) != tt.want {
				t.Errorf("got %+v, want %d findings", got, tt.want)
			}
		})
	}
}

// roundTrip stores a fingerprint as the blob cache does and reads it back.
func roundTrip(t *testing.T, fp *rules.Fingerprint) *rules.Fingerprint {
	t.Helper()
	data, err := json.Marshal(fp)
	if err != nil {
		t.Fatal(err)
	}
	out := &rules.Fingerprint{}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatal(err)
	}

	return out
}

// TestFragmentsSurviveTheCache pins that a fingerprint's statements read the
// same after the JSON round trip the blob cache puts them through.
func TestFragmentsSurviveTheCache(t *testing.T) {
	t.Parallel()
	users := printOf(t, "store/users.go", tallySrc(goTally, "parseUsers", "users"))
	orders := printOf(t, "store/orders.go", tallySrc(goTally, "parseOrders", "orders"))
	stored := roundTrip(t, users)
	if len(stored.Frags) == 0 || !bytes.Equal(stored.Frags, users.Frags) {
		t.Fatalf("round trip changed the statements: %d bytes, was %d", len(stored.Frags), len(users.Frags))
	}
	added := func(int) bool { return true }
	fresh := rules.NewDuplicates(
		map[string]*rules.Fingerprint{"store/users.go": users, "store/orders.go": orders},
		[]rules.DupChange{{Path: "store/orders.go", After: orders, Added: added}}, true)
	cached := rules.NewDuplicates(
		map[string]*rules.Fingerprint{"store/users.go": stored, "store/orders.go": orders},
		[]rules.DupChange{{Path: "store/orders.go", After: orders, Added: added}}, true)
	if len(fresh["store/orders.go"]) != 1 || len(cached["store/orders.go"]) != 1 ||
		fresh["store/orders.go"][0].Note != cached["store/orders.go"][0].Note {
		t.Errorf("fresh %+v and cached %+v differ", fresh, cached)
	}
}

// TestFragmentsTolerateATruncatedPayload pins that a damaged cache entry
// yields no finding and no panic.
func TestFragmentsTolerateATruncatedPayload(t *testing.T) {
	t.Parallel()
	orders := printOf(t, "store/orders.go", tallySrc(goTally, "parseOrders", "orders"))
	full := printOf(t, "store/users.go", tallySrc(goTally, "parseUsers", "users"))
	for cut := range len(full.Frags) {
		broken := roundTrip(t, full)
		broken.Frags = broken.Frags[:cut]
		rules.NewDuplicates(
			map[string]*rules.Fingerprint{"store/users.go": broken, "store/orders.go": orders},
			[]rules.DupChange{{Path: "store/orders.go", After: orders, Added: func(int) bool { return true }}}, true)
	}
}

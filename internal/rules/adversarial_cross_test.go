package rules_test

import "github.com/kyleking/slop-strata/internal/fixturetest"

func crossAdversarial() []advCase {
	return []advCase{
		goDeadExportCase(), pyDeadExportCase(), tsDeadExportCase(),
		goCouldBePrivateCase(), pyCouldBePrivateCase(), tsCouldBePrivateCase(),
		goCloneCase(), pyCloneCase(), tsCloneCase(),
		goDupTestCase(), pyDupTestCase(), tsDupTestCase(),
		goSingleImplCase(), tsSingleImplCase(), goUnusedOptionCase(),
		goSpentTestCase(), pySpentTestCase(), tsSpentTestCase(),
		goStaleConfigCase(), pyStaleConfigCase(), tsStaleConfigCase(),
		goStaleProseCase(), pyStaleProseCase(), tsStaleProseCase(),
	}
}

// tree builds a case over an archive of several files.
func tree(name, archive string, ids ...string) advCase {
	return advCase{rules: ids, Case: fixturetest.Case{Name: name, Base: fixturetest.Files(archive)}}
}

const (
	ruleDead    = "x/dead-export"
	rulePrivate = "x/could-be-private"
)

func goDeadExportCase() advCase {
	const file = "store/store.go"
	c := tree("go: an exported function nothing calls", `
-- store/store.go --
package store

func Open() {}

func Compact() {}
-- cmd/main.go --
package main

import "example.com/app/store"

func main() { store.Open() }
`, ruleDead, rulePrivate)
	c.Dodges = []fixturetest.Variant{
		one("mention it in a comment and reorder the file", file, `package store

// Compact is kept for later. Compact, Compact.
func Compact() {}

func Open() {}
`),
	}
	c.Persists = []fixturetest.Variant{
		one("rename it", file, "package store\n\nfunc Open() {}\n\nfunc Vacuum() {}\n"),
		{Name: "move it to another file of the package", Files: fixturetest.Tree{
			file: "package store\n\nfunc Open() {}\n", "store/compact.go": "package store\n\nfunc Compact() {}\n",
		}},
		one("keep it alive with a reference in its own file", file,
			"package store\n\nfunc Open() {}\n\nfunc Compact() {}\n\nvar _ = Compact\n"),
	}
	c.Gaps = []fixturetest.Variant{
		one("call it from a test and nowhere else", "store/store_test.go",
			"package store\n\nimport \"testing\"\n\nfunc TestCompact(t *testing.T) { Compact() }\n"),
		one("share its name with an unrelated field", "cmd/flags.go",
			"package main\n\ntype flags struct{ Compact bool }\n"),
	}
	c.Fixes = []fixturetest.Variant{one("delete it", file, "package store\n\nfunc Open() {}\n")}

	return c
}

func pyDeadExportCase() advCase {
	const file = "app/store.py"
	c := tree("python: a public function nothing calls", `
-- app/__init__.py --
-- app/store.py --
def open_db():
    return 1


def compact():
    return 2
-- app/main.py --
from app.store import open_db

print(open_db())
`, ruleDead, rulePrivate)
	c.Dodges = []fixturetest.Variant{
		one("mention it in a docstring and reorder the file", file, `def compact():
    """compact is kept for later."""
    return 2


def open_db():
    return 1
`),
	}
	c.Persists = []fixturetest.Variant{
		one("rename it", file, "def open_db():\n    return 1\n\n\ndef vacuum():\n    return 2\n"),
		one("keep it alive with a reference in its own file", file,
			"def open_db():\n    return 1\n\n\ndef compact():\n    return 2\n\n\nKEEP = compact\n"),
	}
	c.Gaps = []fixturetest.Variant{
		one("list it in __all__", file,
			"__all__ = [\"open_db\", \"compact\"]\n"+
				"\n"+
				"\n"+
				"def open_db():\n"+
				"    return 1\n"+
				"\n"+
				"\n"+
				"def compact():\n"+
				"    return 2\n"),
		one("put a decorator on it", file,
			"import functools\n\n\ndef open_db():\n    return 1\n\n\n@functools.cache\ndef compact():\n    return 2\n"),
		one("star-import the module from the package", "app/__init__.py", "from app.store import *\n"),
		one("call it from a test and nowhere else", "tests/test_store.py",
			"from app.store import compact\n\n\ndef test_compact():\n    assert compact() == 2\n"),
	}
	c.Fixes = []fixturetest.Variant{one("delete it", file, "def open_db():\n    return 1\n")}

	return c
}

func tsDeadExportCase() advCase {
	const file = "src/store.ts"
	c := tree("typescript: an exported function nothing imports", `
-- src/store.ts --
export function open(): number {
  return 1
}

export function compact(): number {
  return 2
}
-- src/main.ts --
import { open } from "./store"

console.log(open())
`, ruleDead, rulePrivate)
	c.Dodges = []fixturetest.Variant{
		one("mention it in a comment and reorder the file", file, `// compact is kept for later
export function compact(): number {
  return 2
}

export function open(): number {
  return 1
}
`),
	}
	c.Persists = []fixturetest.Variant{
		one("rename it", file,
			"export function open(): number {\n  return 1\n}\n\nexport function vacuum(): number {\n  return 2\n}\n"),
		one("turn it into an exported arrow constant", file,
			"export function open(): number {\n  return 1\n}\n\nexport const compact = (): number => 2\n"),
		one("export it through a clause", file,
			"export function open(): number {\n"+
				"  return 1\n"+
				"}\n"+
				"\n"+
				"function compact(): number {\n"+
				"  return 2\n"+
				"}\n"+
				"\n"+
				"export { compact }\n"),
	}
	c.Gaps = []fixturetest.Variant{
		one("re-export the whole module through a barrel", "src/index.ts", "export * from \"./store\"\n"),
		one("import it from a test and nowhere else", "src/store.test.ts",
			"import { compact } from \"./store\"\n\nit(\"compacts\", () => {\n  expect(compact()).toBe(2)\n})\n"),
	}
	c.Fixes = []fixturetest.Variant{one("delete it", file, "export function open(): number {\n  return 1\n}\n")}

	return c
}

func goCouldBePrivateCase() advCase {
	const file = "store/store.go"
	c := tree("go: an exported helper only its own file uses", `
-- store/store.go --
package store

func Open() int { return Normalize(1) }

func Normalize(n int) int { return n }
-- cmd/main.go --
package main

import "example.com/app/store"

func main() { store.Open() }
`, rulePrivate)
	c.Dodges = []fixturetest.Variant{
		one("add a doc comment that calls it public", file, `package store

func Open() int { return Normalize(1) }

// Normalize is part of the public API.
func Normalize(n int) int { return n }
`),
	}
	c.Persists = []fixturetest.Variant{
		one("rename it", file, "package store\n"+
			"\n"+
			"func Open() int { return Clean(1) }\n"+
			"\n"+
			"func Clean(n int) int { return n }\n"),
	}
	c.Gaps = []fixturetest.Variant{
		one("call it from a test", "store/store_test.go",
			"package store\n\nimport \"testing\"\n\nfunc TestNormalize(t *testing.T) { Normalize(1) }\n"),
	}
	c.Fixes = []fixturetest.Variant{one("unexport it", file,
		"package store\n\nfunc Open() int { return normalize(1) }\n\nfunc normalize(n int) int { return n }\n")}

	return c
}

func pyCouldBePrivateCase() advCase {
	const file = "app/store.py"
	c := tree("python: a public constant only its own module reads", `
-- app/__init__.py --
-- app/store.py --
PAGE_SIZE = 50


def open_db():
    return PAGE_SIZE
-- app/main.py --
from app.store import open_db

print(open_db())
`, rulePrivate)
	c.Dodges = []fixturetest.Variant{
		one("annotate it and add a comment", file,
			"# the page size is public\nPAGE_SIZE: int = 50\n\n\ndef open_db():\n    return PAGE_SIZE\n"),
	}
	c.Persists = []fixturetest.Variant{
		one("rename it", file, "BATCH = 50\n\n\ndef open_db():\n    return BATCH\n"),
	}
	c.Gaps = []fixturetest.Variant{
		one("list it in __all__", file,
			"__all__ = [\"PAGE_SIZE\", \"open_db\"]\n\nPAGE_SIZE = 50\n\n\ndef open_db():\n    return PAGE_SIZE\n"),
	}
	c.Fixes = []fixturetest.Variant{one("give it a leading underscore", file,
		"_PAGE_SIZE = 50\n\n\ndef open_db():\n    return _PAGE_SIZE\n")}

	return c
}

func tsCouldBePrivateCase() advCase {
	const file = "src/store.ts"
	c := tree("typescript: an exported constant only its own file reads", `
-- src/store.ts --
export const PAGE_SIZE = 50

export function open(): number {
  return PAGE_SIZE
}
-- src/main.ts --
import { open } from "./store"

console.log(open())
`, rulePrivate)
	c.Dodges = []fixturetest.Variant{
		one("annotate it and add a comment", file,
			"// the page size is public\n"+
				"export const PAGE_SIZE: number = 50\n"+
				"\n"+
				"export function open(): number {\n"+
				"  return PAGE_SIZE\n"+
				"}\n"),
	}
	c.Persists = []fixturetest.Variant{
		one("rename it", file, "export const BATCH = 50\n\nexport function open(): number {\n  return BATCH\n}\n"),
		one("declare it with let", file,
			"export let PAGE_SIZE = 50\n\nexport function open(): number {\n  return PAGE_SIZE\n}\n"),
	}
	c.Gaps = []fixturetest.Variant{
		one("re-export the whole module through a barrel", "src/index.ts", "export * from \"./store\"\n"),
	}
	c.Fixes = []fixturetest.Variant{one("drop the export", file,
		"const PAGE_SIZE = 50\n\nexport function open(): number {\n  return PAGE_SIZE\n}\n")}

	return c
}

const ruleClone = "x/clone-function"

// cloneShape is one function written out in two files of a language. Lead
// opens each file, and every other field but the paths is function text.
type cloneShape struct {
	name, fileA, fileB string
	lead               string
	sigA, sigB, body   string
	renamedSig         string // B's first line under another name
	relabeled          string // B with other locals and literals
	commented          string // B with comments added
	gapName, gap       string // B changed just enough to hash apart
	fixA, fixB         string // whole files
}

func (s cloneShape) build() advCase {
	c := advCase{rules: []string{ruleClone}}
	c.Name = s.name
	c.Base = fixturetest.Tree{s.fileA: s.lead + s.sigA + s.body, s.fileB: s.lead + s.sigB + s.body}
	c.Dodges = []fixturetest.Variant{
		one("rename the locals and change the separator", s.fileB, s.lead+s.relabeled),
		one("add comments to one copy", s.fileB, s.lead+s.commented),
	}
	c.Persists = []fixturetest.Variant{one("rename one copy", s.fileB, s.lead+s.renamedSig+s.body)}
	c.Gaps = []fixturetest.Variant{one(s.gapName, s.fileB, s.lead+s.gap)}
	c.Fixes = []fixturetest.Variant{
		{Name: "keep one function and use it from both", Files: fixturetest.Tree{s.fileA: s.fixA, s.fileB: s.fixB}},
	}

	return c
}

const goParseBody = `	out := map[string]string{}
	for _, line := range lines {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}

	return out
}
`

func goCloneCase() advCase {
	const lead = "package conf\n\nimport \"strings\"\n\n"
	const sig = "func parseHeaders(lines []string) map[string]string {\n"

	return cloneShape{
		name: "go: the same function in two files", fileA: "conf/env.go", fileB: "conf/headers.go", lead: lead,
		sigA: "func parseEnv(lines []string) map[string]string {\n", sigB: sig, body: goParseBody,
		renamedSig: "func readHeaders(lines []string) map[string]string {\n",
		relabeled: `func parseHeaders(rows []string) map[string]string {
	result := map[string]string{}
	for _, row := range rows {
		name, text, found := strings.Cut(row, ":")
		if !found {
			continue
		}
		result[strings.TrimSpace(name)] = strings.TrimSpace(text)
	}

	return result
}
`,
		commented: `// parseHeaders reads header lines.
func parseHeaders(lines []string) map[string]string {
	out := map[string]string{}
	// every line is one header
	for _, line := range lines {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue // not a header
		}
		out[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}

	return out
}
`,
		gapName: "add a statement that does nothing to one copy",
		gap:     sig + "\t_ = len(lines)\n" + goParseBody,
		fixA:    lead + "func parsePairs(lines []string) map[string]string {\n" + goParseBody,
		fixB:    "package conf\n\nfunc headers(lines []string) map[string]string { return parsePairs(lines) }\n",
	}.build()
}

const pyParseBody = `    out = {}
    for line in lines:
        key, sep, value = line.partition("=")
        if not sep:
            continue
        out[key.strip()] = value.strip()
    if not out:
        raise ValueError("empty")
    return out
`

func pyCloneCase() advCase {
	const sig = "def parse_headers(lines):\n"

	return cloneShape{
		name: "python: the same function in two files", fileA: "conf/env.py", fileB: "conf/headers.py",
		sigA: "def parse_env(lines):\n", sigB: sig, body: pyParseBody, renamedSig: "def read_headers(lines):\n",
		relabeled: `def parse_headers(rows):
    result = {}
    for row in rows:
        name, found, text = row.partition(":")
        if not found:
            continue
        result[name.strip()] = text.strip()
    if not result:
        raise ValueError("no headers")
    return result
`,
		commented: `def parse_headers(lines):
    out = {}
    # every line is one header
    for line in lines:
        key, sep, value = line.partition("=")
        if not sep:
            continue  # not a header
        out[key.strip()] = value.strip()
    if not out:
        raise ValueError("empty")
    return out
`,
		gapName: "add a docstring to one copy",
		gap:     sig + "    \"\"\"Read header lines.\"\"\"\n" + pyParseBody,
		fixA:    "def parse_pairs(lines):\n" + pyParseBody,
		fixB:    "from conf.env import parse_pairs\n\nheaders = parse_pairs\n",
	}.build()
}

const tsParseBody = `  const out: Record<string, string> = {}
  for (const line of lines) {
    const at = line.indexOf("=")
    if (at < 0) {
      continue
    }
    out[line.slice(0, at).trim()] = line.slice(at + 1).trim()
  }
  return out
}
`

func tsCloneCase() advCase {
	const typed = "(lines: string[]): Record<string, string> {\n"

	return cloneShape{
		name: "typescript: the same function in two files", fileA: "src/env.ts", fileB: "src/headers.ts",
		sigA: "export function parseEnv" + typed, sigB: "export function parseHeaders" + typed, body: tsParseBody,
		renamedSig: "export function readHeaders" + typed,
		relabeled: `export function parseHeaders(rows: string[]): Record<string, string> {
  const result: Record<string, string> = {}
  for (const row of rows) {
    const colon = row.indexOf(":")
    if (colon < 0) {
      continue
    }
    result[row.slice(0, colon).trim()] = row.slice(colon + 1).trim()
  }
  return result
}
`,
		commented: `export function parseHeaders(lines: string[]): Record<string, string> {
  const out: Record<string, string> = {}
  // every line is one header
  for (const line of lines) {
    const at = line.indexOf("=")
    if (at < 0) {
      continue // not a header
    }
    out[line.slice(0, at).trim()] = line.slice(at + 1).trim()
  }
  return out
}
`,
		gapName: "drop the return type from one copy",
		gap:     "export function parseHeaders(lines: string[]) {\n" + tsParseBody,
		fixA:    "export function parsePairs" + typed + tsParseBody,
		fixB:    "import { parsePairs } from \"./env\"\n\nexport const parseHeaders = parsePairs\n",
	}.build()
}

const ruleDupTest = "x/dup-test-case"

const goTestBody = `	got := add(1, 2)
	want := 3
	if got != want {
		t.Fatalf("got %d, want %d", got, want)
	}
	if got < 0 {
		t.Fatal("negative")
	}
	t.Log(got)
}
`

func goDupTestCase() advCase {
	const file = "calc/add_test.go"
	const head = "package calc\n\nimport \"testing\"\n\n"
	c := single(ruleDupTest, "go: two tests that differ only in their data", file,
		head+"func TestAddSmall(t *testing.T) {\n"+goTestBody+"\nfunc TestAddLarge(t *testing.T) {\n"+goTestBody)
	c.Dodges = []fixturetest.Variant{
		one("change the data and the local names in one", file,
			head+"func TestAddSmall(t *testing.T) {\n"+goTestBody+`
func TestAddLarge(t *testing.T) {
	sum := add(1000, 2000)
	expected := 3000
	if sum != expected {
		t.Fatalf("sum %d, expected %d", sum, expected)
	}
	if sum < 0 {
		t.Fatal("below zero")
	}
	t.Log(sum)
}
`),
		one("add a comment to one", file,
			head+"func TestAddSmall(t *testing.T) {\n"+goTestBody+
				"\nfunc TestAddLarge(t *testing.T) {\n\t// large numbers take the same path\n"+goTestBody),
	}
	c.Persists = []fixturetest.Variant{
		one("rename one test", file,
			head+"func TestAddSmall(t *testing.T) {\n"+goTestBody+"\nfunc TestAddHuge(t *testing.T) {\n"+goTestBody),
	}
	c.Gaps = []fixturetest.Variant{
		one("add a statement that does nothing to one", file,
			head+"func TestAddSmall(t *testing.T) {\n"+goTestBody+"\n"+
				"func TestAddLarge(t *testing.T) {\n"+
				"\tt.Helper()\n"+goTestBody),
		{Name: "move one test to another file", Files: fixturetest.Tree{
			file: "package calc\n" +
				"\n" +
				"import \"testing\"\n" +
				"\n" +
				"func TestAddSmall(t *testing.T) {\n" + goTestBody,
			"calc/big_test.go": "package calc\n" +
				"\n" +
				"import \"testing\"\n" +
				"\n" +
				"func TestAddLarge(t *testing.T) {\n" + goTestBody,
			"calc/placeholder.go": "package calc\n",
		}},
	}
	c.Fixes = []fixturetest.Variant{one("fold them into one table", file, head+`func TestAdd(t *testing.T) {
	for _, tc := range []struct{ a, b, want int }{{1, 2, 3}, {1000, 2000, 3000}} {
		if got := add(tc.a, tc.b); got != tc.want {
			t.Errorf("add(%d, %d) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
`)}

	return c
}

const pyTestBody = `    got = add(1, 2)
    want = 3
    assert got == want
    assert got > 0
    assert isinstance(got, int)
    assert str(got) == "3"
    assert got != 0
    print(got)
`

func pyDupTestCase() advCase {
	const file = "tests/test_add.py"
	c := single(ruleDupTest, "python: two tests that differ only in their data", file,
		"def test_add_small():\n"+pyTestBody+"\n\ndef test_add_large():\n"+pyTestBody)
	c.Dodges = []fixturetest.Variant{
		one("change the data and the local names in one", file, "def test_add_small():\n"+pyTestBody+`

def test_add_large():
    total = add(1000, 2000)
    expected = 3000
    assert total == expected
    assert total > 0
    assert isinstance(total, int)
    assert str(total) == "3000"
    assert total != 0
    print(total)
`),
		one("add a comment to one", file,
			"def test_add_small():\n"+pyTestBody+"\n"+
				"\n"+
				"def test_add_large():\n"+
				"    # large numbers take the same path\n"+pyTestBody),
	}
	c.Persists = []fixturetest.Variant{
		one("rename one test", file, "def test_add_small():\n"+pyTestBody+"\n\ndef test_add_huge():\n"+pyTestBody),
	}
	c.Gaps = []fixturetest.Variant{
		one("add a docstring to one", file,
			"def test_add_small():\n"+pyTestBody+"\n"+
				"\n"+
				"def test_add_large():\n"+
				"    \"\"\"Large numbers.\"\"\"\n"+pyTestBody),
	}
	c.Fixes = []fixturetest.Variant{one("parametrize one test", file, `import pytest


@pytest.mark.parametrize(("a", "b", "want"), [(1, 2, 3), (1000, 2000, 3000)])
def test_add(a, b, want):
    assert add(a, b) == want
`)}

	return c
}

const tsTestBody = `  const got = add(1, 2)
  const want = 3
  expect(got).toBe(want)
  expect(got).toBeGreaterThan(0)
  expect(typeof got).toBe("number")
  expect(String(got)).toBe("3")
  expect(got).not.toBe(0)
})
`

func tsDupTestCase() advCase {
	const file = "src/add.test.ts"
	c := single(ruleDupTest, "typescript: two tests that differ only in their data", file,
		"it(\"adds small numbers\", () => {\n"+tsTestBody+"\nit(\"adds large numbers\", () => {\n"+tsTestBody)
	c.Dodges = []fixturetest.Variant{
		one("change the data and the local names in one", file, "it(\"adds small numbers\", () => {\n"+tsTestBody+`
it("adds large numbers", () => {
  const total = add(1000, 2000)
  const expected = 3000
  expect(total).toBe(expected)
  expect(total).toBeGreaterThan(0)
  expect(typeof total).toBe("number")
  expect(String(total)).toBe("3000")
  expect(total).not.toBe(0)
})
`),
		one("add a comment to one", file,
			"it(\"adds small numbers\", () => {\n"+tsTestBody+
				"\nit(\"adds large numbers\", () => {\n  // large numbers take the same path\n"+tsTestBody),
	}
	c.Persists = []fixturetest.Variant{
		one("rename one test", file,
			"it(\"adds small numbers\", () => {\n"+tsTestBody+"\nit(\"adds huge numbers\", () => {\n"+tsTestBody),
	}
	c.Gaps = []fixturetest.Variant{
		one("mark one of them only", file,
			"it(\"adds small numbers\", () => {\n"+tsTestBody+"\nit.only(\"adds large numbers\", () => {\n"+tsTestBody),
	}
	c.Fixes = []fixturetest.Variant{one("fold them into one table", file, `it.each([
  [1, 2, 3],
  [1000, 2000, 3000],
])("adds %i and %i", (a, b, want) => {
  expect(add(a, b)).toBe(want)
})
`)}

	return c
}

const ruleSingleImpl = "x/single-impl-iface"

func goSingleImplCase() advCase {
	const file = "store/store.go"
	c := tree("go: an interface with one method and one implementation", `
-- store/store.go --
package store

type getter interface {
	Lookup(key string) string
}
-- store/mem.go --
package store

type mem struct{}

func (mem) Lookup(key string) string { return key }
`, ruleSingleImpl)
	c.Dodges = []fixturetest.Variant{
		one("add a doc comment and a second type beside it", file, `package store

type options struct{}

// getter is the seam for other stores.
type getter interface {
	Lookup(key string) string
}
`),
	}
	c.Persists = []fixturetest.Variant{
		one("rename the interface", file, "package store\n\ntype reader interface {\n\tLookup(key string) string\n}\n"),
	}
	c.Gaps = []fixturetest.Variant{
		{Name: "give it a second method", Files: fixturetest.Tree{
			file: "package store\n" +
				"\n" +
				"type getter interface {\n" +
				"\tLookup(key string) string\n" +
				"\tClose() error\n" +
				"}\n",
			"store/mem.go": "package store\n" +
				"\n" +
				"type mem struct{}\n" +
				"\n" +
				"func (mem) Lookup(key string) string { return key }\n" +
				"\n" +
				"func (mem) Close() error { return nil }\n",
		}},
		one("let an unrelated type share the method name", "index/index.go",
			"package index\n\ntype table struct{}\n\nfunc (table) Lookup(row int) int { return row }\n"),
	}
	c.Fixes = []fixturetest.Variant{{Name: "use the one type directly", Remove: []string{file}}}

	return c
}

func tsSingleImplCase() advCase {
	const file = "src/store.ts"
	c := tree("typescript: an interface with one member and one implementation", `
-- src/store.ts --
export interface Store {
  lookup(key: string): string
}
-- src/mem.ts --
import type { Store } from "./store"

export class MemStore implements Store {
  lookup(key: string): string {
    return key
  }
}
`, ruleSingleImpl)
	c.Dodges = []fixturetest.Variant{
		one("add a doc comment", file, "/** Store is the seam for other stores. */\n"+
			"export interface Store {\n"+
			"  lookup(key: string): string\n"+
			"}\n"),
	}
	c.Persists = []fixturetest.Variant{
		{Name: "rename the interface", Files: fixturetest.Tree{
			file: "export interface Reader {\n  lookup(key: string): string\n}\n",
			"src/mem.ts": "import type { Reader } from \"./store\"\n" +
				"\n" +
				"export class MemStore implements Reader {\n" +
				"  lookup(key: string): string {\n" +
				"    return key\n" +
				"  }\n" +
				"}\n",
		}},
	}
	c.Gaps = []fixturetest.Variant{
		one("drop the implements clause and rely on structure", "src/mem.ts",
			"export class MemStore {\n  lookup(key: string): string {\n    return key\n  }\n}\n"),
		one("give it a second member", file,
			"export interface Store {\n  lookup(key: string): string\n  readonly name?: string\n}\n"),
	}
	c.Fixes = []fixturetest.Variant{{Name: "use the class itself", Remove: []string{file}, Files: fixturetest.Tree{
		"src/mem.ts": "export class MemStore {\n  lookup(key: string): string {\n    return key\n  }\n}\n",
	}}}

	return c
}

func goUnusedOptionCase() advCase {
	const file = "client/options.go"
	c := tree("go: an option no caller passes", `
-- client/options.go --
package client

type Option func(*Client)

func WithRetries(n int) Option { return func(c *Client) { c.retries = n } }

func WithTimeout(seconds int) Option { return func(c *Client) { c.timeout = seconds } }
-- cmd/main.go --
package main

import "example.com/app/client"

func main() { client.New(client.WithRetries(3)) }
`, "x/unused-option")
	c.Dodges = []fixturetest.Variant{
		one("document it", file, `package client

type Option func(*Client)

func WithRetries(n int) Option { return func(c *Client) { c.retries = n } }

// WithTimeout sets the timeout. Callers will want WithTimeout soon.
func WithTimeout(seconds int) Option { return func(c *Client) { c.timeout = seconds } }
`),
	}
	c.Persists = []fixturetest.Variant{
		one("rename it", file, `package client

type Option func(*Client)

func WithRetries(n int) Option { return func(c *Client) { c.retries = n } }

func WithDeadline(seconds int) Option { return func(c *Client) { c.timeout = seconds } }
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("drop the With prefix", file, `package client

type Option func(*Client)

func WithRetries(n int) Option { return func(c *Client) { c.retries = n } }

func Timeout(seconds int) Option { return func(c *Client) { c.timeout = seconds } }
`),
		one("pass it in a test and nowhere else", "client/options_test.go",
			"package client\n\nimport \"testing\"\n\nfunc TestTimeout(t *testing.T) { New(WithTimeout(1)) }\n"),
	}
	c.Fixes = []fixturetest.Variant{one("delete it", file, `package client

type Option func(*Client)

func WithRetries(n int) Option { return func(c *Client) { c.retries = n } }
`)}

	return c
}

const ruleSpent = "x/spent-test"

// spentCase builds the three variants a path rule can have: an edit of the
// content, a rename to another spent word, and a rename to a neutral name.
func spentCase(name, file, other, neutral, body string) advCase {
	c := single(ruleSpent, name, file, body)
	c.Dodges = []fixturetest.Variant{one("edit the file and keep its name", file, body+"\n")}
	c.Persists = []fixturetest.Variant{
		{Name: "rename it to another one-shot word", Remove: []string{file}, Files: fixturetest.Tree{other: body}},
	}
	c.Gaps = []fixturetest.Variant{
		{Name: "rename it to a word that says nothing", Remove: []string{file}, Files: fixturetest.Tree{neutral: body}},
	}
	c.Fixes = []fixturetest.Variant{{Name: "delete the test with the code it tested", Remove: []string{file}}}

	return c
}

func goSpentTestCase() advCase {
	return spentCase("go: a test for a migration that already ran", "store/migrate_v2_test.go",
		"store/backfill_v2_test.go", "store/schema_v2_test.go",
		"package store\n\nimport \"testing\"\n\nfunc TestV2(t *testing.T) { upgrade(t) }\n")
}

func pySpentTestCase() advCase {
	return spentCase("python: a test for a migration that already ran", "tests/test_migration_0042.py",
		"tests/test_backfill_0042.py", "tests/test_schema_0042.py", "def test_0042():\n    assert upgrade() == 1\n")
}

func tsSpentTestCase() advCase {
	return spentCase("typescript: a test for a backfill that already ran", "src/backfill-users.test.ts",
		"src/one-shot-users.test.ts", "src/users-shape.test.ts",
		"it(\"fills\", () => {\n  expect(fill()).toBe(1)\n})\n")
}

const ruleStaleConfig = "x/stale-config"

// staleConfigCase builds a case over a config file with one key no code
// reads. The reader file reads timeout, and each variant adds or edits files.
func staleConfigCase(name, reader, readerBody string) advCase {
	c := advCase{rules: []string{ruleStaleConfig}, config: []string{"settings.toml"}}
	c.Name = name
	c.Base = fixturetest.Tree{"settings.toml": "timeout = 5\nretry_limit = 3\n", reader: readerBody}
	c.Dodges = []fixturetest.Variant{
		one("reorder the keys and add a comment", "settings.toml", "# how often to retry\n"+
			"retry_limit = 3\n"+
			"timeout = 5\n"),
	}
	c.Persists = []fixturetest.Variant{
		one("move the key under a table", "settings.toml", "timeout = 5\n\n[http]\nretry_limit = 3\n"),
		one("change its value", "settings.toml", "timeout = 5\nretry_limit = 10\n"),
	}
	c.Fixes = []fixturetest.Variant{one("delete the key", "settings.toml", "timeout = 5\n")}

	return c
}

func goStaleConfigCase() advCase {
	c := staleConfigCase("go: a config key no code reads", "app/config.go",
		"package app\n\ntype config struct {\n\tTimeout int `toml:\"timeout\"`\n}\n")
	c.Gaps = []fixturetest.Variant{
		one("name an unrelated variable like the key", "app/retry.go",
			"package app\n\nfunc attempts() int {\n\tretryLimit := 3\n\n\treturn retryLimit\n}\n"),
	}

	return c
}

func pyStaleConfigCase() advCase {
	c := staleConfigCase("python: a config key no code reads", "app/config.py",
		"def timeout(cfg):\n    return cfg[\"timeout\"]\n")
	c.Gaps = []fixturetest.Variant{
		one("write the key in a string nothing reads config with", "app/messages.py",
			"HELP = {\"retry_limit\": \"how often to retry\"}\n"),
	}

	return c
}

func tsStaleConfigCase() advCase {
	c := staleConfigCase("typescript: a config key no code reads", "src/config.ts",
		"export function timeout(cfg: Settings): number {\n  return cfg.timeout\n}\n")
	c.Gaps = []fixturetest.Variant{
		one("declare the field in a type and never read it", "src/settings.ts",
			"export interface Settings {\n  timeout: number\n  retryLimit: number\n}\n"),
	}

	return c
}

func goStaleProseCase() advCase {
	const file = "store/store.go"
	c := single(ruleXStaleProseID, "go: a comment names a function that is gone", file, `package store

// open retries the way fetchRecords() does.
func open() {}
`)
	c.Dodges = []fixturetest.Variant{
		one("move the comment and add code", file, `package store

func closeAll() {}

// open retries the way fetchRecords() does.
func open() {}
`),
	}
	c.Persists = []fixturetest.Variant{
		one("write it as a block comment", file, "package store\n"+
			"\n"+
			"/* open retries the way fetchRecords() does. */\n"+
			"func open() {}\n"),
		one("put the name in backticks", file, "package store\n"+
			"\n"+
			"// open retries the way `fetchRecords` does.\n"+
			"func open() {}\n"),
	}
	c.Gaps = []fixturetest.Variant{
		one("keep the name alive in a string nothing reads", "store/names.go",
			"package store\n\nvar legacy = []string{\"fetchRecords\"}\n"),
		one("phrase it as what the code no longer does", file,
			"package store\n\n// open retries on its own instead of `fetchRecords()`.\nfunc open() {}\n"),
	}
	c.Fixes = []fixturetest.Variant{one("say what open does", file, "package store\n"+
		"\n"+
		"// open retries three times.\n"+
		"func open() {}\n")}

	return c
}

// ruleXStaleProseID is the stale prose rule's id.
const ruleXStaleProseID = "x/stale-prose"

func pyStaleProseCase() advCase {
	const file = "app/store.py"
	c := single(ruleXStaleProseID, "python: a comment names a function that is gone", file,
		`# open_db retries the way fetch_records() does
def open_db():
    return 1
`)
	c.Dodges = []fixturetest.Variant{
		one("move the comment and add code", file, `LIMIT = 3


# open_db retries the way fetch_records() does
def open_db():
    return 1
`),
	}
	c.Persists = []fixturetest.Variant{
		one("move it into the docstring", file, "def open_db():\n"+
			"    \"\"\"Retry the way fetch_records() does.\"\"\"\n"+
			"    return 1\n"),
	}
	c.Gaps = []fixturetest.Variant{
		one("keep the name alive in a string nothing reads", "app/names.py", "LEGACY = [\"fetch_records\"]\n"),
		one("phrase it as what the code no longer does", file,
			"# open_db retries on its own rather than `fetch_records()`\ndef open_db():\n    return 1\n"),
	}
	c.Fixes = []fixturetest.Variant{one("say what open_db does", file, "# open_db retries three times\n"+
		"def open_db():\n"+
		"    return 1\n")}

	return c
}

func tsStaleProseCase() advCase {
	const file = "src/store.ts"
	c := tree("typescript: the README names a function that is gone", `
-- src/store.ts --
export function open(): number {
  return 1
}
-- README.md --
# Store

Call `+"`fetchRecords()`"+` before `+"`open()`"+`.
`, ruleXStaleProseID)
	c.Dodges = []fixturetest.Variant{
		one("edit the code and leave the README", file, "export function open(): number {\n  return 2\n}\n"),
	}
	c.Persists = []fixturetest.Variant{
		one("reword the sentence around the name", "README.md", "# Store\n\nYou must call `fetchRecords()` first.\n"),
	}
	c.Gaps = []fixturetest.Variant{
		one("move the example into a fenced block", "README.md",
			"# Store\n\n```ts\nfetchRecords()\nopen()\n```\n"),
		one("keep the name alive in a string nothing reads", "src/names.ts",
			"export const legacy = [\"fetchRecords\"]\n"),
	}
	c.Fixes = []fixturetest.Variant{one("name the function that exists", "README.md", "# Store\n\nCall `open()`.\n")}

	return c
}

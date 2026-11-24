package rules_test

import (
	"maps"
	"slices"
	"testing"
)

// declCase is one file and the names its fingerprint must hold. Kinds lists
// "name=kind" for the exported names that keep a declaration kind.
type declCase struct {
	name     string
	path     string
	src      string
	declared []string
	exported []string
	kinds    []string
	entries  []string
}

func goDeclCases() []declCase {
	return []declCase{
		{
			name: "go functions, types, constants, and variables, grouped or not",
			path: "p/a.go",
			src: `package p

type Store struct{}

type (
	Reader interface{ Read() }
	alias  = Store
)

const Limit, floor = 10, 1

const (
	First = iota
	second
)

var Default, _ = Store{}, 0

var (
	Shared int
	local  string
)

func New() *Store { return nil }

func (s *Store) Close() {}

func helper() {}
`,
			declared: []string{
				"Default", "First", "Limit", "New", "Reader", "Shared", "Store", "alias", "floor", "helper", "local",
				"second",
			},
			exported: []string{"Default", "First", "Limit", "New", "Reader", "Shared", "Store"},
			kinds: []string{
				"Default=value", "First=value", "Limit=value", "Reader=type", "Shared=value", "Store=type",
			},
		},
		{
			name: "go generic function and an option helper",
			path: "p/b.go",
			src: `package p

func Map[T any](xs []T) []T { return xs }

func WithLimit(n int) Option { return nil }
`,
			declared: []string{"Map", "WithLimit"},
			exported: []string{"Map", "WithLimit"},
		},
	}
}

func pyDeclCases() []declCase {
	return []declCase{
		{
			name: "python functions and classes, decorated or not, and assignments",
			path: "pkg/a.py",
			src: `import functools

__all__ = ["load", "Config"]
__version__ = "1.0"

LIMIT = 10
_cache: dict = {}
T = TypeVar("T")
ratio: float


def load():
    return 1


async def fetch():
    return 2


@functools.cache
def cached():
    return 3


class Config:
    def method(self):
        nested = 1
        return nested


@dataclass
class _Point:
    x: int


if LIMIT:
    conditional = 1
`,
			declared: []string{
				"Config", "LIMIT", "T", "_Point", "__all__", "__version__", "_cache", "cached", "fetch", "load",
				"ratio",
			},
			exported: []string{"Config", "LIMIT", "T", "cached", "fetch", "load", "ratio"},
			kinds:    []string{"LIMIT=assigned", "T=assigned", "cached=decorated", "ratio=assigned"},
			entries:  []string{":load", ":Config"},
		},
		{
			name: "python __all__ as a tuple, extended later",
			path: "pkg/b.py",
			src: "__all__ = (\"one\",)\n" +
				"__all__ += [\"two\"]\n" +
				"\n" +
				"\n" +
				"def one():\n" +
				"    pass\n" +
				"\n" +
				"\n" +
				"def two():\n" +
				"    pass\n",
			declared: []string{"__all__", "one", "two"},
			exported: []string{"one", "two"},
			entries:  []string{":one", ":two"},
		},
	}
}

func tsDeclCases() []declCase {
	return []declCase{
		{
			name: "typescript declarations of every kind, exported or not",
			path: "src/a.ts",
			src: `export function run(): void {}
function hidden(): void {}
export class Store {
  get(): number { return 1 }
}
class Local {}
export abstract class Base {}
export const LIMIT = 10, FLOOR = 1
const inner = 2
export let counter = 0
export interface Options { depth: number }
export type $Infer<T> = T
export enum Mode { A, B }
export declare function ambient(): void
export function overloaded(a: string): void
export function overloaded(a: number): void
export function overloaded(a: unknown): void {}
function later(): void {}
export { later, inner as renamed }
`,
			declared: []string{
				"$Infer", "Base", "FLOOR", "LIMIT", "Local", "Mode", "Options", "Store", "ambient", "counter", "hidden",
				"inner", "later", "overloaded", "run",
			},
			exported: []string{
				"$Infer", "Base", "FLOOR", "LIMIT", "Mode", "Options", "Store", "ambient", "counter", "later",
				"overloaded", "renamed", "run",
			},
			kinds: []string{"Base=class", "Store=class"},
		},
		{
			name: "typescript default exports and a re-export declare nothing by a new name",
			path: "src/b.tsx",
			src: "export default function App() { return null }\n" +
				"export { helper } from \"./helper\"\n" +
				"export * from \"./all\"\n",
			declared: []string{"App"},
			exported: []string{"App", "helper"},
		},
		{
			name: "typescript names inside a function, a namespace, or a module block are not top-level",
			path: "src/c.ts",
			src: "function outer() {\n" +
				"  const local = 1\n" +
				"  function nested() {}\n" +
				"}\n" +
				"declare module \"x\" {\n" +
				"  export function augmented(): void\n" +
				"}\n",
			declared: []string{"outer"},
		},
	}
}

func TestCollectRecordsEveryTopLevelDeclaration(t *testing.T) {
	t.Parallel()
	for _, tc := range slices.Concat(goDeclCases(), pyDeclCases(), tsDeclCases()) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fp := printOf(t, tc.path, tc.src)
			if got := slices.Sorted(maps.Keys(fp.Declared)); !slices.Equal(got, tc.declared) {
				t.Errorf("declared = %v, want %v", got, tc.declared)
			}
			if got := slices.Sorted(maps.Keys(fp.Exported)); !slices.Equal(got, tc.exported) {
				t.Errorf("exported = %v, want %v", got, tc.exported)
			}
			var kinds []string
			for _, name := range slices.Sorted(maps.Keys(fp.Kinds)) {
				kinds = append(kinds, name+"="+fp.Kinds[name])
			}
			if !slices.Equal(kinds, tc.kinds) {
				t.Errorf("kinds = %v, want %v", kinds, tc.kinds)
			}
			if !slices.Equal(fp.EntryRefs, tc.entries) {
				t.Errorf("entry refs = %v, want %v", fp.EntryRefs, tc.entries)
			}
		})
	}
}

// TestDeclaredLineIsTheDeclarationsOwn pins the line a finding is reported
// on: the def below its decorators, and each name of a grouped declaration on
// its own line.
func TestDeclaredLineIsTheDeclarationsOwn(t *testing.T) {
	t.Parallel()
	cases := []struct {
		want map[string]int
		name string
		path string
		src  string
	}{
		{
			name: "python def under two decorators", path: "a.py",
			src:  "@one\n@two\ndef run():\n    pass\n",
			want: map[string]int{"run": 3},
		},
		{
			name: "go grouped constants", path: "a.go",
			src:  "package p\n\nconst (\n\tA = 1\n\tB = 2\n)\n",
			want: map[string]int{"A": 4, "B": 5},
		},
		{
			name: "typescript declarators of one statement", path: "a.ts",
			src:  "export const a = 1,\n  b = 2\n",
			want: map[string]int{"a": 1, "b": 2},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := printOf(t, tc.path, tc.src).Declared; !maps.Equal(got, tc.want) {
				t.Errorf("declared = %v, want %v", got, tc.want)
			}
		})
	}
}

// exportFeedCases pin which newly recorded kinds the two export rules read.
func exportFeedCases() []crossCase {
	const dead = "x/dead-export"
	const private = "x/could-be-private"
	goKinds := "package p\n\ntype Unused struct{}\n\nconst Limit = 1\n\nvar Shared = Limit\n"

	return []crossCase{
		{
			name: "go type, constant, and variable are recorded and not reported as dead", rule: dead,
			files: map[string]string{"a.go": goKinds},
		},
		{
			name: "go constant used only in its file is not reported as private", rule: private,
			files: map[string]string{"a.go": goKinds},
		},
		{
			name: "typescript class is not reported", rule: dead,
			files: map[string]string{"a.ts": "export class Widget {}\n"},
		},
		{
			name: "typescript constant nothing imports is dead", rule: dead, want: 1,
			files: map[string]string{"a.ts": "export const LIMIT = 1\n"},
		},
		{
			name: "typescript dollar-named type nothing imports is dead", rule: dead, want: 1,
			files: map[string]string{"a.ts": "export type $Infer<T> = T\n"},
		},
		{
			name: "python assignment nothing reads is never dead", rule: dead,
			files: map[string]string{"conf.py": "html_theme = \"flask\"\n"},
		},
		{
			name: "python assignment read only in its module could be private", rule: private, want: 1,
			files: map[string]string{"a.py": "LIMIT = 1\n\n\ndef _f():\n    return LIMIT\n"},
		},
		{
			name: "python decorated function nothing names is never dead", rule: dead,
			files: map[string]string{"a.py": "@app.route(\"/\")\ndef index():\n    return 1\n"},
		},
		{
			name: "python decorated function used only in its module could be private", rule: private, want: 1,
			files: map[string]string{"a.py": "@cache\ndef load():\n    return 1\n\n\n_X = load()\n"},
		},
		{
			name: "python name listed in __all__ is public", rule: private,
			files: map[string]string{"a.py": "__all__ = [\"LIMIT\"]\nLIMIT = 1\n_X = LIMIT\n"},
		},
		{
			name: "python module with __all__ still reports what it leaves out, starred or not", rule: dead, want: 1,
			files: map[string]string{
				"pkg/__init__.py": "from pkg.a import *\n",
				"pkg/a.py":        "__all__ = [\"kept\"]\n\n\ndef kept():\n    pass\n\n\ndef dropped():\n    pass\n",
			},
		},
		{
			name: "python module star-imported without __all__ hands every name on", rule: dead,
			files: map[string]string{
				"pkg/__init__.py": "from pkg.a import *\n",
				"pkg/a.py":        "def kept():\n    pass\n",
			},
		},
		{
			name: "typescript module re-exported whole hands every name on", rule: dead,
			files: map[string]string{
				"src/index.ts": "export * from \"./a\"\n",
				"src/a.ts":     "export function kept() {}\n",
			},
		},
		{
			name: "typescript module re-exported under a namespace hands every name on", rule: dead,
			files: map[string]string{
				"src/index.ts": "export * as a from \"./a\"\n",
				"src/a.ts":     "export function kept() {}\n",
			},
		},
		{
			name: "typescript module imported as a namespace is still judged by name", rule: dead, want: 1,
			files: map[string]string{
				"src/index.ts": "import * as a from \"./a\"\n\nconsole.log(a)\n",
				"src/a.ts":     "export function unused() {}\n",
			},
		},
	}
}

func TestExportRulesReadOnlyTheKindsThatHold(t *testing.T) {
	t.Parallel()
	for _, tc := range exportFeedCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := countRule(flatten(crossFileOf(t, tc.files, true)), tc.rule); got != tc.want {
				t.Errorf("%s hits = %d, want %d", tc.rule, got, tc.want)
			}
		})
	}
}

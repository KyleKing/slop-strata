package rules_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/fixturetest"
	"github.com/kyleking/slop-strata/internal/rules"
)

const ruleImportCycle = "x/import-cycle"

// cycleFindings runs the cross-file passes over a tree and answers the import
// cycle findings with their IDs, ordered by path.
func cycleFindings(t *testing.T, tree fixturetest.Tree) []rules.Finding {
	t.Helper()
	byPath := crossFileOf(t, tree, true)
	var out []rules.Finding
	for _, path := range slices.Sorted(mapKeys(byPath)) {
		found := rules.Identify(path, byPath[path])
		for i := range found {
			if found[i].Rule == ruleImportCycle {
				out = append(out, found[i])
			}
		}
	}

	return out
}

func mapKeys[V any](m map[string]V) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// cycleIDs adapts cycleFindings to the variant helper.
func cycleIDs(t *testing.T) fixturetest.Eval {
	t.Helper()

	return func(tree fixturetest.Tree) []string {
		found := cycleFindings(t, tree)
		ids := make([]string, 0, len(found))
		for i := range found {
			ids = append(ids, found[i].ID)
		}

		return ids
	}
}

func pythonCycleCase() fixturetest.Case {
	return fixturetest.Case{
		Name: "python: two modules import each other",
		Base: fixturetest.Files(`
-- pkg/__init__.py --
-- pkg/a.py --
from pkg.b import helper


class Foo:
    def run(self):
        return helper(self)
-- pkg/b.py --
import os

from pkg.a import Foo


def helper(x):
    return isinstance(x, Foo) and os.sep
`),
		Dodges: pythonCycleDodges(),
		Fixes:  pythonCycleFixes(),
	}
}

func pythonCycleDodges() []fixturetest.Variant {
	return []fixturetest.Variant{
		{Name: "reorder imports, add blank lines and comments", Files: fixturetest.Files(`
-- pkg/b.py --
# the cycle is still here

from pkg.a import Foo

import os  # moved below


def helper(x):
    return isinstance(x, Foo) and os.sep
`)},
		{Name: "rename the local alias", Files: fixturetest.Files(`
-- pkg/b.py --
import os

from pkg.a import Foo as Thing


def helper(x):
    return isinstance(x, Thing) and os.sep
`)},
		{Name: "move the import lower in the module", Files: fixturetest.Files(`
-- pkg/b.py --
import os


def helper(x):
    return isinstance(x, Foo) and os.sep


from pkg.a import Foo  # noqa: E402
`)},
		{Name: "spell it as a relative import", Files: fixturetest.Files(`
-- pkg/b.py --
import os

from .a import Foo


def helper(x):
    return isinstance(x, Foo) and os.sep
`)},
		{Name: "import the module instead of the name", Files: fixturetest.Files(`
-- pkg/b.py --
import os

import pkg.a


def helper(x):
    return isinstance(x, pkg.a.Foo) and os.sep
`)},
		{Name: "wrap the import in a module-level try", Files: fixturetest.Files(`
-- pkg/b.py --
import os

try:
    from pkg.a import Foo
except ImportError:
    Foo = None


def helper(x):
    return isinstance(x, Foo) and os.sep
`)},
		{Name: "re-export through the package __init__", Files: fixturetest.Files(`
-- pkg/__init__.py --
from pkg.a import Foo

__all__ = ["Foo"]
-- pkg/b.py --
import os

from pkg import Foo


def helper(x):
    return isinstance(x, Foo) and os.sep
`)},
	}
}

func pythonCycleFixes() []fixturetest.Variant {
	return []fixturetest.Variant{
		{Name: "extract the shared class into a third module", Files: fixturetest.Files(`
-- pkg/base.py --
class Foo:
    def run(self):
        return self
-- pkg/a.py --
from pkg.b import helper
from pkg.base import Foo

print(helper(Foo()))
-- pkg/b.py --
import os

from pkg.base import Foo


def helper(x):
    return isinstance(x, Foo) and os.sep
`)},
		{Name: "drop the back edge", Files: fixturetest.Files(`
-- pkg/b.py --
import os


def helper(x):
    return hasattr(x, "run") and os.sep
`)},
	}
}

func typescriptCycleCase() fixturetest.Case {
	return fixturetest.Case{
		Name: "typescript: two modules import each other",
		Base: fixturetest.Files(`
-- src/a.ts --
import { helper } from './b'

export class Foo {
  run(): boolean {
    return helper(this)
  }
}
-- src/b.ts --
import { readFileSync } from 'node:fs'
import { Foo } from './a'

export function helper(x: unknown): boolean {
  return x instanceof Foo && readFileSync !== undefined
}
`),
		Dodges: typescriptCycleDodges(),
		Fixes:  typescriptCycleFixes(),
	}
}

func typescriptCycleDodges() []fixturetest.Variant {
	return []fixturetest.Variant{
		{Name: "reorder imports, add blank lines and comments", Files: fixturetest.Files(`
-- src/b.ts --
// the cycle is still here

import { Foo } from './a'

import { readFileSync } from 'node:fs' // moved below

export function helper(x: unknown): boolean {
  return x instanceof Foo && readFileSync !== undefined
}
`)},
		{Name: "rename the local alias", Files: fixturetest.Files(`
-- src/b.ts --
import { readFileSync } from 'node:fs'
import { Foo as Thing } from './a'

export function helper(x: unknown): boolean {
  return x instanceof Thing && readFileSync !== undefined
}
`)},
		{Name: "move the import lower in the module", Files: fixturetest.Files(`
-- src/b.ts --
import { readFileSync } from 'node:fs'

export function helper(x: unknown): boolean {
  return x instanceof Foo && readFileSync !== undefined
}

import { Foo } from './a'
`)},
		{Name: "write the compiled extension", Files: fixturetest.Files(`
-- src/b.ts --
import { readFileSync } from 'node:fs'
import { Foo } from './a.js'

export function helper(x: unknown): boolean {
  return x instanceof Foo && readFileSync !== undefined
}
`)},
		{Name: "import the namespace", Files: fixturetest.Files(`
-- src/b.ts --
import { readFileSync } from 'node:fs'
import * as a from './a'

export function helper(x: unknown): boolean {
  return x instanceof a.Foo && readFileSync !== undefined
}
`)},
		{Name: "mix a type-only name into the value import", Files: fixturetest.Files(`
-- src/b.ts --
import { readFileSync } from 'node:fs'
import { Foo, type Foo as FooType } from './a'

export function helper(x: unknown): x is FooType {
  return x instanceof Foo && readFileSync !== undefined
}
`)},
		{Name: "re-export through a new barrel", Files: fixturetest.Files(`
-- src/index.ts --
export { Foo } from './a'
-- src/b.ts --
import { readFileSync } from 'node:fs'
import { Foo } from './index'

export function helper(x: unknown): boolean {
  return x instanceof Foo && readFileSync !== undefined
}
`)},
		{Name: "re-export everything through a barrel directory", Files: fixturetest.Files(`
-- src/api/index.ts --
export * from '../a'
-- src/b.ts --
import { readFileSync } from 'node:fs'
import { Foo } from './api'

export function helper(x: unknown): boolean {
  return x instanceof Foo && readFileSync !== undefined
}
`)},
	}
}

func typescriptCycleFixes() []fixturetest.Variant {
	return []fixturetest.Variant{
		{Name: "extract the shared class into a third module", Files: fixturetest.Files(`
-- src/base.ts --
export class Foo {}
-- src/a.ts --
import { helper } from './b'
import { Foo } from './base'

export const ok = helper(new Foo())
-- src/b.ts --
import { Foo } from './base'

export function helper(x: unknown): boolean {
  return x instanceof Foo
}
`)},
		{Name: "import only the type once only the type is used", Files: fixturetest.Files(`
-- src/b.ts --
import type { Foo } from './a'

export function helper(x: Foo | null): boolean {
  return x !== null
}
`)},
	}
}

func TestImportCycleHoldsThroughDodgesAndClearsOnFixes(t *testing.T) {
	t.Parallel()
	fixturetest.Run(t, []fixturetest.Case{pythonCycleCase(), typescriptCycleCase()}, cycleIDs(t))
}

func TestImportCycleLeavesWorkaroundsAndTestsAlone(t *testing.T) {
	t.Parallel()
	const a = "from pkg.b import helper\n\n\nclass Foo:\n    pass\n"
	cases := []struct {
		files fixturetest.Tree
		name  string
		want  int
	}{
		{name: "module-level imports both ways", want: 1, files: fixturetest.Tree{
			"pkg/a.py": a, "pkg/b.py": "from pkg.a import Foo\n\n\ndef helper():\n    return Foo\n",
		}},
		{name: "back edge deferred into a function", files: fixturetest.Tree{
			"pkg/a.py": a, "pkg/b.py": "def helper():\n    from pkg.a import Foo\n\n    return Foo\n",
		}},
		{name: "back edge under TYPE_CHECKING", files: fixturetest.Tree{
			"pkg/a.py": a,
			"pkg/b.py": "from typing import TYPE_CHECKING\n\nif TYPE_CHECKING:\n    from pkg.a import Foo\n\n\n" +
				"def helper() -> 'Foo': ...\n",
		}},
		{name: "back edge in the else of TYPE_CHECKING still runs", want: 1, files: fixturetest.Tree{
			"pkg/a.py": a,
			"pkg/b.py": "import typing\n\nif typing.TYPE_CHECKING:\n    Foo = object\nelse:\n" +
				"    from pkg.a import Foo\n\n\n" +
				"def helper():\n    return Foo\n",
		}},
		{name: "back edge under a main guard", files: fixturetest.Tree{
			"pkg/a.py": a,
			"pkg/b.py": "def helper():\n    return 1\n\n\nif __name__ == \"__main__\":\n" +
				"    from pkg.a import Foo\n\n    print(Foo)\n",
		}},
		{name: "a cycle only a test file closes", files: fixturetest.Tree{
			"pkg/a.py": a, "pkg/b.py": "from pkg.test_a import fixture\n\n\ndef helper():\n    return fixture\n",
			"pkg/test_a.py": "from pkg.a import Foo\n\nfixture = Foo()\n",
		}},
		{name: "typescript dynamic import back edge", files: fixturetest.Tree{
			"a.ts": "import { helper } from './b'\nexport const foo = helper\n",
			"b.ts": "export async function helper() {\n  return (await import('./a')).foo\n}\n",
		}},
		{name: "typescript re-export closes a cycle", want: 1, files: fixturetest.Tree{
			"a.ts": "import { helper } from './b'\nexport const foo = helper\n",
			"b.ts": "export { foo } from './a'\nexport function helper() {}\n",
		}},
		{name: "go packages never report", files: fixturetest.Tree{
			"go.mod": "module example.com/app\n\ngo 1.26\n",
			"a/a.go": "package a\n\nimport \"example.com/app/b\"\n\nfunc A() { b.B() }\n",
			"b/b.go": "package b\n\nfunc B() {}\n",
			"b/b_test.go": "package b_test\n\nimport (\n\t\"testing\"\n\n\t\"example.com/app/a\"\n)\n\n" +
				"func TestB(t *testing.T) { a.A() }\n",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := cycleFindings(t, tc.files); len(got) != tc.want {
				t.Errorf("import cycles = %+v, want %d", got, tc.want)
			}
		})
	}
}

func TestImportCycleNoteShowsTheCheapestCut(t *testing.T) {
	t.Parallel()
	cases := []struct {
		files    fixturetest.Tree
		name     string
		wantPath string
		wantNote string
		wantLine int
	}{
		{
			name: "the edge importing one name is the cut, anchored on its import line",
			files: fixturetest.Files(`
-- pkg/a.py --
from pkg.b import one, two, three


class Foo:
    pass
-- pkg/b.py --
import os

from pkg.a import Foo

one = two = three = Foo
`),
			wantPath: "pkg/b.py", wantLine: 3,
			wantNote: "a.py -> b.py -> a.py; cheapest cut: b.py imports 1 name (Foo) from a.py; " +
				"2 files import each other in pkg",
		},
		{
			name:     "a large group shows one short cycle and the member count",
			files:    ringWithShortcut(8),
			wantPath: "m1.py", wantLine: 2,
			wantNote: "m0.py -> m1.py -> m0.py; cheapest cut: m1.py imports 1 name (only) from m0.py; " +
				"8 files import each other",
		},
		{
			name:     "a long shortest cycle is elided in the middle",
			files:    ringWithShortcut(0),
			wantPath: "m0.py", wantLine: 1,
			wantNote: "m1.py -> m2.py -> m3.py -> (4 more) -> m0.py -> m1.py; " +
				"cheapest cut: m0.py imports 2 names (p, q) from m1.py; 8 files import each other",
		},
		{
			name: "a module import is priced by the names used",
			files: fixturetest.Tree{
				"a.ts": "import * as b from './b'\nexport const x = b.one() + b.two()\n",
				"b.ts": "import './a'\nexport function one() { return 1 }\nexport function two() { return 2 }\n",
			},
			wantPath: "b.ts", wantLine: 1,
			wantNote: "a.ts -> b.ts -> a.ts; cheapest cut: b.ts imports a.ts as a whole; 2 files import each other",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			byPath := crossFileOf(t, tc.files, true)
			var got []rules.Finding
			for _, f := range byPath[tc.wantPath] {
				if f.Rule == ruleImportCycle {
					got = append(got, f)
				}
			}
			if len(got) != 1 || len(cycleFindings(t, tc.files)) != 1 {
				t.Fatalf("findings in %s = %+v, want exactly one cycle in the tree", tc.wantPath, got)
			}
			if got[0].Note != tc.wantNote || got[0].Line != tc.wantLine {
				t.Errorf("finding = line %d %q\nwant line %d %q", got[0].Line, got[0].Note, tc.wantLine, tc.wantNote)
			}
			if !strings.HasPrefix(got[0].Note, got[0].Match+"; ") {
				t.Errorf("match %q must open the note", got[0].Match)
			}
		})
	}
}

// ringWithShortcut builds eight modules that import each other in a ring, two
// names per import. A shortcut at n >= 1 adds a one-name import from m1 back
// to m0, the cheap cut of a group that is otherwise one long cycle.
func ringWithShortcut(n int) fixturetest.Tree {
	const size = 8
	tree := fixturetest.Tree{}
	for i := range size {
		body := fmt.Sprintf("from m%d import p, q\n", (i+1)%size)
		if n > 0 && i == 1 {
			body += "from m0 import only\n"
		}
		tree[fmt.Sprintf("m%d.py", i)] = body + "p = q = only = 1\n"
	}

	return tree
}

func TestImportCycleIDIsItsMemberSet(t *testing.T) {
	t.Parallel()
	pair := []string{"pkg/a.py", "pkg/b.py"}
	cases := []struct {
		name    string
		rule    string
		members []string
		same    bool
	}{
		{name: "the same members under the same rule", rule: ruleImportCycle, members: pair, same: true},
		{name: "a member added", rule: ruleImportCycle, members: []string{"pkg/a.py", "pkg/b.py", "pkg/c.py"}},
		{name: "a member renamed", rule: ruleImportCycle, members: []string{"pkg/a.py", "pkg/c.py"}},
		{name: "another rule", rule: "x/other", members: pair},
	}
	want := rules.SetID(ruleImportCycle, pair)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := rules.SetID(tc.rule, tc.members); (got == want) != tc.same {
				t.Errorf("SetID(%s, %v) = %s against %s, want same=%v", tc.rule, tc.members, got, want, tc.same)
			}
		})
	}
	// Identify keys a set-shaped finding by its members wherever it is anchored.
	here := rules.Identify("pkg/a.py", []rules.Finding{{Rule: ruleImportCycle, Line: 1, Match: "x", Members: pair}})
	there := rules.Identify("pkg/b.py", []rules.Finding{{Rule: ruleImportCycle, Line: 9, Match: "y", Members: pair}})
	if here[0].ID != want || there[0].ID != want {
		t.Errorf("Identify gave %s and %s, want %s for both", here[0].ID, there[0].ID, want)
	}
}

func TestNewImportCyclesReportsNewAndGrownOnly(t *testing.T) {
	t.Parallel()
	prints := func(tree fixturetest.Tree) map[string]*rules.Fingerprint {
		out := map[string]*rules.Fingerprint{}
		for path, src := range tree {
			out[path] = printOf(t, path, src)
		}

		return out
	}
	pair := fixturetest.Tree{"a.py": "import b\n", "b.py": "import a\n", "c.py": "x = 1\n"}
	triangle := fixturetest.Tree{"a.py": "import b\n", "b.py": "import c\n", "c.py": "import a\n"}
	none := fixturetest.Tree{"a.py": "import b\n", "b.py": "x = 1\n", "c.py": "x = 1\n"}
	cases := []struct {
		base, rev    fixturetest.Tree
		name         string
		experimental bool
		want         int
	}{
		{name: "a new cycle", base: none, rev: pair, experimental: true, want: 1},
		{name: "a grown cycle", base: pair, rev: triangle, experimental: true, want: 1},
		{name: "an unchanged cycle", base: pair, rev: pair, experimental: true},
		{name: "a shrunk cycle", base: triangle, rev: pair, experimental: true},
		{name: "a fixed cycle", base: pair, rev: none, experimental: true},
		{name: "the rule is experimental", base: none, rev: pair},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := flatten(rules.NewImportCycles(prints(tc.base), prints(tc.rev), tc.experimental))
			if len(got) != tc.want {
				t.Errorf("new cycles = %+v, want %d", got, tc.want)
			}
		})
	}
}

func TestImportRefsCarryTheKindTheirPositionGives(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		path string
		src  string
		want []string
	}{
		{
			name: "python statements", path: "m.py",
			src: "import a.b as ab, c\nfrom . import x\nfrom ..pkg import y as z, w\nfrom lib import *\n" +
				"from __future__ import annotations\n",
			want: []string{"a.b@1", "c@1", ".[x]@2", "..pkg[y,w]@3", "lib[*]@4"},
		},
		{
			name: "python positions", path: "m.py",
			src: "from typing import TYPE_CHECKING\nif TYPE_CHECKING:\n    import t\nelse:\n    import e\n" +
				"if not TYPE_CHECKING:\n    import n\ndef f():\n    import d\n    g = lambda: __import__('x')\n" +
				"class C:\n    import k\ntry:\n    import r\nexcept ImportError:\n    r = None\n" +
				"if __name__ == '__main__':\n    import m\n",
			want: []string{
				"typing[TYPE_CHECKING]@1", "t type@3", "e@5", "n@7", "d deferred@9", "k@12", "r@14", "m main@18",
			},
		},
		{
			name: "typescript statements", path: "m.ts",
			src: "import D, { a, b as c } from './x'\nimport * as ns from './ns'\nimport './side'\n" +
				"import type { T } from './t'\nimport { type U, v } from './mixed'\nimport { type W } from './w'\n" +
				"export { e } from './e'\nexport * from './star'\nexport * as all from './all'\n" +
				"export type { X } from './xt'\nexport const local = 1\n" +
				"export type * from './ts'\nexport type * as tns from './tns'\n",
			want: []string{
				"./x[default,a,b]@1", "./ns[*]@2", "./side@3", "./t[T] type@4", "./mixed[U] type@5", "./mixed[v]@5",
				"./w[W] type@6", "./e[e]@7", "./star[*]@8", "./all[*]@9", "./xt[X] type@10",
				"./ts[*] type@12", "./tns[*] type@13",
			},
		},
		{
			name: "typescript positions", path: "m.ts",
			src: "const top = require('./top')\nconst lazy = () => import('./lazy')\n" +
				"function f() {\n  const inner = require('./inner')\n  return inner\n}\n" +
				"const p = import('./eager')\nrequire(name)\nrequire('./a', './b')\n",
			want: []string{"./top@1", "./lazy deferred@2", "./inner deferred@4", "./eager deferred@7"},
		},
		{
			name: "go import block", path: "m.go",
			src: "package m\n\nimport (\n\t\"fmt\"\n\tstore \"example.com/app/store\"\n" +
				"\t_ \"example.com/app/driver\"\n)\n\nvar _ = fmt.Sprint\n",
			want: []string{"fmt@4", "example.com/app/store@5", "example.com/app/driver@6"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			for _, ref := range printOf(t, tc.path, tc.src).ImportRefs {
				text := ref.Spec
				if len(ref.Names) > 0 {
					text += "[" + strings.Join(ref.Names, ",") + "]"
				}
				if ref.Kind != "" {
					text += " " + ref.Kind
				}
				got = append(got, fmt.Sprintf("%s@%d", text, ref.Line))
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("import refs =\n  %v\nwant\n  %v", got, tc.want)
			}
		})
	}
}

func TestResolveImportsReadsTheGoModulePath(t *testing.T) {
	t.Parallel()
	tree := fixturetest.Tree{
		"go.mod": "// declares the path\nmodule \"example.com/app\"\n\ngo 1.26\n",
		"cmd/main.go": "package main\n\nimport \"example.com/app/internal/store\"\n\n" +
			"func main() { store.Open() }\n",
		"internal/store/s.go": "package store\n\nfunc Open() {}\n",
		"README.md":           "# notes\n",
	}
	blobs := make([]rules.BlobPrint, 0, len(tree))
	for path, src := range tree {
		blobs = append(blobs, rules.BlobPrint{Path: path, Print: printOf(t, path, src)})
	}
	im := rules.ResolveImports(blobs, nil)
	edges := im.Graph.Edges()
	if len(edges) != 1 || edges[0].From != "cmd/main.go" || edges[0].To != "internal/store/s.go" ||
		!slices.Equal(edges[0].Names, []string{"Open"}) {
		t.Errorf("edges = %+v, want cmd/main.go importing Open from internal/store/s.go", edges)
	}
	if len(im.Cycles()) != 0 {
		t.Errorf("cycles = %+v, want none", im.Cycles())
	}
}

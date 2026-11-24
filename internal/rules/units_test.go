package rules_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/rules"
)

// unitLines renders a fingerprint's units as "name" lines, with "+" on a
// public name, "-> callee" on a forwarder, and the mass when it has one.
func unitLines(fp *rules.Fingerprint) []string {
	out := make([]string, 0, len(fp.Units))
	for _, u := range fp.Units {
		line := u.Name
		if u.Public {
			line += "+"
		}
		if u.Forwards != "" {
			line += " -> " + u.Forwards
		}
		if u.Mass > 0 {
			line += fmt.Sprintf(" %.2f", u.Mass)
		}
		out = append(out, line)
	}

	return out
}

const goUnitsSrc = `package a

type T struct{ x int }

type (
	U int
	v interface{ M() }
)

const K = 1

const (
	k1 = iota
	K2
)

var A, b = 1, 2

var _ = 3

func (t *T) Get() int {
	if t.x > 0 {
		return t.x
	}

	return 0
}

func (t T) set(x int) { t.x = x }

func F() {}
`

const pyUnitsSrc = `import os

X = 1
_y: int = 2
__all__ = ['f']
a, b = 1, 2


@dec
def f(a):
    if a:
        return 1
    return 2


async def _g():
    pass


class C(Base):
    Z = 3

    def __init__(self):
        self.a = 1

    @property
    def m(self):
        return self.a

    def _p(self):
        def inner():
            return 1
        return inner
`

const tsUnitsSrc = `export const K = 1, L = 2
const arrow = (x: number) => { return x ? 1 : 2 }
export default function main() {}
export class C {
  constructor() {}
  m() { return 1 }
  h = () => { return 2 }
  #p() {}
  field = 3
}
interface I { a: string }
export type A = string
enum E { X }
function over(a: string): void
function over(a: unknown) {}
export { arrow as pub, over }
const { x, y } = K as never
`

func TestCollectListsTheUnitsOfAFile(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		path string
		src  string
		want []string
	}{
		{
			name: "go: functions, methods by receiver, and every name of a grouped declaration",
			path: "a.go", src: goUnitsSrc,
			want: []string{"T+", "U+", "v", "K+", "k1", "K2+", "A+", "b", "T.Get+ 4.90", "T.set 1.00", "F+ 1.00"},
		},
		{
			name: "python: decorated functions, classes with their methods, plain assignments",
			path: "a.py", src: pyUnitsSrc,
			want: []string{"X+", "_y", "f+ 4.00", "_g 1.41", "C+ 1.41", "C.m+ 1.41", "C._p 2.83"},
		},
		{
			name: "typescript: declarations with and without export, class members, a default export",
			path: "a.ts", src: tsUnitsSrc,
			want: []string{
				"K+", "L+", "arrow+ 2.00", "main+ 1.00", "C+ 1.00", "C.m+ 1.00", "C.h+ 1.00", "C.#p+ 1.00",
				"I", "A+", "E", "over+ 1.00",
			},
		},
		{
			name: "typescript: a default export with no name is a unit named default",
			path: "b.ts", src: "export default () => { return 1 }\n",
			want: []string{"default+ 1.00"},
		},
		{
			name: "functions at file scope that no declaration holds weigh on an unnamed unit",
			path: "c.ts", src: "export const one = 1\nregister(() => { return 2 })\n",
			want: []string{"one+", " 1.00"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := unitLines(weighedPrint(t, tc.path, tc.src)); !slices.Equal(got, tc.want) {
				t.Errorf("units =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

// forwardCase is one function named f or F and what it should forward to.
type forwardCase struct {
	name string
	path string
	src  string
	want string
}

func goForwardCases() []forwardCase {
	fn := func(name, decl, want string) forwardCase {
		return forwardCase{name: "go: " + name, path: "a.go", src: "package a\n\n" + decl + "\n", want: want}
	}

	return []forwardCase{
		fn("returns one call of its parameters", "func F(a, b int) int { return g(a, b) }", "g"),
		fn("a variadic pass-through", "func F(a ...int) int { return g(a...) }", "g"),
		fn("a call that is not returned", "func F(a int) {\n\t// Pass it on.\n\tg(a)\n}", "g"),
		fn("a method of the same receiver", "func (t *T) F(a int) int { return t.g(a) }", ".g"),
		fn("another package's function", "func F(a int) int { return pkg.G(a) }", "*"),
		fn("a field's method", "func (t *T) F(a int) int { return t.inner.G(a) }", "*"),
		fn("an argument it computes", "func F(a int) int { return g(a + 1) }", ""),
		fn("a literal argument", "func F(a int) int { return g(a, 0) }", ""),
		fn("two statements", "func F(a int) int {\n\tcheck(a)\n\n\treturn g(a)\n}", ""),
		fn("no call", "func (t *T) F() int { return t.x }", ""),
		fn("a receiver field as the argument", "func (t *T) F() int { return len(t.items) }", ""),
	}
}

func pythonForwardCases() []forwardCase {
	fn := func(name, src, want string) forwardCase {
		return forwardCase{name: "python: " + name, path: "a.py", src: src, want: want}
	}

	return []forwardCase{
		fn("after a docstring, with splats",
			"def f(a, *args, **kw):\n    \"\"\"Doc.\"\"\"\n    return g(a, *args, **kw)\n", "g"),
		fn("keyword arguments of its own", "def f(a, b=1):\n    return g(a, flag=b)\n", "g"),
		fn("a method of self", "class C:\n    def f(self, a):\n        return self._f(a)\n", "._f"),
		fn("awaited", "async def f(a):\n    return await g(a)\n", "g"),
		fn("a method of something self holds", "class C:\n    def f(self, a):\n        return self.x.f(a)\n", "*"),
		fn("a type named in an annotation is no parameter", "def f(a: Cfg):\n    return g(Cfg)\n", ""),
		fn("only a docstring", "def f(a):\n    \"\"\"Doc.\"\"\"\n", ""),
		fn("a stub body", "def f(a):\n    raise NotImplementedError\n", ""),
	}
}

func typescriptForwardCases() []forwardCase {
	fn := func(name, src, want string) forwardCase {
		return forwardCase{name: "typescript: " + name, path: "a.ts", src: src + "\n", want: want}
	}

	return []forwardCase{
		fn("a function declaration", "function f(a: number) { return g(a) }", "g"),
		fn("an arrow with an expression body", "const f = (a: number) => g(a)", "g"),
		fn("a spread", "const f = (...rest: number[]) => { return g(...rest) }", "g"),
		fn("a method of this", "class C { f(a: number) { return this.g(a) } }", ".g"),
		fn("an object literal argument", "const f = (a: number) => g({ a })", ""),
		fn("a value, not a function", "const f = g(1)", ""),
	}
}

func TestCollectTellsAForwarderFromRealWork(t *testing.T) {
	t.Parallel()
	for _, tc := range slices.Concat(goForwardCases(), pythonForwardCases(), typescriptForwardCases()) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := "no unit"
			for _, u := range weighedPrint(t, tc.path, tc.src).Units {
				if base := u.Name[strings.LastIndexByte(u.Name, '.')+1:]; strings.EqualFold(base, "f") {
					got = u.Forwards
				}
			}
			if got != tc.want {
				t.Errorf("forwards = %q, want %q", got, tc.want)
			}
		})
	}
}

const pyUsesSrc = `import os.path as p
import pkg.mod
from .impl import helper as h, other, used
from . import sub

__all__ = ['other']


def f(self):
    p.join(used)
    pkg.mod.go()
    sub.thing.run()
    return h(self.inner.value, make().result)
`

const tsUsesSrc = `import def, { a as b, type T, unused } from './x'
import * as ns from './y'
export { q as r } from './w'
export const f = (o: Opts): ns.Kind => { def(b); ns.run(); return o.items[0].name }
export { unused as again }
`

const goUsesSrc = `package a

import (
	"fmt"
	st "example.com/app/store"
	"example.com/app/kv/v2"
	_ "embed"
)

func f(x T) st.Kind {
	fmt.Println(x.field.Name)
	kv.Open().Close()

	return st.New()
}
`

// refLines renders imports as "spec[names]{local=name}@module".
func refLines(fp *rules.Fingerprint) string {
	refs := make([]string, 0, len(fp.ImportRefs))
	for _, r := range fp.ImportRefs {
		text := r.Spec
		if len(r.Names) > 0 {
			text += fmt.Sprint(r.Names)
		}
		if len(r.Aliases) > 0 {
			text += "{" + strings.TrimSuffix(strings.TrimPrefix(fmt.Sprint(r.Aliases), "map["), "]") + "}"
		}
		if r.Local != "" {
			text += "@" + r.Local
		}
		refs = append(refs, strings.ReplaceAll(text, ":", "="))
	}

	return strings.Join(refs, " ")
}

func TestCollectRecordsHowImportedNamesAreUsed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		path string
		src  string
		want []string // selected, bound and unused, members, imports
	}{
		{
			name: "python", path: "a.py", src: pyUsesSrc,
			want: []string{
				"map[p:[join] pkg:[go mod] sub:[run thing]]", "[other]", "[inner result run thing value]",
				"os.path@p pkg.mod@pkg .impl[helper other used]{h=helper} .[sub]",
			},
		},
		{
			name: "typescript", path: "a.ts", src: tsUsesSrc,
			want: []string{
				"map[ns:[Kind run]]", "[T r unused]", "[items name]",
				"./x[T] ./x[default a unused]{b=a def=default} ./y[*]@ns ./w[q]{r=q}",
			},
		},
		{
			name: "go", path: "a.go", src: goUsesSrc,
			want: []string{
				"map[fmt:[Println] kv:[Open] st:[Kind New]]", "[]", "[Close Name field]",
				"fmt@fmt example.com/app/store@st example.com/app/kv/v2@kv embed@_",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fp := weighedPrint(t, tc.path, tc.src)
			got := []string{fmt.Sprint(fp.Selected), fmt.Sprint(fp.Passed), fmt.Sprint(fp.Members), refLines(fp)}
			if !slices.Equal(got, tc.want) {
				t.Errorf("got\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

func TestCollectNamesADefaultExportAndExportAliases(t *testing.T) {
	t.Parallel()
	fp := weighedPrint(t, "a.ts", "const x = () => 1\nfunction y() {}\nexport default x\nexport { y as z }\n")
	if fp.Default != "x" || fmt.Sprint(fp.ExportAs) != "map[z:y]" {
		t.Errorf("default = %q, export aliases = %v, want x and z for y", fp.Default, fp.ExportAs)
	}
	named := weighedPrint(t, "b.ts", "export default class Store {}\n")
	if named.Default != "Store" {
		t.Errorf("default = %q, want Store", named.Default)
	}
}

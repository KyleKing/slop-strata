package rules_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/rules"
)

// literalGroups fingerprints a tree and lists its literal groups.
func literalGroups(t *testing.T, tree map[string]string) []rules.LiteralGroup {
	t.Helper()
	prints := map[string]*rules.Fingerprint{}
	for path, src := range tree {
		prints[path] = printOf(t, path, src)
	}

	return rules.LiteralGroups(prints)
}

// litLang writes, in one language, a file that tests a value against a
// literal, one that hands the literal to a call, and one that names it. LIT
// is the literal as written and FN a name for the function.
type litLang struct {
	name, ext string
	head      string
	test      string
	write     string
	named     string
	constant  string // the name named declares
}

func litLangs() []litLang {
	return []litLang{
		{
			name: "go", ext: ".go", head: "package api\n\n",
			test:  "func FN(r req) bool {\n\treturn r.kind == LIT\n}\n",
			write: "func FN(r req) {\n\tr.send(1, LIT)\n}\n",
			named: "const vendorType = LIT\n", constant: "vendorType",
		},
		{
			name: "python", ext: ".py",
			test:  "def FN(r):\n    return r.kind == LIT\n",
			write: "def FN(r):\n    r.send(1, LIT)\n",
			named: "VENDOR_TYPE = LIT\n", constant: "VENDOR_TYPE",
		},
		{
			name: "typescript", ext: ".ts",
			test:  "export function FN(r: Req): boolean {\n  return r.kind === LIT\n}\n",
			write: "export function FN(r: Req) {\n  r.send(1, LIT)\n}\n",
			named: "export const VENDOR_TYPE = LIT\n", constant: "VENDOR_TYPE",
		},
	}
}

// fillLit writes one piece of a file with its literal and function name.
func fillLit(tmpl, fn, lit string) string {
	return strings.NewReplacer("FN", fn, "LIT", lit).Replace(tmpl)
}

// vendor is the literal the fixtures repeat, in double quotes.
const (
	vendorValue = "application/vnd.app+json"
	vendor      = `"` + vendorValue + `"`
)

// tested is a tree of two files that test a value against the literal and
// two that hand it to a call: the smallest group no constant names.
func (l *litLang) tested() map[string]string {
	return map[string]string{
		"api/accept" + l.ext: l.head + fillLit(l.test, "accept", vendor),
		"api/check" + l.ext:  l.head + fillLit(l.test, "check", vendor),
		"api/post" + l.ext:   l.head + fillLit(l.write, "post", vendor),
		"api/put" + l.ext:    l.head + fillLit(l.write, "put", vendor),
	}
}

func TestLiteralGroupsListATestedLiteral(t *testing.T) {
	t.Parallel()
	for _, l := range litLangs() {
		t.Run(l.name, func(t *testing.T) {
			t.Parallel()
			got := literalGroups(t, l.tested())
			line := strings.Count(l.head, "\n") + 2
			want := []rules.LiteralGroup{{
				Value: vendorValue, Files: 4, Written: 4, Uses: 2, Rank: 2,
				Sites: []rules.LiteralSite{
					{Path: "api/accept" + l.ext, Line: line, Written: 1, Uses: 1},
					{Path: "api/check" + l.ext, Line: line, Written: 1, Uses: 1},
					{Path: "api/post" + l.ext, Line: line, Written: 1},
					{Path: "api/put" + l.ext, Line: line, Written: 1},
				},
			}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("groups = %+v, want %+v", got, want)
			}
		})
	}
}

func TestLiteralGroupsListALiteralAConstantNames(t *testing.T) {
	t.Parallel()
	for _, l := range litLangs() {
		t.Run(l.name, func(t *testing.T) {
			t.Parallel()
			got := literalGroups(t, map[string]string{
				"api/names" + l.ext: l.head + fillLit(l.named, "", vendor),
				"api/post" + l.ext: l.head + fillLit(l.write, "post", vendor) + "\n" +
					fillLit(l.write, "patch", vendor),
				"api/put" + l.ext: l.head + fillLit(l.write, "put", vendor),
			})
			if len(got) != 1 {
				t.Fatalf("groups = %+v, want one", got)
			}
			g := got[0]
			named := &rules.LiteralConstant{
				Name: l.constant, Path: "api/names" + l.ext, Line: strings.Count(l.head, "\n") + 1,
			}
			if !reflect.DeepEqual(g.Constant, named) {
				t.Errorf("constant = %+v, want %+v", g.Constant, named)
			}
			if g.Files != 3 || g.Written != 4 || g.Uses != 0 || g.Rank != 3 {
				t.Errorf("group = %+v, want 3 files, 4 places, no use, rank 3", g)
			}
		})
	}
}

// litCase is one tree and the values of the groups it should list.
type litCase struct {
	files map[string]string
	name  string
	want  []string
}

// swap returns a tree with some files replaced or added, and the ones mapped
// to "" taken out.
func swap(tree, with map[string]string) map[string]string {
	out := map[string]string{}
	for name, body := range tree {
		out[name] = body
	}
	for name, body := range with {
		if body == "" {
			delete(out, name)
		} else {
			out[name] = body
		}
	}

	return out
}

// literalFloorCases are trees at and under the floors of a group.
func literalFloorCases() []litCase {
	g := &litLangs()[0]
	base := g.tested()
	testing8 := func(lit string) map[string]string {
		out := map[string]string{}
		for name, body := range base {
			out[name] = strings.ReplaceAll(body, vendor, lit)
		}

		return out
	}

	return []litCase{
		{name: "four places in four files", files: base, want: []string{vendorValue}},
		{
			name: "three files hold the four places",
			files: swap(base, map[string]string{
				"api/put.go": "", "api/post.go": base["api/post.go"] + "\n" + fillLit(g.write, "put", vendor),
			}),
			want: []string{vendorValue},
		},
		{
			name: "two files hold the four places",
			files: map[string]string{
				"api/accept.go": base["api/accept.go"] + "\n" + fillLit(g.test, "check", vendor),
				"api/post.go":   base["api/post.go"] + "\n" + fillLit(g.write, "put", vendor),
			},
		},
		{name: "three places in three files", files: swap(base, map[string]string{"api/put.go": ""})},
		{
			name:  "one place tests it",
			files: swap(base, map[string]string{"api/check.go": g.head + fillLit(g.write, "check", vendor)}),
		},
		{
			name: "every place tests it and none writes it",
			files: swap(base, map[string]string{
				"api/post.go": g.head + fillLit(g.test, "post", vendor),
				"api/put.go":  g.head + fillLit(g.test, "put", vendor),
			}),
		},
		{name: "content of the minimum length", files: testing8(`"01234-67"`), want: []string{"01234-67"}},
		{name: "content under the minimum length", files: testing8(`"01234-6"`)},
		{name: "a format string", files: testing8(`"application/%s+json"`)},
		{name: "a raw string joins a quoted one", files: swap(base, map[string]string{
			"api/put.go": strings.ReplaceAll(base["api/put.go"], vendor, "`"+vendorValue+"`"),
		}), want: []string{vendorValue}},
	}
}

// literalAsideCases are trees whose literal is one the pass leaves out for
// what it is or where it stands.
func literalAsideCases() []litCase {
	langs := litLangs()
	g, py, ts := &langs[0], &langs[1], &langs[2]
	goBase, pyBase, tsBase := g.tested(), py.tested(), ts.tested()
	rename := func(tree map[string]string, from, to string) map[string]string {
		out := map[string]string{}
		for name, body := range tree {
			out[strings.Replace(name, from, to, 1)] = body
		}

		return out
	}
	const statusTested = "export function FN(r: Req): boolean {\n  return r.kind === \"published\"\n}\n"
	const statusSet = "export function FN(r: Req) {\n  r.send(1, \"published\")\n}\n"
	status := map[string]string{
		"src/a.ts": strings.ReplaceAll(statusTested, "FN", "a"),
		"src/b.ts": strings.ReplaceAll(statusTested, "FN", "b"),
		"src/c.ts": strings.ReplaceAll(statusSet, "FN", "c"),
		"src/d.ts": strings.ReplaceAll(statusSet, "FN", "d"),
	}

	return []litCase{
		{name: "a literal no identifier spells", files: status, want: []string{"published"}},
		{
			name:  "typescript: a type of the tree spells the literal",
			files: swap(status, map[string]string{"src/types.ts": "export type Status = \"draft\" | \"published\"\n"}),
		},
		{
			name:  "typescript: a field of the tree has the literal's name",
			files: swap(status, map[string]string{"src/flags.ts": "export const flags = { published: true }\n"}),
		},
		{
			name: "python: a Literal of the tree spells the literal",
			files: swap(pyBase, map[string]string{
				"api/kinds.py": "from typing import Literal\n\nKind = Literal[\"text/plain\", " + vendor + "]\n",
			}),
		},
		{
			name: "python: an annotation of the tree spells the literal",
			files: swap(pyBase, map[string]string{
				"api/kinds.py": "def kind(r) -> " + vendor + ":\n    return r.kind\n",
			}),
		},
		{
			name: "python: a dunder name",
			files: map[string]string{
				"api/accept.py": strings.ReplaceAll(pyBase["api/accept.py"], vendor, `"__main__"`),
				"api/check.py":  strings.ReplaceAll(pyBase["api/check.py"], vendor, `"__main__"`),
				"api/post.py":   strings.ReplaceAll(pyBase["api/post.py"], vendor, `"__main__"`),
				"api/put.py":    strings.ReplaceAll(pyBase["api/put.py"], vendor, `"__main__"`),
			},
		},
		{name: "go: test files", files: rename(goBase, ".go", "_test.go")},
		{name: "go: an examples directory", files: rename(goBase, "api/", "examples/api/")},
		{name: "typescript: story files", files: rename(tsBase, ".ts", ".stories.ts")},
		{name: "typescript: configuration files", files: rename(tsBase, ".ts", ".config.ts")},
		{name: "python: a migrations directory", files: rename(pyBase, "api/", "migrations/")},
		{
			name: "files of one base name",
			files: map[string]string{
				"a/route.ts": tsBase["api/accept.ts"], "b/route.ts": tsBase["api/check.ts"],
				"c/route.ts": tsBase["api/post.ts"], "d/route.ts": tsBase["api/put.ts"],
			},
		},
		{
			name: "files of one base name and one other",
			files: map[string]string{
				"a/route.ts": tsBase["api/accept.ts"], "b/route.ts": tsBase["api/check.ts"],
				"c/route.ts": tsBase["api/post.ts"], "d/client.ts": tsBase["api/put.ts"],
			},
			want: []string{vendorValue},
		},
	}
}

func TestLiteralGroupsFloorsAndAsides(t *testing.T) {
	t.Parallel()
	for _, tc := range append(literalFloorCases(), literalAsideCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			for _, g := range literalGroups(t, tc.files) {
				got = append(got, g.Value)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("groups = %q, want %q", got, tc.want)
			}
		})
	}
}

// roleCase is one file and what the fingerprint records of the literal in it.
type roleCase struct {
	name, path, src string
	want            rules.LiteralUse
}

func goRoleCases() []roleCase {
	const head = "package api\n\n"

	return []roleCase{
		{
			name: "go: an equality test is a use", path: "a.go",
			src:  head + "func f(r req) bool {\n\treturn r.kind != " + vendor + "\n}\n",
			want: rules.LiteralUse{Lines: []int{4}, Count: 1, Uses: 1},
		},
		{
			name: "go: a switch case is a use", path: "a.go",
			src: head + "func f(r req) int {\n\tswitch r.kind {\n\tcase \"text/plain\", " + vendor +
				":\n\t\treturn 1\n\t}\n\n\treturn 0\n}\n",
			want: rules.LiteralUse{Lines: []int{5}, Count: 1, Uses: 1},
		},
		{
			name: "go: an index is a use", path: "a.go",
			src:  head + "func f(m map[string]int) int {\n\treturn m[" + vendor + "]\n}\n",
			want: rules.LiteralUse{Lines: []int{4}, Count: 1, Uses: 1},
		},
		{
			name: "go: the key of a lookup call is a use", path: "a.go",
			src:  head + "func f(h header) string {\n\treturn h.Get(" + vendor + ")\n}\n",
			want: rules.LiteralUse{Lines: []int{4}, Count: 1, Uses: 1},
		},
		{
			name: "go: the second argument of a lookup call is not", path: "a.go",
			src:  head + "func f(h header) {\n\th.Set(\"Accept\", " + vendor + ")\n}\n",
			want: rules.LiteralUse{Lines: []int{4}, Count: 1},
		},
		{
			name: "go: a constant at file scope names it", path: "a.go",
			src:  head + "const (\n\tplain = \"text/plain\"\n\tvendorType = " + vendor + "\n)\n",
			want: rules.LiteralUse{Lines: []int{5}, Count: 1, Name: "vendorType", NameLine: 5},
		},
		{
			name: "go: a variable inside a function does not", path: "a.go",
			src:  head + "func f() string {\n\tvar kind = " + vendor + "\n\n\treturn kind\n}\n",
			want: rules.LiteralUse{Lines: []int{4}, Count: 1},
		},
		{
			name: "go: places past the fourth are counted and not located", path: "a.go",
			src:  head + "func f(r req) {\n" + strings.Repeat("\tr.send(1, "+vendor+")\n", 6) + "}\n",
			want: rules.LiteralUse{Lines: []int{4, 5, 6, 7}, Count: 6},
		},
		{name: "go: an import path", path: "a.go", src: "package api\n\nimport " + vendor + "\n"},
		{
			name: "go: a struct tag", path: "a.go",
			src: head + "type req struct {\n\tKind string `json:\"kind,omitempty\"`\n}\n",
		},
		{
			name: "go: a map key", path: "a.go",
			src: head + "var sizes = map[string]int{\n\t" + vendor + ": 1,\n}\n",
		},
	}
}

func pyRoleCases() []roleCase {
	return []roleCase{
		{
			name: "python: a membership test is a use", path: "a.py",
			src:  "def f(r):\n    return r.kind in (\"text/plain\", " + vendor + ")\n",
			want: rules.LiteralUse{Lines: []int{2}, Count: 1, Uses: 1},
		},
		{
			name: "python: a match case is a use", path: "a.py",
			src:  "def f(r):\n    match r.kind:\n        case " + vendor + ":\n            return 1\n    return 0\n",
			want: rules.LiteralUse{Lines: []int{3}, Count: 1, Uses: 1},
		},
		{
			name: "python: a subscript is a use", path: "a.py",
			src:  "def f(sizes):\n    return sizes[" + vendor + "]\n",
			want: rules.LiteralUse{Lines: []int{2}, Count: 1, Uses: 1},
		},
		{
			name: "python: an ordering test is not", path: "a.py",
			src:  "def f(r):\n    return r.kind < " + vendor + "\n",
			want: rules.LiteralUse{Lines: []int{2}, Count: 1},
		},
		{
			name: "python: a class attribute names it", path: "a.py",
			src:  "class Kind:\n    VENDOR = " + vendor + "\n",
			want: rules.LiteralUse{Lines: []int{2}, Count: 1, Name: "VENDOR", NameLine: 2},
		},
		{
			name: "python: an assignment inside a function does not", path: "a.py",
			src:  "def f():\n    kind = " + vendor + "\n    return kind\n",
			want: rules.LiteralUse{Lines: []int{2}, Count: 1},
		},
		{
			name: "python: a Literal member is a type", path: "a.py",
			src:  "from typing import Literal\n\nKind = Literal[" + vendor + "]\n",
			want: rules.LiteralUse{Typed: true},
		},
		{name: "python: a docstring", path: "a.py", src: "def f():\n    " + vendor + "\n    return 1\n"},
		{name: "python: a dictionary key", path: "a.py", src: "SIZES = {" + vendor + ": 1}\n"},
		{name: "python: an f-string", path: "a.py", src: "def f(v):\n    return f\"application/vnd.{v}+json\"\n"},
		{
			name: "python: a string written in pieces", path: "a.py",
			src: "def f(r):\n    r.send(\"application/\" \"vnd.app+json\")\n",
		},
		{name: "python: a string over two lines", path: "a.py", src: "KIND = \"\"\"application/\nvnd.app+json\"\"\"\n"},
	}
}

func tsRoleCases() []roleCase {
	return []roleCase{
		{
			name: "typescript: a switch case is a use", path: "a.ts",
			src: "export function f(r: Req) {\n  switch (r.kind) {\n    case " + vendor +
				":\n      return 1\n  }\n  return 0\n}\n",
			want: rules.LiteralUse{Lines: []int{3}, Count: 1, Uses: 1},
		},
		{
			name: "typescript: includes on a list of literals is a use", path: "a.ts",
			src:  "export const f = (r: Req) => [\"text/plain\", " + vendor + "].includes(r.kind)\n",
			want: rules.LiteralUse{Lines: []int{1}, Count: 1, Uses: 1},
		},
		{
			name: "typescript: a subscript is a use", path: "a.ts",
			src:  "export const f = (sizes: Sizes) => sizes[" + vendor + "]\n",
			want: rules.LiteralUse{Lines: []int{1}, Count: 1, Uses: 1},
		},
		{
			name: "typescript: a const under as const names it", path: "a.ts",
			src:  "const VENDOR = " + vendor + " as const\n",
			want: rules.LiteralUse{Lines: []int{1}, Count: 1, Name: "VENDOR", NameLine: 1},
		},
		{
			name: "typescript: an enum member names it", path: "a.ts",
			src:  "export enum Kind {\n  Vendor = " + vendor + ",\n}\n",
			want: rules.LiteralUse{Lines: []int{2}, Count: 1, Name: "Vendor", NameLine: 2},
		},
		{
			name: "typescript: a let does not", path: "a.ts",
			src:  "let kind = " + vendor + "\n",
			want: rules.LiteralUse{Lines: []int{1}, Count: 1},
		},
		{
			name: "typescript: a literal type is a type", path: "a.ts",
			src:  "export type Kind = \"text/plain\" | " + vendor + "\n",
			want: rules.LiteralUse{Typed: true},
		},
		{
			name: "typescript: a member of a list under as const is a type", path: "a.ts",
			src:  "export const KINDS = [\"text/plain\", " + vendor + "] as const\n",
			want: rules.LiteralUse{Typed: true},
		},
		{
			name: "typescript: a member of a schema's enum is a type", path: "a.ts",
			src:  "export const kind = z.enum([\"text/plain\", " + vendor + "]).describe(\"the body's kind\")\n",
			want: rules.LiteralUse{Typed: true},
		},
		{
			name: "typescript: the argument of a schema's literal is a type", path: "a.ts",
			src:  "export const kind = z.object({ kind: z.literal(" + vendor + ") })\n",
			want: rules.LiteralUse{Typed: true},
		},
		{
			name: "typescript: a comparison with typeof", path: "a.ts",
			src: "export const f = (v: unknown) => typeof v === \"undefined\"\n",
		},
		{name: "typescript: a module path", path: "a.ts", src: "import { kind } from " + vendor + "\n"},
		{name: "typescript: a dynamic import", path: "a.ts", src: "export const load = () => import(" + vendor + ")\n"},
		{name: "typescript: a directive", path: "a.ts", src: "\"use client\"\n\nexport const a = 1\n"},
		{
			name: "typescript: a template with a value in it", path: "a.ts",
			src: "export const f = (v: string) => `application/vnd.${v}+json`\n",
		},
		{
			name: "typescript: an attribute of markup", path: "a.tsx",
			src: "export const A = () => <div className=\"flex items-center gap-2\" id={" + vendor + "} />\n",
		},
		{name: "typescript: an object key", path: "a.ts", src: "export const sizes = { " + vendor + ": 1 }\n"},
	}
}

func TestLiteralRoles(t *testing.T) {
	t.Parallel()
	cases := append(append(goRoleCases(), pyRoleCases()...), tsRoleCases()...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fp := printOf(t, tc.path, tc.src)
			if reflect.DeepEqual(tc.want, rules.LiteralUse{}) {
				if len(fp.Literals) != 0 {
					t.Errorf("literals = %+v, want none", fp.Literals)
				}

				return
			}
			if got := fp.Literals[vendorValue]; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("literal = %+v, want %+v (all: %+v)", got, tc.want, fp.Literals)
			}
		})
	}
}

func TestLiteralGroupsRankAndCap(t *testing.T) {
	t.Parallel()
	g := &litLangs()[0]
	tree := map[string]string{}
	for i, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		tmpl := g.write
		if i < 2 {
			tmpl = g.test
		}
		tree["api/"+name+".go"] = g.head + fillLit(tmpl, name, vendor) + "\n" +
			fillLit(g.write, name+"Hash", `"sha256-base64"`)
	}
	tree["api/sums.go"] = g.head + fillLit(g.named, "", `"sha256-base64"`)
	got := literalGroups(t, tree)
	if len(got) != 2 {
		t.Fatalf("groups = %+v, want two", got)
	}
	// A value a constant names ranks by its files: 11. The other by files
	// times the share of places that test it: 10 x 2/10.
	named, tested := got[0], got[1]
	if named.Value != "sha256-base64" || named.Rank != 11 || named.Files != 11 || len(named.Sites) != 8 {
		t.Errorf("first = %+v, want sha256-base64 at rank 11 over 11 files, 8 of them listed", named)
	}
	if tested.Value != vendorValue || tested.Rank != 2 || tested.Uses != 2 || tested.Written != 10 {
		t.Errorf("second = %+v, want %s at rank 2 with 2 uses of 10 places", tested, vendorValue)
	}
}

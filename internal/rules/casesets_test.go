package rules_test

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/rules"
)

// caseFamilies fingerprints a tree and lists its case-set families.
func caseFamilies(t *testing.T, tree map[string]string) []rules.CaseFamily {
	t.Helper()
	prints := map[string]*rules.Fingerprint{}
	for path, src := range tree {
		prints[path] = printOf(t, path, src)
	}

	return rules.CaseFamilies(prints)
}

// caseSites lists a family's sites as path, unit, and kind.
func caseSites(f *rules.CaseFamily) string {
	names := make([]string, 0, len(f.Sites))
	for _, s := range f.Sites {
		names = append(names, fmt.Sprintf("%s %s %s", s.Path, s.Unit, s.Kind))
	}

	return strings.Join(names, ", ")
}

// Three places that each enumerate the kinds of a shape, per language: a
// switch, an if chain that leaves the last kind to its else, and a table of
// handlers.
const (
	goShapeSwitch = `package shape

func Area(k Kind) int {
	switch k {
	case KindCircle:
		return 1
	case KindSquare, KindTriangle:
		return 2
	case KindHexagon:
		return 3
	}

	return 0
}
`
	goShapeIf = `package shape

func Name(k Kind) string {
	if k == KindCircle {
		return "circle"
	} else if k == KindSquare {
		return "square"
	} else if KindTriangle == k {
		return "triangle"
	} else {
		return "other"
	}
}
`
	goShapeTable = `package shape

var sides = map[Kind]func() int{
	KindCircle:   circleSides,
	KindSquare:   squareSides,
	KindTriangle: triangleSides,
	KindHexagon:  hexagonSides,
}
`
	pyShapeSwitch = `def area(kind):
    match kind:
        case Kind.CIRCLE:
            return 1
        case Kind.SQUARE | Kind.TRIANGLE:
            return 2
        case Kind.HEXAGON:
            return 3
    return 0
`
	pyShapeIf = `def name(kind):
    if kind == Kind.CIRCLE:
        return "circle"
    elif kind == Kind.SQUARE:
        return "square"
    elif kind in (Kind.TRIANGLE,):
        return "triangle"
    else:
        return "other"
`
	pyShapeTable = `SIDES = {
    Kind.CIRCLE: circle_sides,
    Kind.SQUARE: square_sides,
    Kind.TRIANGLE: lambda: 3,
    Kind.HEXAGON: partial(sides, 6),
}
`
	tsShapeSwitch = `export function area(kind: Kind): number {
  switch (kind) {
    case Kind.Circle:
      return 1
    case Kind.Square:
    case Kind.Triangle:
      return 2
    case Kind.Hexagon:
      return 3
  }
  return 0
}
`
	tsShapeIf = `export function name(kind: Kind): string {
  if (kind === Kind.Circle) {
    return 'circle'
  } else if (kind === Kind.Square) {
    return 'square'
  } else if (Kind.Triangle === kind) {
    return 'triangle'
  } else {
    return 'other'
  }
}
`
	tsShapeTable = `export const sides: Record<Kind, () => number> = {
  [Kind.Circle]: circleSides,
  [Kind.Square]: squareSides,
  [Kind.Triangle]: () => 3,
  [Kind.Hexagon]: function () { return 6 },
}
`
)

// shapeFixture is the three places in one language, and how a result names
// them and the cases.
type shapeFixture struct {
	name, ext        string
	sw, chain, table string
	units            string // of the switch, the chain, and the table
	cases            []string
}

func shapeFixtures() []shapeFixture {
	goCases := []string{"KindCircle", "KindHexagon", "KindSquare", "KindTriangle"}
	pyCases := []string{"Kind.CIRCLE", "Kind.HEXAGON", "Kind.SQUARE", "Kind.TRIANGLE"}
	tsCases := []string{"Kind.Circle", "Kind.Hexagon", "Kind.Square", "Kind.Triangle"}

	return []shapeFixture{
		{
			name: "go", ext: ".go", sw: goShapeSwitch, chain: goShapeIf, table: goShapeTable,
			units: "Area Name sides", cases: goCases,
		},
		{
			name: "python", ext: ".py", sw: pyShapeSwitch, chain: pyShapeIf, table: pyShapeTable,
			units: "area name SIDES", cases: pyCases,
		},
		{
			name: "typescript", ext: ".ts", sw: tsShapeSwitch, chain: tsShapeIf, table: tsShapeTable,
			units: "area name sides", cases: tsCases,
		},
		{
			name: "tsx", ext: ".tsx", sw: tsShapeSwitch, chain: tsShapeIf, table: tsShapeTable,
			units: "area name sides", cases: tsCases,
		},
	}
}

// checkShapeFamily holds a family to the three places of a shape fixture: the
// chain lacks the last case and has the only default.
func checkShapeFamily(t *testing.T, f *rules.CaseFamily, tt *shapeFixture) {
	t.Helper()
	if !reflect.DeepEqual(f.Cases, tt.cases) {
		t.Errorf("cases = %v, want %v", f.Cases, tt.cases)
	}
	units := strings.Fields(tt.units)
	wantSites := strings.ReplaceAll(fmt.Sprintf("shape/area.x %s switch, shape/name.x %s if, shape/sides.x %s table",
		units[0], units[1], units[2]), ".x", tt.ext)
	if caseSites(f) != wantSites {
		t.Fatalf("sites = %s, want %s", caseSites(f), wantSites)
	}
	checkShapeSites(t, f, tt.cases[1]) // the hexagon, which the chain leaves to its else
	// Three places in a tree of three: a case all of them hold weighs
	// ln(1 + 3/3), and the one the chain lacks ln(1 + 3/2).
	if f.Weight != 3 || f.Rank != 8.99 {
		t.Errorf("weight = %v, rank = %v, want 3 and 8.99", f.Weight, f.Rank)
	}
}

// checkShapeSites holds the three sites of a shape family to what each
// names, lacks, and leaves to a default.
func checkShapeSites(t *testing.T, f *rules.CaseFamily, last string) {
	t.Helper()
	sw, chain, table := &f.Sites[0], &f.Sites[1], &f.Sites[2]
	if len(sw.Missing) != 0 || len(table.Missing) != 0 || !reflect.DeepEqual(chain.Missing, []string{last}) {
		t.Errorf("missing = %v, %v, %v, want only the chain to lack %s", sw.Missing, chain.Missing, table.Missing, last)
	}
	if sw.Default || !chain.Default || table.Default {
		t.Errorf("default = %v, %v, %v, want only the chain's else", sw.Default, chain.Default, table.Default)
	}
	if sw.Cases != 4 || chain.Cases != 3 || table.Cases != 4 || sw.Line < 2 || sw.EndLine <= sw.Line {
		t.Errorf("switch = %+v, chain = %+v, table = %+v", sw, chain, table)
	}
}

func TestCaseFamiliesJoinASwitchAChainAndATable(t *testing.T) {
	t.Parallel()
	for _, tt := range shapeFixtures() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := caseFamilies(t, map[string]string{
				"shape/area" + tt.ext: tt.sw, "shape/name" + tt.ext: tt.chain, "shape/sides" + tt.ext: tt.table,
			})
			if len(got) != 1 {
				t.Fatalf("got %d families, want one: %+v", len(got), got)
			}
			checkShapeFamily(t, &got[0], &tt)
		})
	}
}

// Places that enumerate a case of every other kind the pass reads.
const (
	goTypeSwitch = `package ast

func walk(n Node) {
	switch v := n.(type) {
	case *ast.Ident, *ast.BasicLit:
		leaf(v)
	case *CallExpr:
		call(v)
	case string, []byte, error:
		raw(v)
	default:
	}
}
`
	goTypeNames = `package ast

func name(n Node) string {
	switch n.(type) {
	case *ast.Ident:
		return "ident"
	case *ast.BasicLit:
		return "lit"
	case *CallExpr:
		return "call"
	}

	return ""
}
`
	goBareSwitch = `package tok

func class(t Token) int {
	switch {
	case t == token.ADD || t == token.SUB:
		return 1
	case t == token.MUL:
		return 2
	case t == token.QUO:
		return 3
	}

	return 0
}
`
	goKeyedArray = `package tok

var names = [...]string{
	token.ADD: "+",
	token.SUB: "-",
	token.MUL: "*",
	token.QUO: "/",
}
`
	pyIsinstance = `def show(node):
    if isinstance(node, Leaf):
        return 1
    elif isinstance(node, (Branch, ast.Root)):
        return 2
    elif isinstance(node, Twig):
        return 3
    elif isinstance(node, str):
        return 4
`
	pyClassPatterns = `def size(node):
    match node:
        case Leaf():
            return 1
        case Branch(kids=kids) | ast.Root(kids=kids):
            return 2
        case Twig():
            return 3
        case _:
            return 0
`
	pyStringMatch = `def route(method):
    match method:
        case 'GET' | "HEAD":
            return 1
        case 'POST':
            return 2
        case 'DELETE':
            return 3
`
	pyMembership = `def kind(method):
    if method in ('GET', 'HEAD'):
        return 'safe'
    elif method == 'POST':
        return 'unsafe'
    elif 'DELETE' == method:
        return 'unsafe'
`
	pyHandlers = `HANDLERS = {
    'GET': on_get,
    'HEAD': on_head,
    'POST': on_post,
    'DELETE': on_delete,
}
`
	tsStringSwitch = "export function handle(ev: Ev) {\n  switch (ev.type) {\n    case 'open':\n      return 1\n" +
		"    case \"close\":\n      return 2\n    case `error`:\n      return 3\n" +
		"    default:\n      return 0\n  }\n}\n"
	tsPropertyTable = `export const handlers = {
  open: onOpen,
  close: onClose,
  error: (e: Ev) => fail(e),
  'message': onMessage,
}
`
	tsInstanceofCode = `export function code(e: unknown) {
  if (e instanceof NotFound) {
    return 404
  } else if (e instanceof Denied) {
    return 403
  } else if (e instanceof Busy) {
    return 503
  }
}
`
	tsInstanceofRetry = `export function retry(e: unknown) {
  if (e instanceof NotFound || e instanceof Error) {
    return false
  } else if (e instanceof Busy) {
    return true
  } else if (e instanceof Denied) {
    return false
  }
}
`
	tsSwitchTrue = `export function a(level: Level) {
  switch (true) {
    case level === Level.Debug:
      return 1
    case level === Level.Info:
      return 2
    case level === Level.Warn:
      return 3
  }
}
`
	tsSwitchLevel = `export function b(level: Level) {
  switch (level) {
    case Level.Debug:
      return 'd'
    case Level.Info:
      return 'i'
    case Level.Warn:
      return 'w'
  }
}
`
)

func TestCaseFamiliesReadEachKindOfCase(t *testing.T) {
	t.Parallel()
	tests := []struct {
		tree map[string]string
		name string
		want []string
	}{
		{
			name: "go type switch and a switch that only names the types",
			tree: map[string]string{"ast/walk.go": goTypeSwitch, "ast/name.go": goTypeNames},
			want: []string{"CallExpr", "ast.BasicLit", "ast.Ident"},
		},
		{
			name: "go switch with no subject and an array keyed by constants",
			tree: map[string]string{"tok/class.go": goBareSwitch, "tok/name.go": goKeyedArray},
			want: []string{"token.ADD", "token.MUL", "token.QUO", "token.SUB"},
		},
		{
			name: "python isinstance chain and class patterns",
			tree: map[string]string{"nodes/show.py": pyIsinstance, "nodes/size.py": pyClassPatterns},
			want: []string{"Branch", "Leaf", "Twig", "ast.Root"},
		},
		{
			name: "python strings in a match, a membership test, and a table of handlers",
			tree: map[string]string{
				"http/route.py": pyStringMatch, "http/safe.py": pyMembership, "http/table.py": pyHandlers,
			},
			want: []string{`"DELETE"`, `"GET"`, `"HEAD"`, `"POST"`},
		},
		{
			name: "typescript strings, a table by property name, and instanceof",
			tree: map[string]string{
				"ev/handle.ts": tsStringSwitch, "ev/table.ts": tsPropertyTable,
				"ev/err.ts": tsInstanceofCode, "ev/retry.ts": tsInstanceofRetry,
			},
			want: []string{`"close"`, `"error"`, `"open"`, "Busy", "Denied", "NotFound"},
		},
		{
			name: "typescript switch over true",
			tree: map[string]string{"lvl/a.ts": tsSwitchTrue, "lvl/b.ts": tsSwitchLevel},
			want: []string{"Level.Debug", "Level.Info", "Level.Warn"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var cases []string
			for _, f := range caseFamilies(t, tt.tree) {
				cases = append(cases, f.Cases...)
			}
			slices.Sort(cases)
			if !reflect.DeepEqual(cases, tt.want) {
				t.Errorf("cases = %q, want %q", cases, tt.want)
			}
		})
	}
}

// twice writes one source under two file names of a directory, so whatever
// it enumerates would be a family of two if the pass read it.
func twice(dir, ext, src string) map[string]string {
	return map[string]string{dir + "/one" + ext: src, dir + "/two" + ext: src}
}

// Places that look like a set of cases and are not one.
const (
	pyRecord = `def row(user):
    return {
        'name': user.name,
        'email': user.email,
        'phone': user.phone,
        'city': user.city,
    }
`
	pyRecordBySubscript = `def row(raw):
    return {
        'name': raw['name'],
        'email': raw.get('email'),
        'phone': clean(raw['phone']),
        'city': raw['city'],
    }
`
	tsShorthandRecord = `export function row(name: string, email: string, phone: string, city: string) {
  return { name, email, phone, city }
}
`
	tsRecord = `export function row(u: User) {
  return { name: u.name, email: u.email, phone: u.phone, city: u.city }
}
`
	tsPrefixedRecord = `export function entry(recipient: Recipient, auth: Auth) {
  return {
    recipientEmail: recipient.email,
    recipientName: recipient.name,
    recipientId: recipient.id,
    recipientRole: recipient.role,
    actionAuth: auth.derived,
  }
}
`
	pyPrefixedRecord = `def entry(user, team):
    return {
        'user_id': user.id,
        'user_email': user.email,
        'team_id': team.id,
        'team_name': team.name,
    }
`
	tsNamedArguments = `export function open(app: App, size: number) {
  return new Window('main', {
    owner: app.id,
    width: size,
    height: size,
    theme: dark,
    parent: root.id,
  })
}
`
	pyNamedArguments = `def open(app, size):
    return Window('main', {
        Option.OWNER: app,
        Option.WIDTH: size,
        Option.HEIGHT: size,
        Option.THEME: dark,
    })
`
	goNamedArguments = `package ui

func open() Window {
	return build(map[Option]any{
		OptionOwner:  owner,
		OptionWidth:  width,
		OptionHeight: height,
		OptionTheme:  dark,
	})
}
`
	tsObjectOfMethods = `export function channel(): Channel {
  return {
    async open() {
      await dial()
    },
    send() {
      return push()
    },
    receive(id: string) {
      return poll(id)
    },
    close() {
      return drop()
    },
  }
}
`
	pyMessages = `MESSAGES = {
    'greeting': 'Hello',
    'farewell': 'Goodbye',
    'thanks': 'Thank you',
    'sorry': 'Sorry',
}
`
	tsMessages = `export const messages = {
  greeting: 'Hello',
  farewell: 'Goodbye',
  thanks: 'Thank you',
  sorry: 'Sorry',
}
`
	goMessages = `package i18n

var messages = map[string]string{
	"greeting": "Hello",
	"farewell": "Goodbye",
	"thanks":   "Thank you",
	"sorry":    "Sorry",
}
`
	goStructLiteral = `package cfg

var base = Config{
	Host: hostName,
	Port: portNumber,
	User: userName,
	Path: rootPath,
}
`
	pyBuiltinTypes = `def enc(v):
    if isinstance(v, str):
        return 1
    elif isinstance(v, (int, float)):
        return 2
    elif isinstance(v, (list, tuple)):
        return 3
    elif isinstance(v, dict):
        return 4
`
	goBuiltinTypes = `package enc

func enc(v any) int {
	switch v.(type) {
	case string, []byte:
		return 1
	case int, int64, float64:
		return 2
	case bool:
		return 3
	case error:
		return 4
	}

	return 0
}
`
	tsTypeof = `export function enc(v: unknown) {
  if (typeof v === 'string') {
    return 1
  } else if (typeof v === 'number') {
    return 2
  } else if (typeof v === 'boolean') {
    return 3
  } else if (v instanceof Array || v instanceof Date || v instanceof Map) {
    return 4
  }
}
`
	pyTrivialIf = `def a(x):
    if x is None:
        return 0
    elif x is True:
        return 1
    elif x is False:
        return 2
    elif x == 0:
        return 3
    elif x == 'y':
        return 4
`
	pyTrivialMatch = `def b(x):
    match x:
        case None:
            return 0
        case True:
            return 1
        case False:
            return 2
        case 0 | 1 | 2:
            return 3
        case 'y':
            return 4
`
	goTwoShared = `package shape

func odd(k Kind) int {
	switch k {
	case KindCircle:
		return 1
	case KindSquare:
		return 2
	case KindStar:
		return 3
	}

	return 0
}
`
	pyManySubjects = `def check(a, b, c):
    if a == Mode.FAST:
        return 1
    elif b == Mode.SLOW:
        return 2
    elif c == Mode.SAFE:
        return 3
`
	pyTableInItsSwitch = `def both(kind):
    match kind:
        case Kind.CIRCLE | Kind.SQUARE | Kind.TRIANGLE:
            return {
                Kind.CIRCLE: circle,
                Kind.SQUARE: square,
                Kind.TRIANGLE: triangle,
                Kind.HEXAGON: hexagon,
            }[kind]
        case Kind.HEXAGON:
            return 6
        case _:
            return 0
`
	pySplicedStrings = `def pick(x, p):
    if x == f'{p}-alpha':
        return 1
    elif x == f'{p}-beta':
        return 2
    elif x == f'{p}-gamma':
        return 3
`
)

// quietTrees are trees the pass must list nothing for.
func quietTrees() map[string]map[string]string {
	return map[string]map[string]string{
		"a record copied field by field, python":     twice("user", ".py", pyRecord),
		"a record copied by subscript, python":       twice("user", ".py", pyRecordBySubscript),
		"a record of shorthand names, typescript":    twice("user", ".ts", tsShorthandRecord),
		"a record copied field by field, typescript": twice("user", ".ts", tsRecord),
		"a record under prefixed keys, typescript":   twice("audit", ".ts", tsPrefixedRecord),
		"a record under prefixed keys, python":       twice("audit", ".py", pyPrefixedRecord),
		"the named arguments of a call, typescript":  twice("ui", ".ts", tsNamedArguments),
		"the named arguments of a call, python":      twice("ui", ".py", pyNamedArguments),
		"the named arguments of a call, go":          twice("ui", ".go", goNamedArguments),
		"an object of methods, typescript":           twice("chan", ".ts", tsObjectOfMethods),
		"a table of translated strings, python":      twice("i18n", ".py", pyMessages),
		"a table of translated strings, typescript":  twice("i18n", ".ts", tsMessages),
		"a table of translated strings, go":          twice("i18n", ".go", goMessages),
		"a struct literal, go":                       twice("cfg", ".go", goStructLiteral),
		"builtin type checks, python":                twice("enc", ".py", pyBuiltinTypes),
		"builtin type checks, go":                    twice("enc", ".go", goBuiltinTypes),
		"typeof checks, typescript":                  twice("enc", ".ts", tsTypeof),
		"a chain over one subject after another":     twice("req", ".py", pyManySubjects),
		"strings with a value written in":            twice("fmt", ".py", pySplicedStrings),

		"one switch alone": {"shape/area.go": goShapeSwitch, "shape/other.go": "package shape\n"},
		"two chains that share only trivial cases": {
			"flag/a.py": pyTrivialIf, "flag/b.py": pyTrivialMatch,
		},
		"two chains that share two cases": {"shape/area.go": goShapeSwitch, "shape/odd.go": goTwoShared},
		"tests":                           {"shape/area_test.go": goShapeSwitch, "shape/name_test.go": goShapeIf},
		"a test directory":                {"tests/area.py": pyShapeSwitch, "tests/name.py": pyShapeIf},
		"examples":                        {"examples/a/area.ts": tsShapeSwitch, "examples/b/sides.ts": tsShapeTable},
		"production against a test":       {"shape/area.go": goShapeSwitch, "shape/name_test.go": goShapeIf},
		"scripts, migrations, and seed data": {
			"scripts/area.py": pyShapeSwitch, "db/migrations/name.py": pyShapeIf, "seed_data/sides.py": pyShapeTable,
		},
		"a table inside the switch arm that builds it": {"shape/both.py": pyTableInItsSwitch},
	}
}

func TestCaseFamiliesStayQuiet(t *testing.T) {
	t.Parallel()
	for name, tree := range quietTrees() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := caseFamilies(t, tree); len(got) != 0 {
				t.Errorf("got %d families, want none: %+v", len(got), got)
			}
		})
	}
}

// goKindSwitch is a Go function that switches over the given constants.
func goKindSwitch(name string, cases ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package m\n\nfunc %s(k Kind) int {\n\tswitch k {\n", name)
	for i, c := range cases {
		fmt.Fprintf(&b, "\tcase %s:\n\t\treturn %d\n", c, i)
	}
	b.WriteString("\t}\n\n\treturn 0\n}\n")

	return b.String()
}

// TestCaseFamiliesKeepAPlaceInOneFamily pins the clustering: the place with
// the most joins takes every place joined to it, a place that shares too few
// of the cases the family settles on stays free for another, and no place is
// listed twice. Here a enumerates two sets. Four places hold the first with
// it and two the second, so the family settles on the first, and the two are
// a family of their own.
func TestCaseFamiliesKeepAPlaceInOneFamily(t *testing.T) {
	t.Parallel()
	first := []string{"A1", "A2", "A3", "A4"}
	got := caseFamilies(t, map[string]string{
		"m/a.go": goKindSwitch("a", "A1", "A2", "A3", "A4", "B1", "B2", "B3"),
		"m/b.go": goKindSwitch("b", first...),
		"m/c.go": goKindSwitch("c", first...),
		"m/f.go": goKindSwitch("f", first...),
		"m/g.go": goKindSwitch("g", first...),
		"m/d.go": goKindSwitch("d", "B1", "B2", "B3"),
		"m/e.go": goKindSwitch("e", "B1", "B2", "B3", "C1"),
	})
	if len(got) != 2 {
		t.Fatalf("got %d families, want two: %+v", len(got), got)
	}
	const wantFirst = "m/a.go a switch, m/b.go b switch, m/c.go c switch, m/f.go f switch, m/g.go g switch"
	if s := caseSites(&got[0]); s != wantFirst {
		t.Errorf("first family = %s", s)
	}
	if s := caseSites(&got[1]); s != "m/d.go d switch, m/e.go e switch" {
		t.Errorf("second family = %s", s)
	}
	if !reflect.DeepEqual(got[0].Cases, first) {
		t.Errorf("first family's cases = %v, want %v", got[0].Cases, first)
	}
}

// TestCaseFamiliesAreOrdered pins that the list comes out the same whatever
// order the tree's map is walked in.
func TestCaseFamiliesAreOrdered(t *testing.T) {
	t.Parallel()
	tree := map[string]string{}
	for i := range 12 {
		tree[fmt.Sprintf("p%d/area.go", i)] = strings.ReplaceAll(goShapeSwitch, "Kind", fmt.Sprintf("K%d", i%4))
		tree[fmt.Sprintf("p%d/name.go", i)] = strings.ReplaceAll(goShapeIf, "Kind", fmt.Sprintf("K%d", i%4))
	}
	first := caseFamilies(t, tree)
	if len(first) != 4 {
		t.Fatalf("got %d families, want four", len(first))
	}
	for range 20 {
		if again := caseFamilies(t, tree); !reflect.DeepEqual(first, again) {
			t.Fatalf("two runs differ:\n%+v\n%+v", first, again)
		}
	}
}

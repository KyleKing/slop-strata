package rules

import (
	"crypto/sha1" //nolint:gosec // a dedup hash, not a security primitive
	"encoding/hex"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/kyleking/slop-strata/internal/lang"
)

// minLiteralLen is the shortest string content worth tracking across files;
// below it repeats are vocabulary, not duplication.
const minLiteralLen = 8

// minDupFiles is how many distinct files must share a literal before it is
// reported.
const minDupFiles = 3

// minDupOccurrences is how many times a literal must appear across the tree
// before it is reported.
const minDupOccurrences = 4

// minCloneStatements and minCloneLines keep trivial shapes (getters, one-line
// wrappers) out of the clone report: a body must reach either bound.
const (
	minCloneStatements = 6
	minCloneLines      = 8
)

// LiteralUse is where a string literal first appears in one file and how many
// times the file carries it.
type LiteralUse struct {
	Line  int `json:"line"`
	Count int `json:"count"`
}

// CloneUnit is one function's normalized-body hash plus where it lives. Test
// marks a test case, whose name is the function's or, for a subtest, its label.
type CloneUnit struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
	Line int    `json:"line"`
	Test bool   `json:"test,omitempty"`
}

// IfaceDecl is one interface declaration and the method names it requires.
type IfaceDecl struct {
	Name    string   `json:"name"`
	Methods []string `json:"methods"`
	Line    int      `json:"line"`
}

// Fingerprint is the per-blob record cross-file passes join on. It travels
// inside the blob cache entry, so passes never re-parse.
type Fingerprint struct {
	Literals    map[string]LiteralUse `json:"literals,omitempty"`     // literal text -> first line and count
	MethodImpls map[string][]string   `json:"method_impls,omitempty"` // receiver type -> method names
	Implements  map[string][]int      `json:"implements,omitempty"`   // interface name -> lines (ts)
	Declared    map[string]int        `json:"declared,omitempty"`     // top-level names -> line
	Exported    map[string]int        `json:"exported,omitempty"`     // public-API names -> line
	Kinds       map[string]string     `json:"kinds,omitempty"`        // exported name -> declaration kind, where kept
	Options     map[string]int        `json:"options,omitempty"`      // WithX funcs returning an Option -> line
	Referenced  map[string]int        `json:"referenced,omitempty"`   // identifier -> occurrences
	Imports     map[string]int        `json:"imports,omitempty"`      // imported module names -> line
	Strings     map[string]int        `json:"strings,omitempty"`      // short string contents -> first line
	ProseRefs   map[string]int        `json:"prose_refs,omitempty"`   // code-shaped names in comments -> line
	ConfigKeys  map[string]int        `json:"config_keys,omitempty"`  // config key paths -> line
	Selected    map[string][]string   `json:"selected,omitempty"`     // imported module name -> names read through it
	ExportAs    map[string]string     `json:"export_as,omitempty"`    // exported alias -> local name (ts)
	GoModule    string                `json:"go_module,omitempty"`    // module path, on a go.mod only
	Default     string                `json:"default,omitempty"`      // the name a default export gives (ts)
	EntryRefs   []string              `json:"entry_refs,omitempty"`   // entry targets a package manifest names
	Clones      []CloneUnit           `json:"clones,omitempty"`
	Directives  []Directive           `json:"directives,omitempty"`
	Ifaces      []IfaceDecl           `json:"ifaces,omitempty"`
	ImportRefs  []ImportRef           `json:"import_refs,omitempty"` // imports as written, for the import graph
	Units       []Unit                `json:"units,omitempty"`       // declarations and methods, for module depth
	Passed      []string              `json:"passed,omitempty"`      // imported names bound and never used
	Members     []string              `json:"members,omitempty"`     // names read off a value, not a module
	Frags       Fragments             `json:"frags,omitempty"`       // statements, for the duplicate pass
	Cases       CaseSets              `json:"cases,omitempty"`       // switches, if chains, and tables, by their cases
	JSX         JSXShapes             `json:"jsx,omitempty"`         // markup, by the shape of its elements
	Barrel      bool                  `json:"barrel,omitempty"`      // only imports and re-exports, declares nothing
}

// referenceKinds are the node kinds whose text counts as a use of a name.
var referenceKinds = map[string]bool{ //nolint:gochecknoglobals // fixed vocabulary
	kindIdentifier: true, kindTypeIdentifier: true, "field_identifier": true, "property_identifier": true,
	"shorthand_property_identifier": true, "shorthand_property_identifier_pattern": true,
}

// importStmtKinds are the node kinds of an import statement in the three grammars.
var importStmtKinds = map[string]bool{ //nolint:gochecknoglobals // fixed vocabulary
	"import_declaration": true, kindImportStmt: true, kindImportFrom: true,
}

// Collect builds a blob's fingerprint from its parsed tree.
func Collect(ctx *Ctx) *Fingerprint {
	fp := &Fingerprint{}
	uses := &nameUse{selected: map[string][]string{}, inImports: map[string]int{}}

	frags := newFragCollector(ctx)
	cases := newCaseCollector(ctx)
	markup := newJSXCollector(ctx)
	frags.walk(ctx.Root, fragWalk{}, func(n *ts.Node, kind string) {
		cases.visit(n, kind)
		markup.visit(n, kind)
		collectImportRef(ctx, n, kind, fp)
		collectIface(ctx, n, kind, fp)
		uses.note(ctx, n, kind)
		switch {
		case kind == kindExportSpecifier:
			collectExport(ctx, n, fp)
		case ctx.Lang.LiteralKinds[kind]:
			collectLiteral(ctx, n, fp)
			collectShortString(ctx, n, fp)
		case ctx.Lang.FunctionKinds[kind]:
			collectClone(ctx, n, fp)
		case kind == "comment":
			collectProse(ctx, n, fp)
		case importStmtKinds[kind]:
			collectImports(ctx, n, fp)
		case kind == kindExpressionStmt && ctx.Lang == lang.Python:
			collectDocstring(ctx, n, fp)
		case referenceKinds[kind]:
			if fp.Referenced == nil {
				fp.Referenced = map[string]int{}
			}
			fp.Referenced[ctx.Text(n)]++
		}
	})
	fp.Frags = frags.fragments()
	fp.Cases = cases.caseSets()
	fp.JSX = markup.shapes()
	collectUnits(ctx, fp)
	uses.finish(ctx, fp)
	fp.Barrel = isBarrel(ctx, fp)
	fp.Directives = ctx.commentDirectives()

	return fp
}

// collectLiteral records string literals long enough to be interesting, with
// each distinct literal's first line and per-file count. Module names and
// mapping keys repeat by design, so they are skipped.
func collectLiteral(ctx *Ctx, n *ts.Node, fp *Fingerprint) {
	if !strings.Contains(n.Kind(), "string") || repeatsByDesign(ctx, n) {
		return
	}
	text := ctx.Text(n)
	if len(strings.Trim(text, "\"'`")) < minLiteralLen || strings.Contains(text, "${") || strings.Contains(text, "%") {
		return
	}
	if fp.Literals == nil {
		fp.Literals = map[string]LiteralUse{}
	}
	use, ok := fp.Literals[text]
	if !ok {
		use.Line = lineOf(n)
	}
	use.Count++
	fp.Literals[text] = use
}

// repeatsByDesign reports whether a string is a module path or a mapping key.
func repeatsByDesign(ctx *Ctx, n *ts.Node) bool {
	p := n.Parent()
	if p == nil {
		return false
	}
	switch p.Kind() {
	case "import_spec", kindImportStmt, kindImportFrom, kindExportStatement:
		return true
	case kindArguments:
		call := p.Parent()

		return call != nil && call.Kind() == kindCallExpression && ctx.calleeText(call) == textRequire
	case "pair":
		key := p.ChildByFieldName("key")

		return key != nil && key.Equals(*n)
	case "literal_element":
		keyed := p.Parent()
		if keyed == nil || keyed.Kind() != "keyed_element" {
			return false
		}
		key := keyed.ChildByFieldName("key")

		return key != nil && key.Equals(*p)
	}

	return false
}

// collectClone hashes a function's normalized subtree: identifiers and
// literals replaced by placeholders and comments left out, so the same body
// under different names, with or without a comment, hashes alike.
func collectClone(ctx *Ctx, n *ts.Node, fp *Fingerprint) {
	span := int(n.EndPosition().Row) - int(n.StartPosition().Row) + 1
	if span < minCloneLines && blockStatements(n) < minCloneStatements {
		return
	}
	var b strings.Builder
	var walk func(*ts.Node)
	walk = func(c *ts.Node) {
		kind := c.Kind()
		switch {
		case ctx.Lang.CommentKinds[kind]:
		case ctx.Lang.LiteralKinds[kind]:
			b.WriteString("<lit>")
		case kind == kindIdentifier || kind == kindTypeIdentifier || kind == "field_identifier" ||
			kind == "property_identifier" || kind == "shorthand_property_identifier_pattern":
			b.WriteString("<id>")
		default:
			b.WriteString(kind)
			b.WriteByte('(')
			for i := range c.NamedChildCount() {
				walk(c.NamedChild(i))
			}
			b.WriteByte(')')
		}
	}
	walk(n)
	sum := sha1.Sum([]byte(b.String())) //nolint:gosec // dedup hash
	name, test := testCase(ctx, n)
	if !test {
		name = lang.FunctionName(ctx.Lang, n, ctx.Src)
	}
	fp.Clones = append(fp.Clones, CloneUnit{
		Name: name,
		Hash: hex.EncodeToString(sum[:8]),
		Line: lineOf(n),
		Test: test,
	})
}

// testCase names a function that is one test case: a top-level Go Test or
// Benchmark function, a Python test_ function, a Go t.Run subtest closure or a
// TS it or test callback, which are named by their label. Go's b.Run closures
// are benchmarks, not cases, and stay out.
func testCase(ctx *Ctx, n *ts.Node) (string, bool) {
	switch ctx.Lang {
	case lang.Go:
		if n.Kind() == kindFunctionDecl {
			name := lang.FunctionName(ctx.Lang, n, ctx.Src)
			return name, strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Benchmark")
		}

		return callbackLabel(ctx, n, "t.Run")
	case lang.Python:
		name := lang.FunctionName(ctx.Lang, n, ctx.Src)
		return name, n.Kind() == kindFunctionDef && strings.HasPrefix(name, "test_")
	case lang.TypeScript, lang.TSX:
		if label, ok := callbackLabel(ctx, n, "it"); ok {
			return label, true
		}

		return callbackLabel(ctx, n, "test")
	}

	return "", false
}

// callbackLabel reads a function passed as the second argument of a call to
// callee, returning the call's first argument as its label.
func callbackLabel(ctx *Ctx, n *ts.Node, callee string) (string, bool) {
	args := n.Parent()
	if args == nil || args.NamedChildCount() < 2 || !args.NamedChild(1).Equals(*n) {
		return "", false
	}
	call := args.Parent()
	if call == nil || !ctx.Lang.CallKinds[call.Kind()] || ctx.calleeText(call) != callee {
		return "", false
	}
	label := args.NamedChild(0)

	return strings.Trim(ctx.Text(label), "\"'`"), strings.Contains(label.Kind(), "string")
}

// Declaration kinds Kinds keeps for an exported name. The export rules read
// them to leave alone the kinds a name match judges badly: a function, a
// Python class, and a TypeScript type have none.
const (
	declType      = "type"      // a Go type
	declValue     = "value"     // a Go constant or variable
	declClass     = "class"     // a TypeScript class
	declAssigned  = "assigned"  // a Python module-level assignment
	declDecorated = "decorated" // a Python function or class under a decorator
)

// declare records one name a statement at file scope declares, and whether
// the language makes it part of the file's public API: Go by case, Python by
// a leading underscore, TypeScript by the export keyword. The line is the
// declaration's own, below any decorator. Kind is one of the kinds above, or
// empty.
func (fp *Fingerprint) declare(n *ts.Node, name string, exported bool, kind string) {
	if name == "" || name == "_" {
		return
	}
	if fp.Declared == nil {
		fp.Declared = map[string]int{}
	}
	fp.Declared[name] = lineOf(n)
	if !exported {
		return
	}
	if fp.Exported == nil {
		fp.Exported = map[string]int{}
	}
	fp.Exported[name] = lineOf(n)
	if kind == "" {
		return
	}
	if fp.Kinds == nil {
		fp.Kinds = map[string]string{}
	}
	fp.Kinds[name] = kind
}

// collectExport records the name one specifier of an `export { a, b as c }`
// clause offers: the alias is the public name when present.
func collectExport(ctx *Ctx, n *ts.Node, fp *Fingerprint) {
	name := n.ChildByFieldName("alias")
	if name == nil {
		name = n.ChildByFieldName(fieldName)
	}
	if name == nil {
		return
	}
	if fp.Exported == nil {
		fp.Exported = map[string]int{}
	}
	fp.Exported[ctx.Text(name)] = lineOf(n)
}

// identifierShape reports whether text is a plain identifier.
func identifierShape(text string) bool {
	for _, r := range text {
		if r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}

	return text != ""
}

// collectIface reads one node of the walk for the single-implementation
// rule: an interface declaration and the methods it requires (Go method
// specs, TS members), a Go method's receiver, and a TS implements clause.
func collectIface(ctx *Ctx, n *ts.Node, kind string, fp *Fingerprint) {
	switch kind {
	case kindTypeDecl:
		collectGoIface(ctx, n, fp)
	case kindMethodDecl:
		collectGoMethod(ctx, n, fp)
	case kindInterfaceDecl:
		collectTSIface(ctx, n, fp)
	case "implements_clause":
		collectTSImplements(ctx, n, fp)
	}
}

// collectGoIface records each Go interface's method set. A grouped type block
// holds one spec per type, so each spec is checked.
func collectGoIface(ctx *Ctx, decl *ts.Node, fp *Fingerprint) {
	for _, spec := range lang.NamedChildren(decl) {
		if spec.Kind() != "type_spec" {
			continue
		}
		name := spec.ChildByFieldName("name")
		typ := spec.ChildByFieldName("type")
		if name == nil || typ == nil || typ.Kind() != "interface_type" {
			continue
		}
		var methods []string
		lang.EachNamed(typ, func(n *ts.Node) {
			if n.Kind() == "method_elem" || n.Kind() == "method_spec" {
				if m := n.ChildByFieldName("name"); m != nil {
					methods = append(methods, ctx.Text(m))
				}
			}
		})
		fp.Ifaces = append(fp.Ifaces, IfaceDecl{Name: ctx.Text(name), Line: lineOf(spec), Methods: methods})
	}
}

// collectGoMethod records which receiver type a method extends.
func collectGoMethod(ctx *Ctx, decl *ts.Node, fp *Fingerprint) {
	name := decl.ChildByFieldName("name")
	recv := decl.ChildByFieldName("receiver")
	if name == nil || recv == nil {
		return
	}
	// The receiver text is "(v T)" or "(v *T)": pull the last word inside the
	// parens, which is the type name.
	text := strings.Trim(ctx.Text(recv), "()")
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return
	}
	base := strings.TrimLeft(parts[len(parts)-1], "*")
	if fp.MethodImpls == nil {
		fp.MethodImpls = map[string][]string{}
	}
	fp.MethodImpls[base] = append(fp.MethodImpls[base], ctx.Text(name))
}

// collectTSIface records a TS interface's member count (treated as its method
// set for the single-implementor check).
func collectTSIface(ctx *Ctx, decl *ts.Node, fp *Fingerprint) {
	name := decl.ChildByFieldName("name")
	body := decl.ChildByFieldName("body")
	if name == nil || body == nil {
		return
	}
	var methods []string
	for i := range body.NamedChildCount() {
		m := body.NamedChild(i)
		if m.Kind() == "method_signature" || m.Kind() == "property_signature" {
			if mn := m.ChildByFieldName("name"); mn != nil {
				methods = append(methods, ctx.Text(mn))
			}
		}
	}
	fp.Ifaces = append(fp.Ifaces, IfaceDecl{Name: ctx.Text(name), Line: lineOf(decl), Methods: methods})
}

// collectTSImplements records the interfaces one `implements` clause names.
func collectTSImplements(ctx *Ctx, n *ts.Node, fp *Fingerprint) {
	for i := range n.NamedChildCount() {
		c := n.NamedChild(i)
		if c.Kind() == kindTypeIdentifier || c.Kind() == kindIdentifier || c.Kind() == kindGenericType {
			if fp.Implements == nil {
				fp.Implements = map[string][]int{}
			}
			fp.Implements[ctx.Text(c)] = append(fp.Implements[ctx.Text(c)], lineOf(n))
		}
	}
}

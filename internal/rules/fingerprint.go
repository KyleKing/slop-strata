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

// minCloneLines keeps trivial shapes (getters, one-line wrappers) out of the
// clone report.
const minCloneLines = 5

// LiteralUse is where a string literal first appears in one file and how many
// times the file carries it.
type LiteralUse struct {
	Line  int `json:"line"`
	Count int `json:"count"`
}

// CloneUnit is one function's normalized-body hash plus where it lives.
type CloneUnit struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
	Line int    `json:"line"`
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
	Options     map[string]int        `json:"options,omitempty"`      // WithX funcs returning an Option -> line
	Referenced  map[string]bool       `json:"referenced,omitempty"`   // referenced identifiers
	Imports     map[string]int        `json:"imports,omitempty"`      // imported module names -> line
	Strings     map[string]int        `json:"strings,omitempty"`      // short string contents -> first line
	ProseRefs   map[string]int        `json:"prose_refs,omitempty"`   // code-shaped names in comments -> line
	ConfigKeys  map[string]int        `json:"config_keys,omitempty"`  // config key paths -> line
	Clones      []CloneUnit           `json:"clones,omitempty"`
	Ifaces      []IfaceDecl           `json:"ifaces,omitempty"`
}

// Collect builds a blob's fingerprint from its parsed tree.
func Collect(ctx *Ctx) *Fingerprint {
	fp := &Fingerprint{}

	lang.EachNamed(ctx.Root, func(n *ts.Node) {
		kind := n.Kind()
		switch {
		case ctx.Lang.LiteralKinds[kind]:
			collectLiteral(ctx, n, fp)
			collectShortString(ctx, n, fp)
		case ctx.Lang.FunctionKinds[kind]:
			collectClone(ctx, n, fp)
		case kind == "comment":
			collectProse(ctx, n, fp)
		case kind == "import_declaration" || kind == "import_statement" || kind == "import_from_statement":
			collectImports(ctx, n, fp)
		case kind == "expression_statement" && ctx.Lang == lang.Python:
			collectDocstring(ctx, n, fp)
		case kind == kindIdentifier || kind == "field_identifier" || kind == "property_identifier" ||
			kind == "shorthand_property_identifier" || kind == "shorthand_property_identifier_pattern":
			if fp.Referenced == nil {
				fp.Referenced = map[string]bool{}
			}
			fp.Referenced[ctx.Text(n)] = true
		}
	})
	collectDecls(ctx, fp)
	collectExports(ctx, fp)
	collectIfaces(ctx, fp)

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
	case "import_spec", "import_statement", "import_from_statement", kindExportStatement:
		return true
	case "arguments":
		call := p.Parent()

		return call != nil && call.Kind() == kindCallExpression && ctx.calleeText(call) == "require"
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
// literals replaced by placeholders, so the same body under different names
// hashes alike.
func collectClone(ctx *Ctx, n *ts.Node, fp *Fingerprint) {
	span := int(n.EndPosition().Row) - int(n.StartPosition().Row) + 1
	if span < minCloneLines {
		return
	}
	var b strings.Builder
	var walk func(*ts.Node)
	walk = func(c *ts.Node) {
		kind := c.Kind()
		switch {
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
	fp.Clones = append(fp.Clones, CloneUnit{
		Name: lang.FunctionName(ctx.Lang, n, ctx.Src),
		Hash: hex.EncodeToString(sum[:8]),
		Line: lineOf(n),
	})
}

// collectDecls records top-level declared names: package-level functions,
// types, variables, and Go's functional-option helpers.
func collectDecls(ctx *Ctx, fp *Fingerprint) {
	declKinds := map[string]bool{
		"function_declaration": true, "method_declaration": true, "type_declaration": true,
		"var_declaration": true, "const_declaration": true, "var_spec": true, "const_spec": true,
		"function_definition": true, "class_definition": true, "assignment": true,
		"lexical_declaration": true, "variable_declaration": true, "interface_declaration": true,
		"type_alias_declaration": true, "enum_declaration": true, "variable_declarator": true,
	}
	lang.EachNamed(ctx.Root, func(n *ts.Node) {
		if !declKinds[n.Kind()] || !isTopLevel(n) {
			return
		}
		text := declName(ctx, n)
		if text == "" {
			return
		}
		if fp.Declared == nil {
			fp.Declared = map[string]int{}
		}
		fp.Declared[text] = lineOf(n)
		recordExport(ctx, fp, n, text)
		if ctx.Lang == lang.Go && isOptionFunc(ctx, n) {
			if fp.Options == nil {
				fp.Options = map[string]int{}
			}
			fp.Options[text] = lineOf(n)
		}
	})
}

// declName resolves a declaration's name text, empty when it has none.
func declName(ctx *Ctx, n *ts.Node) string {
	name := n.ChildByFieldName("name")
	if name == nil {
		name = n.NamedChild(0)
	}
	if name == nil {
		return ""
	}
	text := ctx.Text(name)
	if !identifierShape(text) {
		return ""
	}

	return text
}

// recordExport marks a declared name exported per its language's rule.
func recordExport(ctx *Ctx, fp *Fingerprint, n *ts.Node, text string) {
	if !exportedByLang(ctx, n, text) {
		return
	}
	if fp.Exported == nil {
		fp.Exported = map[string]int{}
	}
	fp.Exported[text] = lineOf(n)
}

// isTopLevel reports whether a declaration sits at file scope: its parent is
// the root or a declaration_list/module/export wrapper.
func isTopLevel(n *ts.Node) bool {
	p := n.Parent()
	if p == nil {
		return true
	}
	switch p.Kind() {
	case "source_file", "program", "module", "declaration_list", "statement_block",
		kindExportStatement, "ambient_declaration":
		return isTopLevel(p)
	default:
		return false
	}
}

// exportedByLang reports whether a top-level declaration is part of the
// file's public API: Go exports by case, Python by leading underscore, and
// TypeScript by the export keyword.
func exportedByLang(ctx *Ctx, n *ts.Node, name string) bool {
	switch ctx.Lang {
	case lang.Go:
		return name != "" && name[0] >= 'A' && name[0] <= 'Z'
	case lang.Python:
		return !strings.HasPrefix(name, "_")
	default:
		p := n.Parent()
		return p != nil && p.Kind() == kindExportStatement
	}
}

// collectExports records the names an `export { a, b as c }` clause offers:
// the alias is the public name when present.
func collectExports(ctx *Ctx, fp *Fingerprint) {
	lang.EachNamed(ctx.Root, func(n *ts.Node) {
		if n.Kind() != "export_specifier" {
			return
		}
		name := n.ChildByFieldName("alias")
		if name == nil {
			name = n.ChildByFieldName("name")
		}
		if name == nil {
			return
		}
		if fp.Exported == nil {
			fp.Exported = map[string]int{}
		}
		fp.Exported[ctx.Text(name)] = lineOf(n)
	})
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

// isOptionFunc reports whether a function declaration looks like a functional
// option: WithXxx returning a type whose name contains "Option".
func isOptionFunc(ctx *Ctx, n *ts.Node) bool {
	if n.Kind() != "function_declaration" {
		return false
	}
	name := n.ChildByFieldName("name")
	if name == nil || !strings.HasPrefix(ctx.Text(name), "With") {
		return false
	}
	result := n.ChildByFieldName("result")

	return result != nil && strings.Contains(ctx.Text(result), "Option")
}

// collectIfaces records interface declarations and their required methods
// (Go method specs, TS members), plus Go method implementations per receiver
// and TS implements clauses.
func collectIfaces(ctx *Ctx, fp *Fingerprint) {
	switch ctx.Lang {
	case lang.Go:
		lang.EachNamed(ctx.Root, func(n *ts.Node) {
			switch n.Kind() {
			case "type_declaration":
				collectGoIface(ctx, n, fp)
			case "method_declaration":
				collectGoMethod(ctx, n, fp)
			}
		})
	case lang.TypeScript, lang.TSX:
		lang.EachNamed(ctx.Root, func(n *ts.Node) {
			switch n.Kind() {
			case "interface_declaration":
				collectTSIface(ctx, n, fp)
			case "class_declaration", "abstract_class_declaration":
				collectTSImplements(ctx, n, fp)
			}
		})
	}
}

// collectGoIface records a Go interface's method set.
func collectGoIface(ctx *Ctx, decl *ts.Node, fp *Fingerprint) {
	spec := decl.NamedChild(decl.NamedChildCount() - 1)
	if spec == nil || spec.Kind() != "interface_type" {
		return
	}
	name := decl.ChildByFieldName("name")
	if name == nil {
		return
	}
	var methods []string
	lang.EachNamed(spec, func(n *ts.Node) {
		if n.Kind() == "method_elem" || n.Kind() == "method_spec" {
			if m := n.ChildByFieldName("name"); m != nil {
				methods = append(methods, ctx.Text(m))
			}
		}
	})
	fp.Ifaces = append(fp.Ifaces, IfaceDecl{Name: ctx.Text(name), Line: lineOf(decl), Methods: methods})
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

// collectTSImplements records `implements I` clauses.
func collectTSImplements(ctx *Ctx, decl *ts.Node, fp *Fingerprint) {
	lang.EachNamed(decl, func(n *ts.Node) {
		if n.Kind() != "implements_clause" {
			return
		}
		for i := range n.NamedChildCount() {
			c := n.NamedChild(i)
			if c.Kind() == kindTypeIdentifier || c.Kind() == kindIdentifier || c.Kind() == kindGenericType {
				if fp.Implements == nil {
					fp.Implements = map[string][]int{}
				}
				fp.Implements[ctx.Text(c)] = append(fp.Implements[ctx.Text(c)], lineOf(n))
			}
		}
	})
}

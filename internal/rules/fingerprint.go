package rules

import (
	"crypto/sha1" //nolint:gosec // a dedup hash, not a security primitive
	"encoding/hex"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/kyleking/slop-strata/internal/lang"
)

// minLiteralLen is the shortest string worth tracking across files; below it
// repeats are vocabulary, not duplication.
const minLiteralLen = 20

// minDupFiles is how many distinct files must share a literal before it is
// reported.
const minDupFiles = 3

// minCloneLines keeps trivial shapes (getters, one-line wrappers) out of the
// clone report.
const minCloneLines = 5

// CloneUnit is one function's normalized-body hash plus where it lives.
type CloneUnit struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
	Line int    `json:"line"`
}

// IfaceDecl is one interface declaration and the method names it requires.
type IfaceDecl struct {
	Name    string   `json:"name"`
	Line    int      `json:"line"`
	Methods []string `json:"methods"`
}

// Fingerprint is the per-blob record cross-file passes join on. It travels
// inside the blob cache entry, so passes never re-parse.
type Fingerprint struct {
	Literals    map[string]int      `json:"literals,omitempty"` // literal text -> first line
	Clones      []CloneUnit         `json:"clones,omitempty"`
	Ifaces      []IfaceDecl         `json:"ifaces,omitempty"`
	MethodImpls map[string][]string `json:"method_impls,omitempty"` // receiver type -> method names
	Implements  map[string][]int    `json:"implements,omitempty"`   // interface name -> lines (ts)
	Declared    map[string]int      `json:"declared,omitempty"`     // top-level names -> line
	Options     map[string]int      `json:"options,omitempty"`      // WithX funcs returning an Option -> line
	Referenced  map[string]bool     `json:"referenced,omitempty"`   // referenced identifiers
}

// Collect builds a blob's fingerprint from its parsed tree.
func Collect(ctx *Ctx) *Fingerprint {
	fp := &Fingerprint{}

	lang.EachNamed(ctx.Root, func(n *ts.Node) {
		kind := n.Kind()
		switch {
		case ctx.Lang.LiteralKinds[kind]:
			collectLiteral(ctx, n, fp)
		case ctx.Lang.FunctionKinds[kind]:
			collectClone(ctx, n, fp)
		case kind == "identifier":
			if fp.Referenced == nil {
				fp.Referenced = map[string]bool{}
			}
			fp.Referenced[ctx.Text(n)] = true
		}
	})
	collectDecls(ctx, fp)
	collectIfaces(ctx, fp)

	return fp
}

// collectLiteral records string literals long enough to be interesting,
// keeping each distinct literal's first line.
func collectLiteral(ctx *Ctx, n *ts.Node, fp *Fingerprint) {
	if !strings.Contains(n.Kind(), "string") {
		return
	}
	text := ctx.Text(n)
	if len(text) < minLiteralLen || strings.Contains(text, "${") || strings.Contains(text, "%") {
		return
	}
	if fp.Literals == nil {
		fp.Literals = map[string]int{}
	}
	if _, ok := fp.Literals[text]; !ok {
		fp.Literals[text] = lineOf(n)
	}
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
		case kind == "identifier" || kind == "type_identifier" || kind == "field_identifier" ||
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
		if fp.Declared == nil {
			fp.Declared = map[string]int{}
		}
		name := n.ChildByFieldName("name")
		if name == nil {
			name = n.NamedChild(0)
		}
		if name == nil {
			return
		}
		text := ctx.Text(name)
		if !identifierShape(text) {
			return
		}
		fp.Declared[text] = lineOf(n)
		if ctx.Lang.ID == "go" && isOptionFunc(ctx, n) {
			if fp.Options == nil {
				fp.Options = map[string]int{}
			}
			fp.Options[text] = lineOf(n)
		}
	})
}

// isTopLevel reports whether a declaration sits at file scope: its parent is
// the root or a declaration_list/module wrapper.
func isTopLevel(n *ts.Node) bool {
	p := n.Parent()
	if p == nil {
		return true
	}
	switch p.Kind() {
	case "source_file", "program", "module", "declaration_list", "statement_block":
		return isTopLevel(p)
	default:
		return false
	}
}

// identifierShape reports whether text is a plain identifier.
func identifierShape(text string) bool {
	for _, r := range text {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}

	return len(text) > 0
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
	switch ctx.Lang.ID {
	case "go":
		lang.EachNamed(ctx.Root, func(n *ts.Node) {
			switch n.Kind() {
			case "type_declaration":
				collectGoIface(ctx, n, fp)
			case "method_declaration":
				collectGoMethod(ctx, n, fp)
			}
		})
	case "typescript", "tsx":
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
			if c.Kind() == "type_identifier" || c.Kind() == "identifier" || c.Kind() == "generic_type" {
				if fp.Implements == nil {
					fp.Implements = map[string][]int{}
				}
				fp.Implements[ctx.Text(c)] = append(fp.Implements[ctx.Text(c)], lineOf(n))
			}
		}
	})
}

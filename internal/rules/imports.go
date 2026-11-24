package rules

import (
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/kyleking/slop-strata/internal/lang"
)

// Import kinds as the cache stores them. The empty kind is a module-level
// import that runs when the file loads.
const (
	importType     = "type"     // read by a type checker only
	importDeferred = "deferred" // inside a function body, or a dynamic import()
	importMain     = "main"     // under Python's if __name__ == "__main__"
)

// importWildcard is the name of an import that binds everything a module offers.
const importWildcard = "*"

// ImportRef is one import as the file writes it, kept unresolved so the
// cross-file pass can match it to a file of the tree. Spec is the module
// specifier: a dotted Python module with its leading dots, a TypeScript module
// string, or a Go import path. Names are the names bound from it, "*" for a
// wildcard, and none when the import binds the module itself. Local is the
// name a bound module goes by in the file: the alias, the first segment of a
// dotted Python import, the namespace of `import * as ns`, or the last
// element of a Go path. Aliases maps each renamed binding to the name it
// imports, so `a as b` reads {b: a} and a default import {local: default}.
type ImportRef struct {
	Aliases map[string]string `json:"aliases,omitempty"`
	Spec    string            `json:"spec"`
	Kind    string            `json:"kind,omitempty"`
	Local   string            `json:"local,omitempty"`
	Names   []string          `json:"names,omitempty"`
	Line    int               `json:"line"`
}

// alias records that the file calls an imported name by another one.
func (r *ImportRef) alias(local, name string) {
	if local == name || local == "" {
		return
	}
	if r.Aliases == nil {
		r.Aliases = map[string]string{}
	}
	r.Aliases[local] = name
}

// collectImportRef records the import a node holds, if it holds one, with
// the kind its position gives it. Collect calls it for every node of its one
// walk over the tree, so imports cost no parse and no walk of their own.
// Specifiers, names, type-only markers, and the enclosing guard or function
// are all read off tree nodes. Kind is the node's kind, which the caller
// has already read.
func collectImportRef(ctx *Ctx, n *ts.Node, kind string, fp *Fingerprint) {
	switch ctx.Lang {
	case lang.Python:
		pyImportRefs(ctx, n, kind, fp)
	case lang.Go:
		if kind != "import_spec" {
			return
		}
		if p := n.ChildByFieldName("path"); p != nil {
			spec := unquote(ctx.Text(p))
			fp.ImportRefs = append(fp.ImportRefs, ImportRef{Spec: spec, Local: goLocal(ctx, n, spec), Line: lineOf(n)})
		}
	default:
		tsImportRefs(ctx, n, kind, fp)
	}
}

// isBarrel reports whether a file only passes names along: every top-level
// statement is an import, a re-export, a comment, a docstring, or a dunder
// assignment such as __all__, and at least one is an import. Go has no such
// file, since a package cannot re-export another.
func isBarrel(ctx *Ctx, fp *Fingerprint) bool {
	if len(fp.ImportRefs) == 0 || ctx.Lang == lang.Go {
		return false
	}
	for _, n := range lang.NamedChildren(ctx.Root) {
		if !passesNamesAlong(ctx, n) {
			return false
		}
	}

	return true
}

// passesNamesAlong reports whether a top-level statement declares nothing of
// the file's own.
func passesNamesAlong(ctx *Ctx, n *ts.Node) bool {
	switch n.Kind() {
	case "comment", kindImportStmt, kindImportFrom, "future_import_statement":
		return true
	case kindExportStatement:
		return n.ChildByFieldName("source") != nil
	case kindExpressionStmt:
		if n.NamedChildCount() != 1 {
			return false
		}
		expr := n.NamedChild(0)
		if expr.Kind() == kindAssignment {
			left := expr.ChildByFieldName("left")

			return left != nil && strings.HasPrefix(ctx.Text(left), "__")
		}

		return strings.Contains(expr.Kind(), "string")
	default:
		return false
	}
}

// goLocal names the package an import binds: its alias, or the last element
// of the path, skipping a major-version element such as v2.
func goLocal(ctx *Ctx, spec *ts.Node, importPath string) string {
	if name := spec.ChildByFieldName("name"); name != nil {
		return ctx.Text(name)
	}
	parts := strings.Split(importPath, "/")
	last := parts[len(parts)-1]
	if len(parts) > 1 && len(last) > 1 && last[0] == 'v' && strings.Trim(last[1:], "0123456789") == "" {
		last = parts[len(parts)-2]
	}

	return last
}

func unquote(text string) string {
	return strings.Trim(text, "\"'`")
}

// pyImportRefs reads `import a.b` and `from a import b` statements.
func pyImportRefs(ctx *Ctx, n *ts.Node, kind string, fp *Fingerprint) {
	switch kind {
	case kindImportStmt:
		when := pyImportKind(ctx, n)
		for _, c := range lang.NamedChildren(n) {
			local := ""
			if name := c.ChildByFieldName("name"); c.Kind() == kindAliasedImport && name != nil {
				if alias := c.ChildByFieldName("alias"); alias != nil {
					local = ctx.Text(alias)
				}
				c = name
			}
			spec := ctx.Text(c)
			if local == "" {
				local, _, _ = strings.Cut(spec, ".")
			}
			fp.ImportRefs = append(fp.ImportRefs, ImportRef{Spec: spec, Kind: when, Local: local, Line: lineOf(n)})
		}
	case kindImportFrom:
		if module := n.ChildByFieldName("module_name"); module != nil {
			fp.ImportRefs = append(fp.ImportRefs, pyFromImport(ctx, n, module))
		}
	}
}

// kindAliasedImport is Python's `name as alias` inside an import.
const kindAliasedImport = "aliased_import"

// pyFromImport reads one `from module import names` statement.
func pyFromImport(ctx *Ctx, n, module *ts.Node) ImportRef {
	ref := ImportRef{
		Spec: strings.Join(strings.Fields(ctx.Text(module)), ""), Kind: pyImportKind(ctx, n), Line: lineOf(n),
	}
	for _, c := range lang.NamedChildren(n) {
		if c.Equals(*module) {
			continue
		}
		switch c.Kind() {
		case "wildcard_import":
			ref.Names = append(ref.Names, importWildcard)
		case kindAliasedImport:
			if name := c.ChildByFieldName("name"); name != nil {
				ref.Names = append(ref.Names, ctx.Text(name))
				if alias := c.ChildByFieldName("alias"); alias != nil {
					ref.alias(ctx.Text(alias), ctx.Text(name))
				}
			}
		case "dotted_name":
			ref.Names = append(ref.Names, ctx.Text(c))
		}
	}

	return ref
}

// pyImportKind classifies a Python import by what encloses it: a function
// body defers it, and the taken branch of an if on TYPE_CHECKING or on
// __name__ == "__main__" keeps it from running on import.
func pyImportKind(ctx *Ctx, n *ts.Node) string {
	kind := ""
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Kind() {
		case kindFunctionDef, "lambda":
			return importDeferred
		case "if_statement":
			if guard := pyGuardKind(ctx, p, n); guard != "" && kind == "" {
				kind = guard
			}
		}
	}

	return kind
}

// pyGuardKind names the guard of an if statement whose taken branch holds n.
// The condition is read as nodes: a TYPE_CHECKING identifier, bare or as the
// attribute of typing, or a __name__ identifier beside a "__main__" string. A
// negated condition guards nothing, since its branch is the one that runs.
func pyGuardKind(ctx *Ctx, ifStmt, n *ts.Node) string {
	cond, body := ifStmt.ChildByFieldName("condition"), ifStmt.ChildByFieldName("consequence")
	if cond == nil || body == nil || n.StartByte() < body.StartByte() || n.EndByte() > body.EndByte() {
		return ""
	}
	var typeChecking, dunderName, mainString, negated bool
	lang.EachNamed(cond, func(c *ts.Node) {
		switch c.Kind() {
		case "not_operator":
			negated = true
		case kindIdentifier:
			text := ctx.Text(c)
			typeChecking = typeChecking || text == "TYPE_CHECKING"
			dunderName = dunderName || text == "__name__"
		case "string_content":
			mainString = mainString || ctx.Text(c) == "__main__"
		}
	})
	switch {
	case negated:
		return ""
	case typeChecking:
		return importType
	case dunderName && mainString:
		return importMain
	}

	return ""
}

// tsImportRefs reads import and re-export statements, dynamic import(), and
// require() with a literal module.
func tsImportRefs(ctx *Ctx, n *ts.Node, kind string, fp *Fingerprint) {
	switch kind {
	case kindImportStmt, kindExportStatement:
		source := n.ChildByFieldName("source")
		if source == nil {
			return
		}
		spec, line := unquote(ctx.Text(source)), lineOf(n)
		values, types := tsBoundNames(ctx, n)
		if len(types.Names) > 0 {
			types.Spec, types.Kind, types.Line = spec, importType, line
			fp.ImportRefs = append(fp.ImportRefs, types)
		}
		if len(values.Names) > 0 || len(types.Names) == 0 {
			values.Spec, values.Kind, values.Line = spec, tsDeferred(ctx, n), line
			fp.ImportRefs = append(fp.ImportRefs, values)
		}
	case kindCallExpression:
		if ref, ok := tsCallImport(ctx, n); ok {
			fp.ImportRefs = append(fp.ImportRefs, ref)
		}
	}
}

// tsCallImport reads a dynamic import() or a require() of one literal module.
// A dynamic import never runs when the file loads, and a require does unless
// a function holds it.
func tsCallImport(ctx *Ctx, call *ts.Node) (ImportRef, bool) {
	callee := call.ChildByFieldName("function")
	if callee == nil {
		return ImportRef{}, false
	}
	kind := importDeferred
	switch callee.Kind() {
	case kindImport:
	case kindIdentifier:
		if ctx.Text(callee) != textRequire {
			return ImportRef{}, false
		}
		kind = tsDeferred(ctx, call)
	default:
		return ImportRef{}, false
	}
	args := call.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() != 1 || args.NamedChild(0).Kind() != "string" {
		return ImportRef{}, false
	}

	return ImportRef{Spec: unquote(ctx.Text(args.NamedChild(0))), Kind: kind, Line: lineOf(call)}, true
}

// tsDeferred answers importDeferred for a node inside a function body.
func tsDeferred(ctx *Ctx, n *ts.Node) string {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if ctx.Lang.FunctionKinds[p.Kind()] {
			return importDeferred
		}
	}

	return ""
}

// hasToken reports whether a node has an anonymous child of the given kind,
// such as the type keyword of `import type`.
func hasToken(n *ts.Node, kind string) bool {
	for i := range n.ChildCount() {
		if c := n.Child(i); !c.IsNamed() && c.Kind() == kind {
			return true
		}
	}

	return false
}

// typeOnlyStatement reports whether an import or re-export is type-only as a
// whole. The pinned grammar predates `export type * from`, and parses its
// type keyword as an error node holding that one word.
func typeOnlyStatement(ctx *Ctx, stmt *ts.Node) bool {
	if hasToken(stmt, textType) {
		return true
	}
	for _, c := range lang.NamedChildren(stmt) {
		if c.IsError() && ctx.Text(c) == textType {
			return true
		}
	}

	return false
}

// textType is the TypeScript keyword that marks an import type-only.
const textType = "type"

// tsBoundNames splits what an import or re-export binds into a value import
// and a type-only one, each with its names, aliases, and namespace. A default
// import binds "default", and a namespace import or `export *` binds the
// wildcard.
func tsBoundNames(ctx *Ctx, stmt *ts.Node) (ImportRef, ImportRef) {
	var values, types ImportRef
	allTypes := typeOnlyStatement(ctx, stmt)
	side := func(typeOnly bool) *ImportRef {
		if allTypes || typeOnly {
			return &types
		}

		return &values
	}
	if stmt.Kind() == kindExportStatement && hasToken(stmt, importWildcard) {
		ref := side(false)
		ref.Names = append(ref.Names, importWildcard)
	}
	lang.EachNamed(stmt, func(c *ts.Node) {
		switch c.Kind() {
		case "import_specifier", kindExportSpecifier:
			if name := c.ChildByFieldName("name"); name != nil {
				side(hasToken(c, textType)).bindName(ctx, c, name)
			}
		case "namespace_import", "namespace_export":
			ref := side(false)
			ref.Names = append(ref.Names, importWildcard)
			if c.Kind() == "namespace_import" && c.NamedChildCount() > 0 {
				ref.Local = ctx.Text(c.NamedChild(0))
			}
		case kindIdentifier:
			if p := c.Parent(); p != nil && p.Kind() == "import_clause" {
				ref := side(false)
				ref.Names = append(ref.Names, textDefault)
				ref.alias(ctx.Text(c), textDefault)
			}
		}
	})

	return values, types
}

// bindName adds one import or export specifier: its name, and its alias when
// it has one.
func (r *ImportRef) bindName(ctx *Ctx, spec, name *ts.Node) {
	r.Names = append(r.Names, ctx.Text(name))
	if alias := spec.ChildByFieldName("alias"); alias != nil {
		r.alias(ctx.Text(alias), ctx.Text(name))
	}
}

// textDefault is the name a module's default export is imported by.
const textDefault = "default"

// kindExportSpecifier is one name of a TypeScript export clause.
const kindExportSpecifier = "export_specifier"

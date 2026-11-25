package rules

import (
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/kyleking/slop-strata/internal/lang"
)

// minLiteralLen is the shortest string content worth tracking across files;
// below it repeats are vocabulary, not duplication.
const minLiteralLen = 8

// maxLiteralLen is the longest string content tracked. Past it a string is a
// block of text, and a block that repeats is the duplicate pass's to find.
const maxLiteralLen = 200

// maxLiteralLines is how many of a literal's places in one file the
// fingerprint keeps the lines of.
const maxLiteralLines = 4

// LiteralUse is how one file writes one string literal. Count is the places
// that write it out, Uses the ones of those where the code branches or looks
// something up on it: an operand of an equality or membership test, a switch
// case, the key of a subscript, or the key handed to a lookup call. Name is
// the constant at file scope whose whole value it is, declared on NameLine,
// and that declaration is one of the places. Typed marks a literal the file
// also writes in a type position, which is not a place.
type LiteralUse struct {
	Name     string `json:"name,omitempty"`
	Lines    []int  `json:"lines,omitempty"` // the first maxLiteralLines places
	Count    int    `json:"count,omitempty"`
	Uses     int    `json:"uses,omitempty"`
	NameLine int    `json:"name_line,omitempty"`
	Typed    bool   `json:"typed,omitempty"`
}

// litRole is what one string literal is to the code around it.
type litRole uint8

const (
	litSkip  litRole = iota // no place: an import path, a mapping key, a docstring, markup
	litTyped                // written as a type, so the compiler checks its spelling
	litUse                  // compared, switched on, or used as a key
	litNamed                // the whole value of a constant at file scope
	litOther                // any other place: an argument, a value, a result
)

// Node kinds the literal pass reads beyond the shared ones.
const (
	kindBinaryExpr    = "binary_expression"
	kindComparison    = "comparison_operator"
	kindParenthesized = "parenthesized_expression"
	kindPair          = "pair"
	kindKeyedElement  = "keyed_element"
	kindLiteralElem   = "literal_element"
	kindCasePattern   = "case_pattern"
	kindExprCaseGo    = "expression_case"
	kindConstSpec     = "const_spec"
	kindEnumAssign    = "enum_assignment"
	kindLexicalDecl   = "lexical_declaration"
	kindLiteralType   = "literal_type"
	kindJSXAttribute  = "jsx_attribute"
	kindJSXExpression = "jsx_expression"
	kindUnaryExpr     = "unary_expression"
	kindSubscriptPy   = "subscript"
	kindSubscriptTS   = "subscript_expression"
	kindIndexExpr     = "index_expression"
	kindArray         = "array"
	kindConcatenated  = "concatenated_string"
	kindTuple         = "tuple"
	kindPySet         = "set"
	kindVarSpec       = "var_spec"
	fieldOperator     = "operator"
	fieldValue        = "value"
	fieldLeft         = "left"
	fieldRight        = "right"
	textLiteral       = "Literal"
	textTypeof        = "typeof"
	textConst         = "const"
)

// litSkipParents are the parents under which a string is no place of a value:
// a module path, a docstring or a directive that is a statement of its own,
// a part of a string written in pieces, a Go struct tag, and markup.
var litSkipParents = map[string]bool{ //nolint:gochecknoglobals // fixed vocabulary
	"import_spec": true, kindImportStmt: true, kindImportFrom: true, kindExportStatement: true,
	kindExpressionStmt: true, kindConcatenated: true, "field_declaration": true,
	kindJSXAttribute: true,
}

// litSpliced are the children that mark a string with a value written into it.
var litSpliced = map[string]bool{ //nolint:gochecknoglobals // fixed vocabulary
	"interpolation": true, "template_substitution": true,
}

// litEqualOps are the operators that test a value against the literal.
var litEqualOps = map[string]bool{ //nolint:gochecknoglobals // fixed vocabulary
	"==": true, "!=": true, "===": true, "!==": true, "in": true, "not in": true,
}

// litLookups are the functions whose first argument is a key: the last name
// of the callee, as written.
var litLookups = map[string]bool{ //nolint:gochecknoglobals // fixed vocabulary
	"get": true, "Get": true, "has": true, "Has": true, "set": true, "Set": true,
	"pop": true, "setdefault": true, "delete": true, "Del": true,
	"getItem": true, "setItem": true, "removeItem": true,
	"getenv": true, "Getenv": true, "LookupEnv": true, "Lookup": true,
	"getAttribute": true, "setAttribute": true, "hasAttribute": true,
}

// litMemberTests are the methods of a list of literals that test a value
// against each of them.
var litMemberTests = map[string]bool{ //nolint:gochecknoglobals // fixed vocabulary
	"includes": true, "indexOf": true,
}

// collectLiteral records a string literal long enough to be interesting, by
// its content, with what the file does with it. A string with a value written
// into it, one written in pieces, a format string, and one that runs over a
// line are left out.
func collectLiteral(ctx *Ctx, n *ts.Node, fp *Fingerprint) {
	content, ok := literalContent(ctx, n)
	if !ok {
		return
	}
	role, name := literalRole(ctx, n)
	if role == litSkip {
		return
	}
	if fp.Literals == nil {
		fp.Literals = map[string]LiteralUse{}
	}
	use := fp.Literals[content]
	if role == litTyped {
		use.Typed = true
		fp.Literals[content] = use

		return
	}
	use.Count++
	if len(use.Lines) < maxLiteralLines {
		use.Lines = append(use.Lines, lineOf(n))
	}
	switch role {
	case litUse:
		use.Uses++
	case litNamed:
		if use.Name == "" {
			use.Name, use.NameLine = name, lineOf(n)
		}
	case litSkip, litTyped, litOther:
	}
	fp.Literals[content] = use
}

// literalContent reads the content of a string the pass tracks: a whole
// string literal on one line, of minLiteralLen to maxLiteralLen bytes, with
// no value written into it and no format verb.
func literalContent(ctx *Ctx, n *ts.Node) (string, bool) {
	if kind := n.Kind(); !strings.Contains(kind, kindString) || kind == kindConcatenated {
		return "", false
	}
	for i := range n.NamedChildCount() {
		if litSpliced[n.NamedChild(i).Kind()] {
			return "", false
		}
	}
	content := unquoted(ctx.Text(n))
	if len(content) < minLiteralLen || len(content) > maxLiteralLen || strings.ContainsAny(content, "%\n") {
		return "", false
	}

	return content, true
}

// literalRole reads what a string is to the code around it, and the name of
// the constant it is the value of.
func literalRole(ctx *Ctx, n *ts.Node) (litRole, string) {
	p := n.Parent()
	if p == nil || litSkipParents[p.Kind()] || repeatsByDesign(ctx, n) {
		return litSkip, ""
	}
	if inTypePosition(ctx, n) {
		return litTyped, ""
	}
	if p.Kind() == kindJSXExpression {
		if gp := p.Parent(); gp != nil && gp.Kind() == kindJSXAttribute {
			return litSkip, ""
		}
	}
	if name, ok := constantName(ctx, n); ok {
		return litNamed, name
	}
	if typeofOperand(ctx, n) {
		return litSkip, ""
	}
	if comparedLiteral(ctx, n) || caseLiteral(n) || keyLiteral(ctx, n) {
		return litUse, ""
	}

	return litOther, ""
}

// repeatsByDesign reports whether a string is a module path or a mapping key.
func repeatsByDesign(ctx *Ctx, n *ts.Node) bool {
	p := n.Parent()
	switch p.Kind() {
	case kindArguments:
		call := p.Parent()

		return call != nil && call.Kind() == kindCallExpression &&
			(ctx.calleeText(call) == textRequire || ctx.calleeText(call) == kindImport)
	case kindPair:
		key := p.ChildByFieldName("key")

		return key != nil && key.Equals(*n)
	case kindLiteralElem:
		keyed := p.Parent()
		if keyed == nil || keyed.Kind() != kindKeyedElement {
			return false
		}
		key := keyed.ChildByFieldName("key")

		return key != nil && key.Equals(*p)
	}

	return false
}

// litSchemaCalls are the functions that declare the values a schema admits:
// the last name of the callee, as zod and its like spell it.
var litSchemaCalls = map[string]bool{ //nolint:gochecknoglobals // fixed vocabulary
	"enum": true, "literal": true,
}

// inTypePosition reports whether a string is written as a type: a TypeScript
// literal type, a member of an array or object under `as const`, an argument
// of a schema's enum or literal, anything inside a Python annotation, or a
// member of a Python Literal[...] wherever it stands.
func inTypePosition(ctx *Ctx, n *ts.Node) bool {
	from := n
	for a := n.Parent(); a != nil; from, a = a, a.Parent() {
		switch a.Kind() {
		case kindLiteralType, kindPyType:
			return true
		case kindSubscriptPy:
			if v := a.ChildByFieldName(fieldValue); v != nil && lastName(ctx.Text(v)) == textLiteral {
				return true
			}
		case kindAsExpr:
			if last := a.Child(a.ChildCount() - 1); !from.Equals(*n) && last != nil && last.Kind() == textConst {
				return true
			}
		case kindCallExpression:
			if litSchemaCalls[lastName(ctx.calleeText(a))] {
				return true
			}
		case kindBlock, kindStatementBlock, kindExpressionStmt:
			return false
		}
	}

	return false
}

// lastName is the part of a dotted name after its last dot.
func lastName(text string) string {
	return text[strings.LastIndexByte(text, '.')+1:]
}

// bare walks up from a value through the parentheses and the Go literal
// element that only wrap it, and returns the wrapped node and its parent.
func bare(n *ts.Node) (*ts.Node, *ts.Node) {
	p := n.Parent()
	for p != nil && (p.Kind() == kindParenthesized || p.Kind() == kindLiteralElem || p.Kind() == kindAsExpr) {
		n, p = p, p.Parent()
	}

	return n, p
}

// constantName names the constant a string is the whole value of: a Go
// const or var of one name at file scope, a Python assignment to one name at
// module or class level, a TypeScript const at file scope, exported or not,
// and a TypeScript enum member.
func constantName(ctx *Ctx, n *ts.Node) (string, bool) {
	v, p := bare(n)
	if p == nil {
		return "", false
	}
	switch p.Kind() {
	case kindExprList:
		return goConstantName(ctx, p)
	case kindAssignment:
		return pyConstantName(ctx, v, p)
	case kindVarDeclarator:
		return tsConstantName(ctx, v, p)
	case kindEnumAssign:
		if name := p.ChildByFieldName(fieldName); name != nil {
			return ctx.Text(name), true
		}
	}

	return "", false
}

// goConstantName reads `const Name = "v"` and `var Name = "v"` at file scope.
func goConstantName(ctx *Ctx, list *ts.Node) (string, bool) {
	spec := list.Parent()
	if list.NamedChildCount() != 1 || spec == nil || (spec.Kind() != kindConstSpec && spec.Kind() != kindVarSpec) {
		return "", false
	}
	decl := spec.Parent()
	for decl != nil && strings.HasSuffix(decl.Kind(), "_spec_list") {
		decl = decl.Parent()
	}
	if decl == nil || decl.Parent() == nil || decl.Parent().Parent() != nil {
		return "", false
	}
	var names []*ts.Node
	for _, c := range lang.NamedChildren(spec) {
		if c.Kind() == kindIdentifier {
			names = append(names, c)
		}
	}
	if len(names) != 1 {
		return "", false
	}

	return ctx.Text(names[0]), true
}

// pyConstantName reads `NAME = "v"` at module level and in a class body.
func pyConstantName(ctx *Ctx, v, assign *ts.Node) (string, bool) {
	left, right := assign.ChildByFieldName(fieldLeft), assign.ChildByFieldName(fieldRight)
	if left == nil || right == nil || !right.Equals(*v) || left.Kind() != kindIdentifier {
		return "", false
	}
	stmt := assign.Parent()
	if stmt == nil || stmt.Kind() != kindExpressionStmt {
		return "", false
	}
	scope := stmt.Parent()
	if scope != nil && scope.Kind() == kindBlock {
		if class := scope.Parent(); class == nil || class.Kind() != kindClassDef {
			return "", false
		}
	} else if scope == nil || scope.Parent() != nil {
		return "", false
	}

	return ctx.Text(left), true
}

// tsConstantName reads `const name = "v"` at file scope, under export or not.
func tsConstantName(ctx *Ctx, v, declarator *ts.Node) (string, bool) {
	name, value := declarator.ChildByFieldName(fieldName), declarator.ChildByFieldName(fieldValue)
	decl := declarator.Parent()
	if name == nil || value == nil || !value.Equals(*v) || name.Kind() != kindIdentifier ||
		decl == nil || decl.Kind() != kindLexicalDecl || decl.NamedChildCount() != 1 ||
		!strings.HasPrefix(ctx.Text(decl), "const") {
		return "", false
	}
	scope := decl.Parent()
	if scope != nil && scope.Kind() == kindExportStatement {
		scope = scope.Parent()
	}
	if scope == nil || scope.Parent() != nil {
		return "", false
	}

	return ctx.Text(name), true
}

// operatorOf reads the operator of a binary expression or a comparison: the
// field where the grammar names one, else the tokens between the operands.
func operatorOf(ctx *Ctx, n *ts.Node) string {
	if op := n.ChildByFieldName(fieldOperator); op != nil {
		return ctx.Text(op)
	}
	var ops []string
	for i := range n.ChildCount() {
		if c := n.Child(i); !c.IsNamed() {
			ops = append(ops, c.Kind())
		}
	}

	return strings.Join(ops, " ")
}

// comparedLiteral reports whether a string is an operand of an equality or
// membership test, alone or as a member of the list the test reads.
func comparedLiteral(ctx *Ctx, n *ts.Node) bool {
	_, p := bare(n)
	if p == nil {
		return false
	}
	switch p.Kind() {
	case kindBinaryExpr, kindComparison:
		return litEqualOps[operatorOf(ctx, p)]
	case kindList, kindTuple, kindPySet:
		if gp := p.Parent(); gp != nil && gp.Kind() == kindComparison {
			return litEqualOps[operatorOf(ctx, gp)]
		}
	case kindArray:
		member := p.Parent()
		if member == nil || member.Kind() != kindMember {
			return false
		}
		prop := member.ChildByFieldName("property")
		object := member.ChildByFieldName("object")

		return prop != nil && object != nil && object.Equals(*p) && litMemberTests[ctx.Text(prop)]
	}

	return false
}

// typeofOperand reports whether a string is compared with a typeof, which
// names a type of the language and nothing of the program.
func typeofOperand(ctx *Ctx, n *ts.Node) bool {
	v, p := bare(n)
	if p == nil || p.Kind() != kindBinaryExpr {
		return false
	}
	for _, side := range []string{fieldLeft, fieldRight} {
		other := p.ChildByFieldName(side)
		if other == nil || other.Equals(*v) {
			continue
		}
		if other.Kind() == kindUnaryExpr && strings.HasPrefix(ctx.Text(other), textTypeof) {
			return true
		}
	}

	return false
}

// caseLiteral reports whether a string is a case of a switch or a match.
func caseLiteral(n *ts.Node) bool {
	v, p := bare(n)
	if p == nil {
		return false
	}
	switch p.Kind() {
	case kindSwitchCase:
		value := p.ChildByFieldName(fieldValue)

		return value != nil && value.Equals(*v)
	case kindExprList:
		gp := p.Parent()

		return gp != nil && gp.Kind() == kindExprCaseGo
	case kindCasePattern:
		return true
	case "union_pattern":
		gp := p.Parent()

		return gp != nil && gp.Kind() == kindCasePattern
	}

	return false
}

// keyLiteral reports whether a string is the key of a subscript or the first
// argument of a lookup call.
func keyLiteral(ctx *Ctx, n *ts.Node) bool {
	v, p := bare(n)
	if p == nil {
		return false
	}
	switch p.Kind() {
	case kindSubscriptPy:
		key := p.ChildByFieldName(kindSubscriptPy)

		return key != nil && key.Equals(*v)
	case kindSubscriptTS, kindIndexExpr:
		key := p.ChildByFieldName("index")

		return key != nil && key.Equals(*v)
	case kindArguments, "argument_list":
		call := p.Parent()
		if call == nil || !ctx.Lang.CallKinds[call.Kind()] || p.NamedChildCount() == 0 || !p.NamedChild(0).Equals(*v) {
			return false
		}

		return litLookups[lastName(ctx.calleeText(call))]
	}

	return false
}

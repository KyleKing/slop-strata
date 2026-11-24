package rules

import (
	"maps"
	"math"
	"slices"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/kyleking/slop-strata/internal/lang"
)

// Unit is one named piece of a file that module depth counts: a top-level
// function, type, class, constant, or variable, or a method written
// Type.method. The unit with no name holds the mass of the functions no named
// unit contains. Forwards names the callee of a function whose body only
// passes its own parameters to one call: a function by name, a method of the
// same receiver as ".name", and any other callee as "*". Public is the
// language's own visibility, which depth reads only where it cannot attribute
// a use.
type Unit struct {
	Name     string  `json:"n,omitempty"`
	Forwards string  `json:"f,omitempty"`
	Line     int     `json:"l,omitempty"`
	End      int     `json:"e,omitempty"`
	Mass     float64 `json:"m,omitempty"`
	Public   bool    `json:"p,omitempty"`
}

// Weight is one function's line range and complexity mass.
type Weight struct {
	Start int
	End   int
	Mass  float64
}

// massScale rounds a stored mass to two decimals, which keeps the cached
// payload short.
const massScale = 100

// Weigh credits each function's mass to the smallest unit that holds its
// lines, so a method's mass is its own and a closure's is its function's.
// Mass no unit holds goes to the unit with no name.
func (fp *Fingerprint) Weigh(weights []Weight) {
	loose := 0.0
	for _, w := range weights {
		best := -1
		for i := range fp.Units {
			u := &fp.Units[i]
			if u.Line > w.Start || w.End > u.End {
				continue
			}
			if best < 0 || u.End-u.Line < fp.Units[best].End-fp.Units[best].Line {
				best = i
			}
		}
		if best < 0 {
			loose += w.Mass
		} else {
			fp.Units[best].Mass += w.Mass
		}
	}
	for i := range fp.Units {
		fp.Units[i].Mass = math.Round(fp.Units[i].Mass*massScale) / massScale
	}
	if loose > 0 {
		fp.Units = append(fp.Units, Unit{Mass: math.Round(loose*massScale) / massScale})
	}
}

// collectUnits lists the file's top-level declarations and the methods of its
// classes, and records each top-level name in Declared and Exported. It reads
// only the statements at file scope and the members of each class, never a
// function body.
func collectUnits(ctx *Ctx, fp *Fingerprint) {
	for _, n := range lang.NamedChildren(ctx.Root) {
		switch ctx.Lang {
		case lang.Go:
			goUnits(ctx, n, fp)
		case lang.Python:
			pyUnits(ctx, n, fp)
		default:
			tsUnits(ctx, n, false, fp)
		}
	}
	for i := range fp.Units {
		u := &fp.Units[i]
		if _, named := fp.Exported[u.Name]; named && ctx.Lang != lang.Go && ctx.Lang != lang.Python {
			u.Public = true // named by an export clause away from its declaration
		}
	}
}

// addUnit records one unit over a node's lines.
func (fp *Fingerprint) addUnit(n *ts.Node, name string, public bool, forwards string) {
	if name == "" || name == "_" || name == lang.AnonymousName {
		return
	}
	fp.Units = append(fp.Units, Unit{
		Name: name, Line: lineOf(n), End: int(n.EndPosition().Row) + 1, Public: public, Forwards: forwards,
	})
}

// goUnits reads one top-level Go declaration. A method is a unit of its own,
// named for its receiver type, and a grouped declaration gives one per name.
func goUnits(ctx *Ctx, n *ts.Node, fp *Fingerprint) {
	switch n.Kind() {
	case kindFunctionDecl, kindMethodDecl:
		name := lang.FunctionName(ctx.Lang, n, ctx.Src)
		fp.addUnit(n, name, goPublic(name), forwardTarget(ctx, n))
		if n.Kind() == kindFunctionDecl {
			fp.declare(n, name, goPublic(name), "")
			fp.option(ctx, n, name)
		}
	case kindTypeDecl, "const_declaration", "var_declaration":
		for _, spec := range lang.NamedChildren(n) {
			if strings.HasSuffix(spec.Kind(), "_list") {
				for _, inner := range lang.NamedChildren(spec) {
					goSpecUnits(ctx, inner, fp)
				}

				continue
			}
			goSpecUnits(ctx, spec, fp)
		}
	}
}

// goSpecUnits records every name one type, const, or var spec declares.
func goSpecUnits(ctx *Ctx, spec *ts.Node, fp *Fingerprint) {
	kind := declValue
	if spec.Kind() == "type_spec" || spec.Kind() == "type_alias" {
		kind = declType
	}
	for i := range spec.ChildCount() {
		// The comma between two names sits under the same field as the names.
		if spec.FieldNameForChild(uint32(i)) != fieldName || !spec.Child(i).IsNamed() {
			continue
		}
		name := ctx.Text(spec.Child(i))
		fp.addUnit(spec, name, goPublic(name), "")
		fp.declare(spec, name, goPublic(name), kind)
	}
}

// option records a Go functional-option helper: WithXxx returning a type
// whose name contains "Option".
func (fp *Fingerprint) option(ctx *Ctx, n *ts.Node, name string) {
	result := n.ChildByFieldName("result")
	if !strings.HasPrefix(name, "With") || result == nil || !strings.Contains(ctx.Text(result), "Option") {
		return
	}
	if fp.Options == nil {
		fp.Options = map[string]int{}
	}
	fp.Options[name] = lineOf(n)
}

// fieldName is the grammar field most declarations keep their name under.
const fieldName = "name"

// goPublic reports whether Go exports a name, read for a method off the
// method and not its receiver.
func goPublic(name string) bool {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}

	return name != "" && name[0] >= 'A' && name[0] <= 'Z'
}

// kindDecorated wraps a Python function or class that carries decorators.
const kindDecorated = "decorated_definition"

// kindClassDef is a Python class.
const kindClassDef = "class_definition"

// pyUnits reads one top-level Python statement: a function, a class with its
// methods, or an assignment to one plain name.
func pyUnits(ctx *Ctx, n *ts.Node, fp *Fingerprint) {
	def := pyDefinition(n)
	kind := ""
	if def != n {
		kind = declDecorated
	}
	switch def.Kind() {
	case kindFunctionDef:
		name := ctx.fieldText(def, fieldName)
		fp.addUnit(n, name, !strings.HasPrefix(name, "_"), forwardTarget(ctx, def))
		fp.declare(def, name, !strings.HasPrefix(name, "_"), kind)
	case kindClassDef:
		name := ctx.fieldText(def, fieldName)
		fp.addUnit(n, name, !strings.HasPrefix(name, "_"), "")
		fp.declare(def, name, !strings.HasPrefix(name, "_"), kind)
		pyMethods(ctx, def, name, fp)
	case kindExpressionStmt:
		if def.NamedChildCount() != 1 {
			return
		}
		stmt := def.NamedChild(0)
		left := stmt.ChildByFieldName("left")
		if left == nil || left.Kind() != kindIdentifier {
			return
		}
		name := ctx.Text(left)
		if name == pyAll {
			pyAllNames(ctx, stmt.ChildByFieldName("right"), fp)
		}
		if stmt.Kind() != kindAssignment {
			return
		}
		fp.declare(n, name, !strings.HasPrefix(name, "_"), declAssigned)
		if !isDunder(name) {
			fp.addUnit(n, name, !strings.HasPrefix(name, "_"), "")
		}
	}
}

// pyAll is the module attribute that lists a Python module's public names.
const pyAll = "__all__"

// pyAllNames records the names a module lists in __all__, assigned or added
// to, as entry targets: the module publishes them whether or not any file of
// the tree reads them.
func pyAllNames(ctx *Ctx, list *ts.Node, fp *Fingerprint) {
	if list == nil || (list.Kind() != kindList && list.Kind() != "tuple") {
		return
	}
	for _, item := range lang.NamedChildren(list) {
		if item.Kind() != kindString {
			continue
		}
		if name := unquote(ctx.Text(item)); identifierShape(name) {
			fp.EntryRefs = append(fp.EntryRefs, ":"+name)
		}
	}
}

// pyDefinition unwraps a decorated definition to the function or class.
func pyDefinition(n *ts.Node) *ts.Node {
	if n.Kind() == kindDecorated {
		if def := n.ChildByFieldName("definition"); def != nil {
			return def
		}
	}

	return n
}

// isDunder reports whether a name is one Python reserves, such as __init__.
func isDunder(name string) bool {
	return strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__")
}

// pyMethods records the methods of a class. A dunder method is called by
// naming the class or by an operator, never by its own name, so its mass
// stays with the class.
func pyMethods(ctx *Ctx, class *ts.Node, owner string, fp *Fingerprint) {
	body := class.ChildByFieldName(fieldBody)
	if body == nil {
		return
	}
	for _, c := range lang.NamedChildren(body) {
		def := pyDefinition(c)
		if def.Kind() != kindFunctionDef {
			continue
		}
		if name := ctx.fieldText(def, fieldName); name != "" && !isDunder(name) {
			fp.addUnit(c, owner+"."+name, !strings.HasPrefix(name, "_"), forwardTarget(ctx, def))
		}
	}
}

// fieldBody is the grammar field a function or class keeps its body under.
const fieldBody = "body"

// fieldText reads the text of a node's named field, or "".
func (c *Ctx) fieldText(n *ts.Node, field string) string {
	if child := n.ChildByFieldName(field); child != nil {
		return c.Text(child)
	}

	return ""
}

// tsUnits reads one top-level TypeScript statement. Exported says whether an
// export statement wraps it.
func tsUnits(ctx *Ctx, n *ts.Node, exported bool, fp *Fingerprint) {
	switch n.Kind() {
	case kindExportStatement:
		tsExportUnits(ctx, n, fp)
	case "ambient_declaration":
		for _, c := range lang.NamedChildren(n) {
			tsUnits(ctx, c, exported, fp)
		}
	case "class_declaration", "abstract_class_declaration":
		name := ctx.fieldText(n, fieldName)
		fp.addUnit(n, name, exported, "")
		fp.declare(n, name, exported, declClass)
		tsMethods(ctx, n, name, fp)
	case "lexical_declaration", "variable_declaration":
		for _, d := range lang.NamedChildren(n) {
			name := d.ChildByFieldName(fieldName)
			if d.Kind() == kindVarDeclarator && name != nil && name.Kind() == kindIdentifier {
				fp.addUnit(d, ctx.Text(name), exported, forwardTarget(ctx, d.ChildByFieldName("value")))
				fp.declare(d, ctx.Text(name), exported, "")
			}
		}
	default:
		tsNamedUnit(ctx, n, exported, fp)
	}
}

// tsNamedUnit records a declaration that is one name and no more: a
// function, an interface, a type alias, or an enum.
func tsNamedUnit(ctx *Ctx, n *ts.Node, exported bool, fp *Fingerprint) {
	switch n.Kind() {
	case kindFunctionDecl, "generator_function_declaration":
		fp.addUnit(n, ctx.fieldText(n, fieldName), exported, forwardTarget(ctx, n))
		fp.declare(n, ctx.fieldText(n, fieldName), exported, "")
	case kindInterfaceDecl, "type_alias_declaration", "enum_declaration":
		fp.addUnit(n, ctx.fieldText(n, fieldName), exported, "")
		fp.declare(n, ctx.fieldText(n, fieldName), exported, "")
	case "function_signature":
		// An overload or an ambient function: a name with no body, which
		// would count twice as a unit beside its implementation.
		fp.declare(n, ctx.fieldText(n, fieldName), exported, "")
	}
}

// tsExportUnits reads an export statement of the file's own names: a wrapped
// declaration, a default export, or a clause that renames local names. A
// re-export from another module declares nothing here.
func tsExportUnits(ctx *Ctx, n *ts.Node, fp *Fingerprint) {
	if n.ChildByFieldName("source") != nil {
		return
	}
	isDefault := hasToken(n, textDefault)
	target := n.ChildByFieldName("declaration")
	if target == nil {
		target = n.ChildByFieldName("value")
	}
	if target == nil {
		tsExportClause(ctx, n, fp)

		return
	}
	before := len(fp.Units)
	tsUnits(ctx, target, true, fp)
	if !isDefault {
		return
	}
	switch {
	case len(fp.Units) > before:
		fp.Default = fp.Units[before].Name
	case target.Kind() == kindIdentifier:
		fp.Default = ctx.Text(target)
	default:
		fp.addUnit(n, textDefault, true, forwardTarget(ctx, target))
	}
}

// tsExportClause reads `export { a, b as c }`: each alias is a second name
// for a local one.
func tsExportClause(ctx *Ctx, n *ts.Node, fp *Fingerprint) {
	lang.EachNamed(n, func(c *ts.Node) {
		if c.Kind() != kindExportSpecifier {
			return
		}
		name, alias := ctx.fieldText(c, fieldName), ctx.fieldText(c, "alias")
		if name == "" || alias == "" || alias == name {
			return
		}
		if fp.ExportAs == nil {
			fp.ExportAs = map[string]string{}
		}
		fp.ExportAs[alias] = name
		for i := range fp.Units {
			if fp.Units[i].Name == name {
				fp.Units[i].Public = true
			}
		}
	})
}

// tsMethods records the methods of a class, and the fields that hold a
// function. The constructor runs by naming the class, so its mass stays there.
func tsMethods(ctx *Ctx, class *ts.Node, owner string, fp *Fingerprint) {
	body := class.ChildByFieldName(fieldBody)
	if body == nil || owner == "" {
		return
	}
	for _, c := range lang.NamedChildren(body) {
		name := ctx.fieldText(c, fieldName)
		if name == "" || name == "constructor" || !identifierShape(strings.TrimPrefix(name, "#")) {
			continue
		}
		fn := c
		if c.Kind() == "public_field_definition" {
			fn = c.ChildByFieldName("value")
		}
		if fn != nil && ctx.Lang.FunctionKinds[fn.Kind()] {
			fp.addUnit(c, owner+"."+name, true, forwardTarget(ctx, fn))
		}
	}
}

// forwardTarget names the function a pure forwarder calls, or "". A forwarder
// has one statement: a call, returned or not, whose every argument is one of
// the function's own parameters. A docstring and comments beside it are
// allowed.
func forwardTarget(ctx *Ctx, fn *ts.Node) string {
	if fn == nil || !ctx.Lang.FunctionKinds[fn.Kind()] {
		return ""
	}
	call := onlyCall(ctx, fn.ChildByFieldName(fieldBody))
	if call == nil {
		return ""
	}
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return ""
	}
	params := map[string]bool{}
	for _, field := range []string{"parameters", "parameter", "receiver"} {
		if list := fn.ChildByFieldName(field); list != nil {
			paramNames(ctx, list, params)
		}
	}
	for _, arg := range lang.NamedChildren(args) {
		if !ownParam(ctx, arg, params) {
			return ""
		}
	}

	return forwardName(ctx, fn, call.ChildByFieldName("function"))
}

// onlyCall answers the call that is a body's only statement, or nil.
func onlyCall(ctx *Ctx, body *ts.Node) *ts.Node {
	if body == nil {
		return nil
	}
	if ctx.Lang.CallKinds[body.Kind()] {
		return body // an arrow function with an expression body
	}
	var only *ts.Node
	for _, st := range bodyStatements(body) {
		if st.Kind() == kindComment || bareString(st) {
			continue
		}
		if only != nil {
			return nil
		}
		only = st
	}
	if only == nil || (only.Kind() != kindReturn && only.Kind() != kindExpressionStmt) {
		return nil
	}

	return callIn(ctx, only)
}

// kindReturn is a return statement in all three grammars.
const kindReturn = "return_statement"

// callIn unwraps a statement that holds one call and nothing else: returned
// or not, awaited or not.
func callIn(ctx *Ctx, expr *ts.Node) *ts.Node {
	for !ctx.Lang.CallKinds[expr.Kind()] {
		switch expr.Kind() {
		case kindReturn, kindExpressionStmt, kindExprList, "await", "await_expression":
		default:
			return nil
		}
		if expr.NamedChildCount() != 1 {
			return nil
		}
		expr = expr.NamedChild(0)
	}

	return expr
}

// bodyStatements lists a block's statements. The Go grammar wraps them in a
// statement list, beside any comment that leads the block.
func bodyStatements(body *ts.Node) []*ts.Node {
	var out []*ts.Node
	for _, c := range lang.NamedChildren(body) {
		if c.Kind() == "statement_list" {
			out = append(out, lang.NamedChildren(c)...)

			continue
		}
		out = append(out, c)
	}

	return out
}

// bareString reports whether a statement is a string and nothing else, as a
// Python docstring is.
func bareString(st *ts.Node) bool {
	return st.Kind() == kindExpressionStmt && st.NamedChildCount() == 1 && st.NamedChild(0).Kind() == kindString
}

// paramNames adds the names a parameter list binds, leaving out the names
// inside type annotations.
func paramNames(ctx *Ctx, n *ts.Node, names map[string]bool) {
	switch n.Kind() {
	case kindPyType, "type_annotation":
		return
	case kindIdentifier:
		names[ctx.Text(n)] = true

		return
	}
	for i := range n.NamedChildCount() {
		paramNames(ctx, n.NamedChild(i), names)
	}
}

// ownParam reports whether a call argument is one of the caller's own
// parameters, bare, spread, or passed by keyword.
func ownParam(ctx *Ctx, arg *ts.Node, params map[string]bool) bool {
	switch arg.Kind() {
	case kindIdentifier:
		return params[ctx.Text(arg)]
	case "keyword_argument":
		value := arg.ChildByFieldName("value")

		return value != nil && value.Kind() == kindIdentifier && params[ctx.Text(value)]
	case "list_splat", "dictionary_splat", "spread_element", "variadic_argument":
		return arg.NamedChildCount() == 1 && ownParam(ctx, arg.NamedChild(0), params)
	}

	return false
}

// Forward targets a Unit stores besides a plain function name.
const (
	// A method of the forwarder's own type carries this prefix.
	forwardMember = "."
	// A callee reached through some other value, which nothing here can
	// place, reads as this.
	forwardElsewhere = "*"
)

// Node kinds the unit pass shares with the other passes.
const (
	kindComment       = "comment"
	kindString        = "string"
	kindPyType        = "type"
	kindTypeDecl      = "type_declaration"
	kindInterfaceDecl = "interface_declaration"
	kindVarDeclarator = "variable_declarator"
)

// forwardName names what a forwarder calls: a function by its name, a method
// of the same receiver as ".name", and anything else as forwardElsewhere.
func forwardName(ctx *Ctx, fn, callee *ts.Node) string {
	if callee == nil {
		return ""
	}
	var object *ts.Node
	var member string
	switch callee.Kind() {
	case kindIdentifier:
		return ctx.Text(callee)
	case kindSelector:
		object, member = callee.ChildByFieldName("operand"), ctx.fieldText(callee, "field")
	case kindAttribute:
		object, member = callee.ChildByFieldName("object"), ctx.fieldText(callee, kindAttribute)
	case kindMember:
		object, member = callee.ChildByFieldName("object"), ctx.fieldText(callee, "property")
	default:
		return ""
	}
	if object != nil && member != "" && isReceiver(ctx, fn, object) {
		return forwardMember + member
	}

	return forwardElsewhere
}

// isReceiver reports whether a node names the value a method was called on:
// TypeScript's this, Python's self or cls, or the Go receiver.
func isReceiver(ctx *Ctx, fn, object *ts.Node) bool {
	switch object.Kind() {
	case "this":
		return true
	case kindIdentifier:
	default:
		return false
	}
	text := ctx.Text(object)
	if ctx.Lang == lang.Python {
		return text == "self" || text == "cls"
	}
	recv := fn.ChildByFieldName("receiver")
	if recv == nil {
		return false
	}
	names := map[string]bool{}
	paramNames(ctx, recv, names)

	return names[text]
}

// Member-access node kinds, one per language.
const (
	kindSelector  = "selector_expression"
	kindAttribute = "attribute"
	kindMember    = "member_expression"
)

// nameUse gathers, during the one walk Collect makes, which names a file
// reads through each base name and how often each identifier sits inside an
// import statement or an export clause.
type nameUse struct {
	selected  map[string][]string
	inImports map[string]int
}

// note reads one node of the walk.
func (u *nameUse) note(ctx *Ctx, n *ts.Node, kind string) {
	switch kind {
	case kindSelector:
		u.selection(ctx, n, "operand", "field")
	case "qualified_type":
		u.selection(ctx, n, "package", fieldName)
	case kindAttribute:
		u.selection(ctx, n, "object", kindAttribute)
	case kindMember:
		u.selection(ctx, n, "object", "property")
	case "nested_type_identifier":
		u.selection(ctx, n, "module", fieldName)
	case kindImportStmt, kindImportFrom:
		u.countImported(ctx, n)
	case kindExportStatement:
		if n.ChildByFieldName("declaration") == nil && n.ChildByFieldName("value") == nil {
			u.countImported(ctx, n)
		}
	}
}

// selection records base.name for the base a member chain starts at. A
// chain that starts at anything but a plain name, such as a call's result,
// is kept under no base: it reads a member and cannot name a module.
func (u *nameUse) selection(ctx *Ctx, n *ts.Node, baseField, nameField string) {
	name, root := n.ChildByFieldName(nameField), n.ChildByFieldName(baseField)
	for root != nil && root.Kind() == n.Kind() {
		root = root.ChildByFieldName(baseField)
	}
	if name == nil || root == nil {
		return
	}
	base := ""
	if root.Kind() == kindIdentifier || root.Kind() == "package_identifier" {
		base = ctx.Text(root)
	}
	u.selected[base] = append(u.selected[base], ctx.Text(name))
}

// countImported counts the identifiers of an import statement or an export
// clause, which name a binding without using it.
func (u *nameUse) countImported(ctx *Ctx, n *ts.Node) {
	lang.EachNamed(n, func(c *ts.Node) {
		if c.Kind() == kindIdentifier {
			u.inImports[ctx.Text(c)]++
		}
	})
}

// finish stores what the depth pass reads: the names selected through each
// imported module, the imported names the file binds and never uses, and the
// member names read off any value that is not an imported module.
func (u *nameUse) finish(ctx *Ctx, fp *Fingerprint) {
	passed, modules := map[string]bool{}, map[string]bool{}
	for i := range fp.ImportRefs {
		ref := &fp.ImportRefs[i]
		if ref.Local != "" {
			modules[ref.Local] = true
			u.keep(fp, ref.Local)
		}
		for _, local := range ref.locals() {
			if ctx.Lang == lang.Python {
				u.keep(fp, local) // a name imported from a package may be a submodule
			}
			if fp.Referenced[local] <= u.inImports[local] {
				passed[local] = true
			}
		}
	}
	fp.Passed = sortedKeys(passed)
	members := map[string]bool{}
	for base, names := range u.selected {
		if modules[base] {
			continue
		}
		for _, name := range names {
			members[name] = true
		}
	}
	fp.Members = sortedKeys(members)
}

// locals lists the names an import's named bindings go by in the file,
// leaving out a wildcard.
func (r *ImportRef) locals() []string {
	renamed := make(map[string]string, len(r.Aliases))
	for local, name := range r.Aliases {
		renamed[name] = local
	}
	out := make([]string, 0, len(r.Names))
	for _, name := range r.Names {
		if name == importWildcard {
			continue
		}
		if local, ok := renamed[name]; ok {
			name = local
		}
		out = append(out, name)
	}

	return out
}

// keep stores the names selected through one base, sorted.
func (u *nameUse) keep(fp *Fingerprint, base string) {
	names := u.selected[base]
	if len(names) == 0 {
		return
	}
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	if fp.Selected == nil {
		fp.Selected = map[string][]string{}
	}
	fp.Selected[base] = sortedKeys(set)
}

// sortedKeys lists a set's members in order, or nil for an empty one.
func sortedKeys(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}

	return slices.Sorted(maps.Keys(set))
}

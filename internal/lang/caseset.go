package lang

import (
	ts "github.com/tree-sitter/go-tree-sitter"
)

// Node kinds, names, and operators the case vocabularies share.
const (
	kindBinary       = "binary_expression"
	kindParenthesize = "parenthesized_expression"
	kindIdent        = "identifier"
	kindTypeIdent    = "type_identifier"
	kindStringLit    = "string"
	kindElseClause   = "else_clause"
	kindElifClause   = "elif_clause"
	kindObject       = "object"
	kindDictionary   = "dictionary"
	kindPair         = "pair"
	kindKeyedElement = "keyed_element"
	kindLiteralValue = "literal_value"
	kindUnary        = "unary_expression"
	kindMemberExpr   = "member_expression"
	kindArgumentList = "argument_list"
	kindSwitchStmt   = "switch_statement"
	kindExprCase     = "expression_case"
	kindSwitchCase   = "switch_case"
	kindTypeCase     = "type_case"
	kindCaseClause   = "case_clause"
	kindFuncLiteral  = "func_literal"
	kindLambda       = "lambda"
	kindCall         = "call"
	kindList         = "list"
	kindSet          = "set"
	kindFloat        = "float"
	kindTrue         = "true"
	kindTuple        = "tuple"
	fieldValue       = "value"
	fieldType        = "type"
	opEqual          = "=="
	opNotEqual       = "!="
)

// CaseSyntax is one grammar's vocabulary for the constructs that enumerate
// cases: a switch or match, an if chain that tests one subject against
// constants, and a literal table keyed by the cases.
type CaseSyntax struct {
	// Switches are the statements whose arms each name the cases they take.
	Switches map[string]bool
	// Arms maps the kind of an arm that names cases to the field they sit
	// under, or to "" when they are the arm's own named children.
	Arms map[string]string
	// TypeArms are the arms whose cases are types.
	TypeArms map[string]bool
	// Defaults are the arms that name no case.
	Defaults map[string]bool
	// Lists are the nodes that hold several cases of one arm or one test.
	Lists map[string]bool
	// Parens are the nodes that only wrap one expression.
	Parens map[string]bool
	// Logic are the nodes that join or negate tests, read for every test in
	// them. A node that is also in Compares is one only under a LogicOps
	// operator.
	Logic map[string]bool
	// LogicOps are the operators that join or negate tests.
	LogicOps map[string]bool
	// Compares are the nodes that test a subject against a value.
	Compares map[string]bool
	// EqualOps are the operators of an equality test.
	EqualOps map[string]bool
	// InOps are the operators that test a subject against a list of values.
	InOps map[string]bool
	// TypeOps are the operators that test a subject's type.
	TypeOps map[string]bool
	// TypeCalls are the functions that test their first argument's type
	// against their second.
	TypeCalls map[string]bool
	// TypePatterns are the patterns that match by type.
	TypePatterns map[string]bool
	// Strings and Numbers are the literals a case may be, and Spliced the
	// children of a string that write a value into it.
	Strings map[string]bool
	Numbers map[string]bool
	Spliced map[string]bool
	// Dotted are the nodes that name a member of something: an enum member,
	// a constant of a package, a qualified type.
	Dotted map[string]bool
	// Idents are the nodes that are one name.
	Idents map[string]bool
	// Computed are the nodes that wrap a key that is an expression.
	Computed map[string]bool
	// Pointers are the type nodes read through to the type they point at.
	Pointers map[string]bool
	// FuncValues are the table values that are a function written in place.
	FuncValues map[string]bool
	// Partials are the functions whose call is a function with arguments
	// bound, which a table may hold as a value.
	Partials map[string]bool
	// Subscripts are the nodes that read one item of a value by its key.
	Subscripts map[string]bool
	// Calls are the call nodes.
	Calls map[string]bool
	// BuiltinTypes are the types every program tests for, which name no case
	// of the program's own.
	BuiltinTypes map[string]bool
	// Tables maps the node of a literal table to the kind of its entries.
	// TableTypes, when set, are the types the literal must be written with.
	Tables     map[string]string
	TableTypes map[string]bool
	// ArgLists are the nodes that hold the arguments of a call. A literal
	// written there is the call's named arguments and no table.
	ArgLists map[string]bool
	// EntryFuncs and EntryNames are the table entries that are a method
	// written in place and a name that is its own key and value.
	EntryFuncs map[string]bool
	EntryNames map[string]bool
	// If, ElseIf, and Else are the if statement, the clause that carries a
	// further condition, and the clause that holds the last arm or the next
	// if. A grammar without one leaves it empty.
	If, ElseIf, Else string
	// True is the literal a switch may take for a subject to hold a
	// condition per arm.
	True string
	// AnyIdent says a bare name compared against is read as a constant
	// whatever its case. Elsewhere only a name that starts with a capital is.
	AnyIdent bool
}

var goCases = &CaseSyntax{ //nolint:gochecknoglobals // fixed vocabulary
	Switches: map[string]bool{kindExprSwitch: true, kindTypeSwitch: true},
	Arms:     map[string]string{kindExprCase: fieldValue, kindTypeCase: fieldType},
	TypeArms: map[string]bool{kindTypeCase: true},
	Defaults: map[string]bool{"default_case": true},
	Lists:    map[string]bool{"expression_list": true},
	Parens:   map[string]bool{kindParenthesize: true},
	Logic:    map[string]bool{kindBinary: true, kindUnary: true},
	LogicOps: map[string]bool{"&&": true, "||": true, "!": true},
	Compares: map[string]bool{kindBinary: true},
	EqualOps: map[string]bool{opEqual: true, opNotEqual: true},
	Strings:  map[string]bool{"interpreted_string_literal": true, "raw_string_literal": true, "rune_literal": true},
	Numbers:  map[string]bool{"int_literal": true, "float_literal": true},
	Dotted:   map[string]bool{"selector_expression": true, "qualified_type": true},
	Idents:   map[string]bool{kindIdent: true, kindTypeIdent: true},
	Pointers: map[string]bool{"pointer_type": true},
	FuncValues: map[string]bool{
		kindFuncLiteral: true,
	},
	Calls: map[string]bool{kindCallExpression: true},
	BuiltinTypes: map[string]bool{
		kindStringLit: true, "bool": true, "error": true, "any": true, "byte": true, "rune": true,
		"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
		"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true, "uintptr": true,
		"float32": true, "float64": true, "complex64": true, "complex128": true, "nil": true,
	},
	Tables: map[string]string{kindLiteralValue: kindKeyedElement},
	TableTypes: map[string]bool{
		"map_type": true, "array_type": true, "implicit_length_array_type": true, "slice_type": true,
	},
	ArgLists: map[string]bool{kindArgumentList: true},
	If:       kindIfStatement,
	True:     kindTrue,
	AnyIdent: true,
}

var pythonCases = &CaseSyntax{ //nolint:gochecknoglobals // fixed vocabulary
	Switches: map[string]bool{"match_statement": true},
	Arms:     map[string]string{kindCaseClause: ""},
	Defaults: map[string]bool{},
	Lists: map[string]bool{
		kindTuple: true, kindList: true, kindSet: true, "union_pattern": true, "case_pattern": true,
	},
	Parens:       map[string]bool{kindParenthesize: true},
	Logic:        map[string]bool{"boolean_operator": true, "not_operator": true},
	Compares:     map[string]bool{"comparison_operator": true},
	EqualOps:     map[string]bool{opEqual: true, opNotEqual: true, "is": true, "is not": true},
	InOps:        map[string]bool{"in": true, "not in": true},
	TypeCalls:    map[string]bool{"isinstance": true},
	TypePatterns: map[string]bool{"class_pattern": true},
	Strings:      map[string]bool{kindStringLit: true},
	Spliced:      map[string]bool{"interpolation": true},
	Numbers:      map[string]bool{"integer": true, kindFloat: true},
	Dotted:       map[string]bool{"attribute": true, "dotted_name": true},
	Idents:       map[string]bool{kindIdent: true},
	FuncValues:   map[string]bool{kindLambda: true},
	Partials:     map[string]bool{"partial": true},
	Subscripts:   map[string]bool{"subscript": true},
	Calls:        map[string]bool{kindCall: true},
	BuiltinTypes: map[string]bool{
		"dict": true, kindList: true, "str": true, "int": true, kindFloat: true, "bool": true, kindTuple: true,
		kindSet: true, "bytes": true, "frozenset": true, "bytearray": true, "complex": true, kindObject: true,
		"type": true, "Mapping": true, "Sequence": true, "Iterable": true,
	},
	Tables:   map[string]string{kindDictionary: kindPair},
	ArgLists: map[string]bool{kindArgumentList: true, "keyword_argument": true},
	If:       kindIfStatement,
	ElseIf:   kindElifClause,
	Else:     kindElseClause,
}

var typescriptCases = &CaseSyntax{ //nolint:gochecknoglobals // fixed vocabulary
	Switches:   map[string]bool{kindSwitchStmt: true},
	Arms:       map[string]string{kindSwitchCase: fieldValue},
	Defaults:   map[string]bool{"switch_default": true},
	Lists:      map[string]bool{},
	Parens:     map[string]bool{kindParenthesize: true},
	Logic:      map[string]bool{kindBinary: true, kindUnary: true},
	LogicOps:   map[string]bool{"&&": true, "||": true, "!": true},
	Compares:   map[string]bool{kindBinary: true},
	EqualOps:   map[string]bool{opEqual: true, opNotEqual: true, "===": true, "!==": true},
	TypeOps:    map[string]bool{"instanceof": true},
	Strings:    map[string]bool{kindStringLit: true, "template_string": true, "property_identifier": true},
	Spliced:    map[string]bool{"template_substitution": true},
	Numbers:    map[string]bool{"number": true},
	Dotted:     map[string]bool{kindMemberExpr: true, "nested_identifier": true},
	Idents:     map[string]bool{kindIdent: true, kindTypeIdent: true},
	Computed:   map[string]bool{"computed_property_name": true},
	FuncValues: map[string]bool{kindArrowFunction: true, "function_expression": true},
	Subscripts: map[string]bool{"subscript_expression": true},
	Calls:      map[string]bool{kindCallExpression: true},
	BuiltinTypes: map[string]bool{
		"Array": true, "Object": true, "String": true, "Number": true, "Boolean": true, "Date": true,
		"Map": true, "Set": true, "Promise": true, "Function": true, "RegExp": true, "Error": true,
	},
	Tables:     map[string]string{kindObject: kindPair},
	ArgLists:   map[string]bool{"arguments": true},
	EntryFuncs: map[string]bool{kindMethodDefinition: true},
	EntryNames: map[string]bool{"shorthand_property_identifier": true},
	If:         kindIfStatement,
	Else:       kindElseClause,
	True:       kindTrue,
}

// Cases answers the grammar's case vocabulary, or nil for a grammar with none.
func (l *Language) Cases() *CaseSyntax {
	switch l {
	case Go:
		return goCases
	case Python:
		return pythonCases
	case TypeScript, TSX:
		return typescriptCases
	}

	return nil
}

// TableEntries lists the entries of a literal table: the pairs of a Python
// dictionary or a TypeScript object, with its methods and shorthand names,
// and the keyed elements of a Go map, array, or slice literal. It answers nil
// for any other node, and for a literal written as an argument of a call.
func (s *CaseSyntax) TableEntries(n *ts.Node) []*ts.Node {
	entry, ok := s.Tables[n.Kind()]
	if !ok {
		return nil
	}
	p := n.Parent()
	if p == nil {
		return nil
	}
	if s.TableTypes != nil {
		if t := p.ChildByFieldName(fieldType); t == nil || !s.TableTypes[t.Kind()] {
			return nil
		}
		if p = p.Parent(); p == nil {
			return nil
		}
	}
	if s.ArgLists[p.Kind()] {
		return nil
	}
	var out []*ts.Node
	for i := range n.NamedChildCount() {
		c := n.NamedChild(i)
		if kind := c.Kind(); kind == entry || s.EntryFuncs[kind] || s.EntryNames[kind] {
			out = append(out, c)
		}
	}

	return out
}

// EntryKeyValue answers the key and value nodes of a table entry, read
// through Go's literal_element. A method's key is its name and its value the
// method, and a shorthand name is both.
func (s *CaseSyntax) EntryKeyValue(entry *ts.Node) (*ts.Node, *ts.Node) {
	switch kind := entry.Kind(); {
	case s.EntryFuncs[kind]:
		name := entry.ChildByFieldName(fieldName)

		return name, entry
	case s.EntryNames[kind]:
		return entry, entry
	}
	key := entry.ChildByFieldName("key")
	if key != nil && key.Kind() == "literal_element" && key.NamedChildCount() > 0 {
		key = key.NamedChild(0)
	}

	return key, entryValue(entry)
}

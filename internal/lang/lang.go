// Package lang maps file extensions to tree-sitter grammars and describes each
// language's node vocabulary, so metric code walks one shape for all three
// supported languages.
package lang

import (
	"path/filepath"
	"strings"
	"sync"

	ts "github.com/tree-sitter/go-tree-sitter"
	tsgo "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tspy "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tests "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// Tree-sitter node kinds shared across the language vocabularies.
const (
	kindComment          = "comment"
	kindIfStatement      = "if_statement"
	kindForStatement     = "for_statement"
	kindWhileStatement   = "while_statement"
	kindMethodDefinition = "method_definition"
	kindTypeSwitch       = "type_switch_statement"
	kindArrowFunction    = "arrow_function"
	kindForInStatement   = "for_in_statement"
	kindDoStatement      = "do_statement"
	kindCatchClause      = "catch_clause"
	kindFuncDecl         = "function_declaration"
	kindExprSwitch       = "expression_switch_statement"
	kindSelect           = "select_statement"
	kindParamDecl        = "parameter_declaration"
	kindVariadicDecl     = "variadic_parameter_declaration"
	kindVarSpec          = "var_spec"
	kindForInClause      = "for_in_clause"
	kindVarDeclarator    = "variable_declarator"
)

// AllChildren is the BindKinds field of a node whose every named child holds
// a name it binds.
const AllChildren = "*"

// Field names the BindKinds tables share.
const (
	fieldName    = "name"
	fieldLeft    = "left"
	fieldPattern = "pattern"
	fieldAlias   = "alias"
)

// Language describes one grammar's node vocabulary for the metric walkers.
type Language struct {
	// CallKinds are node types for a function or method call.
	CallKinds map[string]bool
	// ClassKinds are node types whose name qualifies the methods they hold.
	ClassKinds map[string]bool
	// OpContainerKinds are expression node types whose operator child may be a
	// counted decision operator (and/or/&&/||/??).
	OpContainerKinds map[string]bool
	// FunctionKinds are node types counted as functions.
	FunctionKinds map[string]bool
	// MethodReceiverKinds are node types that make a function a method, used
	// to qualify names (Go's method_declaration carries a receiver).
	MethodKinds map[string]bool
	// CommentKinds are node types counted as comments.
	CommentKinds map[string]bool
	// LiteralKinds are node types treated as literals by duplication rules and
	// clone hashing.
	LiteralKinds map[string]bool
	newLanguage  func() *ts.Language
	// ParamSkipKinds are named children of the parameter container that are
	// not parameters (separators, comments).
	ParamSkipKinds map[string]bool
	// FlatKinds add one cognitive-complexity point without raising nesting
	// (else, elif, catch alternatives already counted elsewhere).
	FlatKinds map[string]bool
	// DecisionKinds are node types that each add one cyclomatic complexity
	// point: branches, loops, and case clauses.
	DecisionKinds map[string]bool
	// NestKinds are node types that raise the cognitive-complexity nesting
	// level inside their body.
	NestKinds map[string]bool
	// DecisionOps are operator spellings that each add one complexity point.
	DecisionOps map[string]bool
	// BlockKinds are node types whose named children are the statements of a
	// function body, a loop body, or a branch arm.
	BlockKinds map[string]bool
	// LoopKinds are node types a break or a continue inside them ends at.
	LoopKinds map[string]bool
	// SwitchKinds are the other node types a break inside them ends at.
	SwitchKinds map[string]bool
	// BindKinds maps a node type that gives names a value to the field that
	// holds those names, or to "*" when every named child holds one.
	BindKinds map[string]string
	// UpdateKinds are the BindKinds that read the name they write (i++).
	UpdateKinds map[string]bool
	// DeclKinds are the BindKinds whose names are new variables of the scope
	// they sit in. A BindKinds node outside this set declares too when one of
	// its tokens is a DeclTokens spelling, and assigns otherwise.
	DeclKinds map[string]bool
	// BareDeclKinds are the DeclKinds that give their names no value when the
	// node has no value field: `var x T`, `let x`.
	BareDeclKinds map[string]bool
	// EachKinds are the loops, or the clause of a loop, that bind the loop's
	// variables anew on every pass: a loop over the items of something.
	EachKinds map[string]bool
	// DeclTokens are the tokens that make a BindKinds node a declaration:
	// Go's ":=" in a range clause, "const" in a for-of loop.
	DeclTokens map[string]bool
	// ScopeKinds are the node types, functions aside, that a declared name
	// ends with: a block, a loop with its header, a comprehension.
	ScopeKinds map[string]bool
	// HoistKinds maps a statement that declares a name for the whole scope it
	// sits in, before and after it, to the field that holds the name: a
	// nested function or class declaration.
	HoistKinds map[string]string
	// OuterKinds are the statements that hand a function's names to a scope
	// outside it: true for the module's (global), false for an enclosing
	// function's (nonlocal).
	OuterKinds map[string]bool
	// SelfNames are the parameter names that stand for the object a method
	// belongs to, where the grammar has no keyword for it.
	SelfNames map[string]bool
	// ReceiverField is the field of a function node that holds its receiver,
	// and ResultField the one that holds its results, where they have names.
	ReceiverField string
	ResultField   string
	ID            string
	Extensions    []string
	kinds         []Kind
	kindsOnce     sync.Once
	// ImplicitLocals is true where a function's assignments declare: every
	// name a function binds anywhere in its body is its own from the start.
	ImplicitLocals bool
}

// Kind is one node kind of a grammar: its name, and whether nodes of it are
// named or anonymous tokens.
type Kind struct {
	Name  string
	Named bool
}

// Kinds lists the grammar's node kinds, indexed by the id a node reports, so
// a walk can read a node's kind without building its name each time.
func (l *Language) Kinds() []Kind {
	l.kindsOnce.Do(func() {
		grammar := l.newLanguage()
		l.kinds = make([]Kind, grammar.NodeKindCount())
		for i := range l.kinds {
			id := uint16(i)
			l.kinds[i] = Kind{Name: grammar.NodeKindForId(id), Named: grammar.NodeKindIsNamed(id)}
		}
	})

	return l.kinds
}

// Go is the spec for *.go.
var Go = &Language{ //nolint:gochecknoglobals // language vocabularies are constants a map must hold
	ID:         "go",
	Extensions: []string{".go"},
	FunctionKinds: map[string]bool{
		kindFuncDecl:         true,
		"method_declaration": true,
		"func_literal":       true,
	},
	MethodKinds:    map[string]bool{"method_declaration": true},
	CommentKinds:   map[string]bool{kindComment: true},
	CallKinds:      map[string]bool{kindCallExpression: true},
	ParamSkipKinds: map[string]bool{kindComment: true},
	LiteralKinds: map[string]bool{
		"interpreted_string_literal": true,
		"raw_string_literal":         true,
		"rune_literal":               true,
		"int_literal":                true,
		"float_literal":              true,
		"imaginary_literal":          true,
	},
	DecisionKinds: map[string]bool{
		kindIfStatement:      true,
		kindForStatement:     true,
		"expression_case":    true,
		"type_case":          true,
		"communication_case": true,
	},
	OpContainerKinds: map[string]bool{"binary_expression": true},
	DecisionOps:      map[string]bool{"&&": true, "||": true},
	NestKinds: map[string]bool{
		kindIfStatement:  true,
		kindForStatement: true,
		kindExprSwitch:   true,
		kindTypeSwitch:   true,
		kindSelect:       true,
	},
	FlatKinds:  map[string]bool{"else": true},
	BlockKinds: map[string]bool{"statement_list": true},
	LoopKinds:  map[string]bool{kindForStatement: true},
	SwitchKinds: map[string]bool{
		kindExprSwitch: true, kindTypeSwitch: true, kindSelect: true,
	},
	BindKinds: map[string]string{
		kindParamDecl: fieldName, kindVariadicDecl: fieldName,
		kindVarSpec: fieldName, "const_spec": fieldName, "short_var_declaration": fieldLeft,
		"range_clause": fieldLeft, "assignment_statement": fieldLeft, "receive_statement": fieldLeft,
		kindTypeSwitch: fieldAlias, "inc_statement": AllChildren, "dec_statement": AllChildren,
	},
	UpdateKinds: map[string]bool{"inc_statement": true, "dec_statement": true},
	DeclKinds: map[string]bool{
		kindParamDecl: true, kindVariadicDecl: true,
		kindVarSpec: true, "const_spec": true, "short_var_declaration": true, kindTypeSwitch: true,
	},
	BareDeclKinds: map[string]bool{kindVarSpec: true},
	EachKinds:     map[string]bool{"range_clause": true},
	DeclTokens:    map[string]bool{":=": true},
	ScopeKinds: map[string]bool{
		"block": true, kindIfStatement: true, kindForStatement: true,
		kindExprSwitch: true, kindTypeSwitch: true, kindSelect: true,
		"expression_case": true, "type_case": true, "default_case": true, "communication_case": true,
	},
	ReceiverField: "receiver",
	ResultField:   "result",
	newLanguage:   func() *ts.Language { return ts.NewLanguage(tsgo.Language()) },
}

// Python is the spec for *.py.
var Python = &Language{ //nolint:gochecknoglobals // language vocabularies are constants a map must hold
	ID:         "python",
	Extensions: []string{".py", ".pyi"},
	FunctionKinds: map[string]bool{
		"function_definition": true,
		"lambda":              true,
	},
	CommentKinds: map[string]bool{kindComment: true},
	CallKinds:    map[string]bool{"call": true},
	ClassKinds:   map[string]bool{"class_definition": true},
	ParamSkipKinds: map[string]bool{
		kindComment:            true,
		"keyword_separator":    true,
		"positional_separator": true,
	},
	LiteralKinds: map[string]bool{
		"string":              true,
		"integer":             true,
		"float":               true,
		"true":                true,
		"false":               true,
		"none":                true,
		"concatenated_string": true,
	},
	DecisionKinds: map[string]bool{
		kindIfStatement:          true,
		"elif_clause":            true,
		kindForStatement:         true,
		kindWhileStatement:       true,
		"except_clause":          true,
		"conditional_expression": true,
		"case_clause":            true,
		kindForInClause:          true,
		"if_clause":              true,
	},
	OpContainerKinds: map[string]bool{"boolean_operator": true},
	DecisionOps:      map[string]bool{"and": true, "or": true},
	NestKinds: map[string]bool{
		kindIfStatement:          true,
		"elif_clause":            true,
		kindForStatement:         true,
		kindWhileStatement:       true,
		"except_clause":          true,
		"case_clause":            true,
		"match_statement":        true,
		"conditional_expression": true,
	},
	FlatKinds:  map[string]bool{"else_clause": true},
	BlockKinds: map[string]bool{"block": true},
	LoopKinds:  map[string]bool{kindForStatement: true, kindWhileStatement: true},
	BindKinds: map[string]string{
		"parameters": AllChildren, "lambda_parameters": AllChildren,
		"default_parameter": fieldName, "typed_default_parameter": fieldName, "named_expression": fieldName,
		"assignment": fieldLeft, "augmented_assignment": fieldLeft, kindForStatement: fieldLeft,
		kindForInClause: fieldLeft, "as_pattern": fieldAlias,
		"import_statement": fieldName, "import_from_statement": fieldName, "aliased_import": fieldAlias,
	},
	DeclKinds: map[string]bool{
		"parameters": true, "lambda_parameters": true, "default_parameter": true,
		"typed_default_parameter": true, kindForInClause: true,
	},
	ScopeKinds: map[string]bool{
		"list_comprehension": true, "set_comprehension": true, "dictionary_comprehension": true,
		"generator_expression": true,
	},
	EachKinds:      map[string]bool{kindForStatement: true},
	HoistKinds:     map[string]string{"function_definition": fieldName, "class_definition": fieldName},
	OuterKinds:     map[string]bool{"global_statement": true, "nonlocal_statement": false},
	SelfNames:      map[string]bool{"self": true, "cls": true},
	ImplicitLocals: true,
	newLanguage:    func() *ts.Language { return ts.NewLanguage(tspy.Language()) },
}

// TypeScript is the spec for *.ts; tsx files share the spec under the TSX
// grammar.
var TypeScript = &Language{ //nolint:gochecknoglobals // language vocabularies are constants a map must hold
	ID:         "typescript",
	Extensions: []string{".ts", ".cts", ".mts"},
	FunctionKinds: map[string]bool{
		kindFuncDecl:                     true,
		"function_expression":            true,
		kindArrowFunction:                true,
		kindMethodDefinition:             true,
		"generator_function_declaration": true,
	},
	MethodKinds:    map[string]bool{kindMethodDefinition: true},
	CommentKinds:   map[string]bool{kindComment: true},
	CallKinds:      map[string]bool{kindCallExpression: true},
	ClassKinds:     map[string]bool{"class_declaration": true, "class": true, "abstract_class_declaration": true},
	ParamSkipKinds: map[string]bool{kindComment: true},
	LiteralKinds: map[string]bool{
		"string":          true,
		"number":          true,
		"template_string": true,
		"true":            true,
		"false":           true,
		"null":            true,
		"regex":           true,
	},
	DecisionKinds: map[string]bool{
		kindIfStatement:      true,
		kindForStatement:     true,
		kindForInStatement:   true,
		kindWhileStatement:   true,
		kindDoStatement:      true,
		"switch_case":        true,
		kindCatchClause:      true,
		"ternary_expression": true,
	},
	OpContainerKinds: map[string]bool{"binary_expression": true, "augmented_assignment_expression": true},
	DecisionOps: map[string]bool{
		"&&": true, "||": true, "??": true, "&&=": true, "||=": true, "??=": true,
	},
	NestKinds: map[string]bool{
		kindIfStatement:      true,
		kindForStatement:     true,
		kindForInStatement:   true,
		kindWhileStatement:   true,
		kindDoStatement:      true,
		"switch_statement":   true,
		kindCatchClause:      true,
		"ternary_expression": true,
	},
	FlatKinds:  map[string]bool{"else_clause": true},
	BlockKinds: map[string]bool{"statement_block": true},
	LoopKinds: map[string]bool{
		kindForStatement: true, kindForInStatement: true, kindWhileStatement: true, kindDoStatement: true,
	},
	SwitchKinds: map[string]bool{"switch_statement": true},
	BindKinds: map[string]string{
		"required_parameter": fieldPattern, "optional_parameter": fieldPattern, kindVarDeclarator: fieldName,
		"assignment_expression": fieldLeft, "augmented_assignment_expression": fieldLeft,
		kindForInStatement: fieldLeft, kindCatchClause: "parameter", kindArrowFunction: "parameter",
		"update_expression": "argument",
	},
	UpdateKinds: map[string]bool{"update_expression": true},
	DeclKinds: map[string]bool{
		"required_parameter": true, "optional_parameter": true, kindVarDeclarator: true,
		kindCatchClause: true, kindArrowFunction: true,
	},
	BareDeclKinds: map[string]bool{kindVarDeclarator: true},
	EachKinds:     map[string]bool{kindForInStatement: true},
	DeclTokens:    map[string]bool{"const": true, "let": true, "var": true},
	ScopeKinds: map[string]bool{
		"statement_block": true, kindForStatement: true, kindForInStatement: true, kindCatchClause: true,
		"switch_case": true, "switch_default": true,
	},
	HoistKinds: map[string]string{
		kindFuncDecl: fieldName, "generator_function_declaration": fieldName,
		"class_declaration": fieldName, "abstract_class_declaration": fieldName,
	},
	newLanguage: func() *ts.Language {
		return ts.NewLanguage(tests.LanguageTypescript())
	},
}

// TSX shares TypeScript's vocabulary under the TSX grammar for *.tsx files.
var TSX = &Language{ //nolint:gochecknoglobals // language vocabularies are constants a map must hold
	ID:               "tsx",
	Extensions:       []string{".tsx", ".jsx"},
	FunctionKinds:    TypeScript.FunctionKinds,
	MethodKinds:      TypeScript.MethodKinds,
	CommentKinds:     TypeScript.CommentKinds,
	LiteralKinds:     TypeScript.LiteralKinds,
	CallKinds:        TypeScript.CallKinds,
	ClassKinds:       TypeScript.ClassKinds,
	ParamSkipKinds:   TypeScript.ParamSkipKinds,
	DecisionKinds:    TypeScript.DecisionKinds,
	OpContainerKinds: TypeScript.OpContainerKinds,
	DecisionOps:      TypeScript.DecisionOps,
	NestKinds:        TypeScript.NestKinds,
	FlatKinds:        TypeScript.FlatKinds,
	BlockKinds:       TypeScript.BlockKinds,
	LoopKinds:        TypeScript.LoopKinds,
	SwitchKinds:      TypeScript.SwitchKinds,
	BindKinds:        TypeScript.BindKinds,
	UpdateKinds:      TypeScript.UpdateKinds,
	DeclKinds:        TypeScript.DeclKinds,
	BareDeclKinds:    TypeScript.BareDeclKinds,
	EachKinds:        TypeScript.EachKinds,
	DeclTokens:       TypeScript.DeclTokens,
	ScopeKinds:       TypeScript.ScopeKinds,
	HoistKinds:       TypeScript.HoistKinds,
	newLanguage:      func() *ts.Language { return ts.NewLanguage(tests.LanguageTSX()) },
}

// All lists the supported languages.
var All = []*Language{Go, Python, TypeScript, TSX} //nolint:gochecknoglobals // the registry is fixed at compile time

var byExt = func() map[string]*Language { //nolint:gochecknoglobals // built once from All
	m := make(map[string]*Language)
	for _, l := range All {
		for _, ext := range l.Extensions {
			m[ext] = l
		}
	}

	return m
}()

// ByPath resolves a file path to its language, or nil for unsupported files.
func ByPath(path string) *Language {
	return byExt[strings.ToLower(filepath.Ext(path))]
}

// Text kinds for files with no grammar that still carry facts the cross-file
// passes read: prose holds names of things, config holds keys code reads, and
// a module manifest names the import path its directory answers to.
const (
	TextProse    = "prose"
	TextConfig   = "config"
	TextManifest = "manifest"
)

// goModFile is the Go module manifest, the one file named by its whole name.
const goModFile = "go.mod"

// textExt maps non-code extensions to their text kind.
var textExt = map[string]string{ //nolint:gochecknoglobals // fixed vocabulary
	".md": TextProse, ".markdown": TextProse, ".mdx": TextProse,
	".rst": TextProse, ".txt": TextProse,
	".toml": TextConfig, ".yaml": TextConfig, ".yml": TextConfig,
	".json": TextConfig, ".pkl": TextConfig,
}

// TextKind classifies a file with no grammar as prose, config, or a module
// manifest, or "" when it is none. Such files get a text fingerprint, never a parse.
func TextKind(path string) string {
	if filepath.Base(path) == goModFile {
		return TextManifest
	}

	return textExt[strings.ToLower(filepath.Ext(path))]
}

// Language constructs the tree-sitter language for parsing.
func (l *Language) Language() *ts.Language {
	return l.newLanguage()
}

// IsTestFile reports whether a path names a test file by the language's
// naming convention; test-only rules key off it.
func (l *Language) IsTestFile(path string) bool {
	base := filepath.Base(path)
	switch l.ID {
	case "go":
		return strings.HasSuffix(base, "_test.go")
	case "python":
		return strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py")
	case "typescript", "tsx":
		return strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") ||
			strings.Contains(path, "__tests__/")
	default:
		return false
	}
}

// testDirs are directory names whose contents are tests or their fixtures.
var testDirs = map[string]bool{ //nolint:gochecknoglobals // a fixed vocabulary
	"__tests__": true, "fixtures": true, "test": true, "testdata": true, "tests": true,
}

// inTestDir reports whether any directory in the path is a test directory.
func inTestDir(path string) bool {
	dir := filepath.Dir(path)
	for _, part := range strings.Split(filepath.ToSlash(dir), "/") {
		if testDirs[part] {
			return true
		}
	}

	return false
}

// IsTestPath reports whether a path is a test file or sits in a test directory,
// falling back to generic naming for paths with no grammar.
func IsTestPath(path string) bool {
	if inTestDir(path) {
		return true
	}
	if l := ByPath(path); l != nil {
		return l.IsTestFile(path)
	}
	base := filepath.Base(path)

	return strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") ||
		strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.go") ||
		strings.HasSuffix(base, "_test.py")
}

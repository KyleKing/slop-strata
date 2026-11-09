// Package lang maps file extensions to tree-sitter grammars and describes each
// language's node vocabulary, so metric code walks one shape for all three
// supported languages.
package lang

import (
	"path/filepath"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"
	tsgo "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tspy "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tests "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// GrammarVersion bumps when a grammar or the queries over it change, so a blob
// cached under a stale grammar is recomputed.
const GrammarVersion = 1

// kindSet builds a node-kind set.
func kindSet(kinds ...string) map[string]bool {
	m := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		m[k] = true
	}

	return m
}

// Language describes one grammar's node vocabulary for the metric walkers.
type Language struct {
	ID         string
	Extensions []string

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
	// CallKinds are node types for a function or method call.
	CallKinds map[string]bool
	// ParamContainerKind holds a function's parameters.
	ParamContainerKind string
	// ParamSkipKinds are named children of the parameter container that are
	// not parameters (separators, comments).
	ParamSkipKinds map[string]bool
	// DecisionKinds are node types that each add one cyclomatic complexity
	// point: branches, loops, and case clauses.
	DecisionKinds map[string]bool
	// OpContainerKinds are expression node types whose operator child may be a
	// counted decision operator (and/or/&&/||/??).
	OpContainerKinds map[string]bool
	// DecisionOps are operator spellings that each add one complexity point.
	DecisionOps map[string]bool
	// NestKinds are node types that raise the cognitive-complexity nesting
	// level inside their body.
	NestKinds map[string]bool
	// FlatKinds add one cognitive-complexity point without raising nesting
	// (else, elif, catch alternatives already counted elsewhere).
	FlatKinds map[string]bool

	newLanguage func() *ts.Language
}

// Go is the spec for *.go.
var Go = &Language{ //nolint:gochecknoglobals // language vocabularies are constants a map must hold
	ID:         "go",
	Extensions: []string{".go"},
	FunctionKinds: map[string]bool{
		"function_declaration": true,
		"method_declaration":   true,
		"func_literal":         true,
	},
	MethodKinds:        map[string]bool{"method_declaration": true},
	CommentKinds:       map[string]bool{"comment": true},
	CallKinds:          map[string]bool{"call_expression": true},
	ParamContainerKind: "parameter_list",
	ParamSkipKinds:     map[string]bool{"comment": true},
	LiteralKinds: map[string]bool{
		"interpreted_string_literal": true,
		"raw_string_literal":         true,
		"rune_literal":               true,
		"int_literal":                true,
		"float_literal":              true,
		"imaginary_literal":          true,
	},
	DecisionKinds: map[string]bool{
		"if_statement":       true,
		"for_statement":      true,
		"expression_case":    true,
		"type_case":          true,
		"communication_case": true,
	},
	OpContainerKinds: map[string]bool{"binary_expression": true},
	DecisionOps:      map[string]bool{"&&": true, "||": true},
	NestKinds: map[string]bool{
		"if_statement":          true,
		"for_statement":         true,
		"switch_statement":      true,
		"type_switch_statement": true,
		"select_statement":      true,
		"func_literal":          true,
	},
	FlatKinds:   map[string]bool{"else": true},
	newLanguage: func() *ts.Language { return ts.NewLanguage(tsgo.Language()) },
}

// Python is the spec for *.py.
var Python = &Language{ //nolint:gochecknoglobals // language vocabularies are constants a map must hold
	ID:         "python",
	Extensions: []string{".py", ".pyi"},
	FunctionKinds: map[string]bool{
		"function_definition": true,
		"lambda":              true,
	},
	CommentKinds:       map[string]bool{"comment": true},
	CallKinds:          map[string]bool{"call": true},
	ParamContainerKind: "parameters",
	ParamSkipKinds: map[string]bool{
		"comment":              true,
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
		"if_statement":           true,
		"elif_clause":            true,
		"for_statement":          true,
		"while_statement":        true,
		"except_clause":          true,
		"conditional_expression": true,
		"case_clause":            true,
	},
	OpContainerKinds: map[string]bool{"boolean_operator": true},
	DecisionOps:      map[string]bool{"and": true, "or": true},
	NestKinds: map[string]bool{
		"if_statement":           true,
		"elif_clause":            true,
		"for_statement":          true,
		"while_statement":        true,
		"except_clause":          true,
		"case_clause":            true,
		"match_statement":        true,
		"conditional_expression": true,
		"function_definition":    true,
		"lambda":                 true,
	},
	FlatKinds:   map[string]bool{"else_clause": true},
	newLanguage: func() *ts.Language { return ts.NewLanguage(tspy.Language()) },
}

// TypeScript is the spec for *.ts; tsx files share the spec under the TSX
// grammar.
var TypeScript = &Language{ //nolint:gochecknoglobals // language vocabularies are constants a map must hold
	ID:         "typescript",
	Extensions: []string{".ts", ".cts", ".mts"},
	FunctionKinds: map[string]bool{
		"function_declaration":           true,
		"function_expression":            true,
		"arrow_function":                 true,
		"method_definition":              true,
		"generator_function_declaration": true,
	},
	MethodKinds:        map[string]bool{"method_definition": true},
	CommentKinds:       map[string]bool{"comment": true},
	CallKinds:          map[string]bool{"call_expression": true},
	ParamContainerKind: "formal_parameters",
	ParamSkipKinds:     map[string]bool{"comment": true},
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
		"if_statement":       true,
		"for_statement":      true,
		"for_in_statement":   true,
		"while_statement":    true,
		"do_statement":       true,
		"switch_case":        true,
		"catch_clause":       true,
		"ternary_expression": true,
	},
	OpContainerKinds: map[string]bool{"binary_expression": true},
	DecisionOps:      map[string]bool{"&&": true, "||": true, "??": true},
	NestKinds: map[string]bool{
		"if_statement":        true,
		"for_statement":       true,
		"for_in_statement":    true,
		"while_statement":     true,
		"do_statement":        true,
		"switch_statement":    true,
		"catch_clause":        true,
		"ternary_expression":  true,
		"function_expression": true,
		"arrow_function":      true,
		"method_definition":   true,
	},
	FlatKinds: map[string]bool{"else": true},
	newLanguage: func() *ts.Language {
		return ts.NewLanguage(tests.LanguageTypescript())
	},
}

// TSX shares TypeScript's vocabulary under the TSX grammar for *.tsx files.
var TSX = &Language{ //nolint:gochecknoglobals // language vocabularies are constants a map must hold
	ID:                 "tsx",
	Extensions:         []string{".tsx", ".jsx"},
	FunctionKinds:      TypeScript.FunctionKinds,
	MethodKinds:        TypeScript.MethodKinds,
	CommentKinds:       TypeScript.CommentKinds,
	LiteralKinds:       TypeScript.LiteralKinds,
	CallKinds:          TypeScript.CallKinds,
	ParamContainerKind: TypeScript.ParamContainerKind,
	ParamSkipKinds:     TypeScript.ParamSkipKinds,
	DecisionKinds:      TypeScript.DecisionKinds,
	OpContainerKinds:   TypeScript.OpContainerKinds,
	DecisionOps:        TypeScript.DecisionOps,
	NestKinds:          TypeScript.NestKinds,
	FlatKinds:          TypeScript.FlatKinds,
	newLanguage:        func() *ts.Language { return ts.NewLanguage(tests.LanguageTSX()) },
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

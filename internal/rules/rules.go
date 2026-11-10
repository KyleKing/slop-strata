// Package rules holds the slop-rule packs: per-blob rules that walk a
// tree-sitter tree, and cross-file passes that run over cached per-blob
// fingerprints.
package rules

import (
	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/kyleking/slop-strata/internal/lang"
)

// Version bumps when a rule's logic changes so cached findings are recomputed.
const Version = 6

// experimentalCacheSlot offsets the cache key of an experimental run, so a
// default run never reads a payload scored without the experimental rules.
const experimentalCacheSlot = 1 << 16

// Rule IDs are constants where the registry and the rule that emits them share
// a spelling.
const (
	ruleGoTypeAny         = "go/type-any"
	rulePyTestReadsSource = "py/test-reads-source"
	rulePyTimingHack      = "py/timing-hack"
	ruleTSAsUnknownAs     = "ts/as-unknown-as"
	ruleXNarratingComment = "x/narrating-comment"
)

// Tree-sitter node kinds and texts several rules match on.
const (
	kindCallExpression  = "call_expression"
	kindExportStatement = "export_statement"
	kindFunctionDef     = "function_definition"
	kindIdentifier      = "identifier"
	kindTypeIdentifier  = "type_identifier"
	kindGenericType     = "generic_type"
	textAny             = "any"
	textUnknown         = "unknown"
)

// Finding is one rule hit: the rule, where, and an optional note.
type Finding struct {
	Rule  string `json:"rule"`
	Note  string `json:"note,omitempty"`
	Match string `json:"match,omitempty"`
	Line  int    `json:"line"`
}

// CacheVersion is the rule-set number a blob's payload is stored under.
func CacheVersion(experimental bool) int {
	if experimental {
		return Version + experimentalCacheSlot
	}

	return Version
}

// Ctx is what a per-blob rule sees.
type Ctx struct {
	Lang         *lang.Language
	Root         *ts.Node
	Path         string
	Src          []byte
	Test         bool
	Experimental bool
}

// Rule is a per-blob check.
type Rule struct {
	Apply        func(*Ctx, *[]Finding)
	ID           string
	TestOnly     bool // only fires where Lang.IsTestFile(path)
	Experimental bool // below the precision bar; runs only when Ctx.Experimental is set
}

// all is the registry: every rule that ships, in id order.
var all = []Rule{ //nolint:gochecknoglobals // the registry is fixed at compile time
	{ID: "go/silent-error", Apply: goSilentError, Experimental: true},
	{ID: ruleGoTypeAny, Apply: goTypeAny, Experimental: true},
	{ID: "go/test-reads-source", TestOnly: true, Apply: goTestReadsSource, Experimental: true},
	{ID: "go/timing-hack", TestOnly: true, Apply: goTimingHack},
	{ID: "py/silent-except", Apply: pySilentExcept},
	{ID: "py/or-default", Apply: pyOrDefault, Experimental: true},
	{ID: "py/type-any", Apply: pyTypeAny, Experimental: true},
	{ID: "py/mock-patch", TestOnly: true, Apply: pyMockPatch, Experimental: true},
	{ID: rulePyTestReadsSource, TestOnly: true, Apply: pyTestReadsSource, Experimental: true},
	{ID: "py/mock-only-assert", TestOnly: true, Apply: pyMockOnlyAssert},
	{ID: "py/constant-echo", TestOnly: true, Apply: pyConstantEcho, Experimental: true},
	{ID: rulePyTimingHack, TestOnly: true, Apply: pyTimingHack},
	{ID: "ts/silent-catch", Apply: tsSilentCatch},
	{ID: ruleTSAsUnknownAs, Apply: tsAsUnknownAs},
	{ID: "ts/record-unknown", Apply: tsRecordUnknown},
	{ID: "ts/type-any", Apply: tsTypeAny, Experimental: true},
	{ID: "ts/module-mock", Apply: tsModuleMock},
	{ID: "ts/test-reads-source", TestOnly: true, Apply: tsTestReadsSource, Experimental: true},
	{ID: "ts/mock-only-assert", TestOnly: true, Apply: tsMockOnlyAssert, Experimental: true},
	{ID: "ts/constant-echo", TestOnly: true, Apply: tsConstantEcho},
	{ID: "ts/timing-hack", TestOnly: true, Apply: tsTimingHack},
	{ID: "x/single-entry-registry", Apply: singleEntryRegistry, Experimental: true},
	{ID: ruleXNarratingComment, Apply: narratingComment, Experimental: true},
	{ID: "x/comment-density", Apply: commentDensity, Experimental: true},
	{ID: "x/spent-test", TestOnly: true, Apply: spentTest, Experimental: true},
}

// Run applies every rule appropriate for the blob.
func Run(ctx *Ctx) []Finding {
	var findings []Finding
	for i := range all {
		rule := &all[i]
		if rule.TestOnly && !ctx.Test {
			continue
		}
		if rule.Experimental && !ctx.Experimental {
			continue
		}
		rule.Apply(ctx, &findings)
	}

	return findings
}

// Text is shorthand for a node's source text.
func (c *Ctx) Text(n *ts.Node) string {
	return lang.Text(n, c.Src)
}

// eachFunc calls fn for every function node in the tree.
func (c *Ctx) eachFunc(fn func(*ts.Node)) {
	lang.EachNamed(c.Root, func(n *ts.Node) {
		if c.Lang.FunctionKinds[n.Kind()] {
			fn(n)
		}
	})
}

// eachCall calls fn for every call node, with the callee's text.
func (c *Ctx) eachCall(fn func(node *ts.Node, callee string)) {
	lang.EachNamed(c.Root, func(n *ts.Node) {
		if !c.Lang.CallKinds[n.Kind()] {
			return
		}
		fn(n, c.calleeText(n))
	})
}

// calleeText answers the function being called: the call's "function" field
// where the grammar names one, else its first child.
func (c *Ctx) calleeText(n *ts.Node) string {
	if f := n.ChildByFieldName("function"); f != nil {
		return c.Text(f)
	}
	if n.NamedChildCount() > 0 {
		return c.Text(n.NamedChild(0))
	}

	return ""
}

// lineOf converts a node's start row to a 1-based line.
func lineOf(n *ts.Node) int {
	return int(n.StartPosition().Row) + 1
}

// blockStatements counts the named statements inside a block node's body.
func blockStatements(n *ts.Node) int {
	for i := range n.NamedChildCount() {
		child := n.NamedChild(i)
		if child.Kind() == "block" || child.Kind() == "statement_block" {
			return int(child.NamedChildCount())
		}
	}

	return 0
}

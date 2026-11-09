package rules

import (
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/kyleking/slop-strata/internal/lang"
)

// pySilentExcept flags an except clause whose body is only `pass` or `...`:
// the failure is swallowed with no record it happened.
func pySilentExcept(ctx *Ctx, findings *[]Finding) {
	if ctx.Lang != lang.Python {
		return
	}
	lang.EachNamed(ctx.Root, func(n *ts.Node) {
		if n.Kind() != "except_clause" {
			return
		}
		children := n.NamedChildCount()
		if children == 0 {
			return
		}
		// The clause's body is its last child; pass or an ellipsis alone means
		// the handler does nothing.
		body := n.NamedChild(children - 1)
		if body.Kind() != "block" || body.NamedChildCount() != 1 {
			return
		}
		stmt := body.NamedChild(0)
		if stmt.Kind() == "pass_statement" ||
			(stmt.Kind() == "expression_statement" && ctx.Text(stmt) == "...") {
			*findings = append(*findings, Finding{Rule: "py/silent-except", Line: lineOf(n)})
		}
	})
}

// pyOrDefault flags `x or <literal>` fallbacks: a default that also swallows
// legitimate falsy values (0, "", [], None) is a silent fallback.
func pyOrDefault(ctx *Ctx, findings *[]Finding) {
	if ctx.Lang != lang.Python {
		return
	}
	literals := map[string]bool{
		"string": true, "concatenated_string": true, "integer": true, "float": true,
		"true": true, "false": true, "none": true,
		"list": true, "dictionary": true, "tuple": true, "set": true,
	}
	lang.EachNamed(ctx.Root, func(n *ts.Node) {
		if n.Kind() != "boolean_operator" {
			return
		}
		op, right := "", (*ts.Node)(nil)
		for i := range n.ChildCount() {
			child := n.Child(i)
			if !child.IsNamed() {
				op = ctx.Text(child)
			} else {
				right = child
			}
		}
		if op == "or" && right != nil && literals[right.Kind()] {
			*findings = append(*findings, Finding{
				Rule: "py/or-default", Line: lineOf(n), Match: ctx.Text(right),
			})
		}
	})
}

// pyTypeAny flags `Any` in type positions: annotations, generic parameters,
// and subscripted types.
func pyTypeAny(ctx *Ctx, findings *[]Finding) {
	if ctx.Lang != lang.Python {
		return
	}
	typeKinds := map[string]bool{"type": true, kindGenericType: true, "subscript": true}
	seen := map[int]bool{}
	lang.EachNamed(ctx.Root, func(n *ts.Node) {
		if n.Kind() != kindIdentifier && n.Kind() != "attribute" {
			return
		}
		text := ctx.Text(n)
		if text != "Any" && !strings.HasSuffix(text, ".Any") {
			return
		}
		for p := n.Parent(); p != nil; p = p.Parent() {
			if typeKinds[p.Kind()] {
				line := lineOf(n)
				if !seen[line] {
					seen[line] = true
					*findings = append(*findings, Finding{Rule: "py/type-any", Line: line, Match: text})
				}

				return
			}
			if p.Kind() == "function_definition" || p.Kind() == "class_definition" {
				return
			}
		}
	})
}

// pyMockPatch flags unittest.mock patching inside tests: the test stops
// exercising the code it names.
func pyMockPatch(ctx *Ctx, findings *[]Finding) {
	if ctx.Lang != lang.Python {
		return
	}
	ctx.eachCall(func(n *ts.Node, callee string) {
		if callee == "patch" || strings.HasPrefix(callee, "patch.") || strings.Contains(callee, ".patch") {
			*findings = append(*findings, Finding{Rule: "py/mock-patch", Line: lineOf(n), Match: callee})
		}
	})
}

// pyTestReadsSource flags tests that open or parse files: a test coupled to
// source, config, or docs text breaks on every unrelated edit.
func pyTestReadsSource(ctx *Ctx, findings *[]Finding) {
	if ctx.Lang != lang.Python {
		return
	}
	ctx.eachCall(func(n *ts.Node, callee string) {
		switch {
		case callee == "ast.parse":
			*findings = append(*findings, Finding{Rule: rulePyTestReadsSource, Line: lineOf(n), Match: callee})
		case callee == "open" || strings.HasSuffix(callee, ".open"),
			strings.HasSuffix(callee, ".read_text"), strings.HasSuffix(callee, ".read_bytes"),
			strings.HasSuffix(callee, ".readlines"):
			*findings = append(*findings, Finding{Rule: rulePyTestReadsSource, Line: lineOf(n), Match: callee})
		}
	})
}

// pyMockAsserts are the unittest.mock assertion names a spy-only test calls.
var pyMockAsserts = []string{ //nolint:gochecknoglobals // a constant vocabulary
	"assert_called", "assert_called_once", "assert_called_with", "assert_called_once_with",
	"assert_any_call", "assert_has_calls", "assert_not_called", "assert_awaited",
}

// pyMockOnlyAssert flags a test function whose every assertion is a mock
// call assertion: it pins calls, not behavior.
func pyMockOnlyAssert(ctx *Ctx, findings *[]Finding) {
	if ctx.Lang != lang.Python {
		return
	}
	ctx.eachFunc(func(fn *ts.Node) {
		name := lang.FunctionName(ctx.Lang, fn, ctx.Src)
		if !strings.HasPrefix(name, "test_") {
			return
		}
		asserts, mockOnly := mockAssertCounts(ctx, fn)
		if asserts > 0 && mockOnly {
			*findings = append(*findings, Finding{Rule: "py/mock-only-assert", Line: lineOf(fn), Match: name})
		}
	})
}

// mockAssertCounts counts a test body's assertions and reports whether every
// one is a mock call assertion.
func mockAssertCounts(ctx *Ctx, fn *ts.Node) (int, bool) {
	asserts := 0
	mockOnly := true
	lang.EachNamed(fn, func(n *ts.Node) {
		switch {
		case n.Kind() == "assert_statement":
			asserts++
			mockOnly = false
		case ctx.Lang.CallKinds[n.Kind()]:
			callee := ctx.calleeText(n)
			last := callee[strings.LastIndex(callee, ".")+1:]
			if !strings.HasPrefix(last, "assert") {
				return
			}
			asserts++
			if !isMockAssert(last) {
				mockOnly = false
			}
		}
	})

	return asserts, mockOnly
}

// isMockAssert reports whether a callee name is a unittest.mock assertion.
func isMockAssert(name string) bool {
	for _, m := range pyMockAsserts {
		if strings.HasPrefix(name, m) {
			return true
		}
	}

	return false
}

// pyConstantEcho flags a test function whose every assert compares a name or
// attribute to a literal and which calls nothing with behavior.
func pyConstantEcho(ctx *Ctx, findings *[]Finding) {
	if ctx.Lang != lang.Python {
		return
	}
	ctx.eachFunc(func(fn *ts.Node) {
		name := lang.FunctionName(ctx.Lang, fn, ctx.Src)
		if !strings.HasPrefix(name, "test_") {
			return
		}
		asserts, echoes, calls := 0, 0, 0
		lang.EachNamed(fn, func(n *ts.Node) {
			switch {
			case ctx.Lang.CallKinds[n.Kind()]:
				calls++
			case n.Kind() == "assert_statement":
				asserts++
				if isEchoComparison(ctx, n) {
					echoes++
				}
			}
		})
		if asserts > 0 && asserts == echoes && calls == 0 {
			*findings = append(*findings, Finding{Rule: "py/constant-echo", Line: lineOf(fn), Match: name})
		}
	})
}

// isEchoComparison reports whether an assert compares a bare name or
// attribute against a literal: the answer is already known at write time.
func isEchoComparison(ctx *Ctx, assert *ts.Node) bool {
	var comp *ts.Node
	lang.EachNamed(assert, func(n *ts.Node) {
		if n.Kind() == "comparison_operator" && comp == nil {
			comp = n
		}
	})
	if comp == nil || comp.NamedChildCount() != 2 {
		return false
	}
	left, right := comp.NamedChild(0), comp.NamedChild(1)
	literal := func(n *ts.Node) bool { return ctx.Lang.LiteralKinds[n.Kind()] }
	nameLike := func(n *ts.Node) bool {
		return n.Kind() == kindIdentifier || n.Kind() == "attribute"
	}

	return literal(left) && nameLike(right) || literal(right) && nameLike(left)
}

// pyTimingHack flags sleeps and frozen clocks in tests: a test synchronized by
// time is a flake in waiting.
func pyTimingHack(ctx *Ctx, findings *[]Finding) {
	if ctx.Lang != lang.Python {
		return
	}
	ctx.eachCall(func(n *ts.Node, callee string) {
		switch {
		case callee == "time.sleep" || callee == "asyncio.sleep" || callee == "sleep",
			strings.HasSuffix(callee, ".sleep"),
			strings.Contains(callee, "freeze_time"):
			*findings = append(*findings, Finding{Rule: rulePyTimingHack, Line: lineOf(n), Match: callee})
		case strings.HasSuffix(callee, ".patch") && n.NamedChildCount() > 1:
			if strings.Contains(ctx.Text(n), "sleep") {
				*findings = append(*findings, Finding{Rule: rulePyTimingHack, Line: lineOf(n), Match: callee})
			}
		}
	})
}

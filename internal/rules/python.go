package rules

import (
	"regexp"
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

// pyOrDefault flags `param or []` and `param or {}`: a parameter defaulting to
// None that the fallback quietly replaces with an empty container. Other
// fallbacks, such as `.get(k) or ""`, are idiom.
func pyOrDefault(ctx *Ctx, findings *[]Finding) {
	if ctx.Lang != lang.Python {
		return
	}
	lang.EachNamed(ctx.Root, func(n *ts.Node) {
		if n.Kind() != "boolean_operator" {
			return
		}
		left := n.ChildByFieldName("left")
		right := n.ChildByFieldName("right")
		op := n.ChildByFieldName("operator")
		if left == nil || right == nil || op == nil || ctx.Text(op) != "or" {
			return
		}
		if left.Kind() != kindIdentifier || !emptyContainer(right) || !isParamOfEnclosing(ctx, n, ctx.Text(left)) {
			return
		}
		*findings = append(*findings, Finding{
			Rule: "py/or-default", Line: lineOf(n), Match: ctx.Text(right),
		})
	})
}

// emptyContainer reports whether a node is an empty list or dict literal.
func emptyContainer(n *ts.Node) bool {
	return (n.Kind() == "list" || n.Kind() == "dictionary") && n.NamedChildCount() == 0
}

// isParamOfEnclosing reports whether name is a parameter of the function that
// holds the node.
func isParamOfEnclosing(ctx *Ctx, n *ts.Node, name string) bool {
	fn := n.Parent()
	for fn != nil && fn.Kind() != kindFunctionDef {
		fn = fn.Parent()
	}
	if fn == nil {
		return false
	}
	params := fn.ChildByFieldName("parameters")
	if params == nil {
		return false
	}
	for _, p := range lang.NamedChildren(params) {
		target := p.ChildByFieldName("name")
		if target == nil && p.Kind() == kindIdentifier {
			target = p
		}
		if target != nil && ctx.Text(target) == name {
			return true
		}
	}

	return false
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
			if p.Kind() == kindFunctionDef || p.Kind() == "class_definition" {
				return
			}
		}
	})
}

// pyMockPatch flags test-time replacement of a target: the test stops
// exercising the code it names.
func pyMockPatch(ctx *Ctx, findings *[]Finding) {
	if ctx.Lang != lang.Python {
		return
	}
	ctx.eachCall(func(n *ts.Node, callee string) {
		if isPatchCallee(callee) {
			*findings = append(*findings, Finding{Rule: "py/mock-patch", Line: lineOf(n), Match: callee})
		}
	})
}

// isPatchCallee reports whether a callee replaces a target for the test's
// duration: unittest.mock's patch and its object form, pytest-mock's mocker,
// and pytest's monkeypatch.
func isPatchCallee(callee string) bool {
	switch callee {
	case "patch", "mock.patch", "patch.object", "mock.patch.object", "monkeypatch.setattr":
		return true
	default:
		return strings.HasPrefix(callee, "mocker.patch")
	}
}

// pyTestReadsSource flags tests that open or parse repository source, config,
// or docs: a test coupled to that text breaks on every unrelated edit.
func pyTestReadsSource(ctx *Ctx, findings *[]Finding) {
	if ctx.Lang != lang.Python {
		return
	}
	ctx.eachCall(func(n *ts.Node, callee string) {
		if pyReadsSource(ctx, n, callee) {
			*findings = append(*findings, Finding{Rule: rulePyTestReadsSource, Line: lineOf(n), Match: callee})
		}
	})
}

// pyReadsSource reports whether a call reads repository files or a module's
// source. Writes and reads of temp or fixture paths do not count.
func pyReadsSource(ctx *Ctx, call *ts.Node, callee string) bool {
	switch {
	case callee == "inspect.getsource" || callee == "inspect.getsourcelines":
		return true
	case callee == "ast.parse":
		return computedSource(firstArgText(ctx, call))
	case writesFile(ctx, call):
		return false
	case callee == "open":
		return readsRepoFile(firstArgText(ctx, call))
	case isPathRead(callee):
		return readsRepoFile(receiverText(ctx, call))
	default:
		return false
	}
}

// openModeLit matches a literal file mode such as "w" or "rb+".
var openModeLit = regexp.MustCompile( //nolint:gochecknoglobals // a compiled pattern is a constant
	`^["'][rwxabt+]{1,3}["']$`)

// writesFile reports whether a call passes a write, append, or create mode.
func writesFile(ctx *Ctx, call *ts.Node) bool {
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return false
	}
	for _, arg := range lang.NamedChildren(args) {
		text := ctx.Text(arg)
		if openModeLit.MatchString(text) && strings.ContainsAny(text, "wxa+") {
			return true
		}
	}

	return false
}

// isPathRead reports whether a callee is a method that reads a path object.
func isPathRead(callee string) bool {
	for _, suffix := range []string{".read_text", ".read_bytes", ".readlines", ".open"} {
		if strings.HasSuffix(callee, suffix) {
			return true
		}
	}

	return false
}

// receiverText answers the text of a method call's object, the path it reads.
func receiverText(ctx *Ctx, call *ts.Node) string {
	fn := call.ChildByFieldName("function")
	if fn == nil {
		return ""
	}
	obj := fn.ChildByFieldName("object")
	if obj == nil {
		return ""
	}

	return ctx.Text(obj)
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

// cooperativeSleeps are the event-loop sleeps that yield control at 0 without
// waiting on time.
//
//nolint:gochecknoglobals // a lookup table is a constant
var cooperativeSleeps = map[string]bool{"asyncio.sleep": true, "anyio.sleep": true, "trio.sleep": true}

// pyTimingHack flags sleeps and frozen clocks in tests: a test synchronized by
// time is a flake in waiting.
func pyTimingHack(ctx *Ctx, findings *[]Finding) {
	if ctx.Lang != lang.Python {
		return
	}
	ctx.eachCall(func(n *ts.Node, callee string) {
		if cooperativeSleeps[callee] && firstArgText(ctx, n) == "0" {
			return
		}
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

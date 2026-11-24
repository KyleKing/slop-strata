package rules

import (
	"regexp"
	"strings"
	"unicode"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/kyleking/slop-strata/internal/lang"
)

func isTS(ctx *Ctx) bool {
	return ctx.Lang == lang.TypeScript || ctx.Lang == lang.TSX
}

// tsSilentCatch flags an empty catch block, and a promise's .catch given a
// function with an empty body, which swallows the rejection the same way. A
// block that holds a comment says why and is left alone, as ESLint's no-empty
// leaves it.
func tsSilentCatch(ctx *Ctx, findings *[]Finding) {
	if !isTS(ctx) {
		return
	}
	lang.EachNamed(ctx.Root, func(n *ts.Node) {
		switch {
		case n.Kind() == "catch_clause" && emptyBlock(n.ChildByFieldName(fieldBody)),
			n.Kind() == kindCallExpression && emptyCatchCallback(ctx, n):
			*findings = append(*findings, Finding{Rule: "ts/silent-catch", Line: lineOf(n)})
		}
	})
}

// emptyBlock reports whether a statement block holds nothing, not even a
// comment.
func emptyBlock(block *ts.Node) bool {
	return block != nil && block.Kind() == "statement_block" && block.NamedChildCount() == 0
}

// emptyCatchCallback reports whether a call is `.catch(fn)` with a function
// whose body is an empty block.
func emptyCatchCallback(ctx *Ctx, call *ts.Node) bool {
	fn := call.ChildByFieldName("function")
	if fn == nil || fn.Kind() != kindMember || ctx.fieldText(fn, "property") != "catch" {
		return false
	}
	arg := firstArg(call)

	return arg != nil && ctx.Lang.FunctionKinds[arg.Kind()] && emptyBlock(arg.ChildByFieldName(fieldBody))
}

// tsAsUnknownAs flags casts through `unknown` and double casts: both erase
// the type the expression really has. The angle-bracket spelling, <T>value,
// is the same cast.
func tsAsUnknownAs(ctx *Ctx, findings *[]Finding) {
	if !isTS(ctx) {
		return
	}
	lang.EachNamed(ctx.Root, func(n *ts.Node) {
		inner, typeArg := castParts(n)
		if inner == nil || typeArg == nil {
			return
		}
		target := strings.Trim(ctx.Text(typeArg), "<>")
		if target == textUnknown || inner.Kind() == kindAsExpr || inner.Kind() == kindTypeAssertion {
			*findings = append(*findings, Finding{Rule: ruleTSAsUnknownAs, Line: lineOf(n), Match: ctx.Text(n)})
		}
	})
}

// The two spellings of a TypeScript cast, each a value and a type.
const (
	kindAsExpr        = "as_expression"
	kindTypeAssertion = "type_assertion"
	castOperands      = 2
)

// castParts answers the value and the target type of a cast in either
// spelling, with the value read through any parentheses, or nils for any
// other node.
func castParts(n *ts.Node) (*ts.Node, *ts.Node) {
	if (n.Kind() != kindAsExpr && n.Kind() != kindTypeAssertion) || n.NamedChildCount() != castOperands {
		return nil, nil
	}
	value, target := n.NamedChild(0), n.NamedChild(1)
	if n.Kind() == kindTypeAssertion {
		value, target = target, value
	}
	for value.Kind() == "parenthesized_expression" && value.NamedChildCount() == 1 {
		value = value.NamedChild(0)
	}

	return value, target
}

// tsRecordUnknown flags `Record<..., unknown>` and `Record<..., any>`: a map
// that says nothing about its values.
func tsRecordUnknown(ctx *Ctx, findings *[]Finding) {
	if !isTS(ctx) {
		return
	}
	lang.EachNamed(ctx.Root, func(n *ts.Node) {
		if n.Kind() != kindGenericType {
			return
		}
		name := n.NamedChild(0)
		if name == nil || ctx.Text(name) != "Record" {
			return
		}
		lang.EachNamed(n, func(c *ts.Node) {
			if c.Kind() == kindPredefinedType && (ctx.Text(c) == "unknown" || ctx.Text(c) == textAny) {
				*findings = append(*findings, Finding{Rule: ruleTSRecordUnknown, Line: lineOf(n), Match: ctx.Text(n)})
			}
		})
	})
}

// tsTypeAny flags `any` annotations.
func tsTypeAny(ctx *Ctx, findings *[]Finding) {
	if !isTS(ctx) {
		return
	}
	lang.EachNamed(ctx.Root, func(n *ts.Node) {
		if n.Kind() == kindPredefinedType && ctx.Text(n) == textAny {
			*findings = append(*findings, Finding{Rule: "ts/type-any", Line: lineOf(n), Match: textAny})
		}
	})
}

// tsModuleMock flags module-level mocking: the test stops exercising the real
// import.
func tsModuleMock(ctx *Ctx, findings *[]Finding) {
	if !isTS(ctx) {
		return
	}
	callees := map[string]bool{
		"vi.mock": true, "vi.doMock": true, "jest.mock": true, "jest.unmock": true,
		"jest.doMock": true, "mock.module": true,
	}
	ctx.eachCall(func(n *ts.Node, callee string) {
		if callees[callee] {
			*findings = append(*findings, Finding{Rule: "ts/module-mock", Line: lineOf(n), Match: callee})
		}
	})
}

// tsTestReadsSource flags tests that read repository source, config, or docs.
func tsTestReadsSource(ctx *Ctx, findings *[]Finding) {
	if !isTS(ctx) {
		return
	}
	ctx.eachCall(func(n *ts.Node, callee string) {
		for _, suffix := range []string{"readFileSync", "readdirSync", "readJSONSync", ".readFile"} {
			if strings.HasSuffix(callee, suffix) {
				if readsRepoFile(ctx.boundText(firstArg(n))) {
					*findings = append(*findings, Finding{Rule: "ts/test-reads-source", Line: lineOf(n), Match: callee})
				}

				return
			}
		}
	})
}

// mockMatchers are the matchers that assert on a spy rather than a result.
var mockMatchers = map[string]bool{ //nolint:gochecknoglobals // a matcher list is a constant
	"toHaveBeenCalled":         true,
	"toHaveBeenCalledTimes":    true,
	"toHaveBeenCalledWith":     true,
	"toHaveBeenNthCalledWith":  true,
	"toHaveBeenLastCalledWith": true,
	"toHaveBeenCalledOnce":     true,
	"toHaveBeenCalledOnceWith": true,
}

// expectChain decomposes `expect(subject).matcher(arg)` into subject, matcher
// name, and the matcher's last argument. It reports nil for any other call.
func expectChain(ctx *Ctx, n *ts.Node) (*ts.Node, string, *ts.Node) {
	fn := n.ChildByFieldName("function")
	if fn == nil || fn.Kind() != "member_expression" || fn.NamedChildCount() < 2 {
		return nil, "", nil
	}
	obj := fn.NamedChild(0)
	prop := fn.NamedChild(fn.NamedChildCount() - 1)
	if obj == nil || obj.Kind() != kindCallExpression || ctx.calleeText(obj) != "expect" {
		return nil, "", nil
	}
	args := obj.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return nil, "", nil
	}
	var arg *ts.Node
	outer := n.ChildByFieldName("arguments")
	if outer != nil && outer.NamedChildCount() > 0 {
		arg = outer.NamedChild(outer.NamedChildCount() - 1)
	}

	return args.NamedChild(0), ctx.Text(prop), arg
}

// tsMockOnlyAssert flags an it/test body where every expect is on a mock: a
// spy matcher, or any matcher over the spy's own call record (`spy.mock.calls`).
// An expect of a literal asserts nothing and is not counted.
func tsMockOnlyAssert(ctx *Ctx, findings *[]Finding) {
	if !isTS(ctx) {
		return
	}
	lang.EachNamed(ctx.Root, func(n *ts.Node) {
		if !ctx.Lang.FunctionKinds[n.Kind()] || !isTestCallback(ctx, n) {
			return
		}
		expects, mockOnly := 0, true
		lang.EachNamed(n, func(c *ts.Node) {
			if !ctx.Lang.CallKinds[c.Kind()] {
				return
			}
			subject, matcher, _ := expectChain(ctx, c)
			if matcher == "" || ctx.Lang.LiteralKinds[subject.Kind()] {
				return
			}
			expects++
			if !mockMatchers[matcher] && !readsSpyRecord(ctx, subject) {
				mockOnly = false
			}
		})
		if expects > 0 && mockOnly {
			*findings = append(*findings, Finding{
				Rule: "ts/mock-only-assert", Line: lineOf(n),
				Match: lang.FunctionName(ctx.Lang, n, ctx.Src),
			})
		}
	})
}

// readsSpyRecord reports whether an expect's subject reads a spy's call
// record: a member chain that passes through `.mock`.
func readsSpyRecord(ctx *Ctx, subject *ts.Node) bool {
	found := false
	lang.EachNamed(subject, func(n *ts.Node) {
		if n.Kind() == kindMember && ctx.fieldText(n, "property") == "mock" {
			found = true
		}
	})

	return found
}

// testModifiers are the members of it and test that still declare one test.
var testModifiers = map[string]bool{ //nolint:gochecknoglobals // a constant vocabulary
	"only": true, "skip": true, "each": true, "concurrent": true, "sequential": true,
	"fails": true, "runIf": true, "skipIf": true,
}

// isTestCallback reports whether a function is the body passed to an it or
// test call, modified or not (it.only, test.each), so an enclosing describe
// block is not judged as one test.
func isTestCallback(ctx *Ctx, fn *ts.Node) bool {
	args := fn.Parent()
	if args == nil || args.Kind() != kindArguments {
		return false
	}
	call := args.Parent()
	if call == nil || call.Kind() != kindCallExpression {
		return false
	}
	callee := ctx.calleeText(call)

	base, rest, modified := strings.Cut(callee, ".")
	if base != "it" && base != "test" {
		return false
	}
	end := strings.IndexFunc(rest, func(r rune) bool { return r != '_' && !unicode.IsLetter(r) })
	if end < 0 {
		end = len(rest)
	}

	return !modified || testModifiers[rest[:end]]
}

// constName matches the CONSTANT_CASE shape an echoed constant has.
var constName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`) //nolint:gochecknoglobals // a compiled pattern is a constant

// tsConstantEcho flags expect(CONSTANT).toBe(literal): the assertion restates
// a value nobody computed. The two sides may be swapped.
func tsConstantEcho(ctx *Ctx, findings *[]Finding) {
	if !isTS(ctx) {
		return
	}
	lang.EachNamed(ctx.Root, func(n *ts.Node) {
		if !ctx.Lang.CallKinds[n.Kind()] {
			return
		}
		subject, matcher, arg := expectChain(ctx, n)
		if subject == nil || (matcher != "toBe" && matcher != "toEqual" && matcher != "toStrictEqual") {
			return
		}
		if arg == nil {
			return
		}
		literal := func(n *ts.Node) bool { return ctx.Lang.LiteralKinds[n.Kind()] }
		constant := func(n *ts.Node) bool { return n.Kind() == kindIdentifier && constName.MatchString(ctx.Text(n)) }
		if constant(subject) && literal(arg) || literal(subject) && constant(arg) {
			*findings = append(*findings, Finding{Rule: "ts/constant-echo", Line: lineOf(n), Match: ctx.Text(n)})
		}
	})
}

// tsTimingHack flags fake timers and sleeps in tests.
func tsTimingHack(ctx *Ctx, findings *[]Finding) {
	if !isTS(ctx) {
		return
	}
	callees := map[string]bool{
		"vi.useFakeTimers": true, "jest.useFakeTimers": true,
		"sinon.useFakeTimers": true, "vi.setSystemTime": true,
		"jest.setSystemTime": true, "vi.advanceTimersByTime": true,
		"jest.advanceTimersByTime": true,
	}
	ctx.eachCall(func(n *ts.Node, callee string) {
		if callees[callee] || strings.HasSuffix(callee, ".sleep") {
			*findings = append(*findings, Finding{Rule: "ts/timing-hack", Line: lineOf(n), Match: callee})
		}
	})
}

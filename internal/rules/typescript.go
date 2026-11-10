package rules

import (
	"regexp"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/kyleking/slop-strata/internal/lang"
)

func isTS(ctx *Ctx) bool {
	return ctx.Lang == lang.TypeScript || ctx.Lang == lang.TSX
}

// tsSilentCatch flags an empty catch block.
func tsSilentCatch(ctx *Ctx, findings *[]Finding) {
	if !isTS(ctx) {
		return
	}
	lang.EachNamed(ctx.Root, func(n *ts.Node) {
		if n.Kind() == "catch_clause" && blockStatements(n) == 0 {
			*findings = append(*findings, Finding{Rule: "ts/silent-catch", Line: lineOf(n)})
		}
	})
}

// tsAsUnknownAs flags casts through `unknown` and double casts: both erase
// the type the expression really has.
func tsAsUnknownAs(ctx *Ctx, findings *[]Finding) {
	if !isTS(ctx) {
		return
	}
	lang.EachNamed(ctx.Root, func(n *ts.Node) {
		if n.Kind() != "as_expression" {
			return
		}
		inner := n.NamedChild(0)
		var typeArg *ts.Node
		for i := range n.NamedChildCount() {
			c := n.NamedChild(i)
			if c != inner {
				typeArg = c
			}
		}
		switch {
		case typeArg != nil && ctx.Text(typeArg) == textUnknown:
			*findings = append(*findings, Finding{Rule: ruleTSAsUnknownAs, Line: lineOf(n), Match: ctx.Text(n)})
		case inner != nil && inner.Kind() == "as_expression":
			*findings = append(*findings, Finding{Rule: ruleTSAsUnknownAs, Line: lineOf(n), Match: ctx.Text(n)})
		}
	})
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
			if c.Kind() == "predefined_type" && (ctx.Text(c) == "unknown" || ctx.Text(c) == textAny) {
				*findings = append(*findings, Finding{Rule: "ts/record-unknown", Line: lineOf(n), Match: ctx.Text(n)})
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
		if n.Kind() == "predefined_type" && ctx.Text(n) == textAny {
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
		"vi.mock": true, "jest.mock": true, "jest.unmock": true,
		"jest.doMock": true, "mock.module": true,
	}
	ctx.eachCall(func(n *ts.Node, callee string) {
		if callees[callee] {
			*findings = append(*findings, Finding{Rule: "ts/module-mock", Line: lineOf(n), Match: callee})
		}
	})
}

// tsTestReadsSource flags tests that read files.
func tsTestReadsSource(ctx *Ctx, findings *[]Finding) {
	if !isTS(ctx) {
		return
	}
	ctx.eachCall(func(n *ts.Node, callee string) {
		for _, suffix := range []string{"readFileSync", "readdirSync", "readJSONSync", ".readFile"} {
			if callee == suffix || strings.HasSuffix(callee, "."+suffix) || strings.HasSuffix(callee, suffix) {
				*findings = append(*findings, Finding{Rule: "ts/test-reads-source", Line: lineOf(n), Match: callee})

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
	if obj == nil || obj.Kind() != "call_expression" || ctx.calleeText(obj) != "expect" {
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

// tsMockOnlyAssert flags an it/test body where every expect is on a mock.
func tsMockOnlyAssert(ctx *Ctx, findings *[]Finding) {
	if !isTS(ctx) {
		return
	}
	lang.EachNamed(ctx.Root, func(n *ts.Node) {
		if !ctx.Lang.FunctionKinds[n.Kind()] {
			return
		}
		expects, mockOnly := 0, true
		lang.EachNamed(n, func(c *ts.Node) {
			if !ctx.Lang.CallKinds[c.Kind()] {
				return
			}
			_, matcher, _ := expectChain(ctx, c)
			if matcher == "" {
				return
			}
			expects++
			if !mockMatchers[matcher] {
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

// constName matches the CONSTANT_CASE shape an echoed constant has.
var constName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`) //nolint:gochecknoglobals // a compiled pattern is a constant

// tsConstantEcho flags expect(CONSTANT).toBe(literal): the assertion restates
// a value nobody computed.
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
		if subject.Kind() == kindIdentifier && constName.MatchString(ctx.Text(subject)) &&
			arg != nil && ctx.Lang.LiteralKinds[arg.Kind()] {
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

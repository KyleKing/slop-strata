package rules

import (
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/kyleking/slop-strata/internal/lang"
)

// goSilentError flags `_ = f(...)` and friends: an assignment that discards a
// call's result, which in Go usually means the error.
func goSilentError(ctx *Ctx, findings *[]Finding) {
	if ctx.Lang != lang.Go {
		return
	}
	lang.EachNamed(ctx.Root, func(n *ts.Node) {
		if n.Kind() != "assignment_statement" && n.Kind() != "short_var_declaration" {
			return
		}
		left := n.ChildByFieldName("left")
		right := n.ChildByFieldName("right")
		if left == nil || right == nil {
			return
		}
		blank := false
		lang.EachNamed(left, func(c *ts.Node) {
			if c.Kind() == "blank_identifier" || (c.Kind() == kindIdentifier && ctx.Text(c) == "_") {
				blank = true
			}
		})
		if !blank {
			return
		}
		hasCall := false
		lang.EachNamed(right, func(c *ts.Node) {
			if c.Kind() == "call_expression" {
				hasCall = true
			}
		})
		if hasCall {
			*findings = append(*findings, Finding{Rule: "go/silent-error", Line: lineOf(n), Match: ctx.Text(right)})
		}
	})
}

// goTypeAny flags `any` and the empty `interface{}` in signatures: the type
// system has stopped describing what flows through.
func goTypeAny(ctx *Ctx, findings *[]Finding) {
	if ctx.Lang != lang.Go {
		return
	}
	lang.EachNamed(ctx.Root, func(n *ts.Node) {
		switch {
		case n.Kind() == kindTypeIdentifier && ctx.Text(n) == textAny:
			*findings = append(*findings, Finding{Rule: ruleGoTypeAny, Line: lineOf(n), Match: textAny})
		case n.Kind() == "interface_type" && n.NamedChildCount() == 0:
			*findings = append(*findings, Finding{Rule: ruleGoTypeAny, Line: lineOf(n), Match: "interface{}"})
		}
	})
}

// goTestReadsSource flags tests that read files.
func goTestReadsSource(ctx *Ctx, findings *[]Finding) {
	if ctx.Lang != lang.Go {
		return
	}
	ctx.eachCall(func(n *ts.Node, callee string) {
		for _, suffix := range []string{".ReadFile", ".Open", ".OpenFile", ".ReadAll"} {
			if strings.HasSuffix(callee, suffix) {
				*findings = append(*findings, Finding{Rule: "go/test-reads-source", Line: lineOf(n), Match: callee})

				return
			}
		}
	})
}

// goTimingHack flags sleeps in tests.
func goTimingHack(ctx *Ctx, findings *[]Finding) {
	if ctx.Lang != lang.Go {
		return
	}
	ctx.eachCall(func(n *ts.Node, callee string) {
		if strings.HasSuffix(callee, ".Sleep") {
			*findings = append(*findings, Finding{Rule: "go/timing-hack", Line: lineOf(n), Match: callee})
		}
	})
}

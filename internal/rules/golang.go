package rules

import (
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/kyleking/slop-strata/internal/lang"
)

// ignoredCallees are callees whose last result is not an error in practice:
// strings.Cut and its kin return a bool, and a Close error is conventionally
// dropped.
var ignoredCallees = map[string]bool{ //nolint:gochecknoglobals // a fixed vocabulary
	"Close": true, "Cut": true, "CutPrefix": true, "CutSuffix": true,
}

// goSilentError flags an assignment whose last target is `_` and whose right
// side is a single call, which discards the call's error, or the name err,
// which discards an error bound a line earlier. Without type information, a
// bound `err` target or an ignored callee clears the hit.
func goSilentError(ctx *Ctx, findings *[]Finding) {
	if ctx.Lang != lang.Go {
		return
	}
	lang.EachNamed(ctx.Root, func(n *ts.Node) {
		if n.Kind() != "assignment_statement" && n.Kind() != kindShortVar {
			return
		}
		left := n.ChildByFieldName("left")
		right := n.ChildByFieldName("right")
		if left == nil || right == nil {
			return
		}
		targets := exprItems(left)
		if len(targets) == 0 || !isBlankTarget(ctx, targets[len(targets)-1]) || bindsErr(ctx, targets) {
			return
		}
		call := soleCall(right)
		if (call == nil || ignoredCallees[lastSegment(ctx.calleeText(call))]) && !soleErr(ctx, right) {
			return
		}
		*findings = append(*findings,
			Finding{Rule: ruleGoSilentError, Line: lineOf(n), Match: oneLine(ctx.Text(right))})
	})
}

// maxMatchLen caps the matched text a finding carries, so a call spanning many
// lines stays one readable line in output, labels, and the baseline.
const maxMatchLen = 80

// oneLine collapses a call's whitespace and cuts it to maxMatchLen runes.
func oneLine(text string) string {
	runes := []rune(strings.Join(strings.Fields(text), " "))
	if len(runes) <= maxMatchLen {
		return string(runes)
	}

	return string(runes[:maxMatchLen]) + "…"
}

// exprItems lists an expression_list's items, or the node itself when the
// grammar did not wrap it.
func exprItems(n *ts.Node) []*ts.Node {
	if n.Kind() != kindExprList {
		return []*ts.Node{n}
	}

	return lang.NamedChildren(n)
}

// isBlankTarget reports whether an assignment target is the blank identifier.
func isBlankTarget(ctx *Ctx, n *ts.Node) bool {
	return n.Kind() == "blank_identifier" || (n.Kind() == kindIdentifier && ctx.Text(n) == "_")
}

// bindsErr reports whether any assignment target is named err.
func bindsErr(ctx *Ctx, targets []*ts.Node) bool {
	for _, t := range targets {
		if t.Kind() == kindIdentifier && ctx.Text(t) == "err" {
			return true
		}
	}

	return false
}

// soleCall answers the call when a right-hand side is exactly one call.
func soleCall(right *ts.Node) *ts.Node {
	items := exprItems(right)
	if len(items) != 1 || items[0].Kind() != kindCallExpression {
		return nil
	}

	return items[0]
}

// soleErr reports whether a right-hand side is the name err and nothing else.
func soleErr(ctx *Ctx, right *ts.Node) bool {
	items := exprItems(right)

	return len(items) == 1 && items[0].Kind() == kindIdentifier && ctx.Text(items[0]) == "err"
}

// lastSegment answers the final dot-separated part of a callee.
func lastSegment(callee string) string {
	return callee[strings.LastIndexByte(callee, '.')+1:]
}

// goTypeAny flags `any` and the empty `interface{}` in signatures, except generic constraints and spread variadics.
func goTypeAny(ctx *Ctx, findings *[]Finding) {
	if ctx.Lang != lang.Go {
		return
	}
	lang.EachNamed(ctx.Root, func(n *ts.Node) {
		switch {
		case n.Kind() == kindTypeIdentifier && ctx.Text(n) == textAny:
			if !ctx.anyIsExempt(n) {
				*findings = append(*findings, Finding{Rule: ruleGoTypeAny, Line: lineOf(n), Match: textAny})
			}
		case n.Kind() == "interface_type" && n.NamedChildCount() == 0:
			if !ctx.anyIsExempt(n) {
				*findings = append(*findings, Finding{Rule: ruleGoTypeAny, Line: lineOf(n), Match: "interface{}"})
			}
		}
	})
}

// anyIsExempt reports whether an any type is a generic constraint or the type
// of a variadic parameter that the function passes on as name....
func (c *Ctx) anyIsExempt(n *ts.Node) bool {
	parent := n.Parent()
	if parent == nil {
		return false
	}
	switch parent.Kind() {
	case "type_constraint":
		return true
	case "variadic_parameter_declaration":
		name := parent.ChildByFieldName("name")

		return name != nil && c.forwardsSpread(parent, c.Text(name))
	}

	return false
}

// forwardsSpread reports whether the function holding node spreads the
// variadic name into a call.
func (c *Ctx) forwardsSpread(node *ts.Node, name string) bool {
	fn := node
	for fn != nil && !c.Lang.FunctionKinds[fn.Kind()] {
		fn = fn.Parent()
	}
	if fn == nil {
		return false
	}
	forwarded := false
	lang.EachNamed(fn, func(n *ts.Node) {
		if n.Kind() == "variadic_argument" && n.NamedChildCount() == 1 && c.Text(n.NamedChild(0)) == name {
			forwarded = true
		}
	})

	return forwarded
}

// goTestReadsSource flags tests that read repository source, config, or docs.
func goTestReadsSource(ctx *Ctx, findings *[]Finding) {
	if ctx.Lang != lang.Go {
		return
	}
	ctx.eachCall(func(n *ts.Node, callee string) {
		for _, suffix := range []string{".ReadFile", ".Open", ".OpenFile", ".ReadAll"} {
			if strings.HasSuffix(callee, suffix) {
				if readsRepoFile(ctx.boundText(firstArg(n))) {
					*findings = append(*findings, Finding{Rule: "go/test-reads-source", Line: lineOf(n), Match: callee})
				}

				return
			}
		}
	})
}

// goTimingHack flags sleeps in tests: a call to Sleep, and a receive from
// time.After that stands alone as a statement, which waits the same way. A
// timeout arm of a select is no sleep.
func goTimingHack(ctx *Ctx, findings *[]Finding) {
	if ctx.Lang != lang.Go {
		return
	}
	ctx.eachCall(func(n *ts.Node, callee string) {
		if strings.HasSuffix(callee, ".Sleep") || (strings.HasSuffix(callee, ".After") && bareReceive(n)) {
			*findings = append(*findings, Finding{Rule: "go/timing-hack", Line: lineOf(n), Match: callee})
		}
	})
}

// bareReceive reports whether a call is received from as a whole statement.
func bareReceive(call *ts.Node) bool {
	recv := call.Parent()
	if recv == nil || recv.Kind() != "unary_expression" || recv.Child(0).Kind() != "<-" {
		return false
	}
	stmt := recv.Parent()

	return stmt != nil && stmt.Kind() == kindExpressionStmt
}

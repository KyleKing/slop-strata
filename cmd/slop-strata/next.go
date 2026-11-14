package main

import (
	"cmp"
	"fmt"
	"io"

	"github.com/kyleking/slop-strata/internal/report"
	"github.com/kyleking/slop-strata/internal/structure"
)

// nextLimit caps the next list of a change.
const nextLimit = 3

const (
	nextGrownFunction  = "grown_function"
	nextIntentMismatch = "intent_mismatch"
	nextNewFinding     = "new_finding"
)

// nextItem is one place a reader should look at, with the numbers that put it there.
type nextItem struct {
	Kind   string `json:"kind"`
	File   string `json:"file,omitempty"`
	Name   string `json:"name,omitempty"`
	Rule   string `json:"rule,omitempty"`
	Reason string `json:"reason"`
	Line   int    `json:"line,omitempty"`
}

// nextItems applies the worth-a-look rule to a scored change and returns up to
// nextLimit items. The change is worth a look exactly when the list is not
// empty. Items come in a fixed order: production functions over CC 10 that grew
// or were added, by mass grown, then new findings by path and line, then a
// stated intent the numbers argue against.
func nextItems(res *changeResult) []nextItem {
	var items []nextItem
	for _, f := range res.grownFuncs {
		if !f.Test && f.CCAfter > structure.CCLimit {
			items = append(items, grownFunctionItem(f))
		}
	}
	for _, f := range res.Findings {
		if f.New {
			items = append(items, nextItem{
				Kind: nextNewFinding, File: f.Path, Line: f.Line, Rule: f.Rule,
				Reason: "new rule hit, not in the baseline",
			})
		}
	}
	if res.disagreement != "" {
		items = append(items, nextItem{
			Kind: nextIntentMismatch, Reason: "stated intent disagrees with the numbers: reads as " + res.disagreement,
		})
	}

	return items[:min(len(items), nextLimit)]
}

func grownFunctionItem(f topFunc) nextItem {
	reason := fmt.Sprintf("new function, CC %d, +%.1f mass", f.CCAfter, f.MassGrown)
	if f.CCBefore > 0 {
		verb := "crossed"
		if f.CCBefore > structure.CCLimit {
			verb = "already over"
		}
		reason = fmt.Sprintf("CC %d→%d, +%.1f mass, %s CC %d",
			f.CCBefore, f.CCAfter, f.MassGrown, verb, structure.CCLimit)
	}

	return nextItem{Kind: nextGrownFunction, File: f.File, Line: f.Line, Name: f.Name, Reason: reason}
}

func (n nextItem) text() string {
	at := ""
	if n.File != "" {
		at = fmt.Sprintf("%s:%d ", n.File, n.Line)
	}
	label := cmp.Or(n.Name, n.Rule)
	if label != "" {
		label += ": "
	}

	return "next: " + at + label + n.Reason
}

// printBrief writes the reading and the next lines, or nothing when the change
// is not worth a look.
func printBrief(w io.Writer, res *changeResult) {
	if len(res.Next) == 0 {
		return
	}
	report.Fprintln(w, res.Reading)
	for _, n := range res.Next {
		report.Fprintln(w, n.text())
	}
}

// lookStatus turns a change worth a look into errWorthALook when --exit-code is set.
func lookStatus(a *args, res *changeResult) error {
	if a.exitCode && len(res.Next) > 0 {
		return errWorthALook
	}

	return nil
}

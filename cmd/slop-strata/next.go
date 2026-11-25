package main

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"io"

	"github.com/kyleking/aragonite/vcs"

	"github.com/kyleking/slop-strata/internal/report"
	"github.com/kyleking/slop-strata/internal/rules"
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
	Kind    string `json:"kind"`
	File    string `json:"file,omitempty"`
	Name    string `json:"name,omitempty"`
	Rule    string `json:"rule,omitempty"`
	Reason  string `json:"reason"`
	Line    int    `json:"line,omitempty"`
	endLine int
}

// nextItems returns up to nextLimit items: grown functions over CC 10, new findings, then a contradicted intent.
func nextItems(res *changeResult) []nextItem {
	items := []nextItem{}
	for _, f := range res.grownFuncs {
		if !f.Test && f.CCAfter > structure.CCLimit {
			items = append(items, grownFunctionItem(f))
		}
	}
	for _, f := range res.Findings {
		if f.New {
			items = append(items, nextItem{
				Kind: nextNewFinding, File: f.Path, Line: f.Line, Rule: f.Rule, Reason: findingReason(f),
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

// notedRule reports whether a rule's findings carry a note that is their
// reason: where the code, the cases, the markup, the class, or the literal
// they repeat already is.
func notedRule(rule string) bool {
	switch rule {
	case rules.RuleNewDuplicate, rules.RuleNewCaseSet, rules.RuleNewJSXShape, rules.RuleNewSibling,
		rules.RuleDupLiteral:
		return true
	}

	return false
}

// findingReason says why a new finding is worth a look. A new duplicate
// carries its own reason: where the code it repeats already is.
func findingReason(f findingRow) string {
	if notedRule(f.Rule) && f.Note != "" {
		return f.Note
	}

	return "new rule hit, not in the baseline"
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

	return nextItem{
		Kind: nextGrownFunction, File: f.File, Line: f.Line, endLine: f.endLine, Name: f.Name, Reason: reason,
	}
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

// snippetLineCap caps the source lines one next function adds to the snippets.
const snippetLineCap = 80

// addNextSnippets adds the after-side source lines of each next function to the
// snippets, keyed by line number like a finding's line.
func (s *session) addNextSnippets(ctx context.Context, cp *contextPayload, files []vcs.CommitFile,
	committed bool, next []nextItem,
) {
	var want []vcs.CommitFile
	for _, n := range next {
		if n.Kind != nextGrownFunction {
			continue
		}
		for i := range files {
			if files[i].Path == n.File {
				want = append(want, files[i])
			}
		}
	}
	blobs, err := s.newBlobs(ctx, want, committed)
	if err != nil {
		cp.Messages = append(cp.Messages, "next function source unavailable: "+err.Error())

		return
	}
	for i := range want {
		f := &want[i]
		side := s.newContent(*f, committed, blobs)
		if side.err != nil {
			cp.Messages = append(cp.Messages, "next function source unavailable: "+side.err.Error())

			continue
		}
		for _, n := range next {
			if n.Kind == nextGrownFunction && n.File == f.Path {
				addFunctionLines(cp, n, side.content)
			}
		}
	}
}

func addFunctionLines(cp *contextPayload, n nextItem, content []byte) {
	lines := bytes.Split(content, []byte{'\n'})
	last := min(n.endLine, n.Line+snippetLineCap-1, len(lines))
	if cp.Snippets[n.File] == nil {
		cp.Snippets[n.File] = map[int]string{}
	}
	for i := n.Line; i <= last; i++ {
		cp.Snippets[n.File][i] = string(lines[i-1])
	}
}

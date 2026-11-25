package rules

import (
	"fmt"
	"slices"
	"strings"
)

// RuleDupLiteral is the id of the rule NewDupLiterals reports under. Its note
// names the files that already write the string out, which a caller may want
// to show.
const RuleDupLiteral = "x/dup-literal"

// maxLiteralShown is the longest value a finding prints before clipping it.
const maxLiteralShown = 60

// maxLiteralPlaces caps the files one finding's note lists.
const maxLiteralPlaces = 3

// NewDupLiterals flags a string a change wrote out once more that the tree
// already wrote out in other files: the string is a group LiteralGroups lists
// for the tree at the end of the change, and at least one of its places was
// there before. Rev is that tree. A string is reported once, on the first
// place the change added.
//
// A place is new when the change added its line, the file writes the string
// more often than it did, and the changed files together do too, so a place
// that moved from one file to another is not. The declaration of the constant
// that names the string is never the new place: a change that adds it is the
// fix.
func NewDupLiterals(rev map[string]*Fingerprint, changes []DupChange, experimental bool) map[string][]Finding {
	if !crossEnabled(RuleDupLiteral, experimental) {
		return nil
	}
	added, net := addedLiterals(rev, changes)
	if len(added) == 0 {
		return nil
	}
	out := map[string][]Finding{}
	groups := literalGroupsOf(rev, added)
	for i := range groups {
		g := &groups[i]
		at, ok := added[g.Value]
		if !ok || g.Written-net[g.Value] < 1 {
			continue
		}
		// The file is one of the group's other files when it wrote the
		// string before the place the change added.
		own := rev[at.path].Literals[g.Value].Lines[0] == at.line
		out[at.path] = append(out[at.path], Finding{
			Rule: RuleDupLiteral, Line: at.line, Scope: unitAt(rev[at.path], at.line),
			Match: clipLiteral(g.Value), Note: literalNote(g, at.path, own),
		})
	}
	for _, fs := range out {
		slices.SortFunc(fs, CompareFindings)
	}

	return out
}

// addedLiterals lists the strings the change wrote out in a new place, each
// with the first such place in the order of the changes, and how many more
// places each string has over the changed files than it had.
func addedLiterals(rev map[string]*Fingerprint, changes []DupChange) (map[string]casePlace, map[string]int) {
	net := netLiterals(changes)
	added := map[string]casePlace{}
	for i := range changes {
		ch := &changes[i]
		if ch.After == nil || rev[ch.Path] != ch.After || ch.Added == nil || literalAside(ch.Path) {
			continue
		}
		for value, use := range ch.After.Literals {
			if _, seen := added[value]; seen || net[value] <= 0 {
				continue
			}
			if line := addedPlace(ch, value, &use); line > 0 {
				added[value] = casePlace{path: ch.Path, line: line}
			}
		}
	}

	return added, net
}

// netLiterals counts, per string, the places the changed files hold after
// the change less the ones they held before it.
func netLiterals(changes []DupChange) map[string]int {
	net := map[string]int{}
	for i := range changes {
		for _, side := range []struct {
			fp   *Fingerprint
			step int
		}{{changes[i].After, 1}, {changes[i].Before, -1}} {
			if side.fp == nil {
				continue
			}
			for value, use := range side.fp.Literals {
				net[value] += side.step * use.Count
			}
		}
	}

	return net
}

// addedPlace answers the line of the first place of a string that a change
// added to a file, or 0 when the file writes the string no more often than it
// did or the added line is the declaration of its constant.
func addedPlace(ch *DupChange, value string, use *LiteralUse) int {
	if ch.Before != nil && ch.Before.Literals[value].Count >= use.Count {
		return 0
	}
	for _, line := range use.Lines {
		if ch.Added(line) && line != use.NameLine {
			return line
		}
	}

	return 0
}

// clipLiteral shortens a value for display, in double quotes.
func clipLiteral(value string) string {
	if len(value) > maxLiteralShown {
		value = strings.ToValidUTF8(value[:maxLiteralShown], "") + "…"
	}

	return `"` + value + `"`
}

// literalNote says how widely the tree writes the string out, where, what
// names it or how many places depend on its spelling, and what to do. File is
// the file of the new place, and own says its first place is the new one, so
// it is not among the files that held the string.
func literalNote(g *LiteralGroup, file string, own bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "written out in %s of %s", plural(g.Written, "place"), plural(g.Files, "file"))
	if g.Constant == nil {
		fmt.Fprintf(&b, ", %d of them a test, a case, or a key", g.Uses)
	}
	places := make([]string, 0, maxLiteralPlaces)
	others := g.Files
	if own {
		others--
	}
	for i := range g.Sites {
		if s := &g.Sites[i]; len(places) < maxLiteralPlaces && (!own || s.Path != file) {
			places = append(places, fmt.Sprintf("%s:%d", s.Path, s.Line))
		}
	}
	b.WriteString(": " + strings.Join(places, ", "))
	if more := others - len(places); more > 0 {
		b.WriteString(", and " + plural(more, "more file"))
	}
	if c := g.Constant; c != nil {
		fmt.Fprintf(&b, ". %s at %s:%d already names it. Read the constant here, or say why this place "+
			"spells the value out", c.Name, c.Path, c.Line)

		return b.String()
	}
	b.WriteString(". A slip in one spelling goes unseen. Name it once and read the name at each place, " +
		"or say why they stay apart")

	return b.String()
}

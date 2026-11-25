package rules

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// RuleNewJSXShape is the id of the rule NewJSXShapes reports under. Its note
// names the places that already hold the block, which a caller may want to
// show.
const RuleNewJSXShape = "x/new-jsx-shape"

// maxJSXSites caps the sites one finding's note lists.
const maxJSXSites = 3

// jsxSpan is a stretch of lines of one file.
type jsxSpan struct {
	path      string
	line, end int
}

// NewJSXShapes flags a block of markup a change added that the tree already
// held under other text and attribute values: the new block joins a group
// JSXGroups lists for the tree at the end of the change, and at least one
// other site of that group was there before. Rev is that tree. A group is
// reported once, on the first block the change added, and a block inside a
// reported one is not reported again.
//
// A block is new when the change added its first line and the changed files
// hold more blocks of its shape than they did before, so a block that moved,
// was re-indented, or had its text edited is not.
func NewJSXShapes(rev map[string]*Fingerprint, changes []DupChange, experimental bool) map[string][]Finding {
	if !crossEnabled(RuleNewJSXShape, experimental) {
		return nil
	}
	added := addedJSXBlocks(rev, changes)
	if len(added) == 0 {
		return nil
	}
	// The largest block first: a block inside a reported one says nothing more.
	type candidate struct {
		block   *jsxBlock
		cluster int
	}
	clusters := newJSXPass(rev).clusters()
	var blocks []candidate
	for i := range clusters {
		for j := range clusters[i].blocks {
			blocks = append(blocks, candidate{block: &clusters[i].blocks[j], cluster: i})
		}
	}
	slices.SortStableFunc(blocks, func(a, b candidate) int { return cmp.Compare(b.block.elems, a.block.elems) })
	out := map[string][]Finding{}
	found := make([]bool, len(clusters))
	var reported []jsxSpan
	for _, c := range blocks {
		fresh, held := splitBlock(c.block, added, reported)
		if len(fresh) == 0 || len(held) == 0 {
			continue
		}
		for _, p := range fresh {
			reported = append(reported, spanOf(p))
		}
		if found[c.cluster] {
			continue
		}
		found[c.cluster] = true
		at := fresh[0]
		out[at.file.path] = append(out[at.file.path], Finding{
			Rule: RuleNewJSXShape, Line: at.record().line, Scope: unitAt(at.file.fp, at.record().line),
			Match: jsxSiteName(held[0]), Note: jsxNote(c.block, fresh, held),
		})
	}

	return out
}

func spanOf(p jsxPlace) jsxSpan {
	return jsxSpan{path: p.file.path, line: p.record().line, end: p.record().end}
}

// splitBlock parts a block's places into the ones the change added, less
// those inside a block already reported, and the ones that were there.
func splitBlock(b *jsxBlock, added map[jsxSpan]bool, reported []jsxSpan) ([]jsxPlace, []jsxPlace) {
	var fresh, held []jsxPlace
	for _, p := range b.places {
		span := spanOf(p)
		switch {
		case !added[jsxSpan{path: span.path, line: span.line}]:
			held = append(held, p)
		case !within(span, reported):
			fresh = append(fresh, p)
		}
	}

	return fresh, held
}

// within reports whether a stretch sits inside one of a list.
func within(s jsxSpan, spans []jsxSpan) bool {
	for i := range spans {
		if o := &spans[i]; o.path == s.path && o.line <= s.line && s.end <= o.end {
			return true
		}
	}

	return false
}

// addedJSXBlocks lists the blocks the change added, by path and first line.
func addedJSXBlocks(rev map[string]*Fingerprint, changes []DupChange) map[jsxSpan]bool {
	net := map[uint64]int{}
	for i := range changes {
		for _, side := range []struct {
			fp   *Fingerprint
			step int
		}{{changes[i].After, 1}, {changes[i].Before, -1}} {
			if side.fp == nil {
				continue
			}
			for _, rec := range side.fp.JSX.decode().recs {
				net[rec.hash] += side.step
			}
		}
	}
	out := map[jsxSpan]bool{}
	for i := range changes {
		ch := &changes[i]
		if ch.After == nil || rev[ch.Path] != ch.After || ch.Added == nil || len(ch.After.JSX) == 0 ||
			jsxAside(ch.Path) {
			continue
		}
		for _, rec := range ch.After.JSX.decode().recs {
			if ch.Added(rec.line) && net[rec.hash] > 0 {
				out[jsxSpan{path: ch.Path, line: rec.line}] = true
			}
		}
	}

	return out
}

// jsxSiteName names a block by its file and enclosing unit. A finding's id
// is made of it, so it leaves the line out and survives line moves.
func jsxSiteName(p jsxPlace) string {
	if unit := unitAt(p.file.fp, p.record().line); unit != "" {
		return p.file.path + " " + unit
	}

	return p.file.path
}

// jsxSiteAt names a block for a reader: its unit, when it has one, and where
// it starts.
func jsxSiteAt(p jsxPlace) string {
	at := fmt.Sprintf("%s:%d", p.file.path, p.record().line)
	if unit := unitAt(p.file.fp, p.record().line); unit != "" {
		return unit + " at " + at
	}

	return at
}

// jsxNote says how big the block is, where the tree already held it, how
// much the sites differ, and what to do instead.
func jsxNote(c *jsxBlock, fresh, held []jsxPlace) string {
	sites := make([]string, 0, maxJSXSites)
	for _, p := range held[:min(len(held), maxJSXSites)] {
		sites = append(sites, jsxSiteAt(p))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "repeats %d elements of markup that %s already: %s", c.elems,
		pluralHolds(len(held)), strings.Join(sites, ", "))
	if more := len(held) - len(sites); more > 0 {
		b.WriteString(", and " + plural(more, "more place"))
	}
	if c.holes == 0 {
		b.WriteString(". The sites differ in nothing")
	} else {
		fmt.Fprintf(&b, ". The sites differ in %s", plural(c.holes, "value"))
	}
	b.WriteString(". Make the block one component that takes what differs as props, or say why the sites stay apart")
	if len(fresh) > 1 {
		fmt.Fprintf(&b, ". The change adds %d such blocks, here and at %s", len(fresh), jsxSiteAt(fresh[1]))
		if more := len(fresh) - 2; more > 0 { //nolint:mnd // the first two are named
			b.WriteString(", and " + plural(more, "more place"))
		}
	}

	return b.String()
}

// pluralHolds counts the other places and gives them their verb.
func pluralHolds(n int) string {
	if n == 1 {
		return "1 other place holds"
	}

	return fmt.Sprintf("%d other places hold", n)
}

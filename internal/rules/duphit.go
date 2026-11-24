package rules

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"
)

// dupHit is one stretch of a changed file with every site it repeats, the
// heaviest first, and the other places the change added the same copy.
type dupHit struct {
	runs  []dupRun
	again []string
}

// pickRuns orders a file's runs by score and nearness and keeps one hit per
// stretch of the file: a run over statements a heavier run already covers
// joins that run's hit as one more site, or is dropped.
func pickRuns(runs []dupRun) []dupHit {
	sortRuns(runs)
	var hits []dupHit
	for k := range runs {
		r := &runs[k]
		joined := false
		for i := range hits {
			first := &hits[i].runs[0]
			over := min(first.q+first.n, r.q+r.n) - max(first.q, r.q)
			if over <= 0 {
				continue
			}
			if 2*over >= first.n && !hits[i].has(r) {
				hits[i].runs = append(hits[i].runs, *r)
			}
			joined = true

			break
		}
		if !joined {
			hits = append(hits, dupHit{runs: []dupRun{*r}})
		}
	}
	slices.SortFunc(hits, func(a, b dupHit) int { return cmp.Compare(a.runs[0].q, b.runs[0].q) })

	return hits
}

// sortRuns orders runs by score, then nearness, then place.
func sortRuns(runs []dupRun) {
	slices.SortFunc(runs, func(a, b dupRun) int {
		return cmp.Or(
			cmp.Compare(b.score, a.score),
			cmp.Compare(a.distance(), b.distance()),
			strings.Compare(a.with.path, b.with.path),
			cmp.Compare(a.c, b.c), cmp.Compare(a.q, b.q),
		)
	})
}

// has reports whether a hit already lists the site a run points at.
func (h *dupHit) has(r *dupRun) bool {
	for i := range h.runs {
		o := &h.runs[i]
		if o.with == r.with && o.c < r.c+r.n && r.c < o.c+o.n {
			return true
		}
	}

	return false
}

// mergeCopies folds a hit into an earlier one when both are the same copy:
// the later stretch repeats the earlier one, or both repeat one stretch of
// the tree, so a change that adds a third and a fourth copy reports once and
// says where the others are.
func mergeCopies(files []fileHits) []fileHits {
	var kept []*dupHit
	for i := range files {
		hits := files[i].hits[:0]
		for _, h := range files[i].hits {
			if first := sameCopy(kept, &h.runs[0]); first != nil {
				first.again = append(first.again, fmt.Sprintf("%s:%d", h.runs[0].file.path, h.runs[0].qLine()))

				continue
			}
			hits = append(hits, h)
			kept = append(kept, &hits[len(hits)-1])
		}
		files[i].hits = hits
	}

	return files
}

// sameCopy answers the earlier hit a run is one more copy of, or nil.
func sameCopy(kept []*dupHit, r *dupRun) *dupHit {
	if len(r.parts) > 0 {
		return nil
	}
	for _, k := range kept {
		first := &k.runs[0]
		if len(first.parts) > 0 || first.expr != r.expr || first.n != r.n || first.tokens != r.tokens {
			continue
		}
		repeatsIt := r.with == first.file && r.c == first.q && (!r.expr || r.csub == first.qsub)
		repeatsSame := r.with == first.with && r.c == first.c && (!r.expr || r.csub == first.csub)
		if repeatsIt || repeatsSame {
			return k
		}
	}

	return nil
}

// distance ranks where the repeated code lives: 0 in the same file, 1 in the
// same directory, 2 anywhere else.
func (r *dupRun) distance() int {
	switch {
	case r.file == r.with:
		return 0
	case path.Dir(r.file.path) == path.Dir(r.with.path):
		return 1
	}

	return 2 //nolint:mnd // the third rank
}

// qLine is the line of the changed file a run starts on.
func (r *dupRun) qLine() int {
	if r.expr {
		return r.file.subs[r.qsub].line
	}

	return r.file.stmts[r.q].line
}

// cLine is the line the repeated code starts on.
func (r *dupRun) cLine() int {
	if r.expr {
		return r.with.subs[r.csub].line
	}

	return r.with.decoded()[r.c].line
}

// dupFindings renders a file's hits as findings.
func dupFindings(q *dupFile, hits []dupHit) []Finding {
	out := make([]Finding, 0, len(hits))
	for i := range hits {
		first := &hits[i].runs[0]
		line := first.qLine()
		out = append(out, Finding{
			Rule: RuleNewDuplicate, Line: line, Scope: unitAt(q.fp, line),
			Match: first.siteName(), Note: hits[i].note(),
		})
	}

	return out
}

// siteName names the site a run repeats by its file and enclosing unit. A
// finding's id is made of it, so it leaves the line out and survives line moves.
func (r *dupRun) siteName() string {
	if unit := unitAt(r.with.fp, r.cLine()); unit != "" {
		return r.with.path + " " + unit
	}

	return r.with.path
}

// siteAt names the site a run repeats for a reader: its unit, when it has
// one, and where it starts.
func (r *dupRun) siteAt() string {
	line := r.cLine()
	at := fmt.Sprintf("%s:%d", r.with.path, line)
	if unit := unitAt(r.with.fp, line); unit != "" {
		return unit + " at " + at
	}

	return at
}

// note says what a hit repeats, where that is, how much of it there is, how
// the sites differ, and what to do instead.
func (h *dupHit) note() string {
	first := &h.runs[0]
	var b strings.Builder
	b.WriteString("repeats ")
	if first.expr {
		b.WriteString("an expression of ")
	}
	b.WriteString(first.siteAt())
	if first.with.wasAdded(first.c) {
		b.WriteString(", which this change also adds")
	}
	b.WriteString([]string{", in this file", ", in the same directory", ", in another directory"}[first.distance()])
	for i := 1; i < len(h.runs) && i < maxDupSites; i++ {
		b.WriteString(", and " + h.runs[i].siteAt())
	}
	if more := len(h.runs) - maxDupSites; more > 0 {
		b.WriteString(", and " + plural(more, "more place"))
	}
	fmt.Fprintf(&b, ": %s, %s. %s", first.size(), first.difference(), first.advice())
	if len(h.again) > 0 {
		shown := h.again[:min(len(h.again), maxDupSites)]
		fmt.Fprintf(&b, ". The change adds this copy %d times, here and at %s", len(h.again)+1,
			strings.Join(shown, ", "))
		if more := len(h.again) - len(shown); more > 0 {
			b.WriteString(", and " + plural(more, "more place"))
		}
	}

	return b.String()
}

// size says how much code a run is.
func (r *dupRun) size() string {
	switch {
	case r.expr:
		return fmt.Sprintf("%d tokens", r.tokens)
	case len(r.parts) > 0:
		n := 0
		for i := range r.parts {
			n += r.parts[i].n
		}

		return fmt.Sprintf("%s in %d stretches, %d tokens", plural(n, "statement"), len(r.parts), r.tokens)
	}

	return fmt.Sprintf("%s, %d tokens", plural(r.n, "statement"), r.tokens)
}

// advice says what to do in place of the copy.
func (r *dupRun) advice() string {
	if r.expr {
		return "Give it one name that both places use instead of writing it again"
	}

	return "Extend or parameterise that code instead of adding a copy"
}

// difference lists what the new site spells differently from the first one
// it repeats.
func (r *dupRun) difference() string {
	if len(r.pairs) == 0 {
		return "identical"
	}
	shown := make([]string, 0, maxDupPairs)
	for i := 0; i < len(r.pairs) && i < maxDupPairs; i++ {
		shown = append(shown, slotText(r.pairs[i][0])+"→"+slotText(r.pairs[i][1]))
	}

	return plural(len(r.pairs), "difference") + " (" + strings.Join(shown, ", ") + ")"
}

// slotText shows a slot's text, without the hash a clipped one ends in.
func slotText(text string) string {
	if cut := strings.Index(text, slotClip); cut >= 0 {
		return text[:cut+len(slotClip)]
	}

	return text
}

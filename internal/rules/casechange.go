package rules

import (
	"fmt"
	"slices"
	"strings"
)

// RuleNewCaseSet is the id of the rule NewCaseSets reports under. Its note
// names the places that already enumerate the cases, which a caller may want
// to show.
const RuleNewCaseSet = "x/new-case-set"

// maxCaseNames caps the cases and the sites one finding's note lists.
const maxCaseNames = 3

// casePlace names one place of a file by where it starts.
type casePlace struct {
	path string
	line int
}

// NewCaseSets flags a switch, an if chain, or a table a change added that
// enumerates cases the tree already enumerated somewhere else: the new place
// joins a family CaseFamilies lists for the tree at the end of the change,
// and at least one other place of that family was there before. Rev is that
// tree. A family is reported once, on the first place the change added.
//
// A place is new when the change added its first line and the changed files
// hold more places with exactly its cases than they did before, so a place
// that moved, was re-indented, or was renamed around is not. One whose cases
// changed is an edit of the place the file held before and is not new either.
func NewCaseSets(rev map[string]*Fingerprint, changes []DupChange, experimental bool) map[string][]Finding {
	if !crossEnabled(RuleNewCaseSet, experimental) {
		return nil
	}
	added := addedCasePlaces(rev, changes)
	if len(added) == 0 {
		return nil
	}
	p := newCasePass(rev)
	out := map[string][]Finding{}
	for _, members := range p.families() {
		var fresh, held []*caseChain
		for _, m := range members {
			ch := &p.chains[m]
			if added[casePlace{path: ch.path, line: ch.rec.line}] {
				fresh = append(fresh, ch)
			} else {
				held = append(held, ch)
			}
		}
		if len(fresh) == 0 || len(held) == 0 {
			continue
		}
		at := fresh[0]
		out[at.path] = append(out[at.path], Finding{
			Rule: RuleNewCaseSet, Line: at.rec.line, Scope: unitAt(at.fp, at.rec.line),
			Match: caseSiteName(held[0]), Note: p.caseNote(members, fresh, held),
		})
	}

	return out
}

// caseSignature is a place's cases as one string.
func caseSignature(rec *caseRec) string {
	return strings.Join(rec.keys, "\x00")
}

// addedCasePlaces lists the places the change added, by path and line.
func addedCasePlaces(rev map[string]*Fingerprint, changes []DupChange) map[casePlace]bool {
	net := map[string]int{}
	for i := range changes {
		for _, side := range []struct {
			fp   *Fingerprint
			step int
		}{{changes[i].After, 1}, {changes[i].Before, -1}} {
			if side.fp == nil {
				continue
			}
			recs := side.fp.Cases.decode()
			for j := range recs {
				net[caseSignature(&recs[j])] += side.step
			}
		}
	}
	out := map[casePlace]bool{}
	for i := range changes {
		ch := &changes[i]
		if ch.After == nil || rev[ch.Path] != ch.After || ch.Added == nil || len(ch.After.Cases) == 0 ||
			caseAside(ch.Path) {
			continue
		}
		edited := editedCaseRecs(ch.Before, ch.After)
		recs := ch.After.Cases.decode()
		for j := range recs {
			rec := &recs[j]
			if ch.Added(rec.line) && net[caseSignature(rec)] > 0 && !sharesCases(rec, edited) {
				out[casePlace{path: ch.Path, line: rec.line}] = true
			}
		}
	}

	return out
}

// editedCaseRecs lists the places of a file's earlier side that its later
// side no longer holds with the same cases: the ones the change edited or
// took out.
func editedCaseRecs(before, after *Fingerprint) []caseRec {
	if before == nil {
		return nil
	}
	left := map[string]int{}
	for _, rec := range after.Cases.decode() {
		left[caseSignature(&rec)]++
	}
	var out []caseRec
	for _, rec := range before.Cases.decode() {
		if sig := caseSignature(&rec); left[sig] > 0 {
			left[sig]--
		} else {
			out = append(out, rec)
		}
	}

	return out
}

// sharesCases reports whether a place shares caseMinKeys cases with one of a
// list of places.
func sharesCases(rec *caseRec, others []caseRec) bool {
	for i := range others {
		n := 0
		for _, key := range others[i].keys {
			if _, ok := slices.BinarySearch(rec.keys, key); ok {
				n++
			}
		}
		if n >= caseMinKeys {
			return true
		}
	}

	return false
}

// caseSiteName names a place by its file and enclosing unit. A finding's id
// is made of it, so it leaves the line out and survives line moves.
func caseSiteName(ch *caseChain) string {
	if unit := unitAt(ch.fp, ch.rec.line); unit != "" {
		return ch.path + " " + unit
	}

	return ch.path
}

// caseSiteAt names a place for a reader: its unit, when it has one, and where
// it starts.
func caseSiteAt(ch *caseChain) string {
	at := fmt.Sprintf("%s:%d", ch.path, ch.rec.line)
	if unit := unitAt(ch.fp, ch.rec.line); unit != "" {
		return unit + " at " + at
	}

	return at
}

// caseNote says which of its family's cases a new place enumerates, where the
// tree already enumerated them, how many places a new case must now be added
// to, and what to do instead.
func (p *casePass) caseNote(members []int, fresh, held []*caseChain) string {
	names := slices.DeleteFunc(p.coreNames(members), func(name string) bool {
		_, named := slices.BinarySearch(fresh[0].rec.keys, name)

		return !named
	})
	shown := strings.Join(names[:min(len(names), maxCaseNames)], ", ")
	if more := len(names) - maxCaseNames; more > 0 {
		shown += fmt.Sprintf(", and %d more", more)
	}
	sites := make([]string, 0, maxCaseNames)
	for _, ch := range held[:min(len(held), maxCaseNames)] {
		sites = append(sites, caseSiteAt(ch))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "enumerates %s (%s) that %s already: %s", plural(len(names), "case"), shown,
		pluralVerb(len(held), "other place"), strings.Join(sites, ", "))
	if more := len(held) - len(sites); more > 0 {
		b.WriteString(", and " + plural(more, "more place"))
	}
	fmt.Fprintf(&b, ". A new case now needs %s. Have the places read one table or type instead of each "+
		"listing the cases, or say why they stay apart", plural(len(members), "edit"))
	if len(fresh) > 1 {
		fmt.Fprintf(&b, ". The change adds %d such places, here and at %s", len(fresh), caseSiteAt(fresh[1]))
		if more := len(fresh) - 2; more > 0 { //nolint:mnd // the first two are named
			b.WriteString(", and " + plural(more, "more place"))
		}
	}

	return b.String()
}

// pluralVerb counts a noun and gives it its verb: "1 other place enumerates",
// "3 other places enumerate".
func pluralVerb(n int, noun string) string {
	if n == 1 {
		return plural(n, noun) + " enumerates"
	}

	return plural(n, noun) + " enumerate"
}

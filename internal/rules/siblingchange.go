package rules

import (
	"fmt"
	"slices"
	"strings"
)

// RuleNewSibling is the id of the rule NewSiblings reports under. Its note
// names the siblings that were already there, which a caller may want to
// show.
const RuleNewSibling = "x/new-sibling"

// maxSiblingNames caps the members and the sites one finding's note lists.
const maxSiblingNames = 3

// sibKey names one container of a file.
type sibKey struct {
	path, kind, name string
}

func (c *sibContainer) key() sibKey {
	return sibKey{path: c.path, kind: c.kind, name: c.name}
}

// NewSiblings flags a class, a type, or a module a change added that is
// written like one the tree already held: the new one joins a set SiblingSets
// lists for the tree at the end of the change, and at least one other site of
// that set was there before. Rev is that tree. A set is reported once, on the
// first sibling the change added.
//
// A class or a type is new when the change added the line that declares it,
// and a module when the change added its file. Either must also be one more
// of its name than the changed files held before, so one that moved to
// another file is not new, and it must not be in line with a container the
// change took away under another name, which is a rename.
func NewSiblings(rev map[string]*Fingerprint, changes []DupChange, experimental bool) map[string][]Finding {
	if !crossEnabled(RuleNewSibling, experimental) {
		return nil
	}
	added := addedSiblings(rev, changes)
	if len(added) == 0 {
		return nil
	}
	p := newSibPass(rev)
	sets := p.sets()
	out := map[string][]Finding{}
	for i := range sets {
		set := &sets[i]
		var fresh, held []*sibContainer
		for _, at := range set.sites {
			if c := p.all[at]; added[c.key()] {
				fresh = append(fresh, c)
			} else {
				held = append(held, c)
			}
		}
		if len(fresh) == 0 || len(held) == 0 {
			continue
		}
		at := fresh[0]
		f := Finding{Rule: RuleNewSibling, Line: at.line, Match: sibSiteName(held[0]), Note: sibNote(set, fresh, held)}
		if at.kind != SiblingModule {
			f.Scope = at.name
		}
		out[at.path] = append(out[at.path], f)
	}

	return out
}

// addedSiblings lists the containers the change added.
func addedSiblings(rev map[string]*Fingerprint, changes []DupChange) map[sibKey]bool {
	net, gone := sibNet(changes)
	out := map[sibKey]bool{}
	for i := range changes {
		ch := &changes[i]
		if ch.After == nil || rev[ch.Path] != ch.After || ch.Added == nil || sibAside(ch.Path) {
			continue
		}
		for _, c := range sibContainers(ch.Path, ch.After) {
			written := ch.Before == nil
			if c.kind != SiblingModule {
				written = ch.Added(c.line)
			}
			if written && net[[2]string{c.kind, c.name}] > 0 && !sibRenamed(c, gone) {
				out[c.key()] = true
			}
		}
	}

	return out
}

// sibNet counts, per kind and name, the containers the changed files hold
// now less the ones they held before, and lists the containers of a name
// they hold fewer of: the ones the change took away.
func sibNet(changes []DupChange) (map[[2]string]int, []*sibContainer) {
	net := map[[2]string]int{}
	var before []*sibContainer
	for i := range changes {
		for _, c := range sibContainers(changes[i].Path, changes[i].After) {
			net[[2]string{c.kind, c.name}]++
		}
		for _, c := range sibContainers(changes[i].Path, changes[i].Before) {
			net[[2]string{c.kind, c.name}]--
			before = append(before, c)
		}
	}
	gone := slices.DeleteFunc(before, func(c *sibContainer) bool { return net[[2]string{c.kind, c.name}] >= 0 })

	return net, gone
}

// sibRenamed reports whether a container is in line with one the change
// took away: the same class under another name.
func sibRenamed(c *sibContainer, gone []*sibContainer) bool {
	for _, old := range gone {
		if old.kind != c.kind || old.lang != c.lang {
			continue
		}
		c.load(c.fp.Frags.lines())
		old.load(old.fp.Frags.lines())
		if j := sibCompare(c, old); j.joins(c, old) {
			return true
		}
	}

	return false
}

// sibSiteName names a sibling by its file and its name. A finding's id is
// made of it, so it leaves the line out and survives line moves.
func sibSiteName(c *sibContainer) string {
	if c.kind == SiblingModule {
		return c.path
	}

	return c.path + " " + c.name
}

// sibSiteAt names a sibling for a reader: its name, when it has one of its
// own, and where it starts.
func sibSiteAt(c *sibContainer) string {
	at := fmt.Sprintf("%s:%d", c.path, c.line)
	if c.kind == SiblingModule {
		return at
	}

	return c.name + " at " + at
}

// sibNote says which siblings were there, which members the new one shares
// with them, how much of those lines up, and what to do instead.
func sibNote(set *sibSet, fresh, held []*sibContainer) string {
	sites := make([]string, 0, maxSiblingNames)
	for _, c := range held[:min(len(held), maxSiblingNames)] {
		sites = append(sites, sibSiteAt(c))
	}
	names := make([]string, 0, maxSiblingNames)
	stmts, differing := 0, 0
	for i := range set.members {
		m := &set.members[i]
		if len(names) < maxSiblingNames {
			names = append(names, m.name)
		}
		stmts += len(m.common)
		differing += m.differing
	}
	what := "this " + fresh[0].kind
	if fresh[0].kind != SiblingModule {
		what = fresh[0].kind + " " + fresh[0].name
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s is written like %s", what, strings.Join(sites, ", "))
	if more := len(held) - len(sites); more > 0 {
		b.WriteString(", and " + plural(more, "more place"))
	}
	fmt.Fprintf(&b, ": %s (%s", plural(len(set.members), "member")+" of one name", strings.Join(names, ", "))
	if more := len(set.members) - len(names); more > 0 {
		fmt.Fprintf(&b, ", and %d more", more)
	}
	fmt.Fprintf(&b, ") share %s in line", plural(stmts, "statement"))
	if differing > 0 {
		fmt.Fprintf(&b, " and differ in %d", differing)
	}
	b.WriteString(". Extend what is there with a shared base, a generic, or a table of what differs, " +
		"or say why the copies stay apart")
	if len(fresh) > 1 {
		fmt.Fprintf(&b, ". The change adds %d of them, here and at %s", len(fresh), sibSiteAt(fresh[1]))
		if more := len(fresh) - 2; more > 0 { //nolint:mnd // the first two are named
			b.WriteString(", and " + plural(more, "more place"))
		}
	}

	return b.String()
}

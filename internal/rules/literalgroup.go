package rules

import (
	"cmp"
	"maps"
	"math"
	"path"
	"slices"
	"strings"

	"github.com/kyleking/slop-strata/internal/lang"
)

// LiteralGroup is one string the tree writes out in several files where a
// name would do: the files, how often they write it, how many of those places
// branch or look something up on it, and the constant that names it already.
type LiteralGroup struct {
	// Constant is the constant at file scope whose whole value is the string,
	// the first in path order. Nil when no file declares one.
	Constant *LiteralConstant `json:"constant,omitempty"`
	// Value is the string's content, without its quotes.
	Value string `json:"value"`
	// Sites are the files that write the string, in path order, the first
	// literalMaxSites of them. Files counts them all.
	Sites []LiteralSite `json:"sites"`
	// Files is how many files write the string.
	Files int `json:"files"`
	// Written is how many places write it, over all the files, the
	// declarations of constants among them.
	Written int `json:"written"`
	// Uses is how many of those places compare a value with it, name it as
	// a switch case, or use it as a key.
	Uses int `json:"uses"`
	// Rank is Files times the share of the places a slip would break: Uses
	// over Written, or 1 when a constant names the value.
	Rank float64 `json:"rank"`
}

// LiteralConstant is a constant at file scope whose whole value is a string.
type LiteralConstant struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Line int    `json:"line"`
}

// LiteralSite is one file of a group: the line of the first place it writes
// the string on, how many places write it, and how many of them are uses.
type LiteralSite struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Written int    `json:"written"`
	Uses    int    `json:"uses"`
}

// literalMinFiles is the fewest files that write a group's string.
const literalMinFiles = 3

// literalMinWritten is the fewest places that write it.
const literalMinWritten = 4

// literalMinUses is the fewest places that must branch or look something up
// on a string no constant names.
const literalMinUses = 2

// literalMinBypass is the fewest places that must write out a string a
// constant names, beside the declarations.
const literalMinBypass = 2

// literalMaxSites caps the files a group lists.
const literalMaxSites = 8

// literalRankScale is what a rank is rounded to, as a divisor.
const literalRankScale = 100

// literalAsideInfix marks a tool's configuration file, `eslint.config.ts`:
// one per package, each naming the same rules and plugins.
const literalAsideInfix = ".config."

// literalAside reports whether the literal pass leaves a file out: what the
// case pass leaves out, and configuration files.
func literalAside(file string) bool {
	return caseAside(file) || strings.Contains(path.Base(file), literalAsideInfix)
}

// literalTally is one string as the pass adds it up over the tree.
type literalTally struct {
	constant *LiteralConstant
	sites    []LiteralSite
	written  int
	uses     int
	decls    int
	typed    bool
}

// LiteralGroups lists the strings a tree writes out in literalMinFiles files
// and literalMinWritten places or more that a name would serve better, ranked
// by files times the share of places a slip would break. A string is listed
// when a constant at file scope already names it and literalMinBypass places
// write it out all the same, or when literalMinUses places compare with it,
// switch on it, or use it as a key and at least one place does none of
// those. Left out: a string some file writes as a type or as an identifier,
// a Python dunder name, a string every file of one base name carries and no
// other, and the files literalAside names.
func LiteralGroups(tree map[string]*Fingerprint) []LiteralGroup {
	return literalGroupsOf(tree, nil)
}

// literalGroupsOf lists the groups of the strings in only, or of every string
// when only is nil.
func literalGroupsOf(tree map[string]*Fingerprint, only map[string]casePlace) []LiteralGroup {
	return renderLiterals(literalTallies(tree, only), tree)
}

// literalTallies adds up each string over the files the pass reads, in path
// order, and keeps the ones that reach the floors. With only set, the strings
// outside it are passed over.
func literalTallies(tree map[string]*Fingerprint, only map[string]casePlace) map[string]*literalTally {
	tallies := map[string]*literalTally{}
	for _, name := range slices.Sorted(maps.Keys(tree)) {
		if fp := tree[name]; fp != nil && len(fp.Literals) > 0 && lang.ByPath(name) != nil {
			tallyFile(tallies, name, fp, only)
		}
	}
	maps.DeleteFunc(tallies, func(value string, t *literalTally) bool { return t.aside(value) })

	return tallies
}

// tallyFile adds one file's strings to the tallies. A file the pass leaves
// out still marks a string it writes as a type, and adds no place.
func tallyFile(tallies map[string]*literalTally, file string, fp *Fingerprint, only map[string]casePlace) {
	aside := literalAside(file)
	for value, use := range fp.Literals {
		if aside && !use.Typed {
			continue
		}
		if _, wanted := only[value]; only != nil && !wanted {
			continue
		}
		t := tallies[value]
		if t == nil {
			t = &literalTally{}
			tallies[value] = t
		}
		t.typed = t.typed || use.Typed
		if !aside && use.Count > 0 {
			t.add(file, &use)
		}
	}
}

// add counts one file's places of the string.
func (t *literalTally) add(file string, use *LiteralUse) {
	t.sites = append(t.sites, LiteralSite{Path: file, Line: use.Lines[0], Written: use.Count, Uses: use.Uses})
	t.written += use.Count
	t.uses += use.Uses
	if use.Name == "" {
		return
	}
	t.decls++
	if t.constant == nil {
		t.constant = &LiteralConstant{Name: use.Name, Path: file, Line: use.NameLine}
	}
}

// aside reports whether the pass drops a string whatever its places do: some
// file writes it as a type, it is under the floors, it is a dunder name, or
// every file that writes it has one base name.
func (t *literalTally) aside(value string) bool {
	return t.typed || len(t.sites) < literalMinFiles || t.written < literalMinWritten ||
		dunder(value) || oneBaseName(t.sites)
}

// dunder reports whether a string is a Python special name, `__main__`.
func dunder(value string) bool {
	const mark = "__"

	return strings.HasPrefix(value, mark) && strings.HasSuffix(value, mark)
}

// oneBaseName reports whether every file of a list has the same base name: a
// route, a page, or an index per directory, kept parallel on purpose.
func oneBaseName(sites []LiteralSite) bool {
	base := path.Base(sites[0].Path)
	for i := range sites[1:] {
		if path.Base(sites[i+1].Path) != base {
			return false
		}
	}

	return true
}

// renderLiterals turns the tallies that qualify into groups, highest rank
// first, then the most places, then by value.
func renderLiterals(tallies map[string]*literalTally, tree map[string]*Fingerprint) []LiteralGroup {
	names := identifiersAmong(tallies, tree)
	out := make([]LiteralGroup, 0, len(tallies))
	for value, t := range tallies {
		share := 1.0
		switch {
		case names[value]:
			continue
		case t.constant != nil && t.written-t.decls >= literalMinBypass:
		case t.uses >= literalMinUses && t.written > t.uses:
			share = float64(t.uses) / float64(t.written)
		default:
			continue
		}
		rank := math.Round(float64(len(t.sites))*share*literalRankScale) / literalRankScale
		out = append(out, LiteralGroup{
			Value: value, Constant: t.constant, Sites: t.sites[:min(len(t.sites), literalMaxSites)],
			Files: len(t.sites), Written: t.written, Uses: t.uses, Rank: rank,
		})
	}
	slices.SortFunc(out, func(a, b LiteralGroup) int {
		return cmp.Or(cmp.Compare(b.Rank, a.Rank), cmp.Compare(b.Written, a.Written), strings.Compare(a.Value, b.Value))
	})

	return out
}

// identifiersAmong lists the tallied strings that some file of the tree also
// writes as an identifier: the name of a field, a parameter, or a member,
// which code reaches by reflection and a constant would only repeat.
func identifiersAmong(tallies map[string]*literalTally, tree map[string]*Fingerprint) map[string]bool {
	var shaped []string
	for value := range tallies {
		if identifierShape(value) {
			shaped = append(shaped, value)
		}
	}
	names := map[string]bool{}
	for _, fp := range tree {
		if fp == nil || len(fp.Referenced) == 0 {
			continue
		}
		for _, value := range shaped {
			if fp.Referenced[value] > 0 {
				names[value] = true
			}
		}
	}

	return names
}

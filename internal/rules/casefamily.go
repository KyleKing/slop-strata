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

// CaseFamily is one set of cases the tree enumerates in more than one place:
// the places, the cases most of them hold, and what each place lacks of those.
type CaseFamily struct {
	// Cases are the cases at least half the sites hold, and at least two of
	// them, in the order of their text.
	Cases []string `json:"cases"`
	// Sites are the places, in path order.
	Sites []CaseSite `json:"sites"`
	// Weight is the sum over Cases of how rare each is: the logarithm of one
	// more than the tree's places per place that holds it.
	Weight float64 `json:"weight"`
	// Rank is Weight times the sites.
	Rank float64 `json:"rank"`
}

// CaseSite is one place of a family. Cases counts the cases the place names,
// whether the family's or its own, and Missing lists the family's cases it
// does not name. Default says an arm of the place takes every other case, so
// a missing case still has somewhere to go.
type CaseSite struct {
	Path    string   `json:"path"`
	Unit    string   `json:"unit"`
	Kind    string   `json:"kind"`
	Missing []string `json:"missing"`
	Line    int      `json:"line"`
	EndLine int      `json:"end_line"`
	Cases   int      `json:"cases"`
	Default bool     `json:"default"`
}

// caseMinShare is the share of the smaller place's cases two places must
// share to be joined.
const caseMinShare = 0.34

// caseMaxSpread is the most places that may hold a case for it to join two of
// them. A case held in more is vocabulary: joining on it would pair every two
// of those places.
const caseMaxSpread = 40

// caseWeightScale is what a weight is rounded to, as a divisor.
const caseWeightScale = 100

// caseMinSites is the fewest places a family has.
const caseMinSites = 2

// caseAsideDirs are the directories, beside the tests and stand-alone ones,
// whose files enumerate cases as data: a script run once, a schema migration,
// and the rows a database is seeded with.
var caseAsideDirs = map[string]bool{ //nolint:gochecknoglobals // fixed vocabulary
	"scripts": true, "script": true, "migrations": true, "migration": true, "alembic": true,
	"seed": true, "seeds": true, "seed_data": true, "seeders": true, "fixture": true,
}

// caseAside reports whether the case pass leaves a file out.
func caseAside(file string) bool {
	if dupAside(file) {
		return true
	}
	for dir := path.Dir(file); dir != "." && dir != "/"; dir = path.Dir(dir) {
		if caseAsideDirs[path.Base(dir)] {
			return true
		}
	}

	return false
}

// caseChain is one place of the tree as the pass reads it.
type caseChain struct {
	fp   *Fingerprint
	path string
	ids  []int // its cases, as numbers of the pass, rising
	rec  caseRec
}

// casePass is one run of the case pass over a tree.
type casePass struct {
	ids    map[string]int
	names  []string
	spread []int // per case, the places that hold it
	chains []caseChain
}

// CaseFamilies lists the sets of cases a tree enumerates in more than one
// place, ranked by places times the rarity of the cases they share. Two
// places are joined when they share caseMinKeys cases that at most
// caseMaxSpread places hold, and caseMinShare of the smaller one's cases.
// The place with the heaviest joins and every place joined to it are one
// family, then the next heaviest of what is left. Tests, stand-alone files,
// scripts, migrations, and seed data are left out. No place is in two
// families.
func CaseFamilies(tree map[string]*Fingerprint) []CaseFamily {
	p := newCasePass(tree)

	return p.render(p.families())
}

// newCasePass decodes the places of every file the pass reads, in path order.
func newCasePass(tree map[string]*Fingerprint) *casePass {
	p := &casePass{ids: map[string]int{}}
	for _, name := range slices.Sorted(maps.Keys(tree)) {
		fp := tree[name]
		if fp == nil || len(fp.Cases) == 0 || lang.ByPath(name) == nil || caseAside(name) {
			continue
		}
		for _, rec := range fp.Cases.decode() {
			ch := caseChain{fp: fp, path: name, rec: rec, ids: make([]int, 0, len(rec.keys))}
			for _, key := range rec.keys {
				ch.ids = append(ch.ids, p.intern(key))
			}
			slices.Sort(ch.ids)
			p.chains = append(p.chains, ch)
		}
	}

	return p
}

// intern numbers a case and counts one more place that holds it.
func (p *casePass) intern(key string) int {
	id, ok := p.ids[key]
	if !ok {
		id = len(p.names)
		p.ids[key] = id
		p.names = append(p.names, key)
		p.spread = append(p.spread, 0)
	}
	p.spread[id]++

	return id
}

// weight is how rare a case is: the logarithm of one more than the tree's
// places per place that holds it.
func (p *casePass) weight(id int) float64 {
	return math.Log(1 + float64(len(p.chains))/float64(p.spread[id]))
}

// caseEdge is one join of two places, by their numbers, with its weight.
type caseEdge struct {
	a, b   int
	weight float64
}

// holders lists, per case, the places that hold it, for the cases few enough
// places hold to join on.
func (p *casePass) holders() [][]int {
	out := make([][]int, len(p.names))
	for i := range p.chains {
		for _, id := range p.chains[i].ids {
			if p.spread[id] > 1 && p.spread[id] <= caseMaxSpread {
				out[id] = append(out[id], i)
			}
		}
	}

	return out
}

// edges lists the joins of the tree, in the order of the places. Per place,
// the later places that share a case with it are counted in one array the
// whole pass reuses, so the cost is the pairs and no map.
func (p *casePass) edges() []caseEdge {
	holders := p.holders()
	var out []caseEdge
	shared := make([]int, len(p.chains))
	var later []int
	for i := range p.chains {
		a := &p.chains[i]
		for _, id := range a.ids {
			places := holders[id]
			from, _ := slices.BinarySearch(places, i+1)
			for _, j := range places[from:] {
				if shared[j] == 0 {
					later = append(later, j)
				}
				shared[j]++
			}
		}
		slices.Sort(later)
		for _, j := range later {
			n, b := shared[j], &p.chains[j]
			shared[j] = 0
			if n >= caseMinKeys && float64(n) >= caseMinShare*float64(min(len(a.ids), len(b.ids))) && !nested(a, b) {
				out = append(out, caseEdge{a: i, b: j, weight: p.sharedWeight(a, b)})
			}
		}
		later = later[:0]
	}

	return out
}

// sharedWeight sums the weight of the cases two places share, of those few
// enough places hold to join on.
func (p *casePass) sharedWeight(a, b *caseChain) float64 {
	total := 0.0
	for _, id := range a.ids {
		if _, ok := slices.BinarySearch(b.ids, id); ok && p.spread[id] <= caseMaxSpread {
			total += p.weight(id)
		}
	}

	return total
}

// nested reports whether one place sits inside the other, as a table does in
// the arm of a switch: one edit of the outer one reaches both.
func nested(a, b *caseChain) bool {
	return a.path == b.path && a.rec.line <= b.rec.end && b.rec.line <= a.rec.end
}

// caseStar is the state of the clustering: who is joined to whom, which
// places are still free, and the weight of each one's joins to free places.
type caseStar struct {
	next  [][]caseEdge // per place, its joins, b being the other place
	pull  []float64
	free  []bool
	links []int // joins to free places
}

func newCaseStar(places int, edges []caseEdge) *caseStar {
	s := &caseStar{
		next: make([][]caseEdge, places), pull: make([]float64, places),
		free: make([]bool, places), links: make([]int, places),
	}
	for _, e := range edges {
		s.next[e.a] = append(s.next[e.a], e)
		s.next[e.b] = append(s.next[e.b], caseEdge{a: e.b, b: e.a, weight: e.weight})
		s.pull[e.a] += e.weight
		s.pull[e.b] += e.weight
		s.links[e.a]++
		s.links[e.b]++
		s.free[e.a], s.free[e.b] = true, true
	}

	return s
}

// take removes a place from the free ones.
func (s *caseStar) take(i int) {
	s.free[i] = false
	for _, e := range s.next[i] {
		s.pull[e.b] -= e.weight
		s.links[e.b]--
	}
}

// center answers the free place with the heaviest joins to free places, the
// first of them in path order, or false when no two free places are joined.
func (s *caseStar) center() (int, bool) {
	best := -1
	for i := range s.free {
		if s.free[i] && s.links[i] > 0 && (best < 0 || s.pull[i] > s.pull[best]) {
			best = i
		}
	}

	return best, best >= 0
}

// around lists a center and the free places joined to it, rising.
func (s *caseStar) around(center int) []int {
	members := []int{center}
	for _, e := range s.next[center] {
		if s.free[e.b] {
			members = append(members, e.b)
		}
	}
	slices.Sort(members)

	return members
}

// families clusters the places: the center and every free place joined to it
// become a family, less the places that hold under caseMinKeys of the cases
// the family settles on, which stay free for a later one.
func (p *casePass) families() [][]int {
	s := newCaseStar(len(p.chains), p.edges())
	var out [][]int
	for {
		center, ok := s.center()
		if !ok {
			break
		}
		members := s.around(center)
		core := p.core(members)
		kept := slices.DeleteFunc(slices.Clone(members), func(m int) bool {
			return m != center && p.holds(m, core) < caseMinKeys
		})
		s.take(center)
		if len(kept) < caseMinSites || len(p.core(kept)) < caseMinKeys {
			continue
		}
		for _, m := range kept {
			if m != center {
				s.take(m)
			}
		}
		out = append(out, kept)
	}

	return out
}

// core lists the cases at least half of the places hold, and at least two.
func (p *casePass) core(members []int) []int {
	count := map[int]int{}
	for _, m := range members {
		for _, id := range p.chains[m].ids {
			count[id]++
		}
	}
	floor := max(caseMinSites, (len(members)+1)/2) //nolint:mnd // half of them, rounded up
	var out []int
	for id, n := range count {
		if n >= floor {
			out = append(out, id)
		}
	}
	slices.Sort(out)

	return out
}

// holds counts the cases of a list that a place names.
func (p *casePass) holds(m int, core []int) int {
	n := 0
	for _, id := range core {
		if _, ok := slices.BinarySearch(p.chains[m].ids, id); ok {
			n++
		}
	}

	return n
}

// coreNames lists a family's cases by name, in the order of their text.
func (p *casePass) coreNames(members []int) []string {
	core := p.core(members)
	names := make([]string, 0, len(core))
	for _, id := range core {
		names = append(names, p.names[id])
	}
	slices.Sort(names)

	return names
}

// render turns the clusters into families, heaviest first.
func (p *casePass) render(clusters [][]int) []CaseFamily {
	out := make([]CaseFamily, 0, len(clusters))
	for _, members := range clusters {
		f := CaseFamily{Cases: p.coreNames(members), Sites: make([]CaseSite, 0, len(members))}
		weight := 0.0
		for _, name := range f.Cases {
			weight += p.weight(p.ids[name])
		}
		for _, m := range members {
			f.Sites = append(f.Sites, caseSite(&p.chains[m], f.Cases))
		}
		f.Weight = math.Round(weight*caseWeightScale) / caseWeightScale
		f.Rank = math.Round(weight*float64(len(members))*caseWeightScale) / caseWeightScale
		out = append(out, f)
	}
	slices.SortStableFunc(out, func(a, b CaseFamily) int {
		return cmp.Or(
			cmp.Compare(b.Rank, a.Rank), cmp.Compare(len(b.Sites), len(a.Sites)),
			strings.Compare(a.Sites[0].Path, b.Sites[0].Path), cmp.Compare(a.Sites[0].Line, b.Sites[0].Line),
		)
	})

	return out
}

// caseSite renders one place against its family's cases.
func caseSite(ch *caseChain, cases []string) CaseSite {
	s := CaseSite{
		Path: ch.path, Unit: unitAt(ch.fp, ch.rec.line), Kind: caseKindNames[ch.rec.kind],
		Line: ch.rec.line, EndLine: ch.rec.end, Cases: len(ch.rec.keys), Default: ch.rec.def,
		Missing: []string{},
	}
	for _, name := range cases {
		if _, ok := slices.BinarySearch(ch.rec.keys, name); !ok {
			s.Missing = append(s.Missing, name)
		}
	}

	return s
}

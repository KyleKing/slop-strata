package rules

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/kyleking/slop-strata/internal/lang"
)

// JSXGroup is one block of markup the tree holds more than once: the same
// elements with the same attribute names, nested the same way, under other
// text and other attribute values.
type JSXGroup struct {
	// Sites are the places that hold the block, in path order.
	Sites []JSXSite `json:"sites"`
	// Blocks is how many separate blocks the sites' units share. Past the
	// first, a block is counted in Elements and Holes and not listed.
	Blocks int `json:"blocks"`
	// Elements is how many elements one site is, over every block.
	Elements int `json:"elements"`
	// Holes is how many of the attribute values, runs of text, and wrapping
	// expressions are not the same at every site: what shared components
	// would take as props.
	Holes int `json:"holes"`
	// Rank is Elements times one less than the sites, less Holes.
	Rank int `json:"rank"`
}

// JSXSite is one place a group's block sits: the largest block, for a group
// of several.
type JSXSite struct {
	Path    string `json:"path"`
	Unit    string `json:"unit"`
	Line    int    `json:"line"`
	EndLine int    `json:"end_line"`
}

// jsxMinSites is the fewest sites a group has.
const jsxMinSites = 2

// jsxPlace is one record of the tree as the pass reads it.
type jsxPlace struct {
	file *jsxTreeFile
	rec  int
}

// jsxTreeFile is one file of the pass.
type jsxTreeFile struct {
	fp   *Fingerprint
	path string
	jsxFile
}

// jsxBlock is one block of markup and the places that hold it, in path order.
type jsxBlock struct {
	places []jsxPlace
	elems  int
	holes  int
}

// jsxCluster is one group being gathered: the blocks the same units share,
// the one that ranks highest first, with their elements and holes summed.
type jsxCluster struct {
	blocks []jsxBlock
	elems  int
	holes  int
}

// sites answers how many sites the group has: those of each of its blocks.
func (c *jsxCluster) sites() int {
	return len(c.blocks[0].places)
}

func (c *jsxCluster) rank() int {
	return c.elems*(c.sites()-1) - c.holes
}

// jsxPass is one run of the JSX pass over a tree.
type jsxPass struct {
	byShape map[uint64][]jsxPlace
	size    map[uint64]int // per shape that passes as a group, its places
	order   []uint64       // the shapes, in the order the tree first holds them
}

// JSXGroups lists the blocks of markup a tree holds more than once, ranked by
// the elements one shared component would take out. A block is an element
// with jsxMinElems elements or more, itself counted. Two blocks are one group
// when they have one shape (see JSXShapes), and a group is kept when its
// sites differ in at most one hole per element. A block inside a block of a
// group with as many sites is not listed again, and the blocks that repeat
// across the same units are one group. Tests, stories, and the stand-alone
// files the duplicate pass leaves out are not read.
func JSXGroups(tree map[string]*Fingerprint) []JSXGroup {
	clusters := newJSXPass(tree).clusters()
	out := make([]JSXGroup, 0, len(clusters))
	for i := range clusters {
		c := &clusters[i]
		places := c.blocks[0].places
		g := JSXGroup{
			Elements: c.elems, Holes: c.holes, Rank: c.rank(), Blocks: len(c.blocks),
			Sites: make([]JSXSite, 0, len(places)),
		}
		for _, p := range places {
			r := p.record()
			g.Sites = append(g.Sites, JSXSite{
				Path: p.file.path, Unit: unitAt(p.file.fp, r.line), Line: r.line, EndLine: r.end,
			})
		}
		out = append(out, g)
	}

	return out
}

func (p jsxPlace) record() *jsxRec {
	return &p.file.recs[p.rec]
}

// jsxAside reports whether the JSX pass leaves a file out.
func jsxAside(file string) bool {
	return dupAside(file)
}

// newJSXPass decodes the markup of every file the pass reads, in path order,
// and settles which shapes pass as a group.
func newJSXPass(tree map[string]*Fingerprint) *jsxPass {
	p := &jsxPass{byShape: map[uint64][]jsxPlace{}, size: map[uint64]int{}}
	for _, name := range slices.Sorted(maps.Keys(tree)) {
		fp := tree[name]
		if fp == nil || len(fp.JSX) == 0 || lang.ByPath(name) == nil || jsxAside(name) {
			continue
		}
		f := &jsxTreeFile{fp: fp, path: name, jsxFile: fp.JSX.decode()}
		for i := range f.recs {
			h := f.recs[i].hash
			if _, seen := p.byShape[h]; !seen {
				p.order = append(p.order, h)
			}
			p.byShape[h] = append(p.byShape[h], jsxPlace{file: f, rec: i})
		}
	}
	for _, h := range p.order {
		places := alike(p.byShape[h])
		p.byShape[h] = places
		if len(places) >= jsxMinSites && jsxPasses(places) {
			p.size[h] = len(places)
		}
	}

	return p
}

// alike drops the places that do not count the elements and holes the first
// one does: two shapes can share a hash.
func alike(places []jsxPlace) []jsxPlace {
	first := places[0].record()

	return slices.DeleteFunc(places, func(p jsxPlace) bool {
		r := p.record()

		return r.elems != first.elems || r.nholes != first.nholes
	})
}

// differing counts the holes that are not filled alike at every place.
func differing(places []jsxPlace) int {
	first := places[0]
	n := 0
	for i := range first.record().nholes {
		v := first.file.holeAt(first.record(), i)
		for _, p := range places[1:] {
			if p.file.holeAt(p.record(), i) != v {
				n++

				break
			}
		}
	}

	return n
}

// jsxPasses reports whether places of one shape differ in few enough holes
// to be one component: at most one per element. Past that the places share a
// skeleton a library dictates and little of what they say.
func jsxPasses(places []jsxPlace) bool {
	return differing(places) <= places[0].record().elems
}

// nestedIn reports whether a place sits inside a block of a group with at
// least as many sites as its own, which lists it already.
func (p *jsxPass) nestedIn(place jsxPlace, sites int) bool {
	recs := place.file.recs
	for at := recs[place.rec].parent; at >= 0; at = recs[at].parent {
		if p.size[recs[at].hash] >= sites {
			return true
		}
	}

	return false
}

// blocks lists the repeated blocks of the tree, in the order the tree first
// holds them.
func (p *jsxPass) blocks() []jsxBlock {
	var out []jsxBlock
	for _, h := range p.order {
		sites := p.size[h]
		if sites == 0 {
			continue
		}
		var kept []jsxPlace
		for _, place := range p.byShape[h] {
			if !p.nestedIn(place, sites) {
				kept = append(kept, place)
			}
		}
		if len(kept) < jsxMinSites {
			continue
		}
		out = append(out, jsxBlock{places: kept, elems: kept[0].record().elems, holes: differing(kept)})
	}

	return out
}

// unitsOf names the units a block's places sit in, in order, as one string:
// two blocks with one answer repeat across the same units.
func unitsOf(b *jsxBlock) string {
	var key strings.Builder
	for _, p := range b.places {
		key.WriteString(p.file.path)
		key.WriteByte(0)
		key.WriteString(unitAt(p.file.fp, p.record().line))
		key.WriteByte(0)
	}

	return key.String()
}

// clusters lists the groups of the tree, highest rank first. The blocks that
// repeat across the same units, site for site, are one group: two forms that
// share five fields are one thing to merge, not five.
func (p *jsxPass) clusters() []jsxCluster {
	blocks := p.blocks()
	slices.SortStableFunc(blocks, func(a, b jsxBlock) int {
		return cmp.Compare(b.elems*(len(b.places)-1)-b.holes, a.elems*(len(a.places)-1)-a.holes)
	})
	var out []jsxCluster
	at := map[string]int{}
	for i := range blocks {
		b := &blocks[i]
		key := unitsOf(b)
		to, seen := at[key]
		if !seen {
			to = len(out)
			at[key] = to
			out = append(out, jsxCluster{})
		}
		c := &out[to]
		c.blocks = append(c.blocks, *b)
		c.elems += b.elems
		c.holes += b.holes
	}
	slices.SortStableFunc(out, func(a, b jsxCluster) int {
		x, y := a.blocks[0].places[0], b.blocks[0].places[0]

		return cmp.Or(
			cmp.Compare(b.rank(), a.rank()), cmp.Compare(b.sites(), a.sites()),
			strings.Compare(x.file.path, y.file.path), cmp.Compare(x.record().line, y.record().line),
		)
	})

	return out
}

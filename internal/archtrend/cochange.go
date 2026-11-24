package archtrend

import (
	"cmp"
	"math"
	"path"
	"slices"
	"strings"

	"github.com/kyleking/slop-strata/internal/depgraph"
	"github.com/kyleking/slop-strata/internal/lang"
)

const (
	// MaxCommitFiles is the size past which a commit is left out of co-change
	// counting: code-maat's cut for a reformat, a merge, or a mass rename.
	MaxCommitFiles = 30
	// MinCoChanges is the support floor: the commits that must have changed
	// both files of a pair.
	MinCoChanges = 4
	// MinSupport is the floor on a pair's support, which weighs each commit
	// by how few other files it changed.
	MinSupport = 1.5
	// MinConfidence is the floor on co-changes over the changes of the file
	// that changed less.
	MinConfidence = 0.5
)

// CoChange records which files change in the same commit. A file keeps its
// record through a rename.
type CoChange struct {
	ids     map[string]int
	commits [][]int
	changes []int
}

// NewCoChange answers an empty record.
func NewCoChange() *CoChange {
	return &CoChange{ids: map[string]int{}}
}

// Rename carries a file's record to its new path.
func (c *CoChange) Rename(from, to string) {
	id, ok := c.ids[from]
	if !ok {
		return
	}
	delete(c.ids, from)
	c.ids[to] = id
}

// Commit records one commit: files are the ones it changed that the caller
// wants paired, and touched is every file it changed. A commit that touched
// more than MaxCommitFiles is not recorded.
func (c *CoChange) Commit(files []string, touched int) {
	if touched > MaxCommitFiles {
		return
	}
	ids := make([]int, 0, len(files))
	for _, f := range files {
		id, ok := c.ids[f]
		if !ok {
			id = len(c.changes)
			c.ids[f] = id
			c.changes = append(c.changes, 0)
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	for _, id := range ids {
		c.changes[id]++
	}
	if len(ids) >= pairSize {
		c.commits = append(c.commits, ids)
	}
}

// shareScale rounds a support and a confidence to four decimals.
const shareScale = 1e4

// pairSize is the fewest files a commit must change to pair any.
const pairSize = 2

// Pair is two files that change together with no import between them.
type Pair struct {
	A string `json:"a"`
	B string `json:"b"`
	// CoChanges is the commits that changed both and no file both import,
	// and ChangesA and ChangesB the commits that changed each.
	CoChanges int `json:"co_changes"`
	ChangesA  int `json:"changes_a"`
	ChangesB  int `json:"changes_b"`
	// Support is the sum, over the CoChanges commits, of one over the other
	// files the commit paired: a commit of just the two counts 1, and a
	// sweep over twenty-one files counts a twentieth.
	Support float64 `json:"support"`
	// Confidence is CoChanges over the smaller of ChangesA and ChangesB.
	Confidence float64 `json:"confidence"`
	// SharedImporter is set when some file imports both.
	SharedImporter bool `json:"shared_importer"`
}

// Scope is what Hidden reads of the tree the pairs are judged against.
type Scope struct {
	// Graph is the tree's resolved import graph.
	Graph *depgraph.Graph
	// Live reports whether a file is a production source file of the tree.
	Live func(file string) bool
	// Package names the package a file belongs to, such as the directory of
	// the nearest package.json. Two files of different packages can import
	// each other by package name, which adds no edge to Graph.
	Package func(file string) string
}

// tally is what the commits that changed both files of a pair add up to.
type tally struct {
	commits int
	support float64
}

// Hidden lists the pairs of live files that changed together with no chain of
// imports from either to the other, over edges of every kind. A commit counts
// for a pair unless it also changed a file both import, directly or through
// other files: that import explains the change. A pair is listed with at
// least MinCoChanges such commits, a support of MinSupport, and a confidence
// of MinConfidence. It is left out when the graph cannot vouch for the
// missing import: two Go files of one directory are one package and need
// none, and two TypeScript files of different packages may import each other
// by package name. The best supported pairs come first, and limit caps the
// list.
func (c *CoChange) Hidden(scope Scope, limit int) []Pair {
	names := make([]string, len(c.changes))
	for file, id := range c.ids {
		names[id] = file
	}
	links := newLinks(scope.Graph)
	unlinked := func(ids [2]int) bool {
		a, b := names[ids[0]], names[ids[1]]

		return scope.Live(a) && scope.Live(b) && !samePackage(a, b) && !unseenImport(scope, a, b) &&
			!links.reach(a)[b] && !links.reach(b)[a]
	}
	candidates := map[[2]int]*tally{}
	for ids, t := range c.count(nil) {
		if c.passes(ids, t) && unlinked(ids) {
			candidates[ids] = &tally{}
		}
	}
	explained := func(ids [2]int, commit []int) bool {
		a, b := links.reach(names[ids[0]]), links.reach(names[ids[1]])

		return slices.ContainsFunc(commit, func(id int) bool { return a[names[id]] && b[names[id]] })
	}
	out := []Pair{}
	for ids, t := range c.count(func(ids [2]int, commit []int) bool {
		return candidates[ids] != nil && !explained(ids, commit)
	}) {
		if !c.passes(ids, t) {
			continue
		}
		p := Pair{
			A: names[ids[0]], B: names[ids[1]], CoChanges: t.commits, Support: t.support,
			ChangesA: c.changes[ids[0]], ChangesB: c.changes[ids[1]],
		}
		if p.A > p.B {
			p.A, p.B, p.ChangesA, p.ChangesB = p.B, p.A, p.ChangesB, p.ChangesA
		}
		p.Support = math.Round(p.Support*shareScale) / shareScale
		p.Confidence = math.Round(float64(t.commits)*shareScale/float64(min(p.ChangesA, p.ChangesB))) / shareScale
		p.SharedImporter = links.sharedImporter(p.A, p.B)
		out = append(out, p)
	}
	slices.SortFunc(out, func(x, y Pair) int {
		return cmp.Or(cmp.Compare(y.Support, x.Support), cmp.Compare(y.CoChanges, x.CoChanges),
			strings.Compare(x.A, y.A), strings.Compare(x.B, y.B))
	})

	return out[:min(len(out), limit)]
}

// count tallies each pair of files over the commits that changed both. With
// keep set, only the commits it accepts for a pair count.
func (c *CoChange) count(keep func(ids [2]int, commit []int) bool) map[[2]int]*tally {
	pairs := map[[2]int]*tally{}
	for _, commit := range c.commits {
		for i, a := range commit {
			for _, b := range commit[i+1:] {
				ids := [2]int{a, b}
				if keep != nil && !keep(ids, commit) {
					continue
				}
				t := pairs[ids]
				if t == nil {
					t = &tally{}
					pairs[ids] = t
				}
				t.commits++
				t.support += 1 / float64(len(commit)-1)
			}
		}
	}

	return pairs
}

// passes reports whether a pair's tally clears the three floors.
func (c *CoChange) passes(ids [2]int, t *tally) bool {
	fewer := min(c.changes[ids[0]], c.changes[ids[1]])

	return t.commits >= MinCoChanges && t.support >= MinSupport &&
		float64(t.commits) >= MinConfidence*float64(fewer)
}

// samePackage reports whether two files are Go files of one directory.
func samePackage(a, b string) bool {
	return strings.HasSuffix(a, ".go") && strings.HasSuffix(b, ".go") && path.Dir(a) == path.Dir(b)
}

// unseenImport reports whether an import between two files could exist that
// the graph does not resolve: both are TypeScript, in different packages.
func unseenImport(scope Scope, a, b string) bool {
	return workspaceLanguage(a) && workspaceLanguage(b) && scope.Package(a) != scope.Package(b)
}

// workspaceLanguage reports whether a file's language imports a sibling
// package of the repository by a name the graph does not resolve.
func workspaceLanguage(file string) bool {
	l := lang.ByPath(file)

	return l != nil && l != lang.Go && l != lang.Python
}

// links answers who imports whom in a graph.
type links struct {
	edge      map[[2]string]bool
	importers map[string][]string
	imports   map[string][]string
	reached   map[string]map[string]bool
}

func newLinks(g *depgraph.Graph) *links {
	l := &links{
		edge: map[[2]string]bool{}, importers: map[string][]string{}, imports: map[string][]string{},
		reached: map[string]map[string]bool{},
	}
	for _, e := range g.Edges() {
		if !l.edge[[2]string{e.From, e.To}] {
			l.importers[e.To] = append(l.importers[e.To], e.From)
			l.imports[e.From] = append(l.imports[e.From], e.To)
		}
		l.edge[[2]string{e.From, e.To}] = true
	}

	return l
}

// reach answers the files a chain of imports leads to from one file.
func (l *links) reach(from string) map[string]bool {
	if seen, ok := l.reached[from]; ok {
		return seen
	}
	seen := map[string]bool{}
	queue := []string{from}
	for len(queue) > 0 {
		file := queue[0]
		queue = queue[1:]
		for _, next := range l.imports[file] {
			if !seen[next] && next != from {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	l.reached[from] = seen

	return seen
}

func (l *links) sharedImporter(a, b string) bool {
	for _, from := range l.importers[a] {
		if l.edge[[2]string{from, b}] {
			return true
		}
	}

	return false
}

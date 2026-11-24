package depgraph

import (
	"cmp"
	"slices"
	"strings"
)

// minCycle is the fewest files a cycle can hold: a file importing itself is
// not one.
const minCycle = 2

// Cycle is one strongly connected component of two or more files: every
// member reaches every other through the graph's edges.
type Cycle struct {
	// Members are the component's files, sorted.
	Members []string
	// Path is the shortest cycle through Cut, starting at the file Cut
	// imports and ending at the file that holds the import. Each file imports
	// the next, and the last imports the first.
	Path []string
	// Cut is the cheapest edge inside the component: the one importing the
	// fewest names.
	Cut Edge
}

// SCCs answers the strongly connected components of two or more files, each
// sorted, ordered by first member. It is Tarjan's algorithm.
func (g *Graph) SCCs() [][]string {
	t := &tarjan{g: g, index: map[string]int{}, low: map[string]int{}, on: map[string]bool{}}
	for _, n := range g.nodes {
		if _, seen := t.index[n]; !seen {
			t.visit(n)
		}
	}
	slices.SortFunc(t.found, func(a, b []string) int { return strings.Compare(a[0], b[0]) })

	return t.found
}

type tarjan struct {
	g     *Graph
	index map[string]int
	low   map[string]int
	on    map[string]bool
	stack []string
	found [][]string
	next  int
}

func (t *tarjan) visit(v string) {
	t.index[v], t.low[v] = t.next, t.next
	t.next++
	t.stack = append(t.stack, v)
	t.on[v] = true
	for _, i := range t.g.out[v] {
		w := t.g.edges[i].To
		if _, seen := t.index[w]; !seen {
			t.visit(w)
			t.low[v] = min(t.low[v], t.low[w])
		} else if t.on[w] {
			t.low[v] = min(t.low[v], t.index[w])
		}
	}
	if t.low[v] != t.index[v] {
		return
	}
	var scc []string
	for {
		w := t.stack[len(t.stack)-1]
		t.stack = t.stack[:len(t.stack)-1]
		t.on[w] = false
		scc = append(scc, w)
		if w == v {
			break
		}
	}
	if len(scc) >= minCycle {
		slices.Sort(scc)
		t.found = append(t.found, scc)
	}
}

// ShortestCycle answers the shortest cycle that uses the edge from one file to
// another: the files from to through from, each importing the next and the
// last importing the first. It is nil when to cannot reach from.
func (g *Graph) ShortestCycle(from, to string) []string {
	prev := map[string]string{to: ""}
	queue := []string{to}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		if v == from {
			var path []string
			for n := from; n != ""; n = prev[n] {
				path = append(path, n)
			}
			slices.Reverse(path)

			return path
		}
		for _, i := range g.out[v] {
			w := g.edges[i].To
			if _, seen := prev[w]; !seen {
				prev[w] = v
				queue = append(queue, w)
			}
		}
	}

	return nil
}

// Cycles answers each strongly connected component with its cheapest edge and
// the shortest cycle through that edge. Ties on cost go to the edge that
// sorts first by importer, then imported file.
func (g *Graph) Cycles() []Cycle {
	sccs := g.SCCs()
	cycles := make([]Cycle, 0, len(sccs))
	for _, members := range sccs {
		in := map[string]bool{}
		for _, m := range members {
			in[m] = true
		}
		inner := g.Filter(func(e Edge) bool { return in[e.From] && in[e.To] })
		cut := slices.MinFunc(inner.edges, func(a, b Edge) int {
			return cmp.Or(cmp.Compare(a.Cost(), b.Cost()), compareEdges(a, b))
		})
		cycles = append(cycles, Cycle{Members: members, Cut: cut, Path: inner.ShortestCycle(cut.From, cut.To)})
	}

	return cycles
}

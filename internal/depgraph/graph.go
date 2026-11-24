// Package depgraph holds the import graph of a tree: files as nodes, resolved
// imports as edges, with strongly connected components, the shortest cycle
// through an edge, and a roll-up of files into directories. Nothing here
// parses: Build resolves the import specifiers a caller already collected.
package depgraph

import (
	"cmp"
	"slices"
	"strings"
)

// Kind says when an import runs, which decides whether it can close a cycle.
type Kind string

// Edge kinds. Only Runtime edges run when a module loads, so only they close
// an import cycle. The others are the usual ways to avoid one.
const (
	// Runtime is a module-level import that runs when the importer loads.
	Runtime Kind = "runtime"
	// TypeOnly is an import a type checker reads and the runtime never does:
	// TypeScript's import type, Python's TYPE_CHECKING block, or any import of
	// a stub or declaration file.
	TypeOnly Kind = "type"
	// Deferred is an import inside a function body, or a dynamic import().
	Deferred Kind = "deferred"
	// MainGuard is a Python import under if __name__ == "__main__".
	MainGuard Kind = "main"
)

// Edge is one file importing another. Names are the imported names, sorted,
// and Line is where the importer first writes the import.
type Edge struct {
	From  string   `json:"from"`
	To    string   `json:"to"`
	Kind  Kind     `json:"kind"`
	Names []string `json:"names,omitempty"`
	Line  int      `json:"line"`
}

// Cost is how many names would have to move to cut the edge. An import that
// names nothing, such as a side-effect import, still costs one.
func (e Edge) Cost() int {
	return max(1, len(e.Names))
}

// Graph is a set of files and the import edges between them.
type Graph struct {
	out   map[string][]int
	nodes []string
	edges []Edge
	uses  []Use
}

// New builds a graph. Edges between the same two files with the same kind
// merge into one, keeping the first line and the union of names. Self edges
// are dropped. Nodes and edges come out sorted, so every answer is stable.
func New(nodes []string, edges []Edge) *Graph {
	g := &Graph{out: map[string][]int{}}
	seen := map[string]bool{}
	for _, n := range nodes {
		seen[n] = true
	}
	merged := map[[3]string]*Edge{}
	for _, e := range edges {
		if e.From == e.To {
			continue
		}
		seen[e.From], seen[e.To] = true, true
		key := [3]string{e.From, e.To, string(e.Kind)}
		have, ok := merged[key]
		if !ok {
			e.Names = slices.Clone(e.Names)
			merged[key] = &e

			continue
		}
		have.Names = append(have.Names, e.Names...)
		if e.Line < have.Line {
			have.Line = e.Line
		}
	}
	for n := range seen {
		g.nodes = append(g.nodes, n)
	}
	slices.Sort(g.nodes)
	for _, e := range merged {
		slices.Sort(e.Names)
		e.Names = slices.Compact(e.Names)
		g.edges = append(g.edges, *e)
	}
	slices.SortFunc(g.edges, compareEdges)
	for i, e := range g.edges {
		g.out[e.From] = append(g.out[e.From], i)
	}

	return g
}

func compareEdges(a, b Edge) int {
	return cmp.Or(strings.Compare(a.From, b.From), strings.Compare(a.To, b.To), cmp.Compare(a.Kind, b.Kind))
}

// Nodes lists the files, sorted. The caller must not modify it.
func (g *Graph) Nodes() []string { return g.nodes }

// Edges lists the edges by importer, imported file, then kind. The caller must
// not modify it.
func (g *Graph) Edges() []Edge { return g.edges }

// Filter answers the graph of the edges keep accepts, over the same files.
func (g *Graph) Filter(keep func(Edge) bool) *Graph {
	var edges []Edge
	for _, e := range g.edges {
		if keep(e) {
			edges = append(edges, e)
		}
	}

	return New(g.nodes, edges)
}

// Fan counts the distinct groups on each side of one group.
type Fan struct {
	// In is how many other groups import this one.
	In int `json:"fan_in"`
	// Out is how many other groups this one imports.
	Out int `json:"fan_out"`
}

// Fans rolls files up into groups, such as directories, and counts for each
// group the distinct other groups it imports and is imported by. Edges inside
// one group count for nothing.
func (g *Graph) Fans(group func(file string) string) map[string]Fan {
	in := map[string]map[string]bool{}
	out := map[string]map[string]bool{}
	add := func(m map[string]map[string]bool, key, other string) {
		if m[key] == nil {
			m[key] = map[string]bool{}
		}
		m[key][other] = true
	}
	for _, e := range g.edges {
		from, to := group(e.From), group(e.To)
		if from == to {
			continue
		}
		add(out, from, to)
		add(in, to, from)
	}
	fans := map[string]Fan{}
	for _, n := range g.nodes {
		d := group(n)
		fans[d] = Fan{In: len(in[d]), Out: len(out[d])}
	}

	return fans
}

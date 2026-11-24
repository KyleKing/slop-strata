package rules

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/kyleking/slop-strata/internal/depgraph"
	"github.com/kyleking/slop-strata/internal/lang"
)

// ruleXImportCycle flags files that import each other at module level.
const ruleXImportCycle = "x/import-cycle"

// Imports is a tree's resolved import graph with what the cycle rule reads
// beside it.
type Imports struct {
	// Graph holds every resolved edge of every kind, test files included.
	Graph   *depgraph.Graph
	barrels map[string]bool
	blobs   []BlobPrint
}

// ResolveImports builds the import graph of a tree from its fingerprints.
// TreePaths is every path in the tree.
func ResolveImports(blobs []BlobPrint, treePaths []string) *Imports {
	im := &Imports{barrels: map[string]bool{}, blobs: blobs}
	files := make([]depgraph.File, 0, len(blobs))
	for _, b := range blobs {
		fp := b.Print
		if len(fp.ImportRefs) == 0 && fp.GoModule == "" && lang.ByPath(b.Path) == nil {
			continue
		}
		files = append(files, graphFile(b.Path, fp))
		if fp.Barrel {
			im.barrels[b.Path] = true
		}
	}
	im.Graph = depgraph.Build(files, treePaths)

	return im
}

// graphFile hands the graph what it reads of one fingerprint.
func graphFile(file string, fp *Fingerprint) depgraph.File {
	f := depgraph.File{
		Path: file, GoModule: fp.GoModule, Exported: fp.Exported, Referenced: fp.Referenced,
		Selected: fp.Selected, ExportAs: fp.ExportAs, Default: fp.Default,
	}
	for _, ref := range fp.ImportRefs {
		f.Imports = append(f.Imports, depgraph.Import{
			Spec: ref.Spec, Kind: edgeKind(ref.Kind), Names: ref.Names, Line: ref.Line,
			Local: ref.Local, Aliases: ref.Aliases,
		})
	}
	if len(fp.Units) > 0 {
		f.Declared = make(map[string]bool, len(fp.Units))
	}
	for i := range fp.Units {
		if u := &fp.Units[i]; u.Name != "" && !strings.Contains(u.Name, ".") {
			f.Declared[u.Name] = u.Public
		}
	}
	if len(fp.Passed) > 0 {
		f.Passed = make(map[string]bool, len(fp.Passed))
	}
	for _, name := range fp.Passed {
		f.Passed[name] = true
	}

	return f
}

// edgeKind maps a stored import kind to the graph's.
func edgeKind(stored string) depgraph.Kind {
	switch stored {
	case importType:
		return depgraph.TypeOnly
	case importDeferred:
		return depgraph.Deferred
	case importMain:
		return depgraph.MainGuard
	default:
		return depgraph.Runtime
	}
}

// Cycles answers the import cycles the rule reports: strongly connected
// components over module-level runtime edges between non-test files. An
// import a type checker alone reads, one deferred into a function body, and
// one under a main guard never close a cycle, and a cycle that needs a test
// file to close is left out.
func (im *Imports) Cycles() []depgraph.Cycle {
	return im.Graph.Filter(func(e depgraph.Edge) bool {
		return e.Kind == depgraph.Runtime && !lang.IsTestPath(e.From) && !lang.IsTestPath(e.To)
	}).Cycles()
}

// CycleID names a cycle as its x/import-cycle finding is named, and answers
// the files the name is hashed over: the members, less the barrels when two
// or more other files remain.
func (im *Imports) CycleID(members []string) (string, []string) {
	key := im.keyMembers(members)

	return SetID(ruleXImportCycle, key), key
}

// CycleFindings renders each cycle as one finding on the import line of its
// cheapest edge, keyed by the cycle's members.
func (im *Imports) CycleFindings() map[string][]Finding {
	out := map[string][]Finding{}
	cycles := im.Cycles()
	for i := range cycles {
		c := &cycles[i]
		prefix := commonDir(c.Members)
		chain := cycleChain(c.Path, prefix)
		note := fmt.Sprintf("%s; cheapest cut: %s; %d files import each other",
			chain, cutText(&c.Cut, prefix), len(c.Members))
		if prefix != "" {
			note += " in " + prefix
		}
		out[c.Cut.From] = append(out[c.Cut.From], Finding{
			Rule: ruleXImportCycle, Line: c.Cut.Line, Match: chain, Note: note, Members: im.keyMembers(c.Members),
		})
	}

	return out
}

// keyMembers answers the members a cycle is identified by: all of them, less
// the barrels when two or more other files remain. Routing an import through
// a file that declares nothing changes no dependency, so it must not make the
// cycle a new finding.
func (im *Imports) keyMembers(members []string) []string {
	var declaring []string
	for _, m := range members {
		if !im.barrels[m] {
			declaring = append(declaring, m)
		}
	}
	if len(declaring) < 2 { //nolint:mnd // a cycle needs two files
		return slices.Clone(members)
	}

	return declaring
}

// commonDir is the directory every path sits under, or "".
func commonDir(paths []string) string {
	dir := path.Dir(paths[0])
	for dir != "." {
		under := true
		for _, p := range paths {
			under = under && strings.HasPrefix(p, dir+"/")
		}
		if under {
			return dir
		}
		dir = path.Dir(dir)
	}

	return ""
}

// maxChain is how many files of a cycle the note spells out.
const maxChain = 5

// cycleChain renders a cycle as "a -> b -> a", each file importing the next.
// A long cycle keeps its first files and its last, with the count between.
func cycleChain(cycle []string, prefix string) string {
	names := make([]string, 0, len(cycle)+1)
	for _, p := range cycle {
		names = append(names, trimDir(p, prefix))
	}
	if len(names) > maxChain {
		hidden := len(names) - maxChain + 1
		names = append(names[:maxChain-2], fmt.Sprintf("(%d more)", hidden), names[len(names)-1])
	}
	names = append(names, names[0])

	return strings.Join(names, " -> ")
}

func trimDir(p, prefix string) string {
	if prefix == "" {
		return p
	}

	return strings.TrimPrefix(p, prefix+"/")
}

// maxCutNames is how many imported names the note lists.
const maxCutNames = 3

// cutText describes the cheapest edge: who imports how many names from whom.
func cutText(cut *depgraph.Edge, prefix string) string {
	from, to := trimDir(cut.From, prefix), trimDir(cut.To, prefix)
	names := cut.Names
	if len(names) == 0 || slices.Contains(names, importWildcard) {
		return fmt.Sprintf("%s imports %s as a whole", from, to)
	}
	listed := strings.Join(names[:min(len(names), maxCutNames)], ", ")
	if len(names) > maxCutNames {
		listed += ", …"
	}
	noun := "names"
	if len(names) == 1 {
		noun = "name"
	}

	return fmt.Sprintf("%s imports %d %s (%s) from %s", from, len(names), noun, listed, to)
}

// NewImportCycles answers the cycles rev has that base does not cover: a
// cycle is new when no cycle of base holds every one of its members, so a new
// cycle and a grown one are reported and an unchanged or shrunk one is not.
// It answers nothing unless the rule is enabled.
func NewImportCycles(base, rev map[string]*Fingerprint, experimental bool) map[string][]Finding {
	if !crossEnabled(ruleXImportCycle, experimental) {
		return nil
	}
	var before [][]string
	for _, fs := range cyclesOf(base) {
		for i := range fs {
			before = append(before, fs[i].Members)
		}
	}
	out := map[string][]Finding{}
	for p, fs := range cyclesOf(rev) {
		for i := range fs {
			members := fs[i].Members
			if !slices.ContainsFunc(before, func(old []string) bool { return subset(members, old) }) {
				out[p] = append(out[p], fs[i])
			}
		}
	}

	return out
}

func cyclesOf(prints map[string]*Fingerprint) map[string][]Finding {
	blobs := make([]BlobPrint, 0, len(prints))
	paths := make([]string, 0, len(prints))
	for p, fp := range prints {
		blobs = append(blobs, BlobPrint{Path: p, Print: fp})
		paths = append(paths, p)
	}

	return ResolveImports(blobs, paths).CycleFindings()
}

// subset reports whether every member of small is in big. Both are sorted.
func subset(small, big []string) bool {
	for _, m := range small {
		if _, ok := slices.BinarySearch(big, m); !ok {
			return false
		}
	}

	return true
}

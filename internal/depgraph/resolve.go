package depgraph

import (
	"path"
	"slices"
	"strings"
)

// Import is one import as a file writes it, before it is resolved to a file.
// Spec is the module specifier: a dotted Python module with its leading dots,
// a TypeScript module string, or a Go import path. Names are the names it
// binds, "*" for a wildcard, and none for an import of the module itself.
// Local is the name a bound module goes by in the importer, and Aliases maps
// each renamed binding to the name it imports.
type Import struct {
	Aliases map[string]string
	Spec    string
	Kind    Kind
	Local   string
	Names   []string
	Line    int
}

// File is what Build reads of one file. Exported and Referenced are the names
// the file offers and uses, which price an import that binds a whole module.
// GoModule is the module path a go.mod declares, and empty for any other file.
// The rest feed Uses: Declared is every top-level name the file declares, true
// for the ones its language makes visible to other files, Selected is the
// names read through each imported module, Passed is the imported names the
// file binds and never uses, ExportAs maps an exported alias to a local name,
// and Default is the name a default export gives.
type File struct {
	Exported   map[string]int
	Referenced map[string]int
	Declared   map[string]bool
	Selected   map[string][]string
	Passed     map[string]bool
	ExportAs   map[string]string
	Path       string
	GoModule   string
	Default    string
	Imports    []Import
}

// wildcard is the name of an import that binds everything a module offers.
const wildcard = "*"

// resolver holds the indexes the per-language resolvers share.
type resolver struct {
	files    map[string]*File
	paths    map[string]bool
	pyRoots  map[string][]pyModule
	goDirs   map[string][]string
	goMods   []goModule
	resolved []Edge
	binds    []binding
}

// Build resolves every file's imports to files of the same tree and answers
// the graph. TreePaths is every path in the tree, which decides whether a
// directory is a Python package. An import that names a file outside the
// tree, or more than one candidate with nothing to choose between them, adds
// no edge.
func Build(files []File, treePaths []string) *Graph {
	r := &resolver{files: map[string]*File{}, paths: map[string]bool{}}
	for _, p := range treePaths {
		r.paths[p] = true
	}
	var nodes []string
	for i := range files {
		f := &files[i]
		r.paths[f.Path] = true
		if f.GoModule != "" {
			r.goMods = append(r.goMods, goModule{path: f.GoModule, dir: path.Dir(f.Path)})
		}
		if languageOf(f.Path) == "" {
			continue
		}
		r.files[f.Path] = f
		nodes = append(nodes, f.Path)
	}
	slices.Sort(nodes)
	r.indexPython(nodes)
	r.indexGo(nodes)
	for _, n := range nodes {
		r.resolveFile(r.files[n])
	}

	g := New(nodes, r.resolved)
	g.uses = r.uses()

	return g
}

// Languages Build resolves imports for.
const (
	langPython     = "python"
	langTypeScript = "typescript"
	langGo         = "go"
)

// Extensions more than one table names.
const (
	extTSX = ".tsx"
	extJSX = ".jsx"
)

// tsExts are the TypeScript-family extensions a file can be parsed under.
var tsExts = []string{".ts", extTSX, ".mts", ".cts", extJSX} //nolint:gochecknoglobals // a fixed vocabulary

// languageOf names the resolver a path takes, or "" for a file with none.
func languageOf(p string) string {
	switch ext := path.Ext(p); {
	case ext == ".py" || ext == ".pyi":
		return langPython
	case ext == ".go":
		return langGo
	case slices.Contains(tsExts, ext):
		return langTypeScript
	default:
		return ""
	}
}

// declarationOnly reports whether a file is a stub or declaration file, which
// a type checker reads and nothing runs.
func declarationOnly(p string) bool {
	return strings.HasSuffix(p, ".pyi") || strings.HasSuffix(p, ".d.ts") ||
		strings.HasSuffix(p, ".d.mts") || strings.HasSuffix(p, ".d.cts")
}

func (r *resolver) resolveFile(f *File) {
	lang := languageOf(f.Path)
	for _, imp := range f.Imports {
		if declarationOnly(f.Path) {
			imp.Kind = TypeOnly
		}
		switch lang {
		case langPython:
			r.resolvePython(f, imp)
		case langTypeScript:
			if to := r.resolveTS(f.Path, imp.Spec); to != "" {
				r.add(f, to, imp, imp.Names)
				r.bind(f, to, &imp, imp.Names)
			}
		case langGo:
			r.resolveGo(f, imp)
		}
	}
}

// add records an edge. An import that binds a whole module is priced by the
// names of the target the importer goes on to use.
func (r *resolver) add(from *File, to string, imp Import, names []string) {
	if declarationOnly(to) {
		imp.Kind = TypeOnly
	}
	if len(names) == 0 || slices.Contains(names, wildcard) {
		if used := usedNames(from, r.files[to]); len(used) > 0 {
			names = used
		}
	}
	r.resolved = append(r.resolved, Edge{From: from.Path, To: to, Kind: imp.Kind, Names: names, Line: imp.Line})
}

// usedNames lists the names a target offers that the importer uses, sorted.
func usedNames(from, to *File) []string {
	if to == nil {
		return nil
	}
	var used []string
	for name := range to.Exported {
		if from.Referenced[name] > 0 {
			used = append(used, name)
		}
	}
	slices.Sort(used)

	return used
}

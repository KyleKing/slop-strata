package depgraph

import (
	"path"
	"strings"
)

// goModule is one go.mod: the module path it declares and its directory.
type goModule struct {
	path string
	dir  string
}

// indexGo lists the importable Go files of each directory. A _test.go file
// belongs to its package's tests, which nothing imports.
func (r *resolver) indexGo(nodes []string) {
	r.goDirs = map[string][]string{}
	for _, file := range nodes {
		if languageOf(file) == langGo && !strings.HasSuffix(file, "_test.go") {
			r.goDirs[path.Dir(file)] = append(r.goDirs[path.Dir(file)], file)
		}
	}
}

// resolveGo adds the edges of one import. Go imports a package, which is a
// directory, so the edge goes to each file of it whose exported names the
// importer uses, and to every file when none can be told apart.
func (r *resolver) resolveGo(f *File, imp Import) {
	dir, ok := r.goDir(imp.Spec)
	if !ok {
		return
	}
	targets := r.goDirs[dir]
	matched := false
	for _, to := range targets {
		r.bind(f, to, &imp, nil)
	}
	for _, to := range targets {
		if used := usedNames(f, r.files[to]); len(used) > 0 {
			r.add(f, to, imp, used)
			matched = true
		}
	}
	if matched {
		return
	}
	for _, to := range targets {
		r.resolved = append(r.resolved, Edge{From: f.Path, To: to, Kind: imp.Kind, Line: imp.Line})
	}
}

// goDir answers the directory an import path names, through the module with
// the longest path that prefixes it, so a nested module wins over its parent.
func (r *resolver) goDir(spec string) (string, bool) {
	best, found := goModule{}, false
	for _, m := range r.goMods {
		if spec != m.path && !strings.HasPrefix(spec, m.path+"/") {
			continue
		}
		if !found || len(m.path) > len(best.path) {
			best, found = m, true
		}
	}
	if !found {
		return "", false
	}

	return path.Join(best.dir, strings.TrimPrefix(strings.TrimPrefix(spec, best.path), "/")), true
}

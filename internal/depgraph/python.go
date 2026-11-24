package depgraph

import (
	"path"
	"strings"
)

// pyModule is one file a dotted module path can name, with the directory the
// path is counted from.
type pyModule struct {
	root string
	file string
}

const pyInit = "__init__"

// indexPython maps every dotted suffix of each module's path to the file, so
// an absolute import resolves under any source root: src/pkg/mod.py answers to
// both src.pkg.mod and pkg.mod. A suffix whose root directory is itself a
// package is left out, because a package directory is never on the import
// path of its own modules.
func (r *resolver) indexPython(nodes []string) {
	r.pyRoots = map[string][]pyModule{}
	for _, file := range nodes {
		if languageOf(file) != langPython {
			continue
		}
		parts := strings.Split(strings.TrimSuffix(file, path.Ext(file)), "/")
		if parts[len(parts)-1] == pyInit {
			parts = parts[:len(parts)-1]
		}
		for i := range parts {
			root := strings.Join(parts[:i], "/")
			if r.isPackage(root) {
				continue
			}
			key := strings.Join(parts[i:], ".")
			r.pyRoots[key] = append(r.pyRoots[key], pyModule{root: root, file: file})
		}
	}
}

// isPackage reports whether a directory holds an __init__ file.
func (r *resolver) isPackage(dir string) bool {
	return dir != "" && (r.paths[dir+"/"+pyInit+".py"] || r.paths[dir+"/"+pyInit+".pyi"])
}

// resolvePython adds the edges of one import statement. A name imported from
// a package is a submodule when a file answers to it, and otherwise a name the
// package's own file defines.
func (r *resolver) resolvePython(f *File, imp Import) {
	if len(imp.Names) == 0 {
		if to := r.pyLookup(f.Path, imp.Spec); to != "" {
			r.add(f, to, imp, nil)
			r.bind(f, to, &imp, nil)
		}

		return
	}
	module := r.pyLookup(f.Path, imp.Spec)
	var fromModule []string
	for _, name := range imp.Names {
		sub := ""
		if name != wildcard {
			sub = r.pyLookup(f.Path, joinDotted(imp.Spec, name))
		}
		switch {
		case sub != "":
			r.add(f, sub, imp, nil)
			module := imp
			module.Local = localName(imp.Aliases, name)
			r.bind(f, sub, &module, nil)
		case module != "":
			fromModule = append(fromModule, name)
		}
	}
	if len(fromModule) > 0 {
		r.add(f, module, imp, fromModule)
		r.bind(f, module, &imp, fromModule)
	}
}

// joinDotted appends a name to a module specifier, which may be all dots.
func joinDotted(spec, name string) string {
	if spec == "" || strings.HasSuffix(spec, ".") {
		return spec + name
	}

	return spec + "." + name
}

// pyLookup answers the file a module specifier names from the importer, or ""
// when the tree has none or has several with nothing to choose between them.
func (r *resolver) pyLookup(importer, spec string) string {
	dots := len(spec) - len(strings.TrimLeft(spec, "."))
	if dots > 0 {
		return r.pyRelative(importer, dots, spec[dots:])
	}
	best, bestRoot := "", -1
	var elsewhere []string
	for _, m := range r.pyRoots[spec] {
		switch {
		case m.root == "" || strings.HasPrefix(importer, m.root+"/"):
			// The nearest root above the importer is the one on its import path.
			if len(m.root) > bestRoot {
				best, bestRoot = m.file, len(m.root)
			}
		case r.topIsPackage(m.root, spec):
			elsewhere = append(elsewhere, m.file)
		}
	}
	if best == "" && len(elsewhere) == 1 {
		best = elsewhere[0]
	}

	return best
}

// topIsPackage reports whether a specifier's first segment is a package under
// root. A root the importer does not sit under is trusted only for packages:
// a lone tools/json.py must not capture every import of the standard json.
func (r *resolver) topIsPackage(root, spec string) bool {
	top, _, _ := strings.Cut(spec, ".")

	return r.isPackage(path.Join(root, top))
}

// pyRelative resolves a relative import: one dot is the importer's own
// package, and each further dot is one directory up.
func (r *resolver) pyRelative(importer string, dots int, rest string) string {
	dir := path.Dir(importer)
	for range dots - 1 {
		if dir == "." {
			return ""
		}
		dir = path.Dir(dir)
	}
	base := dir
	if rest != "" {
		base = path.Join(dir, strings.ReplaceAll(rest, ".", "/"))
	}
	if base == "." {
		return ""
	}
	for _, suffix := range []string{".py", "/" + pyInit + ".py", ".pyi", "/" + pyInit + ".pyi"} {
		candidate := base + suffix
		if r.files[candidate] != nil {
			return candidate
		}
	}

	return ""
}

package depgraph

import (
	"cmp"
	"slices"
	"strings"
)

// Use is one file using a name another file declares. To is the declaring
// file, which an import through a re-exporting file is followed to.
type Use struct {
	From string
	To   string
	Name string
}

// Uses lists the names each file uses of each other file, sorted. An import
// counts only when the importer goes on to use what it binds, so a re-export
// is a use by whoever consumes it and by nobody when nothing does. Only a
// graph Build made has any. The caller must not modify the list.
func (g *Graph) Uses() []Use { return g.uses }

// binding is one resolved import: the importer, the file it reached, and the
// names it asked for, none when it bound the module itself.
type binding struct {
	from  *File
	to    string
	local string
	alias map[string]string
	names []string
}

// bind records a resolved import for Uses.
func (r *resolver) bind(from *File, to string, imp *Import, names []string) {
	r.binds = append(r.binds, binding{from: from, to: to, local: imp.Local, alias: imp.Aliases, names: names})
}

// source is where a file gets a name it does not declare: one name of
// another file, or nothing when the binding is a module.
type source struct {
	file string
	name string
}

// declIndex answers which file declares a name another file offers, following
// re-exports.
type declIndex struct {
	files map[string]*File
	bound map[string]map[string]source
	stars map[string][]string
}

func (r *resolver) declIndex() *declIndex {
	idx := &declIndex{files: r.files, bound: map[string]map[string]source{}, stars: map[string][]string{}}
	for i := range r.binds {
		b := &r.binds[i]
		from := b.from.Path
		if idx.bound[from] == nil {
			idx.bound[from] = map[string]source{}
		}
		for _, name := range b.names {
			if name == wildcard {
				if b.local == "" {
					idx.stars[from] = append(idx.stars[from], b.to)
				}

				continue
			}
			idx.bound[from][localName(b.alias, name)] = source{file: b.to, name: name}
		}
	}

	return idx
}

// maxHops bounds how many re-exports one name is followed through.
const maxHops = 8

// declared answers the file that declares the name a file offers, and the
// name it declares it by.
func (idx *declIndex) declared(file, name string, hops int) (source, bool) {
	f := idx.files[file]
	if f == nil || hops > maxHops {
		return source{}, false
	}
	if _, ok := f.Declared[name]; ok {
		return source{file: file, name: name}, true
	}
	if name == defaultName && f.Default != "" {
		return idx.declared(file, f.Default, hops+1)
	}
	if local, ok := f.ExportAs[name]; ok {
		return idx.declared(file, local, hops+1)
	}
	if src, ok := idx.bound[file][name]; ok {
		return idx.declared(src.file, src.name, hops+1)
	}
	for _, star := range idx.stars[file] {
		if src, ok := idx.declared(star, name, hops+1); ok {
			return src, true
		}
	}

	return source{}, false
}

// defaultName is the name a TypeScript default export is imported by.
const defaultName = "default"

// uses resolves every binding to the declarations the importer uses.
func (r *resolver) uses() []Use {
	idx := r.declIndex()
	seen := map[Use]bool{}
	var out []Use
	for i := range r.binds {
		b := &r.binds[i]
		for _, name := range r.usedOf(b) {
			src, ok := idx.declared(b.to, name, 0)
			use := Use{From: b.from.Path, To: src.file, Name: src.name}
			if !ok || use.From == use.To || seen[use] {
				continue
			}
			seen[use] = true
			out = append(out, use)
		}
	}
	slices.SortFunc(out, func(a, b Use) int {
		return cmp.Or(strings.Compare(a.From, b.From), strings.Compare(a.To, b.To), strings.Compare(a.Name, b.Name))
	})

	return out
}

// usedOf lists the names of its target a binding's importer uses. A named
// import is used unless the importer only binds it, and a bound module is
// used through the names read off it.
func (r *resolver) usedOf(b *binding) []string {
	var used []string
	whole := len(b.names) == 0
	for _, name := range b.names {
		if name == wildcard {
			whole = true

			continue
		}
		if !b.from.Passed[localName(b.alias, name)] {
			used = append(used, name)
		}
	}
	if whole {
		used = append(used, r.usedOfModule(b)...)
	}

	return used
}

// usedOfModule lists the names an importer uses of a module it binds whole:
// the ones it reads off the module's local name. Where those cannot be told,
// which is a wildcard import that binds no namespace, a Go dot import, and a
// Go package whose name is not its directory's, every visible name of the
// target the importer mentions anywhere counts.
func (r *resolver) usedOfModule(b *binding) []string {
	if b.local == "_" {
		return nil
	}
	if selected, ok := b.from.Selected[b.local]; ok {
		return selected
	}
	target := r.files[b.to]
	unnamedWildcard := b.local == "" && len(b.names) > 0
	if target == nil || (!unnamedWildcard && languageOf(b.from.Path) != langGo) {
		return nil
	}
	var used []string
	for name, visible := range target.Declared {
		if visible && b.from.Referenced[name] > 0 {
			used = append(used, name)
		}
	}

	return used
}

// localName answers the name an imported name goes by in the importer.
func localName(aliases map[string]string, name string) string {
	for local, original := range aliases {
		if original == name {
			return local
		}
	}

	return name
}

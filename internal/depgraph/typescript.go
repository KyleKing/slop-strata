package depgraph

import (
	"path"
	"strings"
)

// jsToTS maps the JavaScript extension an import may write to the TypeScript
// sources that compile to it.
var jsToTS = map[string][]string{ //nolint:gochecknoglobals // a fixed vocabulary
	".js":  {".ts", extTSX},
	extJSX: {extTSX, extJSX},
	".mjs": {".mts"},
	".cjs": {".cts"},
}

// resolveTS answers the file a relative specifier names, or "". A bare
// specifier names a package, which is outside the graph, and a declaration
// file declares types and runs nothing, so neither resolves.
func (r *resolver) resolveTS(importer, spec string) string {
	if spec != "." && spec != ".." && !strings.HasPrefix(spec, "./") && !strings.HasPrefix(spec, "../") {
		return ""
	}
	for _, c := range tsCandidates(path.Join(path.Dir(importer), spec)) {
		if r.files[c] != nil && !declarationOnly(c) {
			return c
		}
	}

	return ""
}

// tsCandidates lists the files a module path can name, in the order a
// TypeScript resolver tries them: the source a JavaScript extension compiles
// from, the path as written, the path with each extension, then its index.
func tsCandidates(base string) []string {
	var candidates []string
	if swaps, ok := jsToTS[path.Ext(base)]; ok {
		stem := strings.TrimSuffix(base, path.Ext(base))
		for _, ext := range swaps {
			candidates = append(candidates, stem+ext)
		}
	}
	candidates = append(candidates, base)
	for _, ext := range tsExts {
		candidates = append(candidates, base+ext)
	}
	for _, ext := range tsExts {
		candidates = append(candidates, path.Join(base, "index"+ext))
	}

	return candidates
}

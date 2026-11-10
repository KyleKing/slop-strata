package rules

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/kyleking/slop-strata/internal/config"
	"github.com/kyleking/slop-strata/internal/lang"
)

// BlobPrint pairs one blob's fingerprint with where it came from.
type BlobPrint struct {
	Print *Fingerprint
	Path  string
}

// CrossFile runs the passes that join fingerprints across the tree, returning
// findings per path. These never re-parse; they only read what Collect stored.
// TreePaths is every path in the tree, so prose file references resolve even
// for files no pass fingerprints. Passes whose rules are experimental run only
// when experimental is set.
func CrossFile(
	blobs []BlobPrint, treePaths, entryPoints, configPaths []string, experimental bool,
) map[string][]Finding {
	out := map[string][]Finding{}
	if experimental {
		dupLiterals(blobs, out)
		deadExports(blobs, out, entryPoints)
		staleProse(blobs, out, treePaths)
	}
	clones(blobs, out, experimental)
	singleImplIfaces(blobs, out)
	unusedOptions(blobs, out)
	staleConfig(blobs, out, configPaths)

	return out
}

// staleProse flags a code-shaped name a comment, docstring, or prose file
// mentions that no file declares, references, or imports: the rename the
// prose missed.
func staleProse(blobs []BlobPrint, out map[string][]Finding, treePaths []string) {
	names := proseIndex(blobs)
	files := map[string]bool{}
	for _, p := range treePaths {
		files[filepath.Base(p)] = true
	}
	for _, b := range blobs {
		if lang.IsTestPath(b.Path) || proseExempt(b.Path) {
			continue
		}
		for tok, line := range b.Print.ProseRefs {
			if resolved, isFile := resolvesToken(names, files, tok); !resolved {
				note := "names nothing in the tree"
				if isFile {
					note = "names no file in the tree"
				}
				out[b.Path] = append(out[b.Path], Finding{
					Rule: "x/stale-prose", Line: line, Match: tok, Note: note,
				})
			}
		}
	}
}

// proseExempt reports whether a file records past states, where a name that no
// longer resolves is the content rather than rot.
func proseExempt(path string) bool {
	base := strings.ToUpper(filepath.Base(path))
	for _, prefix := range []string{"CHANGELOG", "HISTORY", "MIGRATION"} {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}
	dirs := strings.Split(filepath.ToSlash(filepath.Dir(path)), "/")
	if strings.HasPrefix(path, "docs/adr") || strings.HasSuffix(dirs[0], "-research") {
		return true
	}

	return slices.Contains(dirs, "adr")
}

// proseIndex gathers what a prose token can name: declared, referenced, and
// imported names.
func proseIndex(blobs []BlobPrint) map[string]bool {
	names := map[string]bool{}
	for _, b := range blobs {
		addNames(names, b.Print)
	}

	return names
}

func addNames(names map[string]bool, fp *Fingerprint) {
	for n := range fp.Declared {
		names[n] = true
	}
	for n := range fp.Referenced {
		names[n] = true
	}
	for n := range fp.Imports {
		names[n] = true
	}
}

// RemovedNameProse flags unresolved prose mentions of names base has and rev lacks.
func RemovedNameProse(base, rev map[string]*Fingerprint, touched map[string]bool) map[string][]Finding {
	present := map[string]bool{}
	for _, fp := range rev {
		addNames(present, fp)
	}
	gone := map[string]bool{}
	for _, fp := range base {
		addNames(gone, fp)
	}
	for n := range gone {
		if present[n] {
			delete(gone, n)
		}
	}
	if len(gone) == 0 {
		return nil
	}

	out := map[string][]Finding{}
	for path, fp := range rev {
		if !proseScanned(path, touched) {
			continue
		}
		for tok, line := range fp.ProseRefs {
			if resolved, isFile := resolvesToken(present, nil, tok); resolved || isFile || !namedBy(gone, tok) {
				continue
			}
			out[path] = append(out[path], Finding{
				Rule: "x/stale-prose", Line: line, Match: tok, Note: "name removed by this change",
			})
		}
	}
	for _, fs := range out {
		slices.SortFunc(fs, func(a, b Finding) int {
			return cmp.Or(cmp.Compare(a.Line, b.Line), strings.Compare(a.Match, b.Match))
		})
	}

	return out
}

func proseScanned(path string, touched map[string]bool) bool {
	if lang.IsTestPath(path) || proseExempt(path) {
		return false
	}

	return touched[path] || lang.TextKind(path) == lang.TextProse
}

func namedBy(gone map[string]bool, tok string) bool {
	if gone[tok] {
		return true
	}
	i := strings.LastIndexByte(tok, '.')

	return i >= 0 && gone[tok[i+1:]]
}

// fileExts are the suffixes that make a dotted token a file name rather than
// a code path.
var fileExts = map[string]bool{ //nolint:gochecknoglobals // fixed vocabulary
	"md": true, "markdown": true, "mdx": true, "rst": true, "txt": true,
	"go": true, "py": true, "ts": true, "tsx": true, "js": true, "jsx": true,
	"json": true, "jsonl": true, "yaml": true, "yml": true, "toml": true,
	"pkl": true, "conf": true, "tape": true, "lock": true, "sum": true,
	"mod": true, "csv": true, "sql": true, "sh": true, "html": true,
	"css": true, "png": true, "svg": true,
}

// resolvesToken answers whether the token names something real, and whether
// it read as a file reference.
func resolvesToken(names, files map[string]bool, tok string) (bool, bool) {
	if names[tok] {
		return true, false
	}
	i := strings.LastIndexByte(tok, '.')
	if i < 0 {
		return false, false
	}
	if allSingleLetters(tok) || hasDigitSegment(tok) {
		return true, false
	}
	leaf := tok[i+1:]
	switch {
	case fileExts[leaf]:
		return files[tok], true
	case looksLikeDomain(leaf):
		return true, false
	default:
		return names[leaf] || names[tok[:strings.IndexByte(tok, '.')]], false
	}
}

// looksLikeDomain reports whether a dotted token's last part has the shape of a
// top-level domain or a plugin name: two to six lowercase letters.
func looksLikeDomain(leaf string) bool {
	if len(leaf) < 2 || len(leaf) > 6 {
		return false
	}
	for _, r := range leaf {
		if r < 'a' || r > 'z' {
			return false
		}
	}

	return true
}

// allSingleLetters reports whether a dotted token is an abbreviation like
// e.g or i.e rather than a path.
func allSingleLetters(tok string) bool {
	for _, part := range strings.Split(tok, ".") {
		if len(part) != 1 {
			return false
		}
	}

	return true
}

// hasDigitSegment reports whether a dotted token carries an all-digit
// segment, the shape of a version like v0.3.1 rather than a name.
func hasDigitSegment(tok string) bool {
	for _, part := range strings.Split(tok, ".") {
		digits := part != ""
		for _, r := range part {
			if r < '0' || r > '9' {
				digits = false

				break
			}
		}
		if digits {
			return true
		}
	}

	return false
}

// staleConfig flags a key in a config_paths file that no code reads by name:
// the config outlived the code that consumed it. Key names normalize case and
// punctuation, so cfg["author_class"], a toml tag, and a field read resolve
// the same.
func staleConfig(blobs []BlobPrint, out map[string][]Finding, configPaths []string) {
	if len(configPaths) == 0 {
		return
	}
	read := map[string]bool{}
	for _, b := range blobs {
		for n := range b.Print.Referenced {
			read[normKey(n)] = true
		}
		for s := range b.Print.Strings {
			read[normKey(s)] = true
		}
	}
	for _, b := range blobs {
		if len(b.Print.ConfigKeys) == 0 || filepath.Base(b.Path) == config.FileName ||
			lang.IsTestPath(b.Path) || !matchAny(configPaths, b.Path) {
			continue
		}
		for key, line := range b.Print.ConfigKeys {
			leaf := key[strings.LastIndexByte(key, '.')+1:]
			if !read[normKey(leaf)] {
				out[b.Path] = append(out[b.Path], Finding{
					Rule: "x/stale-config", Line: line, Match: key,
					Note: "no code reads this key",
				})
			}
		}
	}
}

// normKey folds a config key and a code reference to one spelling, so
// author_class, AuthorClass, and author-class compare equal.
func normKey(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, "_", "")
	name = strings.ReplaceAll(name, "-", "")

	return name
}

// dupLiterals flags a literal carried by minDupFiles or more files and
// appearing minDupOccurrences or more times across the tree.
func dupLiterals(blobs []BlobPrint, out map[string][]Finding) {
	byLiteral := map[string][]BlobPrint{}
	occurrences := map[string]int{}
	for _, b := range blobs {
		for lit, use := range b.Print.Literals {
			byLiteral[lit] = append(byLiteral[lit], b)
			occurrences[lit] += use.Count
		}
	}
	for lit, owners := range byLiteral {
		if len(owners) < minDupFiles || occurrences[lit] < minDupOccurrences {
			continue
		}
		short := lit
		if len(short) > 60 { //nolint:mnd // a display cap, not a rule
			short = short[:60] + "…"
		}
		for _, b := range owners {
			out[b.Path] = append(out[b.Path], Finding{
				Rule: "x/dup-literal", Line: b.Print.Literals[lit].Line,
				Match: short,
				Note:  fmt.Sprintf("shared by %d files", len(owners)),
			})
		}
	}
}

// deadExports flags an exported name no other file references, split by
// whether the declaring file uses it: a name used only in its own file is
// could-be-private. Names a package manifest names as entry points count as
// referenced. Go methods are never reported, because a method can satisfy an
// interface declared outside the tree. Entry-point globs and test files are
// exempt, since their exports answer a caller outside the tree.
func deadExports(blobs []BlobPrint, out map[string][]Finding, entryPoints []string) {
	refCount := referenceIndex(blobs)
	entryNames, entryGlobs := manifestEntries(blobs, entryPoints)
	for _, b := range blobs {
		if lang.IsTestPath(b.Path) || matchAny(entryGlobs, b.Path) {
			continue
		}
		for name, line := range b.Print.Exported {
			if entryNames[name] {
				continue
			}
			elsewhere := refCount[name]
			if b.Print.Referenced[name] > 0 {
				elsewhere--
			}
			if elsewhere > 0 {
				continue
			}
			finding := Finding{
				Rule: "x/dead-export", Line: line, Match: name,
				Note: "exported, referenced nowhere else",
			}
			if selfUses(b.Print, name) > 0 {
				finding = Finding{
					Rule: "x/could-be-private", Line: line, Match: name,
					Note: "exported, used only inside its own file",
				}
			}
			out[b.Path] = append(out[b.Path], finding)
		}
	}
}

// referenceIndex counts the files referencing each identifier.
func referenceIndex(blobs []BlobPrint) map[string]int {
	refCount := map[string]int{}
	for _, b := range blobs {
		for name := range b.Print.Referenced {
			refCount[name]++
		}
	}

	return refCount
}

// manifestEntries splits what package manifests name as entry points into
// "module:name" targets, which mark a name exported, and file paths, which mark
// a file as an entry module alongside the configured globs.
func manifestEntries(blobs []BlobPrint, entryPoints []string) (map[string]bool, []string) {
	names := map[string]bool{}
	globs := slices.Clone(entryPoints)
	for _, b := range blobs {
		for _, ref := range b.Print.EntryRefs {
			if i := strings.LastIndexByte(ref, ':'); i >= 0 {
				names[ref[i+1:]] = true
			} else {
				globs = append(globs, strings.TrimPrefix(ref, "./"))
			}
		}
	}

	return names, globs
}

// selfUses counts a file's uses of a name it declares, discounting the
// declaration itself. A name the file only re-exports has no declaration here,
// so it has no own uses.
func selfUses(fp *Fingerprint, name string) int {
	if _, declared := fp.Declared[name]; !declared {
		return 0
	}

	return fp.Referenced[name] - 1
}

// matchAny reports whether path matches any glob in the list.
func matchAny(globs []string, path string) bool {
	for _, g := range globs {
		if ok, err := filepath.Match(g, path); err == nil && ok {
			return true
		}
	}

	return false
}

// minCloneFiles is how many files must share a function body hash before the
// clone is reported.
const minCloneFiles = 2

// cloneOwner ties a clone unit to the file it was found in.
type cloneOwner struct {
	path string
	unit CloneUnit
}

// clones flags normalized-identical function bodies living in different files
// when experimental is set; same-file test duplicates are always reported.
func clones(blobs []BlobPrint, out map[string][]Finding, experimental bool) {
	byHash := map[string][]cloneOwner{}
	for _, b := range blobs {
		for _, u := range b.Print.Clones {
			byHash[u.Hash] = append(byHash[u.Hash], cloneOwner{b.Path, u})
		}
	}
	for hash, owners := range byHash {
		files := map[string]bool{}
		for _, o := range owners {
			files[o.path] = true
		}
		if len(files) < minCloneFiles {
			if experimental {
				dupTestCases(hash, owners, out)
			}

			continue
		}
		if !experimental {
			continue
		}
		for _, o := range owners {
			out[o.path] = append(out[o.path], Finding{
				Rule: "x/clone-function", Line: o.unit.Line,
				Match: o.unit.Name,
				Note:  fmt.Sprintf("same normalized body in %d files (%s)", len(files), hash[:8]),
			})
		}
	}
}

// dupTestCases flags same-file test cases whose bodies match once identifiers
// and literals are normalized, so cases that differ only in fixture data read
// as duplicates. Helpers and benchmark closures are not cases.
func dupTestCases(hash string, owners []cloneOwner, out map[string][]Finding) {
	var cases []cloneOwner
	for _, o := range owners {
		if o.unit.Test && lang.IsTestPath(o.path) {
			cases = append(cases, o)
		}
	}
	if len(cases) <= 1 {
		return
	}
	for _, o := range cases {
		out[o.path] = append(out[o.path], Finding{
			Rule: "x/dup-test-case", Line: o.unit.Line,
			Match: o.unit.Name,
			Note: fmt.Sprintf("same normalized body as %d tests in this file (%s)",
				len(cases), hash[:8]),
		})
	}
}

// singleImplIfaces flags an interface with exactly one required method
// implemented by exactly one type: a seam for a variation that does not exist.
func singleImplIfaces(blobs []BlobPrint, out map[string][]Finding) {
	implsByMethod, implementsByName := implIndexes(blobs)
	for _, b := range blobs {
		for _, iface := range b.Print.Ifaces {
			if len(iface.Methods) != 1 {
				continue
			}
			implementors := len(implsByMethod[iface.Methods[0]]) + implementsByName[iface.Name]
			if implementors == 1 {
				out[b.Path] = append(out[b.Path], Finding{
					Rule: "x/single-impl-iface", Line: iface.Line,
					Match: iface.Name, Note: "exactly one implementation in the tree",
				})
			}
		}
	}
}

// implIndexes builds the implementor counts singleImplIfaces joins on: Go
// method sets per receiver type, TS implements clauses per interface name.
func implIndexes(blobs []BlobPrint) (map[string]map[string]int, map[string]int) {
	implsByMethod := map[string]map[string]int{} // method -> implementing type count (go)
	implementsByName := map[string]int{}         // interface -> implementor count (ts)
	for _, b := range blobs {
		for typ, methods := range b.Print.MethodImpls {
			for _, m := range methods {
				if implsByMethod[m] == nil {
					implsByMethod[m] = map[string]int{}
				}
				implsByMethod[m][typ]++
			}
		}
		for name, lines := range b.Print.Implements {
			implementsByName[name] += len(lines)
		}
	}

	return implsByMethod, implementsByName
}

// unusedOptions flags a WithX Option helper no file references: an option no
// caller passes.
func unusedOptions(blobs []BlobPrint, out map[string][]Finding) {
	referenced := map[string]bool{}
	for _, b := range blobs {
		for name := range b.Print.Referenced {
			referenced[name] = true
		}
	}
	for _, b := range blobs {
		for name, line := range b.Print.Options {
			// The declaration site references its own name, so absence of a
			// second file referencing it means no caller exists.
			uses := 0
			for _, other := range blobs {
				if other.Print.Referenced[name] > 0 {
					uses++
				}
			}
			if uses <= 1 {
				out[b.Path] = append(out[b.Path], Finding{
					Rule: "x/unused-option", Line: line,
					Match: name, Note: "declared, never passed",
				})
			}
		}
	}
}

// Baseline is the set of findings a repository already accepted, keyed so a
// suppressed hit stays suppressed until the code around it moves.
type Baseline map[string]bool

// baselineKey identifies one finding for the baseline file.
func baselineKey(path, rule string, line int, match string) string {
	return strings.Join([]string{path, rule, strconv.Itoa(line), match}, "\x00")
}

// LoadBaseline reads a baseline file: a JSON list of [path, rule, line, match]
// tuples written by the tool's own baseline-write path.
func LoadBaseline(entries []BaselineEntry) Baseline {
	b := Baseline{}
	for _, e := range entries {
		b[baselineKey(e.Path, e.Rule, e.Line, e.Match)] = true
	}

	return b
}

// BaselineEntry is one recorded accepted finding.
type BaselineEntry struct {
	Path  string `json:"path"`
	Rule  string `json:"rule"`
	Match string `json:"match"`
	Line  int    `json:"line"`
}

// Entries renders a live finding set into baseline entries.
func Entries(files map[string][]Finding) []BaselineEntry {
	var out []BaselineEntry
	for path, fs := range files {
		for _, f := range fs {
			out = append(out, BaselineEntry{Path: path, Rule: f.Rule, Line: f.Line, Match: f.Match})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}

		return out[i].Line < out[j].Line
	})

	return out
}

// Suppress drops findings the baseline already holds.
func (b Baseline) Suppress(path string, fs []Finding) []Finding {
	if len(b) == 0 {
		return fs
	}
	var out []Finding
	for _, f := range fs {
		if !b[baselineKey(path, f.Rule, f.Line, f.Match)] {
			out = append(out, f)
		}
	}

	return out
}

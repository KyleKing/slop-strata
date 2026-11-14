package rules

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/kyleking/slop-strata/internal/lang"
)

// maxStringLen caps the string contents tracked for config-key reads; longer
// strings are prose or payloads, never key names.
const maxStringLen = 64

// CollectText builds a fingerprint for a file with no grammar: prose files
// contribute the code-shaped names they mention, config files the keys they
// define. There is no parse, so nothing else is collected.
func CollectText(path string, content []byte) *Fingerprint {
	fp := &Fingerprint{}
	switch lang.TextKind(path) {
	case lang.TextProse:
		fp.ProseRefs = proseTokens(outsideFences(string(content)), 0)
	case lang.TextConfig:
		fp.ConfigKeys = configKeys(path, content)
	}
	fp.EntryRefs = packageEntryRefs(path, content)
	fp.Directives = textDirectives(path, content)

	return fp
}

// entryManifests maps each package manifest's file name to the reader of the
// entry points it names.
var entryManifests = map[string]func([]byte) []string{ //nolint:gochecknoglobals // a fixed vocabulary
	"package.json":   packageJSONEntries,
	"pyproject.toml": pyprojectEntries,
}

// TextShape names what CollectText reads from a path besides its content: the
// extension that picks the config format and the manifest name, if any.
func TextShape(path string) string {
	base := filepath.Base(path)
	if _, ok := entryManifests[base]; !ok {
		base = ""
	}

	return strings.ToLower(filepath.Ext(path)) + ":" + base
}

// packageEntryRefs answers the entry points a package manifest names: console
// scripts as "module:name" targets, and package.json's main, bin, and exports
// as file paths.
func packageEntryRefs(path string, content []byte) []string {
	if read, ok := entryManifests[filepath.Base(path)]; ok {
		return read(content)
	}

	return nil
}

// pyprojectEntries reads the console-script and entry-point targets in [project].
func pyprojectEntries(content []byte) []string {
	var doc struct {
		Project struct {
			Scripts     map[string]string            `toml:"scripts"`
			GUIScripts  map[string]string            `toml:"gui-scripts"`
			EntryPoints map[string]map[string]string `toml:"entry-points"`
		} `toml:"project"`
	}
	if err := toml.Unmarshal(content, &doc); err != nil {
		return nil
	}
	var out []string
	for _, target := range doc.Project.Scripts {
		out = append(out, target)
	}
	for _, target := range doc.Project.GUIScripts {
		out = append(out, target)
	}
	for _, group := range doc.Project.EntryPoints {
		for _, target := range group {
			out = append(out, target)
		}
	}

	return out
}

// packageJSONEntries reads the file paths package.json names in main, bin, and exports.
func packageJSONEntries(content []byte) []string {
	var doc struct {
		Bin     any    `json:"bin"`
		Exports any    `json:"exports"`
		Main    string `json:"main"`
	}
	if err := json.Unmarshal(content, &doc); err != nil {
		return nil
	}
	out := stringLeaves(doc.Bin)
	out = append(out, stringLeaves(doc.Exports)...)
	if doc.Main != "" {
		out = append(out, doc.Main)
	}

	return out
}

// stringLeaves answers every string inside a decoded JSON value.
func stringLeaves(v any) []string {
	var out []string
	switch t := v.(type) {
	case string:
		out = append(out, t)
	case map[string]any:
		for _, child := range t {
			out = append(out, stringLeaves(child)...)
		}
	case []any:
		for _, child := range t {
			out = append(out, stringLeaves(child)...)
		}
	}

	return out
}

// outsideFences blanks the lines of fenced code blocks, which hold sample code
// and command output rather than claims about the tree, and keeps line numbers.
func outsideFences(text string) string {
	lines := strings.Split(text, "\n")
	fenced := false
	for i, l := range lines {
		if isFence(l) {
			fenced = !fenced
			lines[i] = ""

			continue
		}
		if fenced {
			lines[i] = ""
		}
	}

	return strings.Join(lines, "\n")
}

// collectProse pulls code-shaped names out of a comment node's text.
func collectProse(ctx *Ctx, n *ts.Node, fp *Fingerprint) {
	mergeProse(fp, proseTokens(ctx.Text(n), lineOf(n)))
}

// collectDocstring treats a bare-string expression statement as prose: Python
// docstrings are strings, not comments, to the grammar.
func collectDocstring(ctx *Ctx, n *ts.Node, fp *Fingerprint) {
	if n.NamedChildCount() == 0 {
		return
	}
	child := n.NamedChild(0)
	if !strings.Contains(child.Kind(), "string") {
		return
	}
	mergeProse(fp, proseTokens(ctx.Text(child), lineOf(child)))
}

// mergeProse folds tokens into the fingerprint, keeping each one's first line.
func mergeProse(fp *Fingerprint, toks map[string]int) {
	for tok, line := range toks {
		if fp.ProseRefs == nil {
			fp.ProseRefs = map[string]int{}
		}
		if _, ok := fp.ProseRefs[tok]; !ok {
			fp.ProseRefs[tok] = line
		}
	}
}

// importWord splits an import node's text into the module and name tokens it
// binds, dropping the keywords and path punctuation.
var importWord = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

var importKeywords = map[string]bool{ //nolint:gochecknoglobals // fixed vocabulary
	"import": true, "from": true, "as": true, "export": true,
}

// collectImports records every name an import brings in scope, so a dotted
// mention like json.Marshal resolves through the module's presence even when
// the repo never calls that member.
func collectImports(ctx *Ctx, n *ts.Node, fp *Fingerprint) {
	for _, tok := range importWord.FindAllString(ctx.Text(n), -1) {
		if importKeywords[tok] {
			continue
		}
		if fp.Imports == nil {
			fp.Imports = map[string]int{}
		}
		if _, ok := fp.Imports[tok]; !ok {
			fp.Imports[tok] = lineOf(n)
		}
	}
}

// collectShortString records short string-literal contents: how code spells a
// config key it reads, whether through subscripts, getters, or struct tags.
func collectShortString(ctx *Ctx, n *ts.Node, fp *Fingerprint) {
	if !strings.Contains(n.Kind(), "string") {
		return
	}
	text := strings.Trim(ctx.Text(n), "\"'`")
	if p := n.Parent(); p != nil && p.Kind() == "field_declaration" {
		for _, key := range structTagKeys(text) {
			addString(fp, key, lineOf(n))
		}

		return
	}
	if text == "" || len(text) > maxStringLen || !keyShape(text) {
		return
	}
	addString(fp, text, lineOf(n))
}

// addString records a string content with its first line.
func addString(fp *Fingerprint, text string, line int) {
	if fp.Strings == nil {
		fp.Strings = map[string]int{}
	}
	if _, ok := fp.Strings[text]; !ok {
		fp.Strings[text] = line
	}
}

// structTag matches one `name:"value"` pair of a Go struct tag.
var structTag = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*:"([^",]*)`) //nolint:gochecknoglobals // a compiled pattern

// structTagKeys answers the field names a Go struct tag gives to encoders:
// the value of each pair, without its options.
func structTagKeys(tag string) []string {
	var keys []string
	for _, m := range structTag.FindAllStringSubmatch(tag, -1) {
		if m[1] != "" && keyShape(m[1]) {
			keys = append(keys, m[1])
		}
	}

	return keys
}

// keyShape reports whether text can be a config key or module path segment.
func keyShape(text string) bool {
	for i, r := range text {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case i > 0 && (r == '-' || r == '.' || r == '/' || (r >= '0' && r <= '9')):
		default:
			return false
		}
	}

	return true
}

// linkTarget matches a markdown link destination, an autolink, a bare URL, or a
// bare host-and-path: none of them names code in the repository.
var linkTarget = regexp.MustCompile( //nolint:gochecknoglobals // a compiled pattern is a constant
	`\]\([^)]*\)|<[a-zA-Z][a-zA-Z0-9+.-]*:[^>\s]*>|[a-zA-Z][a-zA-Z0-9+.-]*://\S+|(?:[a-zA-Z0-9-]+\.)+[a-zA-Z]{2,6}/\S*`)

// proseTokens extracts code-shaped names from comment, docstring, and prose
// text, mapping each to its first 1-based line.
func proseTokens(text string, baseLine int) map[string]int {
	out := map[string]int{}
	text = linkTarget.ReplaceAllString(text, " ")
	prev := ""
	for i, l := range strings.Split(text, "\n") {
		for _, tok := range lineTokens(l, prev) {
			if _, ok := out[tok]; !ok {
				out[tok] = baseLine + i + 1
			}
		}
		prev = l
	}

	return out
}

// lineTokens pulls the code-shaped tokens from one line: backticked spans,
// call shapes (name(), dotted paths, camelCase and snake_case words. Bare
// PascalCase words are left out — proper nouns like GitHub share that shape.
// A span that opens its line reads its negator from the end of prev.
func lineTokens(line, prev string) []string {
	var toks []string
	var b strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == '`' {
			if end := strings.IndexByte(line[i+1:], '`'); end > 0 {
				if tok := codeToken(line[i+1 : i+1+end]); tok != "" && !negatedBefore(lead(line[:i], prev)) {
					toks = append(toks, tok)
				}
				i += end + 1

				continue
			}
		}
		b.WriteByte(line[i])
	}
	rest := b.String()
	for _, loc := range wordRef.FindAllStringIndex(rest, -1) {
		if loc[0] > 0 && (rest[loc[0]-1] == ']' || rest[loc[0]-1] == '-') {
			continue
		}
		toks = appendToken(toks, strings.TrimSuffix(rest[loc[0]:loc[1]], "("))
	}

	return toks
}

// negators are the phrases that turn the name after them into a counterexample:
// the prose says what not to write, so the name is not supposed to resolve.
var negators = []string{ //nolint:gochecknoglobals // fixed vocabulary
	"not", "never", "avoid", "instead of", "rather than",
}

// lead answers the text a span follows: its own line's head, or the previous
// line when the span opens its line past any comment markers.
func lead(head, prev string) string {
	if strings.TrimLeft(head, " \t/#*>-") == "" {
		return prev
	}

	return head
}

// negatedBefore reports whether the text ahead of a backticked span ends in a
// negator, as in "(`httputil`, not `http_util`)".
func negatedBefore(before string) bool {
	before = strings.ToLower(strings.TrimRight(before, " \t"))
	for _, neg := range negators {
		if !strings.HasSuffix(before, neg) {
			continue
		}
		head := before[:len(before)-len(neg)]
		if head == "" || !isWordRune(head[len(head)-1]) {
			return true
		}
	}

	return false
}

// isWordRune reports whether b can be part of an English word.
func isWordRune(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '_'
}

// codeToken keeps a backticked span only when it could not be a plain English
// word or a command name: it must carry a dot, an underscore, a capital, or
// digits.
func codeToken(tok string) string {
	tok = strings.TrimSuffix(tok, "()")
	if len(tok) < 3 || allCaps(tok) || notSymbol[tok] || !codeShape(tok) || !letterLead(tok) {
		return ""
	}
	for i, r := range tok {
		if r == '.' || r == '_' || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9') {
			return tok
		}
	}

	return ""
}

// letterLead reports whether text starts the way an identifier can.
func letterLead(text string) bool {
	c := text[0]

	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// wordRef matches a code-shaped word: a dotted path, a call shape, camelCase,
// or snake_case.
var wordRef = regexp.MustCompile(
	`\b[a-zA-Z_][a-zA-Z0-9_]*(?:\.[a-zA-Z_][a-zA-Z0-9_]+)+\b|` +
		`\b[a-zA-Z_][a-zA-Z0-9_]*\(|` +
		`\b[a-z]+[A-Z][a-zA-Z0-9_]*\b|` +
		`\b[a-z_][a-z0-9]*_[a-z0-9_]+\b`,
)

// appendToken keeps a candidate when it is shaped like a code reference and
// long enough to be worth flagging.
func appendToken(toks []string, tok string) []string {
	tok = strings.TrimSuffix(tok, "()")
	if len(tok) < 3 || !codeShape(tok) || allCaps(tok) || notSymbol[tok] {
		return toks
	}

	return append(toks, tok)
}

// notSymbol holds the language keywords and builtins that share the code
// shapes but name no symbol a rename could orphan.
var notSymbol = map[string]bool{ //nolint:gochecknoglobals // fixed vocabulary
	"abstract": true, "and": true, "any": true, "Array": true, "as": true,
	"assert": true, "async": true, "await": true, "bool": true, "Boolean": true,
	"boolean": true, "break": true, "bytes": true, "case": true, "catch": true,
	"chan": true, "class": true, "console": true, "const": true, "continue": true,
	"debug": true, "def": true, "default": true, "defer": true, "delete": true,
	"dict": true, "do": true, "elif": true, "else": true, "enum": true,
	"Error": true, "error": true, "except": true, "export": true, "extends": true,
	"false": true, "False": true, "filter": true, "finally": true, "float": true,
	"fmt": true, "for": true, "from": true, "func": true, "function": true,
	"go": true, "if": true, "implements": true, "import": true, "in": true,
	"info": true, "instanceof": true, "int": true, "interface": true, "is": true,
	"lambda": true, "len": true, "let": true, "list": true, "log": true,
	"map": true, "namespace": true, "never": true, "new": true, "nil": true,
	"None": true, "not": true, "null": true, "number": true, "Number": true,
	"Object": true, "or": true, "override": true, "package": true, "panic": true,
	"pass": true, "print": true, "private": true, "Promise": true,
	"protected": true, "public": true, "raise": true, "range": true,
	"readonly": true, "require": true, "return": true, "select": true,
	"self": true, "set": true, "static": true, "str": true, "String": true,
	"string": true, "struct": true, "super": true, "switch": true, "this": true,
	"throw": true, "true": true, "True": true, "try": true, "tuple": true,
	"type": true, "typeof": true, "undefined": true, textUnknown: true, "var": true,
	"void": true, "warn": true, "while": true, "with": true, "yield": true,
}

// codeShape reports whether text is identifier-shaped or a dotted path of
// identifiers.
func codeShape(text string) bool {
	for _, part := range strings.Split(text, ".") {
		if !identifierShape(part) {
			return false
		}
	}

	return true
}

// allCaps reports whether a token is an acronym, which shares the code shapes
// but almost never names a symbol.
func allCaps(text string) bool {
	for _, r := range text {
		if r >= 'a' && r <= 'z' {
			return false
		}
	}

	return true
}

// configKeys extracts the flattened key paths of a config file, by format.
func configKeys(path string, content []byte) map[string]int {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return jsonKeys(content)
	case ".toml":
		return tomlKeys(content)
	case ".pkl":
		return pklKeys(content)
	default:
		return yamlKeys(content)
	}
}

// jsonKeys flattens a JSON object's key paths; values stay unread.
func jsonKeys(content []byte) map[string]int {
	var v any
	if err := json.Unmarshal(content, &v); err != nil {
		return nil
	}
	text := string(content)
	out := map[string]int{}
	var walk func(prefix string, m map[string]any)
	walk = func(prefix string, m map[string]any) {
		for k, val := range m {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			if _, ok := out[path]; !ok {
				line := 1
				if at := strings.Index(text, `"`+k+`"`); at >= 0 {
					line += strings.Count(text[:at], "\n")
				}
				out[path] = line
			}
			if sub, ok := val.(map[string]any); ok {
				walk(path, sub)
			}
		}
	}
	if m, ok := v.(map[string]any); ok {
		walk("", m)
	}

	return out
}

// tomlKeys reads section headers and assignments, flattening section.key.
func tomlKeys(content []byte) map[string]int {
	out := map[string]int{}
	var prefix string
	for i, l := range strings.Split(string(content), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if strings.HasPrefix(l, "[") {
			prefix = strings.Trim(strings.Trim(l, "[]"), `"`)
			continue
		}
		k, _, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		key := strings.Trim(strings.TrimSpace(k), `"'`)
		if key == "" || !keyShape(key) {
			continue
		}
		if prefix != "" {
			key = prefix + "." + key
		}
		if _, seen := out[key]; !seen {
			out[key] = i + 1
		}
	}

	return out
}

// yamlKeys reads mapping keys by indentation, flattening parent.child.
func yamlKeys(content []byte) map[string]int {
	out := map[string]int{}
	type level struct {
		key    string
		indent int
	}
	var stack []level
	for i, l := range strings.Split(string(content), "\n") {
		m := yamlKey.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		indent := len(m[1])
		key := m[2]
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		path := key
		if len(stack) > 0 {
			path = stack[len(stack)-1].key + "." + key
		}
		if _, ok := out[path]; !ok {
			out[path] = i + 1
		}
		stack = append(stack, level{path, indent})
	}

	return out
}

// yamlKey captures indent and a mapping key; list markers fold into the
// indent so items keep their parent's scope.
var yamlKey = regexp.MustCompile(`^(\s*-?\s*)([A-Za-z0-9_.-]+)\s*:(\s|$)`)

// pklKeys reads `name =`/`name:`/`name {` keys, tracking brace nesting.
func pklKeys(content []byte) map[string]int {
	out := map[string]int{}
	var stack []string
	for i, l := range strings.Split(string(content), "\n") {
		l = strings.TrimSpace(l)
		for strings.HasPrefix(l, "}") && len(stack) > 0 {
			stack = stack[:len(stack)-1]
			l = strings.TrimSpace(strings.TrimPrefix(l, "}"))
		}
		m := pklKey.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		path := m[1]
		if len(stack) > 0 {
			path = stack[len(stack)-1] + "." + m[1]
		}
		if _, ok := out[path]; !ok {
			out[path] = i + 1
		}
		if strings.HasSuffix(l, "{") {
			stack = append(stack, path)
		}
	}

	return out
}

// pklKey captures a key ahead of =, :, or a nested block.
var pklKey = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*(?:[=:{]|$)`)

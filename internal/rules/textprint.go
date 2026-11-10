package rules

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

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
		fp.ProseRefs = proseTokens(string(content), 0)
	case lang.TextConfig:
		fp.ConfigKeys = configKeys(path, content)
	}

	return fp
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
	if text == "" || len(text) > maxStringLen || !keyShape(text) {
		return
	}
	if fp.Strings == nil {
		fp.Strings = map[string]int{}
	}
	if _, ok := fp.Strings[text]; !ok {
		fp.Strings[text] = lineOf(n)
	}
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

// proseTokens extracts code-shaped names from comment, docstring, and prose
// text, mapping each to its first 1-based line.
func proseTokens(text string, baseLine int) map[string]int {
	out := map[string]int{}
	for i, l := range strings.Split(text, "\n") {
		for _, tok := range lineTokens(l) {
			if _, ok := out[tok]; !ok {
				out[tok] = baseLine + i + 1
			}
		}
	}

	return out
}

// lineTokens pulls the code-shaped tokens from one line: backticked spans,
// call shapes (name(), dotted paths, camelCase and snake_case words. Bare
// PascalCase words are left out — proper nouns like GitHub share that shape.
func lineTokens(line string) []string {
	var toks []string
	var b strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == '`' {
			if end := strings.IndexByte(line[i+1:], '`'); end > 0 {
				if tok := codeToken(line[i+1 : i+1+end]); tok != "" {
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
		if loc[0] > 0 && rest[loc[0]-1] == ']' {
			continue
		}
		toks = appendToken(toks, strings.TrimSuffix(rest[loc[0]:loc[1]], "("))
	}

	return toks
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

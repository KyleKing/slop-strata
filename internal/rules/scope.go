package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/kyleking/slop-strata/internal/lang"
)

// idLen is how many hex characters of the hash a finding ID keeps.
const idLen = 10

// funcSpan is one named function's line range.
type funcSpan struct {
	name  string
	start int
	end   int
}

// funcSpans lists the named functions of the tree with their 1-based line ranges.
func (c *Ctx) funcSpans() []funcSpan {
	var spans []funcSpan
	c.eachFunc(func(n *ts.Node) {
		name := lang.FunctionName(c.Lang, n, c.Src)
		if name == lang.AnonymousName {
			return
		}
		spans = append(spans, funcSpan{name: name, start: lineOf(n), end: int(n.EndPosition().Row) + 1})
	})

	return spans
}

// scopeAt names the innermost named function holding a line, or "" at file scope.
func scopeAt(spans []funcSpan, line int) string {
	best := -1
	for i, s := range spans {
		if s.start <= line && line <= s.end && (best < 0 || s.start >= spans[best].start) {
			best = i
		}
	}
	if best < 0 {
		return ""
	}

	return spans[best].name
}

// setScopes fills Scope on the findings a run produced.
func (c *Ctx) setScopes(findings []Finding) {
	if len(findings) == 0 {
		return
	}
	spans := c.funcSpans()
	for i := range findings {
		findings[i].Scope = scopeAt(spans, findings[i].Line)
	}
}

// FindingID names a finding by what survives a line move: the rule, the path,
// the enclosing function, the matched text, and which of the findings sharing
// those four it is, counted from the top of the file.
func FindingID(rule, path, scope, match string, occurrence int) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{rule, path, scope, match, strconv.Itoa(occurrence)}, "\x00")))

	return hex.EncodeToString(sum[:])[:idLen]
}

// Identify answers a copy of one file's findings with their IDs set.
func Identify(path string, fs []Finding) []Finding {
	out := slices.Clone(fs)
	order := make([]int, len(out))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return out[a].Line - out[b].Line })
	seen := map[string]int{}
	for _, i := range order {
		f := &out[i]
		key := strings.Join([]string{f.Rule, f.Scope, f.Match}, "\x00")
		f.ID = FindingID(f.Rule, path, f.Scope, f.Match, seen[key])
		seen[key]++
	}

	return out
}

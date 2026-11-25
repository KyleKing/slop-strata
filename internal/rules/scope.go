package rules

import (
	"cmp"
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

// anchorLen is how many hex characters of the line hash a finding keeps.
const anchorLen = 8

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
	lines := strings.Split(string(c.Src), "\n")
	for i := range findings {
		findings[i].Scope = scopeAt(spans, findings[i].Line)
		if n := findings[i].Line; n >= 1 && n <= len(lines) {
			findings[i].Anchor = anchorOf(lines[n-1])
		}
	}
}

// anchorOf hashes a source line's trimmed text.
func anchorOf(line string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(line)))

	return hex.EncodeToString(sum[:])[:anchorLen]
}

// FindingID names a finding by what survives a line move: rule, path, scope, match, line text,
// and its order among findings sharing those.
func FindingID(rule, path, scope, match, anchor string, occurrence int) string {
	parts := []string{rule, path, scope, match, anchor, strconv.Itoa(occurrence)}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))

	return hex.EncodeToString(sum[:])[:idLen]
}

// SetID names a set-shaped finding by its rule and sorted members alone.
func SetID(rule string, members []string) string {
	sum := sha256.Sum256([]byte(rule + "\x00\x00" + strings.Join(members, "\x00")))

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
		if len(f.Members) > 0 {
			f.ID = SetID(f.Rule, f.Members)

			continue
		}
		key := strings.Join([]string{f.Rule, f.Scope, f.Match, f.Anchor}, "\x00")
		f.ID = FindingID(f.Rule, path, f.Scope, f.Match, f.Anchor, seen[key])
		seen[key]++
	}

	return out
}

// CompareFindings orders the findings of one file for output: by line, rule,
// match, scope, anchor, and note, and last by ID. It is a total order over
// findings that Identify has numbered, so two hits of one rule on one line,
// which differ in nothing but their ID, print in the same order on every run
// whatever order the passes handed them over in.
func CompareFindings(a, b Finding) int {
	return cmp.Or(
		cmp.Compare(a.Line, b.Line),
		strings.Compare(a.Rule, b.Rule),
		strings.Compare(a.Match, b.Match),
		strings.Compare(a.Scope, b.Scope),
		strings.Compare(a.Anchor, b.Anchor),
		strings.Compare(a.Note, b.Note),
		strings.Compare(a.ID, b.ID),
	)
}

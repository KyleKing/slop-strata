package rules

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/kyleking/slop-strata/internal/lang"
)

// ignoreMarker opens an inline suppression: `slop-strata:ignore <rule-id> <reason>`.
const ignoreMarker = "slop-strata:ignore"

// Directive is one inline suppression read from a comment. It covers the lines
// Line through End, and suppresses nothing while Reason is empty.
type Directive struct {
	Rule   string `json:"rule"`
	Reason string `json:"reason,omitempty"`
	Line   int    `json:"line"`
	End    int    `json:"end"`
}

// nolintRules maps a golangci linter to the slop rules it overlaps.
var nolintRules = map[string][]string{ //nolint:gochecknoglobals // a fixed vocabulary
	"errcheck": {ruleGoSilentError},
}

var (
	commentOpener = regexp.MustCompile(`^(?://+|#+|/\*+|<!--)\s*`)
	ignoreForm    = regexp.MustCompile(`^` + ignoreMarker + `(?:\s+(\S+)(?:\s+(\S.*))?)?$`)
	nolintForm    = regexp.MustCompile(`^nolint:([A-Za-z0-9_,-]+)\s+//\s*(\S.*)$`)
	htmlIgnore    = regexp.MustCompile(`(<!--)\s*(` + ignoreMarker + `\b.*)`)
	hashIgnore    = regexp.MustCompile(`(?:^|\s)(#|//)\s*(` + ignoreMarker + `\b.*)$`)
)

// commentBody strips a comment's markers and keeps its first line.
func commentBody(text string) string {
	body, _, _ := strings.Cut(commentOpener.ReplaceAllString(text, ""), "\n")
	body = strings.TrimSpace(body)
	body = strings.TrimSpace(strings.TrimSuffix(body, "*/"))

	return strings.TrimSpace(strings.TrimSuffix(body, "-->"))
}

// bodyDirectives reads the directives one comment body carries. A nolint
// comment counts only in a Go file, and only with a reason.
func bodyDirectives(body string, goFile bool, line, end int) []Directive {
	if m := ignoreForm.FindStringSubmatch(body); m != nil {
		return []Directive{{Rule: m[1], Reason: strings.TrimSpace(m[2]), Line: line, End: end}}
	}
	if !goFile {
		return nil
	}
	m := nolintForm.FindStringSubmatch(body)
	if m == nil {
		return nil
	}
	var out []Directive
	for linter := range strings.SplitSeq(m[1], ",") {
		for _, rule := range nolintRules[linter] {
			out = append(out, Directive{Rule: rule, Reason: strings.TrimSpace(m[2]), Line: line, End: end})
		}
	}

	return out
}

// coverEnd answers the last line a comment covers: its own, plus the next line
// when nothing else shares the comment's first line.
func coverEnd(last int, standalone bool) int {
	if standalone {
		return last + 1
	}

	return last
}

// commentDirectives reads every directive in the file's comments.
func (c *Ctx) commentDirectives() []Directive {
	if c.directivesRead {
		return c.directives
	}
	c.directivesRead = true
	lang.EachNamed(c.Root, func(n *ts.Node) {
		if !c.Lang.CommentKinds[n.Kind()] {
			return
		}
		start := int(n.StartByte())
		lineStart := bytes.LastIndexByte(c.Src[:start], '\n') + 1
		standalone := len(bytes.TrimSpace(c.Src[lineStart:start])) == 0
		last := int(n.EndPosition().Row) + 1
		c.directives = append(c.directives,
			bodyDirectives(commentBody(c.Text(n)), c.Lang == lang.Go, lineOf(n), coverEnd(last, standalone))...)
	})

	return c.directives
}

// textDirectives reads the directives of a prose or config file: HTML comments in Markdown outside code,
// and # or // comments in TOML, YAML, and Pkl.
func textDirectives(path string, content []byte) []Directive {
	re := hashIgnore
	prose := lang.TextKind(path) == lang.TextProse
	switch {
	case prose:
		re = htmlIgnore
	case strings.EqualFold(filepath.Ext(path), ".json"):
		return nil
	}
	var out []Directive
	fenced := false
	for i, l := range strings.Split(string(content), "\n") {
		if prose && isFence(l) {
			fenced = !fenced

			continue
		}
		loc := re.FindStringSubmatchIndex(l)
		if fenced || loc == nil || (prose && strings.Count(l[:loc[2]], "`")%2 == 1) {
			continue
		}
		standalone := strings.TrimSpace(l[:loc[2]]) == ""
		body := commentBody(l[loc[4]:])
		out = append(out, bodyDirectives(body, false, i+1, coverEnd(i+1, standalone))...)
	}

	return out
}

// isFence reports whether a Markdown line opens or closes a fenced code block.
func isFence(line string) bool {
	line = strings.TrimSpace(line)

	return strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~")
}

// covered reports whether an inline directive with a reason covers a finding.
func covered(directives []Directive, f Finding) bool {
	for _, d := range directives {
		if d.Reason != "" && d.Rule == f.Rule && d.Line <= f.Line && f.Line <= d.End {
			return true
		}
	}

	return false
}

// ignoreWithoutReason flags an ignore comment missing its rule id or reason,
// which suppresses nothing.
func ignoreWithoutReason(ctx *Ctx, findings *[]Finding) {
	for _, d := range ctx.commentDirectives() {
		if d.Reason == "" {
			*findings = append(*findings, misuse(d))
		}
	}
}

func misuse(d Directive) Finding {
	return Finding{
		Rule: ruleXIgnoreWithoutReason, Line: d.Line, Match: strings.TrimSpace(ignoreMarker + " " + d.Rule),
		Note: "an ignore needs a rule id and a reason, and suppresses nothing without both",
	}
}

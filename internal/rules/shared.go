package rules

import (
	"regexp"
	"slices"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/kyleking/slop-strata/internal/lang"
)

// narrationWords are the phrases of a comment telling a diff's story rather than
// a standing invariant: they only make sense with the change in memory.
var narrationWords = regexp.MustCompile( //nolint:gochecknoglobals // a compiled pattern is a constant
	`(?i)\b(now uses|was changed|previously|no longer|used to|we changed|this PR|moved from|renamed from)\b`)

// ticketRef matches a tracker id inside a comment.
var ticketRef = regexp.MustCompile(`\b[A-Z]{2,}-\d+\b`) //nolint:gochecknoglobals // a compiled pattern is a constant

// forwardWords mark the ticket references allowed to stay: the ones pointing
// at the condition that retires the comment.
var forwardWords = regexp.MustCompile( //nolint:gochecknoglobals // a compiled pattern is a constant
	`(?i)\b(todo|fixme|remove|when|until|once|after|blocked|unblock|drop)\b`)

// narratingComment flags comments that narrate the change that added them or
// cite a ticket as history. Both rot with the next diff.
func narratingComment(ctx *Ctx, findings *[]Finding) {
	lang.EachNamed(ctx.Root, func(n *ts.Node) {
		if !ctx.Lang.CommentKinds[n.Kind()] {
			return
		}
		text := ctx.Text(n)
		switch {
		case narrationWords.MatchString(text):
			*findings = append(*findings, Finding{Rule: ruleXNarratingComment, Line: lineOf(n), Match: "narration"})
		case ticketRef.MatchString(text) && !forwardWords.MatchString(text):
			*findings = append(*findings, Finding{
				Rule: ruleXNarratingComment, Line: lineOf(n), Match: "ticket-as-history",
			})
		}
	})
}

// commentDensity flags functions where comments carry a third or more of the
// lines: at that density the comments usually restate the code.
func commentDensity(ctx *Ctx, findings *[]Finding) {
	const minComments = 3
	ctx.eachFunc(func(fn *ts.Node) {
		comments := 0
		lang.EachNamed(fn, func(n *ts.Node) {
			if ctx.Lang.CommentKinds[n.Kind()] {
				comments++
			}
		})
		if comments < minComments {
			return
		}
		span := int(fn.EndPosition().Row) - int(fn.StartPosition().Row) + 1
		if comments*3 >= span {
			*findings = append(*findings, Finding{
				Rule: "x/comment-density", Line: lineOf(fn),
				Match: lang.FunctionName(ctx.Lang, fn, ctx.Src),
			})
		}
	})
}

// spentName matches test filenames whose subject was one-shot code: a
// migration, a data repair, a backfill. The test outlives the thing it tested.
var spentName = regexp.MustCompile( //nolint:gochecknoglobals // a compiled pattern is a constant
	`(?i)(migrat|repair|backfill|one[-_.]?shot|fixup|backport|replay)`)

// spentTest flags a test file whose name says its subject was one-shot code.
// The second half of the idea — a subject untouched for months — needs
// history and is reported by the history walk, not this rule.
func spentTest(ctx *Ctx, findings *[]Finding) {
	if spentName.MatchString(ctx.Path) {
		*findings = append(*findings, Finding{
			Rule: "x/spent-test", Line: 1,
			Match: ctx.Path,
			Note:  "test file name suggests a one-shot subject",
		})
	}
}

// registryName matches the names single-lookup maps tend to carry.
var registryName = regexp.MustCompile( //nolint:gochecknoglobals // a compiled pattern is a constant
	`(?i)(register(?:ry|ies)|handler|provider|plugin|backend|driver|adapter)s?$`)

// singleEntryRegistry flags a registry-shaped map initialized with one entry:
// a dispatch table nothing else can join.
func singleEntryRegistry(ctx *Ctx, findings *[]Finding) {
	lang.EachNamed(ctx.Root, func(n *ts.Node) {
		entries, name := registryEntries(ctx, n)
		if entries != 1 || !registryName.MatchString(name) {
			return
		}
		*findings = append(*findings, Finding{Rule: "x/single-entry-registry", Line: lineOf(n), Match: name})
	})
}

// registryEntries counts the entries in a map literal bound to a name, per
// language: (0, name) when the node is not such a binding.
func registryEntries(ctx *Ctx, n *ts.Node) (int, string) {
	switch ctx.Lang {
	case lang.Go:
		// var/const/short declarations: left is the name, right the literal.
		if n.Kind() != "var_declaration" && n.Kind() != "const_declaration" &&
			n.Kind() != "short_var_declaration" && n.Kind() != "var_spec" && n.Kind() != "const_spec" {
			return 0, ""
		}

		return namedLiteralEntries(ctx, n, "literal_value", "keyed_element")
	case lang.Python:
		if n.Kind() != "assignment" {
			return 0, ""
		}

		return namedLiteralEntries(ctx, n, "dictionary", "pair")
	case lang.TypeScript, lang.TSX:
		if n.Kind() != "variable_declarator" {
			return 0, ""
		}

		return namedLiteralEntries(ctx, n, "object", "pair")
	}

	return 0, ""
}

// namedLiteralEntries counts container children inside a declaration's value
// when the declared name is the only identifier on the left.
func namedLiteralEntries(ctx *Ctx, decl *ts.Node, containerKind, entryKind string) (int, string) {
	var name string
	var container *ts.Node
	lang.EachNamed(decl, func(n *ts.Node) {
		switch {
		case n.Kind() == kindIdentifier && name == "":
			name = ctx.Text(n)
		case n.Kind() == containerKind && container == nil:
			container = n
		}
	})
	if name == "" || container == nil {
		return 0, ""
	}
	entries := 0
	for i := range container.NamedChildCount() {
		if container.NamedChild(i).Kind() == entryKind {
			entries++
		}
	}

	return entries, name
}

// sourceExt matches a literal that names a source, config, or doc file.
var sourceExt = regexp.MustCompile( //nolint:gochecknoglobals // a compiled pattern is a constant
	`\.(py|go|ts|tsx|js|toml|yaml|yml|json|md|cfg|ini)\b`)

// quotedLiteral matches a string literal in any of the quoting styles the
// three languages use.
var quotedLiteral = regexp.MustCompile( //nolint:gochecknoglobals // a compiled pattern is a constant
	"\"[^\"]*\"|'[^']*'|`[^`]*`")

// repoRef matches code that reaches the repository from the file itself or from
// a root helper.
var repoRef = regexp.MustCompile( //nolint:gochecknoglobals // a compiled pattern is a constant
	`(?i)__file__|__dirname|__filename|\b(repo|project)_?(root|dir|path)?\b|\w*root\b`)

// tempRef matches code that names a directory the test creates.
var tempRef = regexp.MustCompile( //nolint:gochecknoglobals // a compiled pattern is a constant
	`TempDir|tempfile|mkdtemp|NamedTemporaryFile|(?i)\b(tmp|out)\w*`)

// fixtureDirs hold data a test is meant to read, not the repository's own files.
var fixtureDirs = map[string]bool{ //nolint:gochecknoglobals // a fixed vocabulary
	"__snapshots__": true, "cassettes": true, "fixtures": true, "testdata": true,
}

// readsRepoFile reports whether a path expression names repository source,
// config, or docs, rather than temp output or fixture data.
func readsRepoFile(expr string) bool {
	literals, code := splitLiterals(expr)
	if tempRef.MatchString(code) || slices.ContainsFunc(literals, inFixtureDir) {
		return false
	}
	if repoRef.MatchString(code) || slices.ContainsFunc(literals, parentRelative) {
		return true
	}

	return slices.ContainsFunc(literals, sourceExt.MatchString)
}

// computedSource reports whether an expression is built at run time rather than
// written as a literal, outside any temp directory.
func computedSource(expr string) bool {
	literals, code := splitLiterals(expr)

	return len(literals) == 0 && strings.TrimSpace(code) != "" && !tempRef.MatchString(code)
}

// splitLiterals separates an expression's string literals from its code.
func splitLiterals(expr string) ([]string, string) {
	matches := quotedLiteral.FindAllString(expr, -1)
	literals := make([]string, 0, len(matches))
	for _, lit := range matches {
		literals = append(literals, strings.Trim(lit, "\"'`"))
	}

	return literals, quotedLiteral.ReplaceAllString(expr, " ")
}

// inFixtureDir reports whether a literal path runs through a fixture directory.
func inFixtureDir(lit string) bool {
	for _, seg := range strings.Split(lit, "/") {
		if fixtureDirs[seg] {
			return true
		}
	}

	return false
}

// parentRelative reports whether a literal path climbs out of its directory.
func parentRelative(lit string) bool {
	return strings.Contains(lit, "../") || lit == ".."
}

// firstArgText answers the text of a call's first argument, or "" for none.
func firstArgText(ctx *Ctx, call *ts.Node) string {
	args := call.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return ""
	}

	return ctx.Text(args.NamedChild(0))
}

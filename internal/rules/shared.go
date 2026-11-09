package rules

import (
	"regexp"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/kyleking/slop-strata/internal/lang"
)

// narrationWords are the markers of a comment telling a diff's story rather
// than a standing invariant. The list matches the project's comment rules:
// words that only make sense with the change in memory.
var narrationWords = regexp.MustCompile( //nolint:gochecknoglobals // a compiled pattern is a constant
	`(?i)\b(now|moved|renamed|previously|used to|no longer|was|as of|newly)\b`)

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
			*findings = append(*findings, Finding{Rule: "x/narrating-comment", Line: lineOf(n), Match: "narration"})
		case ticketRef.MatchString(text) && !forwardWords.MatchString(text):
			*findings = append(*findings, Finding{Rule: "x/narrating-comment", Line: lineOf(n), Match: "ticket-as-history"})
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
	switch ctx.Lang.ID {
	case "go":
		// var/const/short declarations: left is the name, right the literal.
		if n.Kind() != "var_declaration" && n.Kind() != "const_declaration" &&
			n.Kind() != "short_var_declaration" && n.Kind() != "var_spec" && n.Kind() != "const_spec" {
			return 0, ""
		}
		return namedLiteralEntries(ctx, n, "literal_value", "keyed_element")
	case "python":
		if n.Kind() != "assignment" {
			return 0, ""
		}
		return namedLiteralEntries(ctx, n, "dictionary", "pair")
	case "typescript", "tsx":
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
		case n.Kind() == "identifier" && name == "":
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

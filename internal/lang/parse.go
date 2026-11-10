package lang

import (
	"fmt"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"
)

// Parser holds one tree-sitter parser per language. A parser is not safe for
// concurrent use, so each worker owns its own and closes it when done.
type Parser struct {
	parsers map[*Language]*ts.Parser
}

// NewParser returns a parser with no grammars loaded; each language's parser is
// created on first use.
func NewParser() *Parser {
	return &Parser{parsers: map[*Language]*ts.Parser{}}
}

// Parse parses content with l's grammar. The caller must Close the tree.
func (p *Parser) Parse(l *Language, content []byte) *ts.Tree {
	parser, ok := p.parsers[l]
	if !ok {
		parser = ts.NewParser()
		if err := parser.SetLanguage(l.Language()); err != nil {
			panic(fmt.Sprintf("loading %s grammar: %v", l.ID, err))
		}
		p.parsers[l] = parser
	}

	return parser.Parse(content, nil)
}

// Close releases every parser the Parser created.
func (p *Parser) Close() {
	for _, parser := range p.parsers {
		parser.Close()
	}
	p.parsers = map[*Language]*ts.Parser{}
}

// Parse parses content once with a parser of its own, for callers outside a
// worker pool.
func Parse(l *Language, content []byte) *ts.Tree {
	p := NewParser()
	defer p.Close()

	return p.Parse(l, content)
}

// Text returns the source a node spans.
func Text(node *ts.Node, src []byte) string {
	return string(src[node.StartByte():node.EndByte()])
}

// NamedChildren collects a node's named children as a slice.
func NamedChildren(node *ts.Node) []*ts.Node {
	out := make([]*ts.Node, 0, node.NamedChildCount())
	for i := range node.NamedChildCount() {
		out = append(out, node.NamedChild(i))
	}

	return out
}

// EachNamed calls fn for every named node in the subtree, node first.
func EachNamed(node *ts.Node, fn func(*ts.Node)) {
	fn(node)
	for i := range node.NamedChildCount() {
		EachNamed(node.NamedChild(i), fn)
	}
}

// AnonymousName is what FunctionName reads for a literal or arrow with no name.
const AnonymousName = "<anonymous>"

// FunctionName best-effort names a function node, qualified by the Go receiver
// type or the class that holds it ("Session.run"). Literals and arrows without
// a name read AnonymousName.
func FunctionName(l *Language, node *ts.Node, src []byte) string {
	name := ""
	if n := node.ChildByFieldName("name"); n != nil {
		name = Text(n, src)
	}
	if name == "" {
		return AnonymousName
	}
	var owner string
	if recv := node.ChildByFieldName("receiver"); recv != nil && l.MethodKinds[node.Kind()] {
		owner = receiverType(Text(recv, src))
	} else {
		owner = enclosingClass(l, node, src)
	}
	if owner == "" {
		return name
	}

	return owner + "." + name
}

// receiverType reads the type name out of a Go receiver such as "(s *Session)"
// or "(Stack[T])".
func receiverType(receiver string) string {
	fields := strings.Fields(strings.Trim(receiver, "()"))
	if len(fields) == 0 {
		return ""
	}
	typ := strings.TrimLeft(fields[len(fields)-1], "*")
	if i := strings.IndexByte(typ, '['); i >= 0 {
		typ = typ[:i]
	}

	return typ
}

// enclosingClass names the class a function is a method of, or "" when a
// function or the file's top level comes first.
func enclosingClass(l *Language, node *ts.Node, src []byte) string {
	for p := node.Parent(); p != nil; p = p.Parent() {
		if l.FunctionKinds[p.Kind()] {
			return ""
		}
		if l.ClassKinds[p.Kind()] {
			if n := p.ChildByFieldName("name"); n != nil {
				return Text(n, src)
			}

			return ""
		}
	}

	return ""
}

// ParamCount counts a function node's declared parameters: named children of
// the parameter container, except Go, where one declaration can name several
// (`a, b int`), so identifiers inside each declaration are counted.
func (l *Language) ParamCount(fnNode *ts.Node) int {
	container := fnNode.ChildByFieldName("parameters")
	if container == nil {
		return 0
	}

	if l.ID == "go" {
		count := 0
		EachNamed(container, func(n *ts.Node) {
			p := n.Parent()
			if n.Kind() == "identifier" && p != nil &&
				(p.Kind() == "parameter_declaration" || p.Kind() == "variadic_parameter_declaration") {
				count++
			}
		})

		return count
	}

	count := 0
	for i := range container.NamedChildCount() {
		if !l.ParamSkipKinds[container.NamedChild(i).Kind()] {
			count++
		}
	}

	return count
}

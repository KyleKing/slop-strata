package lang

import (
	"sync"

	ts "github.com/tree-sitter/go-tree-sitter"
)

// Pool hands out one parser per language, because a parser can only ever carry
// the language it was configured with and parsers are not safe to share
// between goroutines.
type Pool struct {
	parsers map[*Language]*sync.Pool
}

// NewPool prepares a parser pool covering every registered language.
func NewPool() *Pool {
	p := &Pool{parsers: make(map[*Language]*sync.Pool, len(All))}
	for _, l := range All {
		lang := l
		p.parsers[lang] = &sync.Pool{
			New: func() any {
				parser := ts.NewParser()
				// SetLanguage only fails on ABI mismatch with a stale grammar;
				// the pinned modules always match the binding.
				if err := parser.SetLanguage(lang.Language()); err != nil {
					parser.Close()

					return nil
				}

				return parser
			},
		}
	}

	return p
}

// Parse parses content with lang's grammar. A returned tree is owned by the
// caller, which must Close it; the parser returns to the pool on the next call.
func (p *Pool) Parse(l *Language, content []byte) *ts.Tree {
	v := p.parsers[l].Get()
	parser, ok := v.(*ts.Parser)
	if !ok || parser == nil {
		return (&ts.Parser{}).Parse(content, nil)
	}
	defer p.parsers[l].Put(parser)

	return parser.Parse(content, nil)
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

// FunctionName best-effort names a function node: its "name" field when the
// grammar has one, else the receiver-qualified form for Go methods, else
// "<anonymous>" for literals and arrows.
func FunctionName(l *Language, node *ts.Node, src []byte) string {
	if name := node.ChildByFieldName("name"); name != nil {
		return Text(name, src)
	}
	if l.ID == "go" && l.MethodKinds[node.Kind()] {
		if recv := node.ChildByFieldName("receiver"); recv != nil {
			return Text(recv, src) + "." + "<method>"
		}
	}

	return "<anonymous>"
}

// ParamCount counts a function node's declared parameters: named children of
// the parameter container, except Go, where one declaration can name several
// (`a, b int`), so identifiers inside each declaration are counted.
func (l *Language) ParamCount(fnNode *ts.Node) int {
	var container *ts.Node
	for i := range fnNode.NamedChildCount() {
		child := fnNode.NamedChild(i)
		if child.Kind() == l.ParamContainerKind {
			container = child
		}
	}
	if container == nil {
		return 0
	}

	if l.ID == "go" {
		count := 0
		EachNamed(container, func(n *ts.Node) {
			if n.Kind() == "identifier" && n.Parent() != nil &&
				(n.Parent().Kind() == "parameter_declaration" || n.Parent().Kind() == "variadic_parameter_declaration") {
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

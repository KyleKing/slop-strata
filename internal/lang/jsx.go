package lang

import (
	ts "github.com/tree-sitter/go-tree-sitter"
)

// Node kinds and fields of the markup vocabulary.
const (
	kindJSXElement     = "jsx_element"
	kindJSXSelfClosing = "jsx_self_closing_element"
	kindJSXAttribute   = "jsx_attribute"
	kindJSXExpression  = "jsx_expression"
	fieldOpenTag       = "open_tag"
	fieldCloseTag      = "close_tag"
)

// JSXSyntax is one grammar's vocabulary for markup written in its source: the
// elements, what an element is opened with, and what it may hold.
type JSXSyntax struct {
	// Elements are the nodes that are one element, with children or without.
	Elements map[string]bool
	// Attributes are the children of an opening tag that set one name.
	Attributes map[string]bool
	// Expressions are the nodes that write a value of the language into the
	// markup: as a child, as an attribute's value, or spread over the tag.
	Expressions map[string]bool
	// Text are the children that are text as written.
	Text map[string]bool
	// Comments are the nodes inside an expression that say nothing.
	Comments map[string]bool
	// Parens are the nodes that only wrap one expression.
	Parens map[string]bool
	// Opaque are the tags whose element is read as one value and not as
	// markup: a drawing, where the elements are the data.
	Opaque map[string]bool
	// Drawn are the tags of a drawing's parts, which count as no element
	// wherever they are written.
	Drawn map[string]bool
	// OpenTag and CloseTag are the fields of an element with children that
	// hold its two tags. An element without them is its own opening tag.
	OpenTag, CloseTag string
	// Name is the field of an opening tag that holds what it is a tag of.
	Name string
}

var tsxJSX = &JSXSyntax{ //nolint:gochecknoglobals // fixed vocabulary
	Elements:    map[string]bool{kindJSXElement: true, kindJSXSelfClosing: true},
	Attributes:  map[string]bool{kindJSXAttribute: true},
	Expressions: map[string]bool{kindJSXExpression: true},
	Text:        map[string]bool{"jsx_text": true, "html_character_reference": true},
	Comments:    map[string]bool{kindComment: true},
	Parens:      map[string]bool{kindParenthesize: true},
	Opaque:      map[string]bool{"svg": true},
	Drawn: map[string]bool{
		"g": true, "path": true, "circle": true, "ellipse": true, "rect": true, "line": true,
		"polyline": true, "polygon": true, "defs": true, "clipPath": true, "mask": true, "use": true,
		"symbol": true, "linearGradient": true, "radialGradient": true, "stop": true, "tspan": true,
		"pattern": true, "filter": true, "marker": true,
	},
	OpenTag:  fieldOpenTag,
	CloseTag: fieldCloseTag,
	Name:     fieldName,
}

// JSX answers the grammar's markup vocabulary, or nil for a grammar that
// parses none.
func (l *Language) JSX() *JSXSyntax {
	if l == TSX {
		return tsxJSX
	}

	return nil
}

// Opening answers the node that holds an element's name and attributes: its
// opening tag, or the element itself when it has no children.
func (s *JSXSyntax) Opening(n *ts.Node) *ts.Node {
	if open := n.ChildByFieldName(s.OpenTag); open != nil {
		return open
	}

	return n
}

// Inner answers the bytes between an element's two tags, where its children
// start and end. An element with no children answers its own end twice.
func (s *JSXSyntax) Inner(n *ts.Node) (uint, uint) {
	from, to := n.EndByte(), n.EndByte()
	if open := n.ChildByFieldName(s.OpenTag); open != nil {
		from = open.EndByte()
	}
	if closing := n.ChildByFieldName(s.CloseTag); closing != nil {
		to = closing.StartByte()
	}

	return from, to
}

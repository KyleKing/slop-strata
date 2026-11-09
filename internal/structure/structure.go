// Package structure measures a blob's shape: per-function complexity and the
// distributions erosion flattens away.
package structure

import (
	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/kyleking/slop-strata/internal/lang"
)

// FuncMetrics is one function's structural profile. Lines are 1-based and
// inclusive, matching the rows an editor shows.
type FuncMetrics struct {
	Name      string `json:"name"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	SLOC      int    `json:"sloc"`
	CC        int    `json:"cc"`
	Cognitive int    `json:"cognitive"`
	Nesting   int    `json:"nesting"`
	Params    int    `json:"params"`
	Comments  int    `json:"comments"`
}

// FileMetrics is one parsed blob.
type FileMetrics struct {
	Lang       string        `json:"lang"`
	Funcs      []FuncMetrics `json:"funcs"`
	Lines      int           `json:"lines"`
	SLOC       int           `json:"sloc"`
	Comments   int           `json:"comments"`
	ParseError bool          `json:"parse_error,omitempty"`
}

// lineClass names what a line's content is.
type lineClass int

const (
	lineBlank lineClass = iota
	lineCommentOnly
	lineCode
)

// lines is a byte-offset index of every line in a blob.
type lines struct {
	starts []int
	src    []byte
}

func indexLines(src []byte) *lines {
	starts := []int{0}
	for i, b := range src {
		if b == '\n' && i+1 < len(src) {
			starts = append(starts, i+1)
		}
	}

	return &lines{starts: starts, src: src}
}

// span returns the byte range of 0-based row, without its newline.
func (l *lines) span(row int) (int, int) {
	if row < 0 || row >= len(l.starts) {
		return 0, 0
	}
	end := len(l.src)
	if row+1 < len(l.starts) {
		end = l.starts[row+1] - 1 // drop the newline itself
	}

	return l.starts[row], end
}

// isCode reports whether row carries non-whitespace, non-comment bytes, given
// the comment node spans covering it.
func (l *lines) isCode(row int, comments []*ts.Node) bool {
	start, end := l.span(row)
	for i := start; i < end; i++ {
		b := l.src[i]
		if b == ' ' || b == '\t' || b == '\r' {
			continue
		}
		inComment := false
		for _, c := range comments {
			if uint(i) >= c.StartByte() && uint(i) < c.EndByte() {
				inComment = true

				break
			}
		}
		if !inComment {
			return true
		}
	}

	return false
}

// classify marks every row blank, comment-only, or code. A row mixing code and
// a trailing comment counts as code.
func classify(src []byte, root *ts.Node, l *lang.Language) ([]lineClass, *lines) {
	idx := indexLines(src)
	classes := make([]lineClass, len(idx.starts))

	var comments []*ts.Node
	lang.EachNamed(root, func(n *ts.Node) {
		if l.CommentKinds[n.Kind()] {
			comments = append(comments, n)
		}
	})
	byRow := make([][]*ts.Node, len(idx.starts))
	for _, c := range comments {
		for row := int(c.StartPosition().Row); row <= int(c.EndPosition().Row) && row < len(byRow); row++ {
			byRow[row] = append(byRow[row], c)
		}
	}

	for row := range classes {
		start, end := idx.span(row)
		switch {
		case start == end:
			classes[row] = lineBlank
		case idx.isCode(row, byRow[row]):
			classes[row] = lineCode
		default:
			classes[row] = lineCommentOnly
		}
	}

	return classes, idx
}

// complexity accumulates one function's decision counts in a recursive walk.
type complexity struct {
	l         *lang.Language
	src       []byte
	cc        int
	cognitive int
	nesting   int
}

// walk recurses with the cognitive-complexity nesting level.
func (c *complexity) walk(node *ts.Node, level int) {
	kind := node.Kind()

	if c.l.DecisionKinds[kind] {
		c.cc++
	}
	if c.l.OpContainerKinds[kind] && c.isCountedOp(node) {
		c.cc++
		c.cognitive++
	}
	switch {
	case c.l.NestKinds[kind]:
		c.cognitive += 1 + level
		if level+1 > c.nesting {
			c.nesting = level + 1
		}
		level++
	case c.l.FlatKinds[kind]:
		c.cognitive++
	}

	for i := range node.NamedChildCount() {
		c.walk(node.NamedChild(i), level)
	}
}

// isCountedOp reports whether an expression node's operator is a counted
// boolean decision operator (and/or/&&/||/??). The operator is an unnamed
// child, so kinds alone cannot see it.
func (c *complexity) isCountedOp(node *ts.Node) bool {
	for i := range node.ChildCount() {
		child := node.Child(i)
		if !child.IsNamed() && c.l.DecisionOps[lang.Text(child, c.src)] {
			return true
		}
	}

	return false
}

// Analyze measures one parsed blob.
func Analyze(l *lang.Language, tree *ts.Tree, src []byte) FileMetrics {
	root := tree.RootNode()
	classes, _ := classify(src, root, l)

	fm := FileMetrics{
		Lang:       l.ID,
		Lines:      len(classes),
		ParseError: root.HasError(),
	}
	for _, class := range classes {
		switch class {
		case lineCode:
			fm.SLOC++
		case lineCommentOnly:
			fm.Comments++
		case lineBlank:
		}
	}

	lang.EachNamed(root, func(n *ts.Node) {
		if l.FunctionKinds[n.Kind()] {
			fm.Funcs = append(fm.Funcs, measureFunc(l, n, src, classes))
		}
	})

	return fm
}

// measureFunc computes one function node's metrics.
func measureFunc(l *lang.Language, node *ts.Node, src []byte, classes []lineClass) FuncMetrics {
	startRow := int(node.StartPosition().Row)
	endRow := int(node.EndPosition().Row)

	c := &complexity{l: l, src: src}
	for i := range node.NamedChildCount() {
		c.walk(node.NamedChild(i), 0)
	}

	comments := 0
	lang.EachNamed(node, func(n *ts.Node) {
		if l.CommentKinds[n.Kind()] {
			comments++
		}
	})

	sloc := 0
	for row := startRow; row <= endRow && row < len(classes); row++ {
		if classes[row] == lineCode {
			sloc++
		}
	}

	return FuncMetrics{
		Name:      lang.FunctionName(l, node, src),
		StartLine: startRow + 1,
		EndLine:   endRow + 1,
		SLOC:      sloc,
		CC:        1 + c.cc,
		Cognitive: c.cognitive,
		Nesting:   c.nesting,
		Params:    l.ParamCount(node),
		Comments:  comments,
	}
}

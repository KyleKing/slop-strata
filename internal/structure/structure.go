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
	Lang  string        `json:"lang"`
	Funcs []FuncMetrics `json:"funcs"`
	// ErrorCoverage is the byte share under ERROR/MISSING nodes.
	ErrorCoverage float64 `json:"error_coverage,omitempty"`
	Lines         int     `json:"lines"`
	SLOC          int     `json:"sloc"`
	Comments      int     `json:"comments"`
	// ParseError is set for any ERROR or MISSING node, however few bytes it covers.
	ParseError bool `json:"parse_error,omitempty"`
}

// lineClass names what a line's content is.
type lineClass int

const (
	lineBlank lineClass = iota
	lineCommentOnly
	lineCode
)

// byteSpan is a half-open byte range of a blob.
type byteSpan struct{ start, end uint }

func containsAny(spans []byteSpan, i uint) bool {
	for _, s := range spans {
		if i >= s.start && i < s.end {
			return true
		}
	}

	return false
}

// lines is a byte-offset index of every line in a blob, with the comment spans
// each row carries.
type lines struct {
	starts   []int
	src      []byte
	comments [][]byteSpan
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

// isCode reports whether row carries non-whitespace bytes outside its comments
// and outside skip.
func (l *lines) isCode(row int, skip []byteSpan) bool {
	start, end := l.span(row)
	for i := start; i < end; i++ {
		b := l.src[i]
		if b == ' ' || b == '\t' || b == '\r' {
			continue
		}
		if !containsAny(l.comments[row], uint(i)) && !containsAny(skip, uint(i)) {
			return true
		}
	}

	return false
}

// classify marks every row blank, comment-only, or code. A row mixing code and
// a trailing comment counts as code.
func classify(src []byte, root *ts.Node, l *lang.Language) ([]lineClass, *lines) {
	idx := indexLines(src)
	idx.comments = make([][]byteSpan, len(idx.starts))
	lang.EachNamed(root, func(n *ts.Node) {
		if !l.CommentKinds[n.Kind()] {
			return
		}
		span := byteSpan{n.StartByte(), n.EndByte()}
		for row := int(n.StartPosition().Row); row <= int(n.EndPosition().Row) && row < len(idx.comments); row++ {
			idx.comments[row] = append(idx.comments[row], span)
		}
	})

	classes := make([]lineClass, len(idx.starts))
	for row := range classes {
		start, end := idx.span(row)
		switch {
		case start == end:
			classes[row] = lineBlank
		case idx.isCode(row, nil):
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
	if c.l.FunctionKinds[kind] {
		return
	}

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

	for i := range node.ChildCount() {
		child := node.Child(i)
		switch {
		case child.IsNamed():
			c.walk(child, level)
		case c.l.FlatKinds[child.Kind()]:
			c.cognitive++
		}
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
	classes, idx := classify(src, root, l)

	coverage := errorCoverage(root, src)
	fm := FileMetrics{
		Lang:          l.ID,
		Lines:         len(classes),
		ParseError:    root.HasError(),
		ErrorCoverage: coverage,
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
			fm.Funcs = append(fm.Funcs, measureFunc(l, n, src, idx))
		}
	})

	return fm
}

// errorCoverage sums the byte share of ERROR and MISSING nodes, so a file
// counts as a parse failure only when the failure is material.
func errorCoverage(root *ts.Node, src []byte) float64 {
	if !root.HasError() || len(src) == 0 {
		return 0
	}
	var bad uint
	lang.EachNamed(root, func(n *ts.Node) {
		if !n.IsError() && !n.IsMissing() {
			return
		}
		if p := n.Parent(); p != nil && (p.IsError() || p.IsMissing()) {
			return // nested inside an error already counted
		}
		bad += n.EndByte() - n.StartByte()
	})

	return float64(bad) / float64(len(src))
}

// owned is what a function scores as its own: its comments and the spans of
// the functions nested in it, which are scored separately.
type owned struct {
	nested   []byteSpan
	comments int
}

func ownedBy(l *lang.Language, node *ts.Node) owned {
	var o owned
	o.collect(l, node)

	return o
}

func (o *owned) collect(l *lang.Language, node *ts.Node) {
	for i := range node.NamedChildCount() {
		child := node.NamedChild(i)
		if l.FunctionKinds[child.Kind()] {
			o.nested = append(o.nested, byteSpan{child.StartByte(), child.EndByte()})

			continue
		}
		if l.CommentKinds[child.Kind()] {
			o.comments++
		}
		o.collect(l, child)
	}
}

// measureFunc computes one function node's metrics.
func measureFunc(l *lang.Language, node *ts.Node, src []byte, idx *lines) FuncMetrics {
	startRow := int(node.StartPosition().Row)
	endRow := int(node.EndPosition().Row)
	own := ownedBy(l, node)

	c := &complexity{l: l, src: src}
	for i := range node.NamedChildCount() {
		c.walk(node.NamedChild(i), 0)
	}

	sloc := 0
	for row := startRow; row <= endRow && row < len(idx.starts); row++ {
		if idx.isCode(row, own.nested) {
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
		Comments:  own.comments,
	}
}

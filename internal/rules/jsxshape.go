package rules

import (
	"bytes"
	"encoding/binary"
	"slices"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/kyleking/slop-strata/internal/lang"
)

// JSXShapes is the markup of a file, one record per element that holds
// jsxMinElems elements or more, itself counted. The JSX pass groups the
// records across files by shape.
//
// An element's shape is what a shared component would keep: its tag as
// written, its attributes by name in the order of their names, and its
// children in order. What a component would take as a prop is a hole: the
// value of an attribute, a run of text and expressions between elements, and
// the code around markup that an expression wraps (the condition of an `&&`,
// the list a `.map` runs over). The shape hashes that a hole is there and
// keeps what fills it apart, as 16 bits per hole, so two sites of one shape
// can be compared hole by hole.
//
// The bytes are packed as varints: a count, then per record its line as a
// delta, the lines it spans past its first, its elements, how many records
// back its nearest enclosing record is (0 for none), where its holes start as
// a delta, and how many it has; then the 8 bytes of each record's shape; then
// the holes, 2 bytes each.
type JSXShapes []byte

// jsxMinElems is the fewest elements a record holds. Under it a repeat is
// the use of a component, not a block that could be one.
const jsxMinElems = 6

// Sizes of the fixed-width sections of a payload.
const (
	jsxHashBytes = 8
	jsxHoleBytes = 2
)

// jsxRec is one element of a file with enough elements under it.
type jsxRec struct {
	hash   uint64
	line   int
	end    int
	elems  int
	parent int // the nearest enclosing record of the file, or -1
	hole   int // its first hole, among the file's
	nholes int
}

// Marks mixed into a shape hash between its parts, so that two shapes whose
// parts run together differently do not collide.
const (
	jsxMarkAttr uint64 = iota + 1
	jsxMarkChild
	jsxMarkHole
	jsxMarkWrap
	jsxMarkEnd
)

// jsxPrime is the 64-bit FNV prime the shape hash multiplies by.
const jsxPrime = 0x100000001b3

// jsxSeed is the 64-bit FNV offset the shape hash starts from.
const jsxSeed uint64 = 0xcbf29ce484222325

func jsxMix(h, v uint64) uint64 {
	return (h ^ v) * jsxPrime
}

func jsxMixText[T string | []byte](h uint64, text T) uint64 {
	for i := range len(text) {
		h = (h ^ uint64(text[i])) * jsxPrime
	}

	return jsxMix(h, 0xff) //nolint:mnd // a byte no name holds, between names
}

// jsxCollector gathers a file's markup during the fingerprint walk.
type jsxCollector struct {
	ctx   *Ctx
	syn   *lang.JSXSyntax
	recs  []jsxRec
	holes []uint16
	until uint // the byte the outermost element last read ends at
}

// newJSXCollector reads no test file: the JSX pass leaves tests out, and
// their markup would only cost cache.
func newJSXCollector(ctx *Ctx) *jsxCollector {
	if ctx.Test {
		return &jsxCollector{ctx: ctx}
	}

	return &jsxCollector{ctx: ctx, syn: ctx.Lang.JSX()}
}

// visit reads one node of the walk: an element no element read before holds,
// with everything under it.
func (c *jsxCollector) visit(n *ts.Node, kind string) {
	if c.syn == nil || !c.syn.Elements[kind] || n.StartByte() < c.until {
		return
	}
	c.until = n.EndByte()
	holes := len(c.holes)
	if _, elems := c.element(n, -1); elems < jsxMinElems {
		c.holes = c.holes[:holes]
	}
}

// jsxAttr is one attribute of an opening tag, or an expression spread over it.
type jsxAttr struct {
	node *ts.Node // the value, or nil for a name alone
	name string
}

// jsxSpread is the name a spread expression is sorted and hashed under.
const jsxSpread = "..."

// attrs lists the attributes of an opening tag in the order of their names.
func (c *jsxCollector) attrs(open *ts.Node) []jsxAttr {
	var out []jsxAttr
	for i := range open.NamedChildCount() {
		a := open.NamedChild(i)
		switch kind := a.Kind(); {
		case c.syn.Attributes[kind]:
			if a.NamedChildCount() == 0 {
				continue
			}
			attr := jsxAttr{name: c.ctx.Text(a.NamedChild(0))}
			if a.NamedChildCount() > 1 {
				attr.node = a.NamedChild(1)
			}
			out = append(out, attr)
		case c.syn.Expressions[kind]:
			out = append(out, jsxAttr{name: jsxSpread, node: a})
		}
	}
	slices.SortStableFunc(out, func(a, b jsxAttr) int { return strings.Compare(a.name, b.name) })

	return out
}

// element hashes one element bottom-up and answers its shape and how many
// elements it holds, itself counted. A fragment counts none for itself, and
// neither does a part of a drawing. A drawing is one element and one hole,
// whatever it draws. An element of jsxMinElems or more is recorded, under the
// record that holds it.
func (c *jsxCollector) element(n *ts.Node, parent int) (uint64, int) {
	open := c.syn.Opening(n)
	tag := open.ChildByFieldName(c.syn.Name)
	var name []byte
	if tag != nil {
		name = c.ctx.Src[tag.StartByte():tag.EndByte()]
	}
	if c.syn.Opaque[string(name)] {
		c.holes = append(c.holes, jsxHole(jsxHoleText(jsxSeed, c.ctx.Src[n.StartByte():n.EndByte()])))

		return jsxMix(jsxMixText(jsxSeed, name), jsxMarkEnd), 1
	}
	at := len(c.recs)
	c.recs = append(c.recs, jsxRec{
		line: lineOf(n), end: int(n.EndPosition().Row) + 1, parent: parent, hole: len(c.holes),
	})
	h, elems := jsxMixText(jsxSeed, name), 0
	if tag != nil && !c.syn.Drawn[string(name)] {
		elems = 1
	}
	for _, a := range c.attrs(open) {
		h = jsxMixText(jsxMix(h, jsxMarkAttr), a.name)
		h, elems = c.value(a.node, at, h, elems)
	}
	h, elems = c.children(n, at, h, elems)
	h = jsxMix(h, jsxMarkEnd)
	if elems < jsxMinElems {
		c.recs = c.recs[:at]

		return h, elems
	}
	r := &c.recs[at]
	r.hash, r.elems, r.nholes = h, elems, len(c.holes)-r.hole

	return h, elems
}

// value reads what an attribute is set to. A name alone, a string, and an
// expression with no markup in it are one hole. An element, or an expression
// that wraps markup, adds the elements to the shape and the code around them
// as one hole.
func (c *jsxCollector) value(v *ts.Node, at int, h uint64, elems int) (uint64, int) {
	if v == nil {
		c.holes = append(c.holes, 0)

		return h, elems
	}
	if c.syn.Elements[v.Kind()] {
		c.holes = append(c.holes, 0)
		sub, n := c.element(v, at)

		return jsxMix(jsxMix(h, jsxMarkWrap), sub), elems + n
	}
	roots := c.roots(v, nil)
	if len(roots) == 0 {
		c.holes = append(c.holes, jsxHole(jsxHoleText(jsxSeed, c.ctx.Src[v.StartByte():v.EndByte()])))

		return h, elems
	}

	return c.wrapped(v, roots, at, h, elems)
}

// children reads what an element holds between its tags. Text and
// expressions with no markup in them, however many in a row, are one hole.
func (c *jsxCollector) children(n *ts.Node, at int, h uint64, elems int) (uint64, int) {
	run, open := jsxSeed, false
	flush := func() {
		if open {
			h = jsxMix(h, jsxMarkHole)
			c.holes = append(c.holes, jsxHole(run))
			run, open = jsxSeed, false
		}
	}
	from, to := c.syn.Inner(n)
	for i := range n.NamedChildCount() {
		child := n.NamedChild(i)
		if child.StartByte() < from || child.EndByte() > to {
			continue
		}
		kind := child.Kind()
		switch {
		case c.syn.Elements[kind]:
			flush()
			sub, more := c.element(child, at)
			h, elems = jsxMix(jsxMix(h, jsxMarkChild), sub), elems+more
		case c.syn.Text[kind], c.syn.Expressions[kind]:
			text := c.ctx.Src[child.StartByte():child.EndByte()]
			if c.blank(child, text) {
				continue
			}
			if roots := c.roots(child, nil); len(roots) > 0 {
				flush()
				h, elems = c.wrapped(child, roots, at, h, elems)

				continue
			}
			run, open = jsxHoleText(run, text), true
		}
	}
	flush()

	return h, elems
}

// blank reports whether a child writes nothing: text of white space only, or
// an expression that holds only a comment.
func (c *jsxCollector) blank(child *ts.Node, text []byte) bool {
	if !c.syn.Expressions[child.Kind()] {
		return len(bytes.TrimSpace(text)) == 0
	}
	for i := range child.NamedChildCount() {
		if !c.syn.Comments[child.NamedChild(i).Kind()] {
			return false
		}
	}

	return true
}

// roots lists the outermost elements under a node, in order.
func (c *jsxCollector) roots(n *ts.Node, out []*ts.Node) []*ts.Node {
	for i := range n.NamedChildCount() {
		child := n.NamedChild(i)
		if c.syn.Elements[child.Kind()] {
			out = append(out, child)

			continue
		}
		out = c.roots(child, out)
	}

	return out
}

// wrapped reads an expression that holds markup: the kind of the expression
// and its elements go to the shape, and the code around the elements is one
// hole.
func (c *jsxCollector) wrapped(expr *ts.Node, roots []*ts.Node, at int, h uint64, elems int) (uint64, int) {
	inner := expr
	for inner.NamedChildCount() == 1 && (c.syn.Expressions[inner.Kind()] || c.syn.Parens[inner.Kind()]) {
		inner = inner.NamedChild(0)
	}
	h = jsxMixText(jsxMix(h, jsxMarkWrap), inner.Kind())
	around, from := jsxSeed, expr.StartByte()
	for _, root := range roots {
		around = jsxHoleText(around, c.ctx.Src[from:root.StartByte()])
		from = root.EndByte()
	}
	c.holes = append(c.holes, jsxHole(jsxHoleText(around, c.ctx.Src[from:expr.EndByte()])))
	for _, root := range roots {
		sub, more := c.element(root, at)
		h, elems = jsxMix(h, sub), elems+more
	}

	return h, elems
}

// jsxHoleText adds text to the hash of what fills a hole, less its white
// space: the same value indented another way fills the hole alike.
func jsxHoleText(h uint64, text []byte) uint64 {
	for _, b := range text {
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			continue
		}
		h = (h ^ uint64(b)) * jsxPrime
	}

	return h
}

// jsxHole folds the hash of what fills a hole to the 16 bits kept of it.
func jsxHole(h uint64) uint16 {
	h ^= h >> 32 //nolint:mnd // fold the high half in
	h ^= h >> 16 //nolint:mnd // and the next quarter

	return uint16(h) //nolint:gosec // the low 16 bits are what is kept
}

// shapes packs what the walk gathered, or answers nil for a file with none.
func (c *jsxCollector) shapes() JSXShapes {
	if len(c.recs) == 0 {
		return nil
	}
	out := binary.AppendUvarint(nil, uint64(len(c.recs)))
	line, hole := 0, 0
	for i := range c.recs {
		r := &c.recs[i]
		back := 0
		if r.parent >= 0 {
			back = i - r.parent
		}
		for _, v := range []int{r.line - line, r.end - r.line, r.elems, back, r.hole - hole, r.nholes} {
			out = binary.AppendUvarint(out, uint64(max(v, 0)))
		}
		line, hole = r.line, r.hole
	}
	for i := range c.recs {
		out = binary.LittleEndian.AppendUint64(out, c.recs[i].hash)
	}
	for _, v := range c.holes {
		out = binary.LittleEndian.AppendUint16(out, v)
	}

	return out
}

// jsxFile is a file's markup unpacked.
type jsxFile struct {
	recs  []jsxRec
	holes []byte // jsxHoleBytes each
}

// decode unpacks a file's records. A payload cut short, or one whose records
// point past its holes or at no earlier record, answers none.
func (s JSXShapes) decode() jsxFile {
	r := &fragReader{rest: s}
	count := r.next()
	if r.bad || count > len(s) {
		return jsxFile{}
	}
	recs := make([]jsxRec, 0, count)
	line, hole := 0, 0
	for i := range count {
		dline, span, elems, back, dhole, nholes := r.next(), r.next(), r.next(), r.next(), r.next(), r.next()
		if r.bad || back > i {
			return jsxFile{}
		}
		line += dline
		hole += dhole
		recs = append(recs, jsxRec{
			line: line, end: line + span, elems: elems, parent: i - back, hole: hole, nholes: nholes,
		})
		if back == 0 {
			recs[i].parent = -1
		}
	}
	if len(r.rest) < count*jsxHashBytes {
		return jsxFile{}
	}
	for i := range recs {
		recs[i].hash = binary.LittleEndian.Uint64(r.rest[i*jsxHashBytes:])
	}
	holes := r.rest[count*jsxHashBytes:]
	for i := range recs {
		if (recs[i].hole+recs[i].nholes)*jsxHoleBytes > len(holes) {
			return jsxFile{}
		}
	}

	return jsxFile{recs: recs, holes: holes}
}

// holeAt answers what fills hole i of a record.
func (f *jsxFile) holeAt(r *jsxRec, i int) uint16 {
	return binary.LittleEndian.Uint16(f.holes[(r.hole+i)*jsxHoleBytes:])
}

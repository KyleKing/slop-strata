package structure

import (
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/kyleking/slop-strata/internal/lang"
)

// use is one place a function names a variable.
type use struct {
	at    uint // byte offset
	name  int  // index among the function's names
	read  bool
	write bool
}

// flow is every identifier of one function in source order, each marked as a
// read, a write, or both. It reads names only: no scopes and no types, so two
// variables of one name in two closures are one, a name the function never
// gives a value (a global, an import, a callee) is not a variable, and a write
// through a field or an index is a read of the name it starts from.
type flow struct {
	l     *lang.Language
	names map[string]int
	marks map[uint]uint8 // per identifier a binding reaches, by offset
	state []uint8        // scratch, per name
	src   []byte
	uses  []use
}

// Marks of an identifier a binding reaches.
const (
	markWrite  = 1
	markUpdate = 2 // read and written: `n += 1`, `i++`
)

// flowNames are the node kinds that name a variable.
var flowNames = map[string]bool{ //nolint:gochecknoglobals // fixed vocabulary
	"identifier": true, "shorthand_property_identifier": true, "shorthand_property_identifier_pattern": true,
}

// flowPatterns are the node kinds a binding is read through to the names
// under it. Anything else on the way, such as a field access or an index,
// ends the read: the name there is used, not given a value.
var flowPatterns = map[string]bool{ //nolint:gochecknoglobals // fixed vocabulary
	"expression_list": true, "pattern_list": true, "tuple_pattern": true, "list_pattern": true,
	"object_pattern": true, "array_pattern": true, "pair_pattern": true, "rest_pattern": true,
	"assignment_pattern": true, "object_assignment_pattern": true, "list_splat_pattern": true,
	"dictionary_splat_pattern": true, "as_pattern_target": true, "typed_parameter": true,
	"parenthesized_expression": true, "tuple": true, "list": true,
}

// flowMembers are the fields that hold a member's name where the grammar
// spells it as an identifier: Python's `obj.attr` and `f(key=value)`.
var flowMembers = map[string]string{ //nolint:gochecknoglobals // fixed vocabulary
	"attribute": "attribute", "keyword_argument": "name",
}

func newFlow(l *lang.Language, src []byte, fn *ts.Node) *flow {
	f := &flow{l: l, src: src, names: map[string]int{}, marks: map[uint]uint8{}}
	f.collect(fn)
	f.state = make([]uint8, len(f.names))

	return f
}

// collect records the identifiers of a subtree in source order.
func (f *flow) collect(node *ts.Node) {
	kind := node.Kind()
	if flowNames[kind] {
		f.record(node)

		return
	}
	field, binds := f.l.BindKinds[kind]
	member := flowMembers[kind]
	if binds {
		f.bind(node, kind, field)
	}
	for i := range node.ChildCount() {
		child := node.Child(i)
		if !child.IsNamed() || (member != "" && node.FieldNameForChild(uint32(i)) == member) {
			continue
		}
		f.collect(child)
	}
}

// record adds one identifier as a use.
func (f *flow) record(node *ts.Node) {
	text := lang.Text(node, f.src)
	id, ok := f.names[text]
	if !ok {
		id = len(f.names)
		f.names[text] = id
	}
	mark := f.marks[node.StartByte()]
	f.uses = append(f.uses, use{at: node.StartByte(), name: id, write: mark != 0, read: mark != markWrite})
}

// bind marks the names a node gives a value.
func (f *flow) bind(node *ts.Node, kind, field string) {
	mark := uint8(markWrite)
	if f.l.UpdateKinds[kind] {
		mark = markUpdate
	}
	if op := node.ChildByFieldName("operator"); op != nil {
		// `n += 1` reads n, and so does every other operator spelled with a
		// trailing "=". The "of" of a for loop is an operator too, and is not.
		if text := lang.Text(op, f.src); text != "=" && strings.HasSuffix(text, "=") {
			mark = markUpdate
		}
	}
	for i := range node.ChildCount() {
		child := node.Child(i)
		if child.IsNamed() && (field == lang.AllChildren || node.FieldNameForChild(uint32(i)) == field) {
			f.target(child, mark)
		}
	}
}

// target marks the names of one binding target.
func (f *flow) target(node *ts.Node, mark uint8) {
	kind := node.Kind()
	if flowNames[kind] {
		f.marks[node.StartByte()] = mark

		return
	}
	if !flowPatterns[kind] {
		return
	}
	for i := range node.NamedChildCount() {
		f.target(node.NamedChild(i), mark)
	}
}

// What across has seen of one name.
const (
	seenWriteBefore = 1 << iota
	seenInside
	seenReadFirst // the block's first use of the name reads it
	seenWriteInside
	seenAfter
	seenReadAfter // the first use after the block reads it
)

// across counts the variables that cross the two ends of a byte range: the
// ones written before it whose first use inside it is a read, and the ones
// written inside it whose first use after it is a read. A variable a loop
// carries from the end of the range back to code before it is not seen.
func (f *flow) across(start, end uint) (int, int) {
	clear(f.state)
	for i := range f.uses {
		u := &f.uses[i]
		st := &f.state[u.name]
		switch {
		case u.at < start:
			if u.write {
				*st |= seenWriteBefore
			}
		case u.at < end:
			noteInside(st, u)
		case *st&seenAfter == 0:
			*st |= seenAfter
			if u.read {
				*st |= seenReadAfter
			}
		}
	}
	inputs, outputs := 0, 0
	for _, st := range f.state {
		if st&seenWriteBefore != 0 && st&seenReadFirst != 0 {
			inputs++
		}
		if st&seenWriteInside != 0 && st&seenReadAfter != 0 {
			outputs++
		}
	}

	return inputs, outputs
}

// noteInside notes one use within the range.
func noteInside(st *uint8, u *use) {
	if *st&seenInside == 0 {
		*st |= seenInside
		if u.read {
			*st |= seenReadFirst
		}
	}
	if u.write {
		*st |= seenWriteInside
	}
}

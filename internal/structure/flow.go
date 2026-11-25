package structure

import (
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/kyleking/slop-strata/internal/lang"
)

// variable is one name a function declares, in the scope that declares it.
type variable struct {
	decl uint // byte offset of the name that declares it
	// self is true for a method's receiver and for Python's self and cls: the
	// block takes it along as a method and never as a parameter.
	self bool
}

// use is one place a function names a variable.
type use struct {
	at uint // byte offset
	v  int  // index among the flow's variables
	// depth is how many branches and loops the use sits under.
	depth uint16
	read  bool
	write bool
	// bare is true for a declaration that gives no value: `var x T`, `let x`.
	bare bool
}

// scope is the names one function, block, or comprehension declares.
type scope struct {
	names map[string]int
	// results are the named results a bare return reads.
	results []int
	// header is the byte range of the receiver, and of the results, of the
	// function that opens the scope.
	self, result byteSpan
	// fn is true for a function or a class: where ImplicitLocals puts a name.
	fn bool
}

// Names a scope holds that are not variables of it.
const (
	nameGlobal = -1 - iota // the module's: no variable at all
	nameOuter              // an enclosing function's: look further out
)

// flow is every use of every variable of one top-level function and the
// functions nested in it, in the order the code makes them: `x = x[1:]` reads
// x and then writes it, and a comprehension runs its loops before its body. A
// name resolves to the innermost scope that declares it, so a closure's uses
// of a variable of the function around it are uses of that variable, and two
// variables of one name in two blocks are two. A name no scope of the function
// declares (a global, an import, a callee) is not a variable. It has no types,
// and a write through a field or an index is a read of the name it starts
// from.
type flow struct {
	l      *lang.Language
	marks  map[uint]uint8 // per identifier a binding reaches, by offset
	src    []byte
	scopes []scope
	vars   []variable
	uses   []use
	state  []uint16 // scratch, per variable
	first  []uint16 // scratch, per variable: the depth of the block's first write
	depth  uint16   // of the node being read
}

// Marks of an identifier a binding reaches.
const (
	markWrite   = 1
	markUpdate  = 2 // read and written: `n += 1`, `i++`
	markDeclare = 3 // a new variable of the innermost scope
	markBare    = 4 // a new variable that is given no value
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
	"parenthesized_expression": true, "tuple": true, "list": true, "dotted_name": true,
}

// flowAside are the fields of a pattern that hold no name it binds: a
// parameter's type, and the default a pattern falls back to.
var flowAside = map[string]bool{"type": true, "right": true} //nolint:gochecknoglobals // fixed vocabulary

// flowMembers are the fields that hold a member's name where the grammar
// spells it as an identifier: Python's `obj.attr` and `f(key=value)`.
var flowMembers = map[string]string{ //nolint:gochecknoglobals // fixed vocabulary
	"attribute": "attribute", "keyword_argument": "name",
}

// fieldBody is the field a loop, a function, and a comprehension keep their
// body in.
const fieldBody = "body"

// fieldValue is the field a declaration keeps the value it gives in.
const fieldValue = "value"

// newFlow reads the uses of a top-level function and everything nested in it.
func newFlow(l *lang.Language, src []byte, fn *ts.Node) *flow {
	f := &flow{l: l, src: src, marks: map[uint]uint8{}}
	f.collect(fn)
	f.state = make([]uint16, len(f.vars))
	f.first = make([]uint16, len(f.vars))

	return f
}

// collect records the uses of a subtree.
func (f *flow) collect(node *ts.Node) {
	kind := node.Kind()
	if flowNames[kind] {
		f.record(node)

		return
	}
	if _, outer := f.l.OuterKinds[kind]; outer {
		return // read by locals, when the function was entered
	}
	if kind == kindReturn && node.NamedChildCount() == 0 {
		f.bareReturn(node.StartByte())
	}
	opened := f.open(node, kind)
	field, binds := f.l.BindKinds[kind]
	if binds {
		mark := f.bindMark(node, kind)
		f.targets(node, field, func(name *ts.Node) { f.marks[name.StartByte()] = mark })
	}
	nests := f.l.NestKinds[kind]
	if nests {
		f.depth++
	}
	f.children(node, kind, field, binds && field != lang.AllChildren, opened)
	if nests {
		f.depth--
	}
	if opened {
		f.scopes = f.scopes[:len(f.scopes)-1]
	}
}

// bindMark answers what a binding node does to the names it holds.
func (f *flow) bindMark(node *ts.Node, kind string) uint8 {
	switch declares := f.declares(node, kind); {
	case declares && f.l.BareDeclKinds[kind] && node.ChildByFieldName(fieldValue) == nil:
		return markBare
	case declares:
		return markDeclare
	case f.updates(node, kind):
		return markUpdate
	default:
		return markWrite
	}
}

// Where children reads one child of a node.
const (
	placeNow  = iota // in the order of the source
	placeSkip        // not at all: it names a member
	placeName        // after the value it is given
	placeBody        // after the names it holds in
)

// placement says when a node's children are read, by the field each is in.
type placement struct {
	member, field   string
	binds, bodyLast bool
}

// of answers where the child in a field goes. Once a child before it went to
// the body, which inBody says, the rest of a binding node follows.
func (p placement) of(name string, inBody bool) int {
	switch {
	case p.member != "" && name == p.member:
		return placeSkip
	case p.binds && name == p.field:
		return placeName
	case (p.binds || p.bodyLast) && name == fieldBody, p.binds && inBody:
		return placeBody
	default:
		return placeNow
	}
}

// children reads a node's children in the order they run: the names a
// binding gives a value after that value (`x := x + 1` reads the x outside)
// and before the body they hold in, and the body of a comprehension after the
// loops that declare its names.
func (f *flow) children(node *ts.Node, kind, field string, binds, opened bool) {
	p := placement{
		member: flowMembers[kind], field: field, binds: binds,
		bodyLast: opened && f.l.ImplicitLocals && f.l.ScopeKinds[kind],
	}
	if p.member == "" && !binds && !p.bodyLast {
		for i := range node.NamedChildCount() {
			f.collect(node.NamedChild(i))
		}

		return
	}
	var names, body []*ts.Node
	for i := range node.ChildCount() {
		child := node.Child(i)
		if !child.IsNamed() {
			continue
		}
		switch p.of(node.FieldNameForChild(uint32(i)), len(body) > 0) {
		case placeSkip:
		case placeName:
			names = append(names, child)
		case placeBody:
			body = append(body, child)
		default:
			f.collect(child)
		}
	}
	for _, child := range append(names, body...) {
		f.collect(child)
	}
}

// open starts the scope a node opens, if it opens one, and declares the names
// the scope holds from its first line.
func (f *flow) open(node *ts.Node, kind string) bool {
	fn := f.l.FunctionKinds[kind]
	class := f.l.ImplicitLocals && f.l.ClassKinds[kind]
	if !fn && !class && !f.l.ScopeKinds[kind] {
		return false
	}
	sc := scope{names: map[string]int{}, fn: fn || class}
	if fn {
		sc.self = fieldSpan(node, f.l.ReceiverField)
		sc.result = fieldSpan(node, f.l.ResultField)
	}
	f.scopes = append(f.scopes, sc)
	switch {
	case sc.fn && f.l.ImplicitLocals:
		f.locals(node)
	case len(f.l.HoistKinds) > 0:
		for i := range node.NamedChildCount() {
			child := node.NamedChild(i)
			if field, ok := f.l.HoistKinds[child.Kind()]; ok {
				f.hoist(child.ChildByFieldName(field))
			}
		}
	}

	return true
}

// fieldSpan is the byte range of a node's field, or an empty one.
func fieldSpan(node *ts.Node, field string) byteSpan {
	if field == "" {
		return byteSpan{}
	}
	child := node.ChildByFieldName(field)
	if child == nil {
		return byteSpan{}
	}

	return byteSpan{child.StartByte(), child.EndByte()}
}

// locals declares every name a function binds anywhere in its body, where an
// assignment is a declaration. Functions, classes, and comprehensions nested
// in it keep their own.
func (f *flow) locals(node *ts.Node) {
	sc := &f.scopes[len(f.scopes)-1]
	for i := range node.NamedChildCount() {
		child := node.NamedChild(i)
		kind := child.Kind()
		if field, ok := f.l.HoistKinds[kind]; ok {
			f.hoist(child.ChildByFieldName(field))

			continue
		}
		if global, ok := f.l.OuterKinds[kind]; ok {
			f.handOut(sc, child, global)

			continue
		}
		if f.l.FunctionKinds[kind] || f.l.ScopeKinds[kind] {
			continue
		}
		if field, ok := f.l.BindKinds[kind]; ok && !f.l.DeclKinds[kind] {
			f.targets(child, field, func(name *ts.Node) {
				if _, ok := sc.names[lang.Text(name, f.src)]; !ok {
					f.declare(name, false)
				}
			})
		}
		f.locals(child)
	}
}

// handOut marks the names of a global or nonlocal statement as ones the scope
// does not hold.
func (f *flow) handOut(sc *scope, stmt *ts.Node, global bool) {
	name := nameOuter
	if global {
		name = nameGlobal
	}
	for i := range stmt.NamedChildCount() {
		sc.names[lang.Text(stmt.NamedChild(i), f.src)] = name
	}
}

// hoist declares the name of a nested function or class in the scope around
// it, and marks the name as the write that it is.
func (f *flow) hoist(name *ts.Node) {
	if name == nil {
		return
	}
	if _, ok := f.scopes[len(f.scopes)-1].names[lang.Text(name, f.src)]; !ok {
		f.declare(name, false)
	}
	f.marks[name.StartByte()] = markWrite
}

// declare adds a variable to the innermost scope and answers its index.
func (f *flow) declare(name *ts.Node, param bool) int {
	sc := &f.scopes[len(f.scopes)-1]
	text := lang.Text(name, f.src)
	at := name.StartByte()
	id := len(f.vars)
	f.vars = append(f.vars, variable{
		decl: at,
		self: inSpan(sc.self, at) || (param && sc.fn && f.l.SelfNames[text]),
	})
	sc.names[text] = id
	if inSpan(sc.result, at) {
		sc.results = append(sc.results, id)
	}

	return id
}

func inSpan(s byteSpan, at uint) bool { return at >= s.start && at < s.end }

// resolve answers the variable a name stands for at this point of the walk.
func (f *flow) resolve(text string) (int, bool) {
	for i := len(f.scopes) - 1; i >= 0; i-- {
		switch id, ok := f.scopes[i].names[text]; {
		case !ok || id == nameOuter:
		case id == nameGlobal:
			return 0, false
		default:
			return id, true
		}
	}

	return 0, false
}

// record adds one identifier as a use of the variable it resolves to.
func (f *flow) record(node *ts.Node) {
	at := node.StartByte()
	mark := f.marks[at]
	text := lang.Text(node, f.src)
	id, ok := f.variable(node, text, mark >= markDeclare)
	if !ok {
		return
	}
	f.uses = append(f.uses, use{
		at: at, v: id, depth: f.depth, write: mark != 0, read: mark == 0 || mark == markUpdate, bare: mark == markBare,
	})
}

// variable answers the variable an identifier stands for, a new one of the
// innermost scope when the identifier declares.
func (f *flow) variable(node *ts.Node, text string, declares bool) (int, bool) {
	if !declares {
		return f.resolve(text)
	}
	id, ok := f.scopes[len(f.scopes)-1].names[text]
	switch at := node.StartByte(); {
	case !ok || id < 0:
		id = f.declare(node, true)
	case at < f.vars[id].decl:
		// locals met a later assignment to this parameter first.
		f.vars[id].decl = at
	}

	return id, true
}

// bareReturn reads the named results of the function a bare return leaves.
func (f *flow) bareReturn(at uint) {
	for i := len(f.scopes) - 1; i >= 0; i-- {
		if !f.scopes[i].fn {
			continue
		}
		for _, id := range f.scopes[i].results {
			f.uses = append(f.uses, use{at: at, v: id, depth: f.depth, read: true})
		}

		return
	}
}

// declares reports whether a binding node makes new variables.
func (f *flow) declares(node *ts.Node, kind string) bool {
	if f.l.DeclKinds[kind] {
		return true
	}
	if len(f.l.DeclTokens) == 0 {
		return false
	}
	for i := range node.ChildCount() {
		if child := node.Child(i); !child.IsNamed() && f.l.DeclTokens[child.Kind()] {
			return true
		}
	}

	return false
}

// updates reports whether a binding node reads the names it writes.
func (f *flow) updates(node *ts.Node, kind string) bool {
	if f.l.UpdateKinds[kind] {
		return true
	}
	op := node.ChildByFieldName("operator")
	if op == nil {
		return false
	}
	// `n += 1` reads n, and so does every other operator spelled with a
	// trailing "=". The "of" of a for loop is an operator too, and is not.
	text := lang.Text(op, f.src)

	return text != "=" && strings.HasSuffix(text, "=")
}

// targets calls fn for each name a binding node gives a value.
func (f *flow) targets(node *ts.Node, field string, fn func(*ts.Node)) {
	for i := range node.ChildCount() {
		child := node.Child(i)
		if child.IsNamed() && (field == lang.AllChildren || node.FieldNameForChild(uint32(i)) == field) {
			f.target(child, fn)
		}
	}
}

// target calls fn for the names of one binding target.
func (f *flow) target(node *ts.Node, fn func(*ts.Node)) {
	kind := node.Kind()
	if flowNames[kind] {
		fn(node)

		return
	}
	if !flowPatterns[kind] {
		return
	}
	for i := range node.ChildCount() {
		if child := node.Child(i); child.IsNamed() && !flowAside[node.FieldNameForChild(uint32(i))] {
			f.target(child, fn)
		}
	}
}

// What across has seen of one variable.
const (
	seenInside    = 1 << iota
	seenReadFirst // the block's first use of it reads it
	seenWriteInside
	seenLateRead // the block reads it under fewer branches than it first wrote it
	seenValue    // it is given a value before the block
	seenAfter
	seenReadAfter // the first use after the block reads it
	seenLoopRead  // a loop around the block reads it outside the block
	seenRebound   // the innermost loop around the block gives it a new value on every pass
)

// crossing is where a block sits: its byte range, the function it is in, and
// the loops of that function around it, outermost first.
type crossing struct {
	loops      []loopSpan
	start, end uint
	fn         byteSpan
	// ends is true when the block always leaves the function, so that no
	// later line of it sees what the block wrote.
	ends bool
}

// loopSpan is one loop: the whole statement, and the body it repeats. A loop
// over the items of something, which binds its variables anew on every pass,
// has each set.
type loopSpan struct {
	stmt, body byteSpan
	each       bool
}

// across counts the variables that cross the ends of a block. An input is
// declared outside the block and read by the block's first use of it, or
// given a value before the block and read in it under fewer branches than
// the block first writes it, which is a write some path skips. An output is
// written in the block and then read by the first use after it, or kept by a
// loop around the block (declared outside the loop's body and read in the
// loop, unless that loop binds it anew on every pass), or declared outside the
// function the block is in.
func (f *flow) across(c *crossing) (int, int) {
	clear(f.state)
	for i := range f.uses {
		u := &f.uses[i]
		st := &f.state[u.v]
		switch {
		case u.at < c.start:
			f.noteBefore(st, u, c)
		case u.at < c.end:
			f.noteInside(st, u)
		default:
			f.noteAfter(st, u, c)
		}
	}
	inputs, outputs := 0, 0
	for id, st := range f.state {
		v := &f.vars[id]
		if st&seenInside == 0 || v.self {
			continue
		}
		inside := v.decl >= c.start && v.decl < c.end
		if !inside && (st&seenReadFirst != 0 || st&(seenLateRead|seenValue) == seenLateRead|seenValue) {
			inputs++
		}
		if st&seenWriteInside != 0 && outlives(v, st, c, inside) {
			outputs++
		}
	}

	return inputs, outputs
}

// outlives reports whether a variable the block writes is read with that
// value once the block is done: by a later line of the function, by the next
// pass of a loop around the block, or by a function around this one.
func outlives(v *variable, st uint16, c *crossing, inside bool) bool {
	switch {
	case !inSpan(c.fn, v.decl):
		return true
	case c.ends:
		return false
	case st&seenReadAfter != 0:
		return true
	case inside || st&seenRebound != 0:
		return false
	case st&seenLoopRead != 0:
		return true
	}
	if n := len(c.loops); n > 0 && st&seenReadFirst != 0 {
		return !inSpan(c.loops[n-1].body, v.decl)
	}

	return false
}

// noteBefore notes one use ahead of the block: a value the variable has when
// the block starts, and a loop around the block that gives it a new one on
// every pass.
func (f *flow) noteBefore(st *uint16, u *use, c *crossing) {
	if u.write && !u.bare {
		*st |= seenValue
	}
	if n := len(c.loops); u.write && n > 0 && c.loops[n-1].each &&
		inSpan(c.loops[n-1].stmt, u.at) && !inSpan(c.loops[n-1].body, u.at) {
		*st |= seenRebound
	}
	f.noteLoop(st, u, c)
}

// noteAfter notes one use behind the block: whether the first of them reads
// what the block left.
func (f *flow) noteAfter(st *uint16, u *use, c *crossing) {
	if *st&seenAfter == 0 {
		*st |= seenAfter
		if u.read {
			*st |= seenReadAfter
		}
	}
	f.noteLoop(st, u, c)
}

// noteLoop notes a read outside the block that a loop around the block makes
// of a variable declared outside that loop's body.
func (f *flow) noteLoop(st *uint16, u *use, c *crossing) {
	if !u.read || *st&seenLoopRead != 0 {
		return
	}
	for i := range c.loops {
		if inSpan(c.loops[i].stmt, u.at) && !inSpan(c.loops[i].body, f.vars[u.v].decl) {
			*st |= seenLoopRead

			return
		}
	}
}

// noteInside notes one use within the range.
func (f *flow) noteInside(st *uint16, u *use) {
	switch {
	case *st&seenInside == 0:
		*st |= seenInside
		if u.read {
			*st |= seenReadFirst
		} else {
			f.first[u.v] = u.depth
		}
	case u.read && *st&seenReadFirst == 0 && u.depth < f.first[u.v]:
		*st |= seenLateRead
	}
	if u.write {
		*st |= seenWriteInside
	}
}

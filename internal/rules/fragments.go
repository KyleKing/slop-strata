package rules

import (
	"cmp"
	"encoding/binary"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/kyleking/slop-strata/internal/lang"
)

// Fragments is a file's code as a sequence of statements, which the duplicate
// pass aligns across files. A statement is one entry of a block: its record
// covers the tokens it owns, and the statements of a block nested in it follow
// as records of their own, so `if x {` is one record and each line of its body
// another. Comments, docstrings, imports, and a Go package clause are left out.
// A statement with statements nested in it is compound. A compound statement
// that declares a function, a class, or a type is a header, and so is a
// signature with no body, such as an overload: the pass aligns a header and
// never counts it. A statement that only passes a function along, as a
// callback or a value, is not one.
//
// The bytes are packed, because a tree holds a few hundred thousand statements
// and the pass reads only two hashes of most. In order: a count; every shape
// hash, then every exact hash, four bytes each; the length of the records;
// per statement its line as a delta, its token count shifted over the compound
// and header bits, and its slots as indexes into the names; then the names,
// counted, each with its length. A slot is an identifier or a literal, the
// tokens a shape hash leaves out. Everything but the hashes is a varint.
type Fragments []byte

// fragStmt is one decoded statement.
type fragStmt struct {
	slots    []uint32 // indexes into Fragments.Names, in token order
	line     int
	ntok     int
	shape    uint32 // hash of the tokens with every slot replaced by its class
	exact    uint32 // hash of the tokens as written
	compound bool   // holds nested statements
	header   bool   // a signature: it names a declaration, and may open its body
}

// maxSlotText caps the bytes of a slot's text kept for display. A longer text
// keeps a prefix and a hash, so two long literals still compare by content.
const maxSlotText = 32

// slotClip marks where a long slot text was cut.
const slotClip = "…"

// hashWidth is the bytes one packed hash takes.
const hashWidth = 4

// Slot classes in a shape hash.
const (
	shapeIdent   = 1
	shapeLiteral = 2
)

// fragLabelKinds are the leaf kinds a shape hash abstracts as an identifier
// beside the ones a reference is read from.
var fragLabelKinds = map[string]bool{ //nolint:gochecknoglobals // fixed vocabulary
	"package_identifier": true, "label_name": true, "statement_identifier": true,
	"private_property_identifier": true,
}

// fragContainers are the node kinds whose named children are statements.
var fragContainers = map[string]map[string]bool{ //nolint:gochecknoglobals // fixed vocabulary
	lang.Go.ID:     {"source_file": true, "statement_list": true, "field_declaration_list": true},
	lang.Python.ID: {"module": true, kindBlock: true},
	lang.TypeScript.ID: {
		"program": true, kindStatementBlock: true, "class_body": true, "object_type": true,
		"interface_body": true, "enum_body": true, kindSwitchCase: true, kindSwitchDefault: true,
	},
}

// fragPreamble are the statements every file opens with, left out like imports.
var fragPreamble = map[string]bool{ //nolint:gochecknoglobals // fixed vocabulary
	"package_clause": true, "future_import_statement": true,
}

// fragWrappers are the node kinds a declaration sits directly under: an
// export, a decorator, and `const f = () => {}`.
var fragWrappers = map[string]bool{ //nolint:gochecknoglobals // fixed vocabulary
	kindExportStatement: true, "decorated_definition": true, "lexical_declaration": true,
	"variable_declaration": true, "variable_declarator": true, "public_field_definition": true,
}

// fragTypeDecls are the declarations whose members are statements: the line
// that names one is a header, as a function's is.
var fragTypeDecls = map[string]bool{ //nolint:gochecknoglobals // fixed vocabulary
	kindTypeDecl: true, kindInterfaceDecl: true, "enum_declaration": true, "type_alias_declaration": true,
	"internal_module": true,
}

// fragSignatures are the statements that are a signature and nothing else.
var fragSignatures = map[string]bool{ //nolint:gochecknoglobals // fixed vocabulary
	"function_signature": true, "method_signature": true, "abstract_method_signature": true,
	"construct_signature": true, "call_signature": true,
}

// fragClauses are the node kinds that open a statement wherever they sit: the
// arms of a compound statement, which no block holds.
var fragClauses = map[string]bool{ //nolint:gochecknoglobals // fixed vocabulary
	"expression_case": true, "default_case": true, "type_case": true, "communication_case": true,
	"elif_clause": true, "else_clause": true, "except_clause": true, "except_group_clause": true,
	"finally_clause": true, "case_clause": true, "catch_clause": true,
	kindSwitchCase: true, kindSwitchDefault: true,
}

const (
	kindSwitchCase    = "switch_case"
	kindSwitchDefault = "switch_default"
	kindIfStatement   = "if_statement"
)

// Flags of a node kind, read once per grammar.
const (
	fkNamed     = 1 << iota
	fkIdent     // a slot: an identifier
	fkLiteral   // a slot: a literal, taken whole
	fkSilent    // a comment, an import, or a package clause, left out with all under it
	fkClause    // opens a statement wherever it sits
	fkContainer // its named children are statements
	fkArm       // a container whose first children are the arm's own
	fkStmtName  // named like a statement or a declaration
	fkExprStmt  // an expression statement, which may be a docstring
	fkIf        // an if statement, which chains under another in Go
	fkString    // a string, which is a docstring when a statement is only it
	fkScope     // a function, a class, or a type declaration
	fkSignature // a signature with no body
	fkWrap      // holds what it declares as a direct child
)

// fragKind is one node kind as the walk reads it.
type fragKind struct {
	name  string
	flags uint16
}

// fragKinds caches each grammar's kinds, with their flags, by kind id.
var fragKinds sync.Map //nolint:gochecknoglobals // a cache of fixed vocabularies, keyed by language

// kindsOf answers a grammar's kinds with their flags, built on first use.
func kindsOf(l *lang.Language) []fragKind {
	if cached, ok := fragKinds.Load(l); ok {
		return cached.([]fragKind) //nolint:errcheck,forcetypeassert // only this function stores
	}
	id := l.ID
	if l == lang.TSX {
		id = lang.TypeScript.ID
	}
	containers := fragContainers[id]
	kinds := l.Kinds()
	out := make([]fragKind, len(kinds))
	for i, k := range kinds {
		out[i] = fragKind{name: k.Name, flags: kindFlags(l, k, containers)}
	}
	fragKinds.Store(l, out)

	return out
}

// kindFlags reads one kind's flags off the vocabularies.
func kindFlags(l *lang.Language, k lang.Kind, containers map[string]bool) uint16 {
	if !k.Named {
		return 0
	}
	flags := uint16(fkNamed)
	set := func(on bool, flag uint16) {
		if on {
			flags |= flag
		}
	}
	name := k.Name
	set(referenceKinds[name] || fragLabelKinds[name], fkIdent)
	set(l.LiteralKinds[name], fkLiteral)
	set(l.CommentKinds[name] || importStmtKinds[name] || fragPreamble[name], fkSilent)
	set(fragClauses[name], fkClause)
	set(containers[name], fkContainer)
	set(name == kindSwitchCase || name == kindSwitchDefault, fkArm)
	set(strings.HasSuffix(name, "_statement") || strings.HasSuffix(name, "_declaration"), fkStmtName)
	set(name == kindExpressionStmt, fkExprStmt)
	set(name == kindIfStatement, fkIf)
	set(name == kindString || name == "concatenated_string", fkString)
	set(l.FunctionKinds[name] || l.ClassKinds[name] || fragTypeDecls[name], fkScope)
	set(fragSignatures[name], fkSignature)
	set(fragWrappers[name], fkWrap)

	return flags
}

// openStmt is a statement the walk is inside of.
type openStmt struct {
	slots []uint32
	index int
	ntok  int
	shape uint32
	exact uint32
	scope bool // the statement declares a function or a class
}

// fragCollector builds a file's Fragments during the fingerprint walk.
type fragCollector struct {
	ctx   *Ctx
	names map[string]uint32
	kinds []fragKind
	uses  []int
	texts []string
	stmts []fragStmt
	open  []openStmt
}

func newFragCollector(ctx *Ctx) *fragCollector {
	return &fragCollector{ctx: ctx, kinds: kindsOf(ctx.Lang), names: map[string]uint32{}}
}

// walk visits the subtree, calling visit on the named nodes in the order
// lang.EachNamed would, and feeding each token, named or not, to the statement
// that owns it. A fragWalk carries what a node inherits from its parent.
func (c *fragCollector) walk(n *ts.Node, in fragWalk, visit func(*ts.Node, string)) {
	kind := &fragKind{name: "ERROR", flags: fkNamed} // the one kind a grammar does not list
	if id := int(n.KindId()); id < len(c.kinds) {
		kind = &c.kinds[id]
	}
	if kind.flags&fkNamed == 0 {
		if !in.muted && len(c.open) > 0 {
			c.open[len(c.open)-1].word(kind.name) // an anonymous node's kind is its text
		}

		return
	}
	visit(n, kind.name)
	children := n.ChildCount()
	down, opened := c.enter(n, kind, in, children == 0)
	for i := range children {
		c.walk(n.Child(i), down, visit)
	}
	if opened {
		c.close()
	}
}

// enter takes a named node into account before its children: it may open a
// statement, mark the one it sits in as a declaration, or be a token. It
// answers what the children inherit and whether a statement was opened.
func (c *fragCollector) enter(n *ts.Node, kind *fragKind, in fragWalk, leaf bool) (fragWalk, bool) {
	down := fragWalk{parent: kind.flags, muted: in.muted}
	if in.muted {
		return down, false
	}
	if kind.flags&fkSilent != 0 || c.docstring(n, kind.flags) {
		down.muted = true

		return down, false
	}
	opened := opens(kind.flags, in.parent)
	if opened {
		if len(c.open) > 0 {
			outer := &c.open[len(c.open)-1]
			c.stmts[outer.index].compound = true
			c.stmts[outer.index].header = c.stmts[outer.index].header || outer.scope
		}
		c.open = append(c.open, openStmt{index: len(c.stmts), shape: fnvOffset, exact: fnvOffset})
		c.stmts = append(c.stmts, fragStmt{line: lineOf(n)})
	}
	if len(c.open) == 0 {
		return down, false
	}
	if own := &c.open[len(c.open)-1]; opened || in.direct {
		own.scope = own.scope || kind.flags&fkScope != 0
		c.stmts[own.index].header = c.stmts[own.index].header || kind.flags&fkSignature != 0
	}
	down.muted = c.token(n, kind, leaf)
	down.direct = opened || (in.direct && kind.flags&fkWrap != 0)

	return down, opened
}

// fragWalk is what the walk hands from a node to its children: the node's
// flags, whether the subtree is left out, and whether the child is the
// statement's own declaration, reached through nothing but wrappers.
type fragWalk struct {
	parent uint16
	muted  bool
	direct bool
}

// docstring reports whether a node is a statement holding only a string.
func (c *fragCollector) docstring(n *ts.Node, flags uint16) bool {
	if flags&fkExprStmt == 0 || n.NamedChildCount() != 1 {
		return false
	}

	id := int(n.NamedChild(0).KindId())

	return id < len(c.kinds) && c.kinds[id].flags&fkString != 0
}

// opens reports whether a named node starts a statement of its own, from its
// flags and its parent's.
func opens(flags, parent uint16) bool {
	if flags&fkClause != 0 || (flags&fkIf != 0 && parent&fkIf != 0) {
		return true
	}
	if parent&fkContainer == 0 {
		return false
	}

	return parent&fkArm == 0 || flags&fkStmtName != 0
}

// token feeds one named node to the innermost open statement when it is a
// token: a literal, taken whole, or a leaf. It reports whether the node's
// children are already accounted for.
func (c *fragCollector) token(n *ts.Node, kind *fragKind, leaf bool) bool {
	st := &c.open[len(c.open)-1]
	literal := kind.flags&fkLiteral != 0
	if !literal && !leaf {
		return false
	}
	text := c.ctx.Src[n.StartByte():n.EndByte()]
	switch {
	case len(text) == 0:
	case literal:
		st.slot(c, text, shapeLiteral)
	case kind.flags&fkIdent != 0:
		st.slot(c, text, shapeIdent)
	default:
		st.word(string(text))
	}

	return literal
}

// FNV-1a, 32 bits, inlined so a token costs no allocation.
const (
	fnvOffset = 2166136261
	fnvPrime  = 16777619
)

func fnvAdd[T string | []byte](h uint32, s T) uint32 {
	for i := range len(s) {
		h = (h ^ uint32(s[i])) * fnvPrime
	}

	return (h ^ 0xff) * fnvPrime //nolint:mnd // a byte no token holds, between tokens
}

// word adds a token both hashes read as written.
func (st *openStmt) word(text string) {
	st.ntok++
	st.shape = fnvAdd(st.shape, text)
	st.exact = fnvAdd(st.exact, text)
}

// slot adds an identifier or literal: its text to the exact hash, its class
// alone to the shape hash.
func (st *openStmt) slot(c *fragCollector, text []byte, class byte) {
	st.ntok++
	st.shape = (st.shape ^ uint32(class)) * fnvPrime
	st.exact = fnvAdd(st.exact, text)
	st.slots = append(st.slots, c.intern(text))
}

// intern numbers a slot text within the file.
func (c *fragCollector) intern(text []byte) uint32 {
	if len(text) > maxSlotText {
		text = clipSlot(text)
	}
	id, ok := c.names[string(text)]
	if !ok {
		id = uint32(len(c.texts)) //nolint:gosec // a file holds far fewer names than 2^32
		c.texts = append(c.texts, string(text))
		c.names[c.texts[id]] = id
		c.uses = append(c.uses, 0)
	}
	c.uses[id]++

	return id
}

// clipSlot shortens a long slot text to a prefix and a hash of the whole.
func clipSlot(text []byte) []byte {
	sum := fnvAdd(fnvOffset, text)
	cut := maxSlotText
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	out := append(slices.Clone(text[:cut]), slotClip...)

	return strconv.AppendUint(out, uint64(sum), 16)
}

// close finishes the innermost open statement.
func (c *fragCollector) close() {
	st := c.open[len(c.open)-1]
	c.open = c.open[:len(c.open)-1]
	rec := &c.stmts[st.index]
	rec.ntok, rec.shape, rec.exact, rec.slots = st.ntok, st.shape, st.exact, st.slots
}

// fragments packs what the walk gathered, or answers nil for a file with no
// statement. Names are ordered by use, most used first, so the common ones
// pack into one byte.
func (c *fragCollector) fragments() Fragments {
	stmts := c.stmts[:0]
	for i := range c.stmts {
		if c.stmts[i].ntok > 0 {
			stmts = append(stmts, c.stmts[i])
		}
	}
	if len(stmts) == 0 {
		return nil
	}
	order := make([]uint32, len(c.texts))
	for i := range order {
		order[i] = uint32(i)
	}
	slices.SortStableFunc(order, func(a, b uint32) int { return cmp.Compare(c.uses[b], c.uses[a]) })
	rank := make([]uint32, len(order))
	names := make([]string, len(order))
	for r, id := range order {
		rank[id] = uint32(r)
		names[r] = c.texts[id]
	}
	for i := range stmts {
		for j, id := range stmts[i].slots {
			stmts[i].slots[j] = rank[id]
		}
	}

	return packFragments(stmts, names)
}

// packFragments lays statements and names out in the format Fragments documents.
func packFragments(stmts []fragStmt, names []string) Fragments {
	out := binary.AppendUvarint(nil, uint64(len(stmts)))
	for i := range stmts {
		out = binary.LittleEndian.AppendUint32(out, stmts[i].shape)
	}
	for i := range stmts {
		out = binary.LittleEndian.AppendUint32(out, stmts[i].exact)
	}
	var recs []byte
	line := 0
	for i := range stmts {
		s := &stmts[i]
		recs = binary.AppendUvarint(recs, uint64(s.line-line)) //nolint:gosec // lines only rise
		recs = binary.AppendUvarint(recs, packCount(s))
		recs = binary.AppendUvarint(recs, uint64(len(s.slots)))
		for _, id := range s.slots {
			recs = binary.AppendUvarint(recs, uint64(id))
		}
		line = s.line
	}
	out = append(binary.AppendUvarint(out, uint64(len(recs))), recs...)
	out = binary.AppendUvarint(out, uint64(len(names)))
	for _, name := range names {
		out = append(binary.AppendUvarint(out, uint64(len(name))), name...)
	}

	return out
}

// Bits under a packed token count.
const (
	packHeader   = 1
	packCompound = 2
	packBits     = 2
)

// packCount folds a statement's token count and its two bits into one number.
func packCount(s *fragStmt) uint64 {
	n := uint64(s.ntok) << packBits //nolint:gosec // a count
	if s.header {
		n |= packHeader
	}
	if s.compound {
		n |= packCompound
	}

	return n
}

// hashes answers the packed shape hashes, the packed exact hashes, and what
// follows them, without decoding it. A payload cut short answers none.
func (f Fragments) hashes() ([]byte, []byte, []byte) {
	n, used := binary.Uvarint(f)
	if used <= 0 || n > uint64(len(f)) || len(f)-used < int(n)*hashWidth*2 { //nolint:gosec // bounded above
		return nil, nil, nil
	}
	size := int(n) * hashWidth //nolint:gosec // bounded by the payload's length
	body := f[used:]

	return body[:size], body[size : 2*size], body[2*size:]
}

// hashAt reads the i-th packed hash.
func hashAt(packed []byte, i int) uint32 {
	return binary.LittleEndian.Uint32(packed[i*hashWidth:])
}

// fragReader reads varints off a payload and remembers running out.
type fragReader struct {
	rest []byte
	bad  bool
}

func (r *fragReader) next() int {
	v, used := binary.Uvarint(r.rest)
	if used <= 0 || v > math.MaxInt32 {
		r.bad = true

		return 0
	}
	r.rest = r.rest[used:]

	return int(v)
}

// decode unpacks every statement and the names their slots index. A payload
// cut short answers the statements it held whole, and no statement points
// past the names.
func (f Fragments) decode() ([]fragStmt, []string) {
	shapes, exacts, rest := f.hashes()
	r := &fragReader{rest: rest}
	size := r.next()
	if r.bad || size > len(r.rest) {
		return nil, nil
	}
	names := decodeNames(r.rest[size:])
	r.rest = r.rest[:size]
	out := make([]fragStmt, 0, len(shapes)/hashWidth)
	line := 0
	for i := range len(shapes) / hashWidth {
		delta, count, nslots := r.next(), r.next(), r.next()
		if r.bad || nslots > len(r.rest) {
			break
		}
		line += delta
		st := fragStmt{
			line: line, ntok: count >> packBits, header: count&packHeader != 0, compound: count&packCompound != 0,
			shape: hashAt(shapes, i), exact: hashAt(exacts, i), slots: make([]uint32, 0, nslots),
		}
		for range nslots {
			id := r.next()
			if r.bad || id >= len(names) {
				return out, names
			}
			st.slots = append(st.slots, uint32(id)) //nolint:gosec // checked against the names
		}
		out = append(out, st)
	}

	return out, names
}

// decodeNames unpacks the names section.
func decodeNames(section []byte) []string {
	r := &fragReader{rest: section}
	count := r.next()
	names := make([]string, 0, min(count, len(section)))
	for range count {
		size := r.next()
		if r.bad || size > len(r.rest) {
			break
		}
		names = append(names, string(r.rest[:size]))
		r.rest = r.rest[size:]
	}

	return names
}

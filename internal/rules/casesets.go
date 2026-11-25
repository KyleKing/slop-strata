package rules

import (
	"encoding/binary"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/kyleking/slop-strata/internal/lang"
)

// CaseSets is the places of a file that each enumerate a set of cases: a
// switch or match, an if chain that tests one subject against constants, and
// a literal table keyed by the cases. The case pass joins them across files
// by the cases they share.
//
// A case is kept as the text a reader would know it by: a string in double
// quotes, a number as written, a name, or the last two parts of a dotted name
// (`Color.RED`, `token.ADD`, `ast.Ident`). What says nothing about a set is
// left out when the file is read: true, false, and the null of each language,
// a number under caseMinNumber, a string under caseMinString bytes, and a
// type every program tests for. A place with fewer than caseMinKeys cases
// left is not recorded.
//
// The bytes are packed as varints: a count, then per place its line as a
// delta, the lines it spans past its first, its kind shifted over the default
// bit, and its cases as indexes into the names, in the order of their text;
// then the names, counted, each with its length.
type CaseSets []byte

// Kinds of a place that enumerates cases.
const (
	caseKindSwitch = iota // a switch, a type switch, or a match
	caseKindIf            // an if chain
	caseKindTable         // a literal table
)

// caseKindNames are the kinds as a result spells them.
var caseKindNames = [...]string{"switch", "if", "table"} //nolint:gochecknoglobals // fixed vocabulary

// caseMinKeys is the fewest cases a place holds to be recorded, and the
// fewest two places share to be joined.
const caseMinKeys = 3

// caseMinArms is the fewest arms of a switch and branches of an if chain.
const caseMinArms = 3

// caseMinEntries is the fewest entries of a table.
const caseMinEntries = 4

// caseMaxKeys is the most cases a place holds. Past it the place is data.
const caseMaxKeys = 256

// caseMinString and caseMaxString bound the bytes of a string that is a
// case: a shorter one is a separator or a flag, a longer one is prose.
const (
	caseMinString = 2
	caseMaxString = 60
)

// caseMinNumber is the least magnitude of a number that is a case.
const caseMinNumber = 10

// caseShareDen is what the shares of a table's entries are counted over.
const caseShareDen = 5

// caseDispatchNum over caseShareDen is the share of a table's values that
// must be functions, or of its keys that must be named constants, for the
// table to enumerate cases and not hold data.
const caseDispatchNum = 4

// caseRecordNum over caseShareDen is the share of entries that copy a field
// of the same name at which a table is a record.
const caseRecordNum = 2

// caseRec is one place that enumerates cases.
type caseRec struct {
	keys []string // sorted, each once
	line int
	end  int
	kind int
	def  bool // it has an arm for every other case
}

// caseTest is one test of a condition: a subject compared against a case.
type caseTest struct {
	subject string
	key     string
}

// caseCollector gathers a file's case sets during the fingerprint walk.
type caseCollector struct {
	ctx  *Ctx
	syn  *lang.CaseSyntax
	recs []caseRec
}

// newCaseCollector reads no test file: the case pass leaves tests out, and a
// test's tables of inputs would only cost cache.
func newCaseCollector(ctx *Ctx) *caseCollector {
	if ctx.Test {
		return &caseCollector{ctx: ctx}
	}

	return &caseCollector{ctx: ctx, syn: ctx.Lang.Cases()}
}

// visit reads one node of the walk.
func (c *caseCollector) visit(n *ts.Node, kind string) {
	if c.syn == nil {
		return
	}
	switch {
	case c.syn.Switches[kind]:
		c.switchStmt(n)
	case kind == c.syn.If:
		c.ifChain(n)
	default:
		if _, ok := c.syn.Tables[kind]; ok {
			c.table(n)
		}
	}
}

// add records a place when enough cases are left of it.
func (c *caseCollector) add(n *ts.Node, kind int, def bool, keys []string) {
	slices.Sort(keys)
	keys = slices.Compact(keys)
	if len(keys) < caseMinKeys || len(keys) > caseMaxKeys {
		return
	}
	c.recs = append(c.recs, caseRec{
		keys: keys, line: lineOf(n), end: int(n.EndPosition().Row) + 1, kind: kind, def: def,
	})
}

// unwrap reads through parentheses, a computed key, and a pointer type.
func (c *caseCollector) unwrap(n *ts.Node) *ts.Node {
	for n != nil && (c.syn.Parens[n.Kind()] || c.syn.Computed[n.Kind()] || c.syn.Pointers[n.Kind()]) {
		if n.NamedChildCount() != 1 {
			return n
		}
		n = n.NamedChild(0)
	}

	return n
}

// key answers the case a node names, or false when it names none worth
// keeping. Typ says the node stands where a type does.
func (c *caseCollector) key(n *ts.Node, typ bool) (string, bool) {
	n = c.unwrap(n)
	if n == nil {
		return "", false
	}
	kind := n.Kind()
	switch {
	case c.syn.Strings[kind]:
		return c.stringKey(n)
	case c.syn.Numbers[kind]:
		return numberKey(c.ctx.Text(n))
	case c.syn.Dotted[kind], c.syn.Idents[kind]:
		return c.nameKey(c.ctx.Text(n), typ)
	}

	return "", false
}

// quoteChars are the characters that open and close a string.
const quoteChars = "\"'`"

// unquoted answers the content of a string literal as written, or the text
// itself when it has no quotes, as a property name has none.
func unquoted(text string) string {
	if at := strings.IndexAny(text, quoteChars); at >= 0 {
		return strings.Trim(text[at:], quoteChars)
	}

	return text
}

// stringKey reads a string literal's content, in double quotes. A string
// with a value written into it names no case.
func (c *caseCollector) stringKey(n *ts.Node) (string, bool) {
	for i := range n.NamedChildCount() {
		if c.syn.Spliced[n.NamedChild(i).Kind()] {
			return "", false
		}
	}
	text := unquoted(c.ctx.Text(n))
	if len(text) < caseMinString || len(text) > caseMaxString || strings.ContainsAny(text, "\n\r") {
		return "", false
	}

	return `"` + text + `"`, true
}

// numberKey reads a number as written, when it is not a small one.
func numberKey(text string) (string, bool) {
	plain := strings.ReplaceAll(text, "_", "")
	if v, err := strconv.ParseFloat(plain, 64); err == nil && v > -caseMinNumber && v < caseMinNumber {
		return "", false
	}
	if v, err := strconv.ParseInt(plain, 0, 64); err == nil && v > -caseMinNumber && v < caseMinNumber {
		return "", false
	}

	return text, true
}

// dottedName reports whether text is a name or names joined by dots, with
// nothing called, indexed, or spaced in it.
func dottedName(text string) bool {
	if text == "" || text == "_" || len(text) > caseMaxString {
		return false
	}
	for _, r := range text {
		if r != '.' && r != '_' && r != '$' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}

	return true
}

// nameKey reads a name or a dotted name: the last two parts of it. Where a
// type stands every name counts but the builtin ones. Elsewhere a dotted name
// counts when one of its two parts starts with a capital, as an enum member
// and a package's constant do, and a bare name when the grammar reads every
// compared name as a constant or the name starts with a capital.
func (c *caseCollector) nameKey(text string, typ bool) (string, bool) {
	if !dottedName(text) {
		return "", false
	}
	parts := strings.Split(text, ".")
	last := parts[len(parts)-1]
	if len(parts) == 1 {
		if typ {
			return last, !c.syn.BuiltinTypes[last]
		}

		return last, c.syn.AnyIdent || capital(last)
	}
	prev := parts[len(parts)-2]
	if last == "" || prev == "" {
		return "", false
	}

	return prev + "." + last, typ || capital(last) || capital(prev)
}

// capital reports whether a name starts with an upper-case letter.
func capital(name string) bool {
	r, _ := utf8.DecodeRuneInString(name)

	return unicode.IsUpper(r)
}

// operator answers the operator of an expression: the kind of the anonymous
// node the grammar files under operator or operators.
func operator(n *ts.Node) string {
	if op := n.ChildByFieldName("operator"); op != nil {
		return op.Kind()
	}
	if op := n.ChildByFieldName("operators"); op != nil {
		return op.Kind()
	}

	return ""
}

// tests lists the tests of a condition: each comparison of a subject against
// a case, through every and, or, and not.
func (c *caseCollector) tests(n *ts.Node, out []caseTest) []caseTest {
	n = c.unwrap(n)
	if n == nil {
		return out
	}
	kind, op := n.Kind(), operator(n)
	switch {
	case c.syn.Logic[kind] && (c.syn.LogicOps == nil || c.syn.LogicOps[op]):
		for i := range n.NamedChildCount() {
			out = c.tests(n.NamedChild(i), out)
		}
	case c.syn.Compares[kind]:
		out = c.compare(n, op, out)
	case c.syn.Calls[kind]:
		out = c.typeCall(n, out)
	}

	return out
}

// compare reads one comparison: a subject against a constant on either side,
// against each item of a list, or against a type.
func (c *caseCollector) compare(n *ts.Node, op string, out []caseTest) []caseTest {
	if n.NamedChildCount() != 2 { //nolint:mnd // the two sides of a comparison
		return out
	}
	left, right := c.unwrap(n.NamedChild(0)), c.unwrap(n.NamedChild(1))
	switch {
	case c.syn.EqualOps[op]:
		// Either side may be the constant, and where the grammar reads any
		// name as one both are tried: the subject is the side most branches
		// of the chain share.
		for _, side := range [][2]*ts.Node{{left, right}, {right, left}} {
			kind := side[0].Kind()
			if c.syn.Logic[kind] || c.syn.Strings[kind] || c.syn.Numbers[kind] {
				continue // typeof x, a negation, or a literal: no subject of a case
			}
			if key, ok := c.key(side[1], false); ok {
				out = append(out, caseTest{subject: c.ctx.Text(side[0]), key: key})
			}
		}
	case c.syn.InOps[op]:
		return c.listTests(c.ctx.Text(left), right, false, out)
	case c.syn.TypeOps[op]:
		return c.listTests(c.ctx.Text(left), right, true, out)
	}

	return out
}

// listTests adds a test of the subject against a node, or against each item
// of it when it is a list.
func (c *caseCollector) listTests(subject string, n *ts.Node, typ bool, out []caseTest) []caseTest {
	items := []*ts.Node{n}
	if c.syn.Lists[n.Kind()] {
		items = lang.NamedChildren(n)
	}
	for _, item := range items {
		if key, ok := c.key(item, typ); ok {
			out = append(out, caseTest{subject: subject, key: key})
		}
	}

	return out
}

// typeCall reads a call that tests its first argument's type against its
// second, such as isinstance(x, (A, B)).
func (c *caseCollector) typeCall(n *ts.Node, out []caseTest) []caseTest {
	args := n.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() != 2 || !c.syn.TypeCalls[c.ctx.calleeText(n)] {
		return out
	}

	return c.listTests(c.ctx.Text(args.NamedChild(0)), c.unwrap(args.NamedChild(1)), true, out)
}

// oneSubject answers the cases of the subject most branches test, and how
// many branches test it. A chain that tests one thing after another has no
// such subject and enumerates nothing.
func oneSubject(branches [][]caseTest) ([]string, int) {
	count := map[string]int{}
	best := ""
	for _, tests := range branches {
		seen := map[string]bool{}
		for _, t := range tests {
			if seen[t.subject] {
				continue
			}
			seen[t.subject] = true
			count[t.subject]++
			// The first subject to reach a count keeps it on a tie.
			if count[t.subject] > count[best] {
				best = t.subject
			}
		}
	}
	var keys []string
	for _, tests := range branches {
		for _, t := range tests {
			if t.subject == best {
				keys = append(keys, t.key)
			}
		}
	}

	return keys, count[best]
}

// ifChain records an if statement with its else-if branches, from the first
// if of the chain.
func (c *caseCollector) ifChain(n *ts.Node) {
	if n.ChildByFieldName("alternative") == nil {
		return // most ifs have no else, and the walk meets one per few statements
	}
	if p := n.Parent(); p != nil && (p.Kind() == c.syn.If || (c.syn.Else != "" && p.Kind() == c.syn.Else)) {
		return // a later branch of a chain already read
	}
	var branches [][]caseTest
	def := false
	for cur := n; cur != nil; {
		branches = append(branches, c.tests(cur.ChildByFieldName("condition"), nil))
		var last bool
		cur, branches, last = c.chainStep(cur, branches)
		def = def || last
	}
	if keys, tested := oneSubject(branches); tested >= caseMinArms {
		c.add(n, caseKindIf, def, keys)
	}
}

// chainStep reads what follows one if of a chain: the conditions of its
// else-if clauses, the if its else holds, when it holds one, and whether the
// chain ends in an else that takes every other case.
func (c *caseCollector) chainStep(cur *ts.Node, branches [][]caseTest) (*ts.Node, [][]caseTest, bool) {
	var next *ts.Node
	def := false
	follow := func(alt *ts.Node) {
		if alt != nil && alt.Kind() == c.syn.If {
			next = alt
		} else {
			def = true
		}
	}
	for i := range cur.NamedChildCount() {
		child := cur.NamedChild(i)
		switch child.Kind() {
		case c.syn.ElseIf:
			branches = append(branches, c.tests(child.ChildByFieldName("condition"), nil))
		case c.syn.Else:
			follow(child.NamedChild(0))
		}
	}
	if alt := cur.ChildByFieldName("alternative"); alt != nil && c.syn.Else == "" {
		follow(alt)
	}

	return next, branches, def
}

// arms lists the arms of a switch: its own children, or those of its body.
func (c *caseCollector) arms(n *ts.Node) []*ts.Node {
	var out []*ts.Node
	for i := range n.NamedChildCount() {
		child := n.NamedChild(i)
		kind := child.Kind()
		if _, ok := c.syn.Arms[kind]; ok || c.syn.Defaults[kind] {
			out = append(out, child)
		}
	}
	if body := n.ChildByFieldName("body"); body != nil && len(out) == 0 {
		return c.arms(body)
	}

	return out
}

// armValues lists the nodes of an arm that hold its cases: the children
// under the arm's field, or the ones that are lists when it has none.
func (c *caseCollector) armValues(arm *ts.Node) []*ts.Node {
	field := c.syn.Arms[arm.Kind()]
	var out []*ts.Node
	for i := range arm.ChildCount() {
		child := arm.Child(i)
		if !child.IsNamed() {
			continue
		}
		if field == "" && c.syn.Lists[child.Kind()] ||
			field != "" && arm.FieldNameForChild(uint32(i)) == field {
			out = append(out, child)
		}
	}

	return out
}

// bareSwitch reports whether a switch holds a condition per arm: it has no
// subject, or true for one.
func (c *caseCollector) bareSwitch(n *ts.Node) bool {
	subject := n.ChildByFieldName("value")
	if subject == nil {
		return n.ChildByFieldName("subject") == nil
	}

	return c.syn.True != "" && c.unwrap(subject).Kind() == c.syn.True
}

// switchStmt records a switch or a match. One that holds a condition per arm
// is read as an if chain is.
func (c *caseCollector) switchStmt(n *ts.Node) {
	arms := c.arms(n)
	if len(arms) < caseMinArms {
		return
	}
	bare := c.bareSwitch(n)
	var keys []string
	var branches [][]caseTest
	def := false
	for _, arm := range arms {
		if c.syn.Defaults[arm.Kind()] {
			def = true

			continue
		}
		var tests []caseTest
		for _, value := range c.armValues(arm) {
			if bare {
				tests = c.armTests(value, tests)
			} else {
				keys, def = c.armKeys(value, c.syn.TypeArms[arm.Kind()], keys, def)
			}
		}
		branches = append(branches, tests)
	}
	if bare {
		var tested int
		if keys, tested = oneSubject(branches); tested < caseMinArms {
			return
		}
	}
	c.add(n, caseKindSwitch, def, keys)
}

// armTests reads the conditions of one arm of a switch with no subject.
func (c *caseCollector) armTests(n *ts.Node, out []caseTest) []caseTest {
	if c.syn.Lists[n.Kind()] {
		for i := range n.NamedChildCount() {
			out = c.tests(n.NamedChild(i), out)
		}

		return out
	}

	return c.tests(n, out)
}

// armKeys adds the cases one value of an arm names: itself, each item of a
// list, or the type of a pattern that matches by type. A pattern that holds
// nothing matches every case, which makes its arm the default.
func (c *caseCollector) armKeys(n *ts.Node, typ bool, keys []string, def bool) ([]string, bool) {
	kind := n.Kind()
	switch {
	case c.syn.Lists[kind]:
		if n.NamedChildCount() == 0 {
			return keys, true
		}
		for i := range n.NamedChildCount() {
			keys, def = c.armKeys(n.NamedChild(i), typ, keys, def)
		}
	case c.syn.TypePatterns[kind]:
		if key, ok := c.key(n.NamedChild(0), true); ok {
			keys = append(keys, key)
		}
	default:
		if key, ok := c.key(n, typ); ok {
			keys = append(keys, key)
		}
	}

	return keys, def
}

// tableTally counts what the entries of a literal table are.
type tableTally struct {
	keys    []string
	funcs   int // values that are functions
	named   int // keys that are named constants
	quoted  int // keys that are strings
	records int // entries that copy a field of the key's name
}

// table records a literal table when it reads as one arm per case: most of
// its values are functions, or most of its keys are named constants. A table
// that copies fields of the same names off a value is a record and is not
// one, whatever its values are.
func (c *caseCollector) table(n *ts.Node) {
	if n.NamedChildCount() < caseMinEntries {
		return
	}
	entries := c.syn.TableEntries(n)
	if len(entries) < caseMinEntries || len(entries) > caseMaxKeys {
		return
	}
	var t tableTally
	for _, entry := range entries {
		c.tallyEntry(entry, &t)
	}
	size := len(entries)
	dispatch := t.funcs*caseShareDen >= size*caseDispatchNum
	if !dispatch && t.named*caseShareDen < size*caseDispatchNum {
		return
	}
	mostlyNamed := t.named+t.named >= size
	if t.records*caseShareDen >= size*caseRecordNum || (t.records > 0 && t.quoted > 0 && !mostlyNamed) {
		return
	}
	c.add(n, caseKindTable, false, t.keys)
}

// tallyEntry counts one entry of a table.
func (c *caseCollector) tallyEntry(entry *ts.Node, t *tableTally) {
	k, v := c.syn.EntryKeyValue(entry)
	if k == nil || v == nil {
		return
	}
	k, v = c.unwrap(k), c.unwrap(v)
	key, ok := c.key(k, false)
	if ok {
		t.keys = append(t.keys, key)
	}
	kind := k.Kind()
	isString := c.syn.Strings[kind]
	switch {
	case ok && (c.syn.Dotted[kind] || c.syn.Idents[kind]):
		t.named++
	case isString:
		t.quoted++
	}
	if c.dispatches(v) {
		t.funcs++
	}
	if (isString || c.syn.EntryNames[kind]) && c.copiesField(k, v) {
		t.records++
	}
}

// dispatches reports whether a table's value is a function: a name, a member
// of something, a function written in place, or a partial application. A
// method written in place is not one: an object of methods implements an
// interface, and its type already ties the implementations together.
func (c *caseCollector) dispatches(v *ts.Node) bool {
	kind := v.Kind()
	if c.syn.Idents[kind] || c.syn.Dotted[kind] || c.syn.FuncValues[kind] || c.syn.EntryNames[kind] {
		return true
	}
	if !c.syn.Calls[kind] {
		return false
	}
	callee := c.ctx.calleeText(v)

	return c.syn.Partials[callee[strings.LastIndexByte(callee, '.')+1:]]
}

// copiesField reports whether an entry copies a field of its own key's name:
// `"name": user.name`, `name: row["name"]`, `name: get_name()`, a name that is
// its own key and value, or a key that ends in the field's name
// (`recipientEmail: recipient.email`, `"user_id": user.id`).
func (c *caseCollector) copiesField(k, v *ts.Node) bool {
	kind := v.Kind()
	if c.syn.EntryNames[kind] {
		return true
	}
	if !c.syn.Idents[kind] && !c.syn.Dotted[kind] && !c.syn.Subscripts[kind] && !c.syn.Calls[kind] {
		return false
	}
	name := unquoted(c.ctx.Text(k))
	if name == "" {
		return false
	}
	text := c.ctx.Text(v)
	head, _, _ := strings.Cut(text, "(")
	last := strings.TrimSpace(head[strings.LastIndexByte(head, '.')+1:])

	return last == name || strings.HasSuffix(text, name) || endsInField(name, last) ||
		strings.Contains(text, `"`+name+`"`) || strings.Contains(text, "'"+name+"'")
}

// endsInField reports whether a key ends in the name of the field it is set
// from, in any letter case: the key says whose field it is, and the entry is
// still a copy. One letter is no name.
func endsInField(key, field string) bool {
	return len(field) > 1 && len(key) > len(field) && strings.EqualFold(key[len(key)-len(field):], field)
}

// caseSets packs what the walk gathered, or answers nil for a file with none.
func (c *caseCollector) caseSets() CaseSets {
	if len(c.recs) == 0 {
		return nil
	}
	slices.SortStableFunc(c.recs, func(a, b caseRec) int { return a.line - b.line })
	ids := map[string]int{}
	var names []string
	out := binary.AppendUvarint(nil, uint64(len(c.recs)))
	line := 0
	for i := range c.recs {
		r := &c.recs[i]
		flags := r.kind << 1
		if r.def {
			flags |= 1
		}
		for _, v := range []int{r.line - line, r.end - r.line, flags, len(r.keys)} {
			out = binary.AppendUvarint(out, uint64(max(v, 0)))
		}
		for _, key := range r.keys {
			id, ok := ids[key]
			if !ok {
				id = len(names)
				ids[key] = id
				names = append(names, key)
			}
			out = binary.AppendUvarint(out, uint64(id))
		}
		line = r.line
	}
	out = binary.AppendUvarint(out, uint64(len(names)))
	for _, name := range names {
		out = append(binary.AppendUvarint(out, uint64(len(name))), name...)
	}

	return out
}

// decode unpacks a file's places. A payload cut short, or one whose places
// point past its names, answers none.
func (s CaseSets) decode() []caseRec {
	r := &fragReader{rest: s}
	count := r.next()
	out := make([]caseRec, 0, min(count, len(s)))
	var ids [][]int
	line := 0
	for range count {
		delta, span, flags, nkeys := r.next(), r.next(), r.next(), r.next()
		if r.bad || nkeys > len(r.rest) || flags>>1 >= len(caseKindNames) {
			return nil
		}
		line += delta
		out = append(out, caseRec{line: line, end: line + span, kind: flags >> 1, def: flags&1 != 0})
		keys := make([]int, 0, nkeys)
		for range nkeys {
			keys = append(keys, r.next())
		}
		ids = append(ids, keys)
	}
	if r.bad {
		return nil
	}
	names := decodeNames(r.rest)
	for i := range out {
		for _, id := range ids[i] {
			if id >= len(names) {
				return nil
			}
			out[i].keys = append(out[i].keys, names[id])
		}
	}

	return out
}

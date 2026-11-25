package structure

import (
	"cmp"
	"slices"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/kyleking/slop-strata/internal/lang"
)

// SplitMinCognitive is the cognitive complexity a function reaches before a
// block to move out of it is looked for.
const SplitMinCognitive = 15

// splitMinDrop is the least a split must take off the function to be kept,
// and splitMinShare the least share of the function's complexity: moving a
// whole loop body out takes off one level of nesting and leaves the same
// function under a new name.
const (
	splitMinDrop  = 5
	splitMinShare = 0.2
)

// splitMinSide is the least cognitive complexity each of the two functions a
// split leaves must have. Under it one of them is the old function behind a
// guard, or a few lines that are no step of their own.
const splitMinSide = 5

// splitMaxPart is the most statements a run may hold when it is part of a
// block and not all of it: a longer stretch is a half of the function, cut
// where the halves balance, and not a step of it.
const splitMaxPart = 4

// splitMaxInputs and splitMaxOutputs bound how many of the function's
// variables a block may read from the code before it and hand to the code
// after it: more, and the block is not a function waiting to be named.
const (
	splitMaxInputs  = 5
	splitMaxOutputs = 1
)

// splitKeep is how many of a function's blocks are checked for what flows in
// and out of them, the ones that would take the most complexity first.
const splitKeep = 64

// Split is the run of statements at one nesting level (a loop, a loop body, a
// branch arm, or part of one) that would cut a function's cognitive
// complexity most as a function of its own. Lines are 1-based and inclusive.
type Split struct {
	StartLine  int `json:"start_line"`
	EndLine    int `json:"end_line"`
	Statements int `json:"statements"`
	// Drop is the function's cognitive complexity less the larger of
	// Remainder and Cognitive: what the split takes off the worse of the two
	// functions it leaves.
	Drop int `json:"drop"`
	// Cognitive and CC are the block's as a function of its own, so the
	// nesting it sat under no longer counts against it.
	Cognitive int `json:"cognitive"`
	CC        int `json:"cc"`
	// Remainder and RemainderCC are the function's without the block.
	Remainder   int `json:"remainder"`
	RemainderCC int `json:"remainder_cc"`
	// Inputs counts the variables declared outside the block that the block
	// reads, and Outputs the ones the block writes that are read with that
	// value once it is done. Both resolve each name to the scope that declares
	// it, the function around a closure included, and have no types.
	Inputs  int `json:"inputs"`
	Outputs int `json:"outputs"`
	// Exits counts the return, break, and continue statements in the block
	// that leave it. Each is one the new function can spell as a return.
	Exits int `json:"exits"`
}

// blockStats is what a subtree adds to its function, in place.
type blockStats struct {
	cc, cognitive int
	nests         int // nodes that raise the nesting level
	returns       int
	errs          int // returns that hand back an error and nothing else of use
	breaks        int // break statements no loop or switch of the subtree ends
	continues     int // continue statements no loop of the subtree ends
	pinned        int // statements that mean something else in another function
}

func (b *blockStats) add(o blockStats) {
	b.cc += o.cc
	b.cognitive += o.cognitive
	b.nests += o.nests
	b.returns += o.returns
	b.errs += o.errs
	b.breaks += o.breaks
	b.continues += o.continues
	b.pinned += o.pinned
}

// splitCand is one run of statements a function could lose.
type splitCand struct {
	loops            []loopSpan // the loops of the function around the run
	start, end       uint       // bytes
	startRow, endRow uint
	statements       int
	drop             int
	alone, remainder int
	cc               int
	exits            int
	inputs, outputs  int
	whole            bool // every statement of its block
	ends             bool // always leaves the function
}

// splitter walks one function for the runs of statements worth moving out.
type splitter struct {
	l     *lang.Language
	src   []byte
	kept  []splitCand
	loops []loopSpan // the loops around the node being walked
	total int        // the function's cognitive complexity
	// errFunc is true for a Go function whose last result is an error.
	errFunc bool
}

// Statement kinds that leave the block they sit in, and the node Go and
// Python keep a body in.
const (
	kindReturn   = "return_statement"
	kindBreak    = "break_statement"
	kindContinue = "continue_statement"
	kindBlock    = "block"
)

// flows holds the uses of each top-level function of a blob that a split was
// looked for in, by the function's offset: a closure's variables are read
// from the function around it, and several closures share one.
type flows map[uint]*flow

// of answers the flow of the top-level function a function sits in.
func (fs flows) of(l *lang.Language, src []byte, fn *ts.Node) *flow {
	top := fn
	for p := fn.Parent(); p != nil; p = p.Parent() {
		if l.FunctionKinds[p.Kind()] {
			top = p
		}
	}
	f, ok := fs[top.StartByte()]
	if !ok {
		f = newFlow(l, src, top)
		fs[top.StartByte()] = f
	}

	return f
}

// findSplit answers the best split of a function whose cognitive complexity
// and decision count are given, or nil when no block qualifies.
func (fs flows) findSplit(l *lang.Language, node *ts.Node, src []byte, cognitive, decisions int) *Split {
	s := &splitter{l: l, src: src, total: cognitive, errFunc: returnsError(l, node, src)}
	for i := range node.NamedChildCount() {
		s.walk(node.NamedChild(i), 0)
	}
	if len(s.kept) == 0 {
		return nil
	}
	f := fs.of(l, src, node)
	fn := byteSpan{node.StartByte(), node.EndByte()}
	var best *splitCand
	for i := range s.kept {
		c := &s.kept[i]
		c.inputs, c.outputs = f.across(&crossing{start: c.start, end: c.end, fn: fn, loops: c.loops, ends: c.ends})
		if c.inputs <= splitMaxInputs && c.outputs <= splitMaxOutputs && (best == nil || c.beats(best)) {
			best = c
		}
	}
	if best == nil {
		return nil
	}

	return &Split{
		StartLine: int(best.startRow) + 1, EndLine: int(best.endRow) + 1,
		Statements: best.statements, Drop: best.drop,
		Cognitive: best.alone, CC: 1 + best.cc, Remainder: best.remainder, RemainderCC: 1 + decisions - best.cc,
		Inputs: best.inputs, Outputs: best.outputs, Exits: best.exits,
	}
}

// beats orders two blocks that both qualify: the larger drop, then every
// statement of a block before a part of one, then the fewer variables across
// the cut, then the shorter, then the earlier.
func (c *splitCand) beats(o *splitCand) bool {
	return cmp.Or(
		cmp.Compare(o.drop, c.drop),
		cmp.Compare(b2i(o.whole), b2i(c.whole)),
		cmp.Compare(c.inputs+c.outputs, o.inputs+o.outputs),
		cmp.Compare(c.end-c.start, o.end-o.start),
		cmp.Compare(c.start, o.start),
	) < 0
}

func b2i(b bool) int {
	if b {
		return 1
	}

	return 0
}

// returnsError reports whether a function's last result is Go's error.
func returnsError(l *lang.Language, fn *ts.Node, src []byte) bool {
	if l.ResultField == "" {
		return false
	}
	result := fn.ChildByFieldName(l.ResultField)
	if result == nil {
		return false
	}
	if n := result.NamedChildCount(); result.Kind() == "parameter_list" && n > 0 {
		result = result.NamedChild(n - 1).ChildByFieldName("type")
	}

	return result != nil && lang.Text(result, src) == "error"
}

// errorPackages are the packages whose calls make an error.
var errorPackages = map[string]bool{"fmt": true, "errors": true} //nolint:gochecknoglobals // fixed vocabulary

// returnsAnError reports whether a return statement's last value is an error
// that is there: a variable, or a call that makes one. A nil, or a call that
// may answer nil, is the function going on to succeed some other way.
func (s *splitter) returnsAnError(node *ts.Node) bool {
	list := node.NamedChild(0)
	if list == nil || list.NamedChildCount() == 0 {
		return false
	}
	last := list.NamedChild(list.NamedChildCount() - 1)
	switch last.Kind() {
	case "identifier":
		return true
	case "call_expression":
		callee := last.ChildByFieldName("function")
		if callee == nil || callee.Kind() != "selector_expression" {
			return false
		}
		pkg := callee.ChildByFieldName("operand")

		return pkg != nil && errorPackages[lang.Text(pkg, s.src)]
	default:
		return false
	}
}

// stmtStats is one statement of a block with what it adds.
type stmtStats struct {
	node  *ts.Node
	stats blockStats
}

// walk measures a subtree in place, and offers the runs of every block in it.
func (s *splitter) walk(node *ts.Node, level int) blockStats {
	kind := node.Kind()
	if s.l.FunctionKinds[kind] {
		return blockStats{}
	}
	c := complexity{l: s.l, src: s.src}
	inner := c.own(node, kind, level)
	st := blockStats{cc: c.cc, cognitive: c.cognitive, nests: inner - level}
	switch kind {
	case kindReturn:
		st.returns++
		if s.errFunc && s.returnsAnError(node) {
			st.errs++
		}
	case kindBreak:
		st.breaks++
	case kindContinue:
		st.continues++
	case "defer_statement", "yield", "yield_expression":
		st.pinned++
	}
	block := s.l.BlockKinds[kind]
	loop := s.l.LoopKinds[kind]
	if loop {
		s.loops = append(s.loops, loopSpan{
			stmt: byteSpan{node.StartByte(), node.EndByte()}, body: fieldSpan(node, fieldBody),
			each: s.each(node, kind),
		})
	}
	var stmts []stmtStats
	for i := range node.NamedChildCount() {
		child := node.NamedChild(i)
		sub := s.walk(child, inner)
		st.add(sub)
		if block && !s.l.CommentKinds[child.Kind()] {
			stmts = append(stmts, stmtStats{node: child, stats: sub})
		}
	}
	if loop {
		st.breaks, st.continues = 0, 0
		s.loops = s.loops[:len(s.loops)-1]
	}
	if s.l.SwitchKinds[kind] {
		st.breaks = 0
	}
	if block {
		s.offer(node, stmts, level)
	}

	return st
}

// each reports whether a loop goes over the items of something.
func (s *splitter) each(node *ts.Node, kind string) bool {
	if s.l.EachKinds[kind] {
		return true
	}
	for i := range node.NamedChildCount() {
		if s.l.EachKinds[node.NamedChild(i).Kind()] {
			return true
		}
	}

	return false
}

// offer scores every run of a block's statements. A run moved into a function
// of its own loses the nesting it sat under, and the function loses the run.
// What counts is the worse of the two that are left. A run that holds a defer
// or a yield is passed over: either means something else once it is moved. So
// is one whose exits the new function could not spell as a return. A part of
// the block, which is several statements and not all of them, is kept only at
// splitMaxPart statements or fewer, and only when no longer part would take
// more off: where one would, the block is a list of steps and the short run
// is that stretch cut down.
func (s *splitter) offer(block *ts.Node, stmts []stmtStats, level int) {
	var parts []splitCand
	long := 0 // the largest drop of a part that is too long
	site := blockSite{level: level, count: len(stmts)}
	site.fnBody, site.loopBody = s.owner(block)
	for i := range stmts {
		var run blockStats
		for j := i; j < len(stmts); j++ {
			run.add(stmts[j].stats)
			c, ok := s.candidate(&site, stmts[i:j+1], &run, j == len(stmts)-1)
			switch {
			case !ok:
			case i == j || c.whole:
				s.keep(c)
			case c.statements > splitMaxPart:
				long = max(long, c.drop)
			default:
				parts = append(parts, c)
			}
		}
	}
	for i := range parts {
		if parts[i].drop >= long {
			s.keep(parts[i])
		}
	}
}

// blockSite is where a block sits in its function.
type blockSite struct {
	loops            []loopSpan // the loops around it, copied for the first run that is kept
	level            int        // the nesting it sits under
	count            int        // its statements
	fnBody, loopBody bool       // it is the body of the function, or of a loop
}

// candidate scores one run of a block's statements, whose stats are given,
// and reports whether it takes enough off the function and leaves cleanly. A
// run that ends its block has last set.
func (s *splitter) candidate(site *blockSite, stmts []stmtStats, run *blockStats, last bool) (splitCand, bool) {
	alone := run.cognitive - site.level*run.nests
	remainder := s.total - run.cognitive
	drop := s.total - max(alone, remainder)
	if drop < splitMinDrop || float64(drop) < splitMinShare*float64(s.total) || run.pinned > 0 ||
		min(alone, remainder) < splitMinSide {
		return splitCand{}, false
	}
	first, end := stmts[0].node, stmts[len(stmts)-1].node
	ends := s.terminates(end)
	if !run.leavesCleanly(last && site.fnBody, last && site.loopBody, ends) {
		return splitCand{}, false
	}

	if site.loops == nil {
		site.loops = slices.Clone(s.loops)
	}

	return splitCand{
		start: first.StartByte(), end: end.EndByte(),
		startRow: first.StartPosition().Row, endRow: end.EndPosition().Row,
		statements: len(stmts), drop: drop, alone: alone, remainder: remainder, cc: run.cc,
		exits: run.returns + run.breaks + run.continues, loops: site.loops,
		whole: last && len(stmts) == site.count, ends: ends,
	}, true
}

// leavesCleanly reports whether every statement that leaves a run can be a
// return of the function the run becomes, with nothing for the caller to
// check. That holds when nothing leaves it. It holds for returns when the
// run always ends in one, or is the end of the function, so the caller
// returns what the new function does, and when each hands back an error from
// a Go function whose last result is one. And it holds for continues when the
// run is the end of a loop's body, where a return does what they did. A
// break, or a return from a run that can also go on, would need a flag.
func (b *blockStats) leavesCleanly(endsFunc, endsLoop, terminates bool) bool {
	switch {
	case b.returns+b.breaks+b.continues == 0:
		return true
	case b.breaks > 0:
		return false
	case b.continues == 0:
		return endsFunc || b.errs == b.returns || terminates
	default:
		return b.returns == 0 && endsLoop
	}
}

// owner reports whether a block is the body of a function, or of a loop.
func (s *splitter) owner(block *ts.Node) (bool, bool) {
	p := block.Parent()
	if p != nil && p.Kind() == kindBlock && !s.l.BlockKinds[kindBlock] {
		p = p.Parent() // Go keeps a body's statements one node further down
	}
	if p == nil {
		return false, false
	}

	return s.l.FunctionKinds[p.Kind()], s.l.LoopKinds[p.Kind()]
}

// terminates reports whether a statement always leaves the function: a
// return, a raise or a throw, a panic, or a branch with an else whose every
// arm ends in one.
func (s *splitter) terminates(node *ts.Node) bool {
	switch kind := node.Kind(); kind {
	case kindReturn, "raise_statement", "throw_statement":
		return true
	case "expression_statement":
		call := node.NamedChild(0)
		if call == nil || !s.l.CallKinds[call.Kind()] {
			return false
		}
		callee := call.ChildByFieldName("function")

		return callee != nil && s.l.ID == lang.Go.ID && lang.Text(callee, s.src) == "panic"
	case kindBlock, "statement_block", "statement_list", "else_clause":
		for i := int(node.NamedChildCount()) - 1; i >= 0; i-- {
			if child := node.NamedChild(uint(i)); !s.l.CommentKinds[child.Kind()] {
				return s.terminates(child)
			}
		}

		return false
	case "if_statement", "elif_clause":
		return s.branchTerminates(node)
	default:
		return false
	}
}

// branchTerminates reports whether every arm of a branch terminates and the
// last of them is an else.
func (s *splitter) branchTerminates(node *ts.Node) bool {
	closed := false
	for i := range node.ChildCount() {
		child := node.Child(i)
		switch node.FieldNameForChild(uint32(i)) {
		case "consequence":
			if !s.terminates(child) {
				return false
			}
		case "alternative":
			if !s.terminates(child) {
				return false
			}
			closed = child.Kind() != "elif_clause"
		}
	}

	return closed
}

// keep holds a run among the splitKeep with the largest drop, the shorter of
// two that drop the same.
func (s *splitter) keep(c splitCand) {
	if len(s.kept) < splitKeep {
		s.kept = append(s.kept, c)

		return
	}
	worst := 0
	for i := range s.kept {
		if s.kept[worst].ahead(&s.kept[i]) {
			worst = i
		}
	}
	if c.ahead(&s.kept[worst]) {
		s.kept[worst] = c
	}
}

// ahead orders runs before their variables are counted.
func (c *splitCand) ahead(o *splitCand) bool {
	return cmp.Or(
		cmp.Compare(o.drop, c.drop), cmp.Compare(c.end-c.start, o.end-o.start), cmp.Compare(c.start, o.start),
	) < 0
}

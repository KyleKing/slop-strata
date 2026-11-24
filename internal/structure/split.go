package structure

import (
	"cmp"

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
	// Inputs counts the variables the function gives a value before the
	// block that the block reads, and Outputs the ones the block gives a
	// value that the function reads after it. Both are read off names, with
	// no scopes and no types.
	Inputs  int `json:"inputs"`
	Outputs int `json:"outputs"`
	// Exits counts the return, break, and continue statements in the block
	// that leave it.
	Exits int `json:"exits"`
}

// blockStats is what a subtree adds to its function, in place.
type blockStats struct {
	cc, cognitive int
	nests         int // nodes that raise the nesting level
	returns       int
	breaks        int // break statements no loop or switch of the subtree ends
	continues     int // continue statements no loop of the subtree ends
	pinned        int // statements that mean something else in another function
}

func (b *blockStats) add(o blockStats) {
	b.cc += o.cc
	b.cognitive += o.cognitive
	b.nests += o.nests
	b.returns += o.returns
	b.breaks += o.breaks
	b.continues += o.continues
	b.pinned += o.pinned
}

// splitCand is one run of statements a function could lose.
type splitCand struct {
	start, end       uint // bytes
	startRow, endRow uint
	statements       int
	drop             int
	alone, remainder int
	cc               int
	exits            int
	inputs, outputs  int
}

// splitter walks one function for the runs of statements worth moving out.
type splitter struct {
	l     *lang.Language
	src   []byte
	kept  []splitCand
	total int // the function's cognitive complexity
}

// Statement kinds that leave the block they sit in.
const (
	kindReturn   = "return_statement"
	kindBreak    = "break_statement"
	kindContinue = "continue_statement"
)

// findSplit answers the best split of a function whose cognitive complexity
// and decision count are given, or nil when no block qualifies.
func findSplit(l *lang.Language, node *ts.Node, src []byte, cognitive, decisions int) *Split {
	s := &splitter{l: l, src: src, total: cognitive}
	for i := range node.NamedChildCount() {
		s.walk(node.NamedChild(i), 0)
	}
	if len(s.kept) == 0 {
		return nil
	}
	f := newFlow(l, src, node)
	var best *splitCand
	for i := range s.kept {
		c := &s.kept[i]
		c.inputs, c.outputs = f.across(c.start, c.end)
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

// beats orders two blocks that both qualify: the larger drop, then the fewer
// variables across the cut, then the shorter, then the earlier.
func (c *splitCand) beats(o *splitCand) bool {
	return cmp.Or(
		cmp.Compare(o.drop, c.drop),
		cmp.Compare(c.inputs+c.outputs, o.inputs+o.outputs),
		cmp.Compare(c.end-c.start, o.end-o.start),
		cmp.Compare(c.start, o.start),
	) < 0
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
	case kindBreak:
		st.breaks++
	case kindContinue:
		st.continues++
	case "defer_statement", "yield", "yield_expression":
		st.pinned++
	}
	block := s.l.BlockKinds[kind]
	var stmts []stmtStats
	for i := range node.NamedChildCount() {
		child := node.NamedChild(i)
		sub := s.walk(child, inner)
		st.add(sub)
		if block && !s.l.CommentKinds[child.Kind()] {
			stmts = append(stmts, stmtStats{node: child, stats: sub})
		}
	}
	if s.l.LoopKinds[kind] {
		st.breaks, st.continues = 0, 0
	}
	if s.l.SwitchKinds[kind] {
		st.breaks = 0
	}
	if block {
		s.offer(stmts, level)
	}

	return st
}

// offer scores every run of a block's statements. A run moved into a function
// of its own loses the nesting it sat under, and the function loses the run.
// What counts is the worse of the two that are left. A run that holds a defer
// or a yield is passed over: either means something else once it is moved.
func (s *splitter) offer(stmts []stmtStats, level int) {
	for i := range stmts {
		var run blockStats
		for j := i; j < len(stmts); j++ {
			run.add(stmts[j].stats)
			alone := run.cognitive - level*run.nests
			remainder := s.total - run.cognitive
			drop := s.total - max(alone, remainder)
			if drop < splitMinDrop || float64(drop) < splitMinShare*float64(s.total) || run.pinned > 0 {
				continue
			}
			s.keep(splitCand{
				start: stmts[i].node.StartByte(), end: stmts[j].node.EndByte(),
				startRow: stmts[i].node.StartPosition().Row, endRow: stmts[j].node.EndPosition().Row,
				statements: j - i + 1, drop: drop, alone: alone, remainder: remainder, cc: run.cc,
				exits: run.returns + run.breaks + run.continues,
			})
		}
	}
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

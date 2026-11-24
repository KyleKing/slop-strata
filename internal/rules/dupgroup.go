package rules

import (
	"cmp"
	"maps"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DupGroup is one piece of code the tree holds more than once: every site
// that repeats it, how much code a site is, and what a shared function would
// take as parameters.
type DupGroup struct {
	// Sites are the places that hold the code, the first in path order first.
	Sites []DupGroupSite `json:"sites"`
	// Parameters are the identifiers and literals the sites spell differently:
	// one entry per parameter, holding each site's text in the order of Sites.
	Parameters [][]string `json:"parameters"`
	// Statements is how many statements one site is, and 0 for an expression.
	// A signature among them is not counted, here or in Tokens: a merge takes
	// a body away and leaves each function its own signature.
	Statements int `json:"statements"`
	// Tokens is how many tokens one site is.
	Tokens int `json:"tokens"`
	// Removable is Tokens times one less than the sites, less dupParamCost
	// per parameter: the tokens a merge into one function would take out.
	Removable int `json:"removable"`
	// Expression marks a repeated expression inside statements that differ.
	Expression bool `json:"expression"`
}

// DupGroupSite is one place a group's code sits. Line is where it starts and
// LastLine where its last statement starts, since a statement's end is not
// recorded.
type DupGroupSite struct {
	Path     string `json:"path"`
	Unit     string `json:"unit"`
	Line     int    `json:"line"`
	LastLine int    `json:"last_line"`
}

// shapeAt is one statement or expression of the tree, by its shape.
type shapeAt struct {
	shape uint32
	file  int32
	idx   int32
}

// groupPass is one run of the snapshot's duplicate pass over a tree.
type groupPass struct {
	*dupPass
	all   []*dupFile
	stmts []shapeAt // every statement of the tree, ordered by shape
	subs  []shapeAt // every expression, likewise
}

// groupSite is one site of a group being gathered: n statements from at, or
// the expression sub of statement at.
type groupSite struct {
	file *dupFile
	cols []string // the text of each slot, in order
	at   int
	sub  int
}

// DuplicateGroups lists the code a tree holds more than once, ranked by the
// tokens a merge would remove. It applies what NewDuplicates applies to a
// change, with every statement of the tree read as new: tests, examples, and
// benchmarks are left out, two files kept in step never pair, a shape the
// tree holds in many places weighs little or nothing, and the sites of a
// group differ in at most dupMaxParams names together. No statement is in two
// groups: the heaviest pair of stretches claims its statements first.
func DuplicateGroups(tree map[string]*Fingerprint) []DupGroup {
	p := newGroupPass(tree)
	out := p.statementGroups()
	out = append(out, p.expressionGroups()...)
	slices.SortStableFunc(out, func(a, b DupGroup) int {
		return cmp.Or(
			cmp.Compare(b.Removable, a.Removable),
			strings.Compare(a.Sites[0].Path, b.Sites[0].Path),
			cmp.Compare(a.Sites[0].Line, b.Sites[0].Line),
		)
	})

	return out
}

// newGroupPass reads the packed hashes of every code file and orders the
// tree's statements and expressions by shape.
func newGroupPass(tree map[string]*Fingerprint) *groupPass {
	p := &groupPass{dupPass: newDupPass()}
	for _, name := range slices.Sorted(maps.Keys(tree)) {
		if !dupScored(name, tree[name]) {
			continue
		}
		f := p.file(name, tree[name])
		f.whole = true
		at := int32(len(p.all)) //nolint:gosec // a tree holds far fewer files than 2^31
		p.all = append(p.all, f)
		p.stmts = appendShapes(p.stmts, f.shapes, at)
		p.subs = appendShapes(p.subs, f.subShapes, at)
	}
	sortShapes(p.stmts, p.df)
	sortShapes(p.subs, p.subDF)

	return p
}

// appendShapes adds one file's packed shape hashes to a list.
func appendShapes(out []shapeAt, packed []byte, file int32) []shapeAt {
	for i := range len(packed) / hashWidth {
		out = append(out, shapeAt{shape: hashAt(packed, i), file: file, idx: int32(i)})
	}

	return out
}

// sortShapes orders a list by shape, then place, and counts each shape.
func sortShapes(list []shapeAt, df map[uint32]int) {
	slices.SortFunc(list, func(a, b shapeAt) int {
		return cmp.Or(cmp.Compare(a.shape, b.shape), cmp.Compare(a.file, b.file), cmp.Compare(a.idx, b.idx))
	})
	for lo := 0; lo < len(list); {
		hi := lo + 1
		for hi < len(list) && list[hi].shape == list[lo].shape {
			hi++
		}
		df[list[lo].shape] = hi - lo
		lo = hi
	}
}

// sitesOf answers the places of a sorted list that hold a shape.
func sitesOf(list []shapeAt, shape uint32) []shapeAt {
	lo := sort.Search(len(list), func(i int) bool { return list[i].shape >= shape })
	hi := lo
	for hi < len(list) && list[hi].shape == shape {
		hi++
	}

	return list[lo:hi]
}

// runStart names an aligned run by its two files and where it starts in each.
type runStart struct {
	file, with *dupFile
	q, c       int
}

// pairRuns lists, heaviest first, the best window of every run of statements
// two production sites share, when it meets the floor a finding meets.
func (p *groupPass) pairRuns() []dupRun {
	var out []dupRun
	seen := map[runStart]bool{}
	for lo := 0; lo < len(p.stmts); {
		hi := lo + 1
		for hi < len(p.stmts) && p.stmts[hi].shape == p.stmts[lo].shape {
			hi++
		}
		sites := p.stmts[lo:hi]
		lo = hi
		if rarity(len(sites)) == 0 {
			continue
		}
		for b := 1; b < len(sites); b++ {
			for a := range b {
				if run, ok := p.pairRun(sites[a], sites[b], seen); ok {
					out = append(out, run)
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b dupRun) int {
		return cmp.Or(
			cmp.Compare(b.score, a.score), cmp.Compare(b.tokens, a.tokens),
			strings.Compare(a.with.path, b.with.path), cmp.Compare(a.c, b.c),
			strings.Compare(a.file.path, b.file.path), cmp.Compare(a.q, b.q),
		)
	})

	return out
}

// pairRun grows two statements of one shape into the run around them and
// answers its best window. The later statement is the run's own side. A run
// is read once, from whichever of its statements comes up first.
func (p *groupPass) pairRun(earlier, later shapeAt, seen map[runStart]bool) (dupRun, bool) {
	q, c := p.all[later.file], p.all[earlier.file]
	if q.aside || c.aside || (q != c && p.mirror(q, c)) {
		return dupRun{}, false
	}
	if int(later.idx) >= len(q.decoded()) || int(earlier.idx) >= len(c.decoded()) || q.stmts[later.idx].header {
		return dupRun{}, false // the first two are a payload cut short
	}
	run := extend(q, int(later.idx), dupSite{file: c, idx: int(earlier.idx)})
	key := runStart{file: q, with: c, q: run.q, c: run.c}
	if seen[key] {
		return dupRun{}, false
	}
	seen[key] = true
	if p.ceiling(&run) < dupMinScore {
		return dupRun{}, false
	}
	best, ok := p.window(run)
	if !ok || best.backToBack() {
		return dupRun{}, false
	}

	return best, true
}

// ceiling is the most a run could score: its weight with nothing taken off.
func (p *groupPass) ceiling(run *dupRun) float64 {
	total := 0.0
	for k := run.q; k < run.q+run.n; k++ {
		if st := &run.file.stmts[k]; !st.header {
			total += float64(st.ntok) * p.idf(st.shape)
		}
	}

	return total
}

// statementGroups turns the pair runs into groups: each run whose statements
// no heavier group holds becomes one, with every other site of the tree that
// lines up with it.
func (p *groupPass) statementGroups() []DupGroup {
	var out []DupGroup
	runs := p.pairRuns()
	for i := range runs {
		run := &runs[i]
		if anyTaken(run.file, run.q, run.n) || anyTaken(run.with, run.c, run.n) {
			continue
		}
		sites := p.gather(run)
		for i := range sites {
			take(sites[i].file, sites[i].at, run.n)
		}
		if isData(sites[0].cols) {
			continue
		}
		out = append(out, groupOf(sites, run.n, bodyTokens(sites[0].file, sites[0].at, run.n), false))
	}

	return out
}

// bodyTokens counts the tokens of n statements, signatures left out: a merge
// takes a body away and leaves each function its own signature.
func bodyTokens(f *dupFile, at, n int) int {
	total := 0
	for k := at; k < at+n; k++ {
		if st := &f.stmts[k]; !st.header {
			total += st.ntok
		}
	}

	return total
}

// anyTaken reports whether a group already holds one of n statements from at.
func anyTaken(f *dupFile, at, n int) bool {
	for k := at; k < at+n && k < len(f.taken); k++ {
		if f.taken[k] {
			return true
		}
	}

	return false
}

// take marks n statements from at as held by a group.
func take(f *dupFile, at, n int) {
	if f.taken == nil {
		f.taken = make([]bool, len(f.decoded()))
	}
	for k := at; k < at+n; k++ {
		f.taken[k] = true
	}
}

// dataShare is the share of a site's identifiers and literals that must be
// identifiers for the site to be code. Under it the site is a table of
// values, which repeats by being regenerated and not by being copied.
const dataShare = 0.1

// isData reports whether a site is a table of values: almost every slot of
// it is a literal.
func isData(cols []string) bool {
	names := 0
	for _, text := range cols {
		if isName(text) {
			names++
		}
	}

	return float64(names) < dataShare*float64(len(cols))
}

// isName reports whether a slot's text is an identifier and not a literal.
func isName(text string) bool {
	first, _ := utf8.DecodeRuneInString(text)

	return (unicode.IsLetter(first) || first == '_' || first == '$') && !strings.ContainsAny(text, "\"'`")
}

// paramBudget is how many parameters a group of this weight may have and
// still meet the floor, never more than dupMaxParams.
func paramBudget(weight float64) int {
	return min(dupMaxParams, int((weight-dupMinScore)/dupParamCost))
}

// gather lists the sites of a run's group: its two, then every other stretch
// of the tree with the same statements, fewest differences first, while the
// group stays within its parameter budget.
func (p *groupPass) gather(run *dupRun) []groupSite {
	first := groupSite{file: run.with, at: run.c}
	first.cols = stretchCols(first.file, first.at, run.n)
	second := groupSite{file: run.file, at: run.q}
	second.cols = stretchCols(second.file, second.at, run.n)
	sites := []groupSite{first, second}
	budget := paramBudget(run.weight)
	for _, cand := range p.candidates(run, first) {
		if !p.fits(sites, cand, run.n) {
			continue
		}
		if sites = append(sites, cand); len(paramCols(sites)) > budget {
			sites = sites[:len(sites)-1]
		}
	}

	return sites
}

// candidates lists the stretches that line up with a group's first site and
// that no group holds, fewest differences from it first.
func (p *groupPass) candidates(run *dupRun, first groupSite) []groupSite {
	anchor := run.c
	for k := run.c; k < run.c+run.n; k++ {
		if p.df[first.file.stmts[k].shape] < p.df[first.file.stmts[anchor].shape] {
			anchor = k
		}
	}
	var out []groupSite
	for _, at := range sitesOf(p.stmts, first.file.stmts[anchor].shape) {
		f := p.all[at.file]
		start := int(at.idx) - (anchor - run.c)
		if f.aside || start < 0 || start+run.n > len(f.decoded()) || anyTaken(f, start, run.n) ||
			!sameStretch(first.file, first.at, f, start, run.n) {
			continue
		}
		cand := groupSite{file: f, at: start, cols: stretchCols(f, start, run.n)}
		if diffCols(first.cols, cand.cols) <= dupMaxParams {
			out = append(out, cand)
		}
	}
	slices.SortStableFunc(out, func(a, b groupSite) int {
		return cmp.Compare(diffCols(first.cols, a.cols), diffCols(first.cols, b.cols))
	})

	return out
}

// sameStretch reports whether n statements of two files line up one for one.
func sameStretch(a *dupFile, at int, b *dupFile, bt, n int) bool {
	for k := range n {
		x, y := &a.stmts[at+k], &b.stmts[bt+k]
		if x.shape != y.shape || x.ntok != y.ntok || len(x.slots) != len(y.slots) {
			return false
		}
	}

	return true
}

// fits reports whether a stretch may join the sites gathered so far: it
// overlaps none of them, sits right behind none of them inside one function,
// and is in no file kept in step with one of theirs.
func (p *groupPass) fits(sites []groupSite, cand groupSite, n int) bool {
	for i := range sites {
		s := &sites[i]
		if s.file != cand.file {
			if p.mirror(s.file, cand.file) {
				return false
			}

			continue
		}
		pair := dupRun{file: s.file, with: s.file, q: max(s.at, cand.at), c: min(s.at, cand.at), n: n}
		if pair.q-pair.c < n || pair.backToBack() {
			return false
		}
	}

	return true
}

// stretchCols lists the slot texts of n statements, signatures left out as a
// run leaves them out: two functions always differ in name.
func stretchCols(f *dupFile, at, n int) []string {
	var out []string
	for k := at; k < at+n; k++ {
		if st := &f.stmts[k]; !st.header {
			for _, id := range st.slots {
				out = append(out, f.names[id])
			}
		}
	}

	return out
}

// diffCols counts the distinct pairs of texts two sites differ in.
func diffCols(a, b []string) int {
	seen := map[[2]string]bool{}
	for k := range min(len(a), len(b)) {
		if a[k] != b[k] {
			seen[[2]string{a[k], b[k]}] = true
		}
	}

	return len(seen)
}

// paramCols answers the parameters of a group: per distinct way the sites
// spell one slot differently, each site's text. A name renamed throughout
// counts once, as it does between two sites.
func paramCols(sites []groupSite) [][]string {
	var out [][]string
	seen := map[string]bool{}
	for k := range sites[0].cols {
		col := make([]string, len(sites))
		same := true
		for i := range sites {
			if k < len(sites[i].cols) {
				col[i] = sites[i].cols[k]
			}
			same = same && col[i] == col[0]
		}
		if key := strings.Join(col, "\x00"); !same && !seen[key] {
			seen[key] = true
			out = append(out, col)
		}
	}

	return out
}

// groupOf renders the gathered sites as a group, in path order.
func groupOf(sites []groupSite, n, tokens int, expr bool) DupGroup {
	slices.SortStableFunc(sites, func(a, b groupSite) int {
		return cmp.Or(strings.Compare(a.file.path, b.file.path), cmp.Compare(a.at, b.at), cmp.Compare(a.sub, b.sub))
	})
	params := paramCols(sites)
	for i := range params {
		for j := range params[i] {
			params[i][j] = slotText(params[i][j])
		}
	}
	g := DupGroup{
		Parameters: params, Tokens: tokens, Expression: expr,
		Removable: tokens*(len(sites)-1) - dupParamCost*len(params),
	}
	if g.Parameters == nil {
		g.Parameters = [][]string{}
	}
	for k := sites[0].at; !expr && k < sites[0].at+n; k++ {
		if !sites[0].file.stmts[k].header {
			g.Statements++
		}
	}
	for i := range sites {
		s := &sites[i]
		line, last := s.file.stmts[s.at].line, s.file.stmts[s.at+n-1].line
		if expr {
			line = s.file.subs[s.sub].line
			last = line
		}
		g.Sites = append(g.Sites, DupGroupSite{
			Path: s.file.path, Unit: unitAt(s.file.fp, line), Line: line, LastLine: last,
		})
	}

	return g
}

package rules

import (
	"fmt"
	"maps"
	"math"
	"path"
	"slices"
	"strings"

	"github.com/kyleking/slop-strata/internal/lang"
)

// RuleNewDuplicate is the id of the rule NewDuplicates reports under. Its note
// says where the repeated code is, which a caller may want to show.
const RuleNewDuplicate = "x/new-duplicate"

// DupChange is one file a change touched, as NewDuplicates reads it. Before is
// nil for a file the change added, and After for one it deleted or could not
// score. Added reports whether the change added a line of the after side.
type DupChange struct {
	Before *Fingerprint
	After  *Fingerprint
	Added  func(line int) bool
	Path   string
}

// dupMinScore is the least a run of added statements must score to be
// reported: the tokens of its new statements, each weighted by how rare its
// shape is in the tree, less dupParamCost for every parameter. DESIGN.md
// describes the replay that set it and the bounds below.
const dupMinScore = 20

// dupParamCost is what one parameter takes off the score: a distinct pair of
// identifiers or literals the two sites differ in, which a shared function
// would have to take as an argument.
const dupParamCost = 8

// dupMaxParams is the most parameters two sites may differ in.
const dupMaxParams = 3

// dupFree is how many other places may hold a statement's shape while it
// still counts in full: a few copies are the duplication the rule is after,
// not an idiom.
const dupFree = 4

// dupCap is how many other places hold a shape before it is boilerplate and
// counts nothing. Between dupFree and dupCap the weight falls with the
// logarithm of the count.
const dupCap = 32

// maxDupWindow bounds the run length searched for its best window. A longer
// run is judged whole.
const maxDupWindow = 256

// mirrorShare is the share of statements two files of one name must hold
// alike to be read as copies kept in step, which never report each other.
const mirrorShare = 0.8

// maxDupSites and maxDupPairs cap what one finding's note lists.
const (
	maxDupSites = 3
	maxDupPairs = 3
)

// dupFile is one code file of the tree, decoded as far as the pass needed.
type dupFile struct {
	fp    *Fingerprint
	path  string
	added []bool // per statement, on a line the change added; nil for an untouched file
	fresh []bool // per statement, added and not accounted for by a statement the change removed
	fragDecoded
	fragHashes
	taken  []bool // per statement, already in a group of the snapshot's pass
	aside  bool   // a test, an example, or a benchmark
	whole  bool   // every statement counts as new: the snapshot's pass, which has no change
	loaded bool
}

func (f *dupFile) count() int { return len(f.shapes) / hashWidth }

// counts reports whether the file's i-th statement may be part of a run.
func (f *dupFile) counts(i int) bool { return f.whole || f.added[i] }

// isFresh reports whether the file's i-th statement adds weight to a run.
func (f *dupFile) isFresh(i int) bool { return f.whole || f.fresh[i] }

// wasAdded reports whether the change added the file's i-th statement.
func (f *dupFile) wasAdded(i int) bool { return i < len(f.added) && f.added[i] }

// decoded answers the file's statements, unpacked on first use.
func (f *dupFile) decoded() []fragStmt {
	if !f.loaded {
		f.fragDecoded = f.fp.Frags.decode()
		f.loaded = true
	}

	return f.stmts
}

// dupSite is one statement of the tree.
type dupSite struct {
	file *dupFile
	idx  int
}

// dupRun is two aligned stretches of statements: n of them from q in the
// changed file and from c in the file that already held them. A run of one
// expression has n of 1 and says which expression of each statement it is. A
// run joined from several stretches lists them as parts, in the order of the
// changed file, and is itself the first of them with the sums of all.
type dupRun struct {
	file, with *dupFile
	pairs      [][2]string // the distinct texts the two sites differ in
	parts      []dupRun
	q, c, n    int
	qsub, csub int // the expression of each side, when expr is set
	tokens     int
	weight     float64
	score      float64
	expr       bool
}

// dupPass is one run of the rule over a change.
type dupPass struct {
	files   map[string]*dupFile
	df      map[uint32]int       // per statement shape the change added, the places that hold it
	occ     map[uint32][]dupSite // those places, while under the cap
	subDF   map[uint32]int       // the same two for the expressions the change added
	subOcc  map[uint32][]dupSite
	mirrors map[[2]string]bool
	net     netCount
}

func newDupPass() *dupPass {
	return &dupPass{
		files: map[string]*dupFile{}, df: map[uint32]int{}, occ: map[uint32][]dupSite{},
		subDF: map[uint32]int{}, subOcc: map[uint32][]dupSite{}, mirrors: map[[2]string]bool{},
	}
}

// NewDuplicates flags code a change added that repeats code the tree already
// held, or that the change added twice: a run of added statements that aligns,
// statement for statement, with another site, the two differing in at most a
// few identifiers or literals. Rev is the tree at the end of the change.
//
// Tests, examples, and benchmarks are left out on both sides. A statement the
// change also removed somewhere, as written or with other names, was moved or
// edited and is not new, so a moved function, a re-indented block, and a body
// pulled out into a shared helper report nothing.
func NewDuplicates(rev map[string]*Fingerprint, changes []DupChange, experimental bool) map[string][]Finding {
	if !crossEnabled(RuleNewDuplicate, experimental) {
		return nil
	}

	out := map[string][]Finding{}
	for _, fh := range dupHits(rev, changes) {
		if fs := dupFindings(fh.file, fh.hits); len(fs) > 0 {
			out[fh.file.path] = fs
		}
	}

	return out
}

// fileHits is the hits of one changed file.
type fileHits struct {
	file *dupFile
	hits []dupHit
}

// dupHits runs the pass: per changed file with new statements, the stretches
// that repeat code, each with every site it repeats. Whole statements that
// line up come first, then stretches too short alone that add up between two
// functions, then an expression inside statements nothing else covered.
func dupHits(rev map[string]*Fingerprint, changes []DupChange) []fileHits {
	p := newDupPass()
	queries := p.queries(rev, changes)
	if len(queries) == 0 {
		return nil
	}
	p.scan(rev, queries)
	out := make([]fileHits, 0, len(queries))
	for _, q := range queries {
		runs, parts := p.runs(q)
		hits := pickRuns(runs)
		hits = append(hits, joins(q, parts, hits)...)
		hits = append(hits, p.exprs(q, coveredBy(q, hits))...)
		slices.SortStableFunc(hits, byLine)
		out = append(out, fileHits{file: q, hits: hits})
	}

	return mergeCopies(out)
}

// coveredBy marks the statements of q that a hit already reports.
func coveredBy(q *dupFile, hits []dupHit) []bool {
	covered := make([]bool, len(q.stmts))
	for i := range hits {
		first := &hits[i].runs[0]
		for k := first.q; k < first.q+first.n; k++ {
			covered[k] = true
		}
		for j := range first.parts {
			for k := first.parts[j].q; k < first.parts[j].q+first.parts[j].n; k++ {
				covered[k] = true
			}
		}
	}

	return covered
}

// asideDirs are the directories, beside the test ones, whose files stand
// alone by design: each example, benchmark, and end-to-end fixture carries
// its own copy of what it needs, so a repeat between two of them is the point.
var asideDirs = map[string]bool{ //nolint:gochecknoglobals // fixed vocabulary
	"example": true, "examples": true, "demo": true, "demos": true, "sample": true, "samples": true,
	"bench": true, "benchmark": true, "benchmarks": true, "e2e": true, "playground": true, "playgrounds": true,
	"template": true, "templates": true, "__testfixtures__": true,
}

// asidePrefix marks a directory of files a generator copies out, one
// directory per flavor: `template-react`, `template-vue-ts`.
const asidePrefix = "template-"

// dupAside reports whether a file is a test or sits in a tree of stand-alone
// files. The rule reports neither, and never pairs one with production code.
func dupAside(file string) bool {
	if lang.IsTestPath(file) {
		return true
	}
	dir := path.Dir(file)
	for dir != "." && dir != "/" {
		if base := path.Base(dir); asideDirs[base] || strings.HasPrefix(base, asidePrefix) {
			return true
		}
		dir = path.Dir(dir)
	}

	return false
}

// Aside reports whether a file is a test, an example, a benchmark, or another
// file that stands alone by design: the files the duplicate passes leave out,
// for a caller that ranks code worth reworking and wants the same ones out.
func Aside(file string) bool { return dupAside(file) }

// dupScored reports whether the pass reads a file: code with statements.
func dupScored(file string, fp *Fingerprint) bool {
	return fp != nil && len(fp.Frags) > 0 && lang.ByPath(file) != nil
}

// queries lists the changed files that hold a new statement, with each
// statement marked added and fresh.
func (p *dupPass) queries(rev map[string]*Fingerprint, changes []DupChange) []*dupFile {
	p.net = netCounts(changes)
	net := p.net
	var out []*dupFile
	for i := range changes {
		ch := &changes[i]
		if ch.After == nil || rev[ch.Path] != ch.After || !dupScored(ch.Path, ch.After) || ch.Added == nil ||
			dupAside(ch.Path) {
			continue
		}
		f := p.file(ch.Path, ch.After)
		stmts := f.decoded()
		f.added = make([]bool, len(stmts))
		f.fresh = make([]bool, len(stmts))
		found := false
		for j := range stmts {
			f.added[j] = ch.Added(stmts[j].line)
			f.fresh[j] = f.added[j] && net.exact[stmts[j].exact] > 0 && net.shape[stmts[j].shape] > 0
			found = found || f.fresh[j]
		}
		if found {
			out = append(out, f)
		}
	}
	slices.SortFunc(out, func(a, b *dupFile) int { return strings.Compare(a.path, b.path) })

	return out
}

// netCount is, per exact hash and per shape hash, how many more statements
// the after sides of a change hold than its before sides did, and the same
// for expressions.
type netCount struct {
	exact, shape       map[uint32]int
	subExact, subShape map[uint32]int
}

// netCounts tallies a change's statements, after sides less before sides.
func netCounts(changes []DupChange) netCount {
	net := netCount{
		exact: map[uint32]int{}, shape: map[uint32]int{}, subExact: map[uint32]int{}, subShape: map[uint32]int{},
	}
	tally := func(fp *Fingerprint, by int) {
		if fp == nil {
			return
		}
		h := fp.Frags.hashes()
		for i := range len(h.shapes) / hashWidth {
			net.shape[hashAt(h.shapes, i)] += by
			net.exact[hashAt(h.exacts, i)] += by
		}
		for i := range len(h.subShapes) / hashWidth {
			net.subShape[hashAt(h.subShapes, i)] += by
			net.subExact[hashAt(h.subExacts, i)] += by
		}
	}
	for i := range changes {
		tally(changes[i].After, 1)
		tally(changes[i].Before, -1)
	}

	return net
}

// file answers the pass's record of one file, made on first use.
func (p *dupPass) file(name string, fp *Fingerprint) *dupFile {
	if f, ok := p.files[name]; ok {
		return f
	}
	f := &dupFile{path: name, fp: fp, aside: dupAside(name), fragHashes: fp.Frags.hashes()}
	p.files[name] = f

	return f
}

// scan counts, for every statement shape the change added, the places the
// tree holds it, and keeps those places while the shape is under the cap. It
// reads only the packed shape hashes of each file.
func (p *dupPass) scan(rev map[string]*Fingerprint, queries []*dupFile) {
	for _, q := range queries {
		for j, st := range q.stmts {
			if q.added[j] {
				p.df[st.shape] = 0
			}
		}
		for i := range q.subs {
			if q.fresh[q.subs[i].stmt] {
				p.subDF[q.subs[i].shape] = 0
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(rev)) {
		if !dupScored(name, rev[name]) {
			continue
		}
		h := rev[name].Frags.hashes()
		p.scanTable(name, rev[name], h.shapes, p.df, p.occ)
		p.scanTable(name, rev[name], h.subShapes, p.subDF, p.subOcc)
	}
}

// scanTable adds one file's statements or expressions to the counts and the
// kept places.
func (p *dupPass) scanTable(name string, fp *Fingerprint, shapes []byte, df map[uint32]int,
	occ map[uint32][]dupSite,
) {
	var f *dupFile
	for i := range len(shapes) / hashWidth {
		h := hashAt(shapes, i)
		n, wanted := df[h]
		if !wanted {
			continue
		}
		df[h] = n + 1
		if n <= dupCap {
			if f == nil {
				f = p.file(name, fp)
			}
			occ[h] = append(occ[h], dupSite{file: f, idx: i})
		}
	}
}

// idf weighs a statement by how rare its shape is: 1 while the tree holds it
// in at most dupFree other places, falling with the logarithm of the count to
// 0 at dupCap.
func (p *dupPass) idf(shape uint32) float64 { return rarity(p.df[shape]) }

// rarity is the weight of a shape the tree holds in so many places.
func rarity(places int) float64 {
	others := places - 1
	switch {
	case others <= dupFree:
		return 1
	case others >= dupCap:
		return 0
	}

	return 1 - math.Log(float64(others)/dupFree)/math.Log(float64(dupCap)/dupFree)
}

// runs lists the best window of every aligned run that starts from a fresh,
// rare statement of q: first the ones that meet the floor, then the ones that
// do not and may still add up with others.
func (p *dupPass) runs(q *dupFile) ([]dupRun, []dupRun) {
	var out, parts []dupRun
	type start struct {
		with *dupFile
		q, c int
	}
	seen := map[start]bool{}
	for i, st := range q.stmts {
		if !q.fresh[i] || st.header || p.idf(st.shape) == 0 {
			continue
		}
		for _, site := range p.occ[st.shape] {
			if site.idx >= len(site.file.decoded()) || !p.pairs(q, i, site) {
				continue // the first is a payload cut short
			}
			run := extend(q, i, site)
			if seen[start{run.with, run.q, run.c}] {
				continue
			}
			seen[start{run.with, run.q, run.c}] = true
			best, ok := p.window(run)
			switch {
			case best.n == 0 || best.backToBack():
			case ok:
				out = append(out, best)
			default:
				parts = append(parts, best)
			}
		}
	}

	return out, parts
}

// pairs reports whether statement i of q may be matched with a site: not with
// itself, not with a copy the change added further on (the later copy reports
// the earlier), not with a test, an example, or a benchmark, and not across
// two files kept in step.
func (p *dupPass) pairs(q *dupFile, i int, site dupSite) bool {
	c := site.file
	if c == q {
		return site.idx != i && (!q.wasAdded(site.idx) || site.idx < i)
	}

	return p.pairsFile(q, c, c.wasAdded(site.idx))
}

// pairsFile reports whether a statement of q may be matched with one of
// another file, which the change added or did not.
func (p *dupPass) pairsFile(q, c *dupFile, added bool) bool {
	if c.aside || (added && c.path > q.path) {
		return false
	}

	return !p.mirror(q, c)
}

// stem is a file's name without its extension: `index.cts` and `index.mts`
// are one file written out twice.
func stem(file string) string {
	base := path.Base(file)

	return strings.TrimSuffix(base, path.Ext(base))
}

// mirror reports whether two files of one name, whatever their extensions,
// hold mostly the same statements: a tree copied whole and kept in step,
// where every change lands twice by design.
func (p *dupPass) mirror(a, b *dupFile) bool {
	if stem(a.path) != stem(b.path) {
		return false
	}
	key := [2]string{min(a.path, b.path), max(a.path, b.path)}
	if is, ok := p.mirrors[key]; ok {
		return is
	}
	left := map[uint32]int{}
	for i := range a.count() {
		left[hashAt(a.exacts, i)]++
	}
	shared := 0
	for i := range b.count() {
		if h := hashAt(b.exacts, i); left[h] > 0 {
			left[h]--
			shared++
		}
	}
	is := float64(shared) >= mirrorShare*float64(min(a.count(), b.count()))
	p.mirrors[key] = is

	return is
}

// extend grows a seed pair into the longest run of statements of equal shape
// around it, staying on added lines in q and never letting the two stretches
// of one file overlap.
func extend(q *dupFile, i int, site dupSite) dupRun {
	c := site.file
	cs := c.decoded()
	same := func(a, b int) bool {
		if a < 0 || b < 0 || a >= len(q.stmts) || b >= len(cs) || !q.counts(a) {
			return false
		}
		x, y := &q.stmts[a], &cs[b]

		return x.shape == y.shape && x.ntok == y.ntok && len(x.slots) == len(y.slots)
	}
	lo, hi := 0, 1
	for same(i-lo-1, site.idx-lo-1) {
		lo++
	}
	for same(i+hi, site.idx+hi) {
		hi++
	}
	run := dupRun{file: q, with: c, q: i - lo, c: site.idx - lo, n: lo + hi}
	if c == q {
		run.n = min(run.n, max(run.q-run.c, run.c-run.q))
	}

	return run
}

// window answers the stretch of a run that scores highest within the
// parameter budget, when that stretch meets the floor. A stretch starts at a
// statement that counts or at a header and never ends on a compound
// statement, whose body would then be outside it: two sites that share an
// `if` line and differ in what it guards repeat a condition, not code.
func (p *dupPass) window(run dupRun) (dupRun, bool) {
	best := dupRun{}
	starts := run.n
	if run.n > maxDupWindow {
		starts = 1
	}
	for s := range starts {
		w := dupRun{file: run.file, with: run.with, q: run.q + s, c: run.c + s}
		seen := map[[2]string]bool{}
		for e := s; e < run.n; e++ {
			gained := p.grow(&w, seen)
			if e == s && !gained && !run.file.stmts[w.q].header {
				break
			}
			if len(w.pairs) > dupMaxParams {
				break
			}
			w.score = w.weight - dupParamCost*float64(len(w.pairs))
			if !run.file.stmts[w.q+w.n-1].compound && w.score > best.score {
				best = w
				best.pairs = slices.Clone(w.pairs)
			}
		}
	}

	return best, best.n > 0 && best.score >= dupMinScore
}

// backToBack reports whether a run sits right behind the code it repeats,
// with at most one statement between them, inside one function: the arms of
// one branch, or a pair such as a minimum and a maximum. There is no other
// code to extend there, only two lines a reader sees together.
func (r *dupRun) backToBack() bool {
	if r.file != r.with {
		return false
	}
	if gap := max(r.q-r.c, r.c-r.q) - r.n; gap > 1 {
		return false
	}

	return unitAt(r.file.fp, r.file.stmts[r.q].line) == unitAt(r.file.fp, r.file.stmts[r.c].line)
}

// grow adds the next statement pair to a run: its tokens, its weight when the
// statement is new, and the slots the two sites fill differently. A header
// adds neither weight nor differences: two functions always differ in name.
// It reports whether the pair added weight.
func (p *dupPass) grow(w *dupRun, seen map[[2]string]bool) bool {
	a := &w.file.stmts[w.q+w.n]
	b := &w.with.decoded()[w.c+w.n]
	fresh := w.file.isFresh(w.q + w.n)
	w.tokens += a.ntok
	w.n++
	if a.header {
		return false
	}
	gain := 0.0
	if fresh {
		gain = float64(a.ntok) * p.idf(a.shape)
	}
	w.weight += gain
	for k := range a.slots {
		x, y := w.file.names[a.slots[k]], w.with.names[b.slots[k]]
		if x == y {
			continue
		}
		if pair := [2]string{y, x}; !seen[pair] {
			seen[pair] = true
			w.pairs = append(w.pairs, pair)
		}
	}

	return gain > 0
}

// plural counts a noun: "1 statement", "2 statements".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}

	return fmt.Sprintf("%d %ss", n, noun)
}

// unitAt names the smallest declaration or method that holds a line, or ""
// for a line at file scope.
func unitAt(fp *Fingerprint, line int) string {
	best := -1
	for i := range fp.Units {
		u := &fp.Units[i]
		if u.Name == "" || u.Line > line || line > u.End {
			continue
		}
		if best < 0 || u.End-u.Line < fp.Units[best].End-fp.Units[best].Line {
			best = i
		}
	}
	if best < 0 {
		return ""
	}

	return fp.Units[best].Name
}

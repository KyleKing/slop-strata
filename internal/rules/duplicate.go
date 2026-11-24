package rules

import (
	"cmp"
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
	fp     *Fingerprint
	path   string
	added  []bool // per statement, on a line the change added; nil for an untouched file
	fresh  []bool // per statement, added and not accounted for by a statement the change removed
	stmts  []fragStmt
	names  []string
	shapes []byte
	exacts []byte
	aside  bool // a test, an example, or a benchmark
}

func (f *dupFile) count() int { return len(f.shapes) / hashWidth }

// wasAdded reports whether the change added the file's i-th statement.
func (f *dupFile) wasAdded(i int) bool { return i < len(f.added) && f.added[i] }

// decoded answers the file's statements, unpacked on first use.
func (f *dupFile) decoded() []fragStmt {
	if f.stmts == nil {
		f.stmts, f.names = f.fp.Frags.decode()
	}

	return f.stmts
}

// dupSite is one statement of the tree.
type dupSite struct {
	file *dupFile
	idx  int
}

// dupRun is two aligned stretches of statements: n of them from q in the
// changed file and from c in the file that already held them.
type dupRun struct {
	file, with *dupFile
	pairs      [][2]string // the distinct texts the two sites differ in
	q, c, n    int
	tokens     int
	weight     float64
	score      float64
}

// dupPass is one run of the rule over a change.
type dupPass struct {
	files   map[string]*dupFile
	df      map[uint32]int
	occ     map[uint32][]dupSite
	mirrors map[[2]string]bool
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

	p := &dupPass{
		files: map[string]*dupFile{}, df: map[uint32]int{}, occ: map[uint32][]dupSite{},
		mirrors: map[[2]string]bool{},
	}
	queries := p.queries(rev, changes)
	if len(queries) == 0 {
		return nil
	}
	p.scan(rev, queries)
	out := map[string][]Finding{}
	for _, q := range queries {
		if fs := dupFindings(q, pickRuns(p.runs(q))); len(fs) > 0 {
			out[q.path] = fs
		}
	}

	return out
}

// asideDirs are the directories, beside the test ones, whose files stand
// alone by design: each example, benchmark, and end-to-end fixture carries
// its own copy of what it needs, so a repeat between two of them is the point.
var asideDirs = map[string]bool{ //nolint:gochecknoglobals // fixed vocabulary
	"example": true, "examples": true, "demo": true, "demos": true, "sample": true, "samples": true,
	"bench": true, "benchmark": true, "benchmarks": true, "e2e": true, "playground": true, "playgrounds": true,
}

// dupAside reports whether a file is a test or sits in a tree of stand-alone
// files. The rule reports neither, and never pairs one with production code.
func dupAside(file string) bool {
	if lang.IsTestPath(file) {
		return true
	}
	dir := path.Dir(file)
	for dir != "." && dir != "/" {
		if asideDirs[path.Base(dir)] {
			return true
		}
		dir = path.Dir(dir)
	}

	return false
}

// dupScored reports whether the pass reads a file: code with statements.
func dupScored(file string, fp *Fingerprint) bool {
	return fp != nil && len(fp.Frags) > 0 && lang.ByPath(file) != nil
}

// queries lists the changed files that hold a new statement, with each
// statement marked added and fresh.
func (p *dupPass) queries(rev map[string]*Fingerprint, changes []DupChange) []*dupFile {
	net := netCounts(changes)
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
// the after sides of a change hold than its before sides did.
type netCount struct {
	exact, shape map[uint32]int
}

// netCounts tallies a change's statements, after sides less before sides.
func netCounts(changes []DupChange) netCount {
	net := netCount{exact: map[uint32]int{}, shape: map[uint32]int{}}
	tally := func(fp *Fingerprint, by int) {
		if fp == nil {
			return
		}
		shapes, exacts, _ := fp.Frags.hashes()
		for i := range len(shapes) / hashWidth {
			net.shape[hashAt(shapes, i)] += by
			net.exact[hashAt(exacts, i)] += by
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
	f := &dupFile{path: name, fp: fp, aside: dupAside(name)}
	f.shapes, f.exacts, _ = fp.Frags.hashes()
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
	}
	for _, name := range slices.Sorted(maps.Keys(rev)) {
		if dupScored(name, rev[name]) {
			p.scanFile(name, rev[name])
		}
	}
}

// scanFile adds one file's statements to the counts and the kept places.
func (p *dupPass) scanFile(name string, fp *Fingerprint) {
	shapes, _, _ := fp.Frags.hashes()
	var f *dupFile
	for i := range len(shapes) / hashWidth {
		h := hashAt(shapes, i)
		n, wanted := p.df[h]
		if !wanted {
			continue
		}
		p.df[h] = n + 1
		if n <= dupCap {
			if f == nil {
				f = p.file(name, fp)
			}
			p.occ[h] = append(p.occ[h], dupSite{file: f, idx: i})
		}
	}
}

// idf weighs a statement by how rare its shape is: 1 while the tree holds it
// in at most dupFree other places, falling with the logarithm of the count to
// 0 at dupCap.
func (p *dupPass) idf(shape uint32) float64 {
	others := p.df[shape] - 1
	switch {
	case others <= dupFree:
		return 1
	case others >= dupCap:
		return 0
	}

	return 1 - math.Log(float64(others)/dupFree)/math.Log(float64(dupCap)/dupFree)
}

// runs lists the best window of every aligned run that starts from a fresh,
// rare statement of q and meets the floors.
func (p *dupPass) runs(q *dupFile) []dupRun {
	var out []dupRun
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
			if best, ok := p.window(run); ok && !best.backToBack() {
				out = append(out, best)
			}
		}
	}

	return out
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
	if c.aside {
		return false
	}
	if c.wasAdded(site.idx) && c.path > q.path {
		return false
	}

	return !p.mirror(q, c)
}

// mirror reports whether two files of one base name hold mostly the same
// statements: a tree copied whole and kept in step, where every change lands
// twice by design.
func (p *dupPass) mirror(a, b *dupFile) bool {
	if path.Base(a.path) != path.Base(b.path) {
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
		if a < 0 || b < 0 || a >= len(q.stmts) || b >= len(cs) || !q.added[a] {
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
	fresh := w.file.fresh[w.q+w.n]
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

// dupHit is one stretch of a changed file with every site it repeats, the
// heaviest first.
type dupHit struct {
	runs []dupRun
}

// pickRuns orders a file's runs by score and nearness and keeps one hit per
// stretch of the file: a run over statements a heavier run already covers
// joins that run's hit as one more site, or is dropped.
func pickRuns(runs []dupRun) []dupHit {
	slices.SortFunc(runs, func(a, b dupRun) int {
		return cmp.Or(
			cmp.Compare(b.score, a.score),
			cmp.Compare(a.distance(), b.distance()),
			strings.Compare(a.with.path, b.with.path),
			cmp.Compare(a.c, b.c), cmp.Compare(a.q, b.q),
		)
	})
	var hits []dupHit
	for _, r := range runs {
		joined := false
		for i := range hits {
			first := &hits[i].runs[0]
			over := min(first.q+first.n, r.q+r.n) - max(first.q, r.q)
			if over <= 0 {
				continue
			}
			if 2*over >= first.n && !hits[i].has(r) {
				hits[i].runs = append(hits[i].runs, r)
			}
			joined = true

			break
		}
		if !joined {
			hits = append(hits, dupHit{runs: []dupRun{r}})
		}
	}
	slices.SortFunc(hits, func(a, b dupHit) int { return cmp.Compare(a.runs[0].q, b.runs[0].q) })

	return hits
}

// has reports whether a hit already lists the site a run points at.
func (h *dupHit) has(r dupRun) bool {
	for i := range h.runs {
		o := &h.runs[i]
		if o.with == r.with && o.c < r.c+r.n && r.c < o.c+o.n {
			return true
		}
	}

	return false
}

// distance ranks where the repeated code lives: 0 in the same file, 1 in the
// same directory, 2 anywhere else.
func (r *dupRun) distance() int {
	switch {
	case r.file == r.with:
		return 0
	case path.Dir(r.file.path) == path.Dir(r.with.path):
		return 1
	}

	return 2 //nolint:mnd // the third rank
}

// dupFindings renders a file's hits as findings.
func dupFindings(q *dupFile, hits []dupHit) []Finding {
	out := make([]Finding, 0, len(hits))
	for i := range hits {
		first := &hits[i].runs[0]
		line := q.stmts[first.q].line
		out = append(out, Finding{
			Rule: RuleNewDuplicate, Line: line, Scope: unitAt(q.fp, line),
			Match: first.siteName(), Note: hits[i].note(),
		})
	}

	return out
}

// siteName names the site a run repeats by its file and enclosing unit. A
// finding's id is made of it, so it leaves the line out and survives line moves.
func (r *dupRun) siteName() string {
	if unit := unitAt(r.with.fp, r.with.decoded()[r.c].line); unit != "" {
		return r.with.path + " " + unit
	}

	return r.with.path
}

// siteAt names the site a run repeats for a reader: its unit, when it has
// one, and where it starts.
func (r *dupRun) siteAt() string {
	line := r.with.decoded()[r.c].line
	at := fmt.Sprintf("%s:%d", r.with.path, line)
	if unit := unitAt(r.with.fp, line); unit != "" {
		return unit + " at " + at
	}

	return at
}

// note says what a hit repeats, where that is, how much of it there is, how
// the sites differ, and what to do instead.
func (h *dupHit) note() string {
	first := &h.runs[0]
	var b strings.Builder
	b.WriteString("repeats " + first.siteAt())
	if first.with.wasAdded(first.c) {
		b.WriteString(", which this change also adds")
	}
	b.WriteString([]string{", in this file", ", in the same directory", ", in another directory"}[first.distance()])
	for i := 1; i < len(h.runs) && i < maxDupSites; i++ {
		b.WriteString(", and " + h.runs[i].siteAt())
	}
	if more := len(h.runs) - maxDupSites; more > 0 {
		b.WriteString(", and " + plural(more, "more place"))
	}
	fmt.Fprintf(&b, ": %s, %d tokens, %s. Extend or parameterise that code instead of adding a copy",
		plural(first.n, "statement"), first.tokens, first.difference())

	return b.String()
}

// difference lists what the new site spells differently from the first one
// it repeats.
func (r *dupRun) difference() string {
	if len(r.pairs) == 0 {
		return "identical"
	}
	shown := make([]string, 0, maxDupPairs)
	for i := 0; i < len(r.pairs) && i < maxDupPairs; i++ {
		shown = append(shown, slotText(r.pairs[i][0])+"→"+slotText(r.pairs[i][1]))
	}

	return plural(len(r.pairs), "difference") + " (" + strings.Join(shown, ", ") + ")"
}

// slotText shows a slot's text, without the hash a clipped one ends in.
func slotText(text string) string {
	if cut := strings.Index(text, slotClip); cut >= 0 {
		return text[:cut+len(slotClip)]
	}

	return text
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

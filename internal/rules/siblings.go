package rules

import (
	"cmp"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/kyleking/slop-strata/internal/lang"
)

// SiblingSet is a set of classes, types, or modules written in parallel:
// each holds members of the same names whose bodies are mostly the same
// statements, so a shared base, a generic, or a table of what differs would
// stand in for the copies.
type SiblingSet struct {
	// Kind is what the sites are: "class", "type" (a Go type with the
	// methods one file gives it), or "module" (the functions of a file).
	Kind string `json:"kind"`
	// Sites are the siblings, in path order.
	Sites []SiblingSite `json:"sites"`
	// Members names the functions the siblings share: each is in line, in
	// the site the others were joined to and in at least half of the sites.
	Members []string `json:"members"`
	// Statements is how many statements of the shared members line up, in
	// order and shape for shape, at every site that holds the member.
	Statements int `json:"statements"`
	// Tokens is the tokens of those statements, counted once.
	Tokens int `json:"tokens"`
	// Differing is how many statements of the shared members do not line up,
	// summed over the sites.
	Differing int `json:"differing"`
	// Rank is, summed over the shared members, the tokens that line up times
	// one less than the sites that hold the member.
	Rank int `json:"rank"`
}

// SiblingSite is one sibling of a set.
type SiblingSite struct {
	Path string `json:"path"`
	// Name is the class or the type, and for a module the file's name
	// without its extension.
	Name    string `json:"name"`
	Line    int    `json:"line"`
	EndLine int    `json:"end_line"`
	// Others counts the site's own members of sibMinMember tokens or more
	// that the set does not share.
	Others int `json:"others"`
}

// The kinds of a sibling set.
const (
	SiblingClass  = "class"
	SiblingType   = "type"
	SiblingModule = "module"
)

// Bounds of the sibling pass follow. DESIGN.md has the prototype they came
// from and what was read to keep them.

// sibMinMember is the fewest tokens a member's body holds to count: under it
// a body is an accessor, a forwarder, or a stub an interface asks for.
const sibMinMember = 20

// sibMinShared is the fewest members two siblings share, by name and in line
// with each other.
const sibMinShared = 2

// sibMinTokens is the fewest tokens that line up across a set.
const sibMinTokens = 60

// sibAlikeNum over sibAlikeDen is the share of two members' tokens that line
// up before the two are in line: twice the tokens in common over the tokens
// of both.
const sibAlikeNum, sibAlikeDen = 7, 10

// sibCoverDen is the part of the smaller sibling's tokens that the tokens in
// line must reach.
const sibCoverDen = 3

// sibMaxSpread is the most containers that may hold a member name while it
// still proposes pairs: past it a name is the vocabulary of a framework (get,
// run, main) and says nothing of who is a sibling.
const sibMaxSpread = 40

// sibMaxCells bounds the table one alignment fills. Two longer bodies are
// compared by the statements they open and close with.
const sibMaxCells = 1 << 18

// sibMinSites is the fewest sites a set has.
const sibMinSites = 2

// sibStmt is one statement of a member's body.
type sibStmt struct {
	shape uint32
	ntok  int
}

// sibMember is one function of a container, with its body.
type sibMember struct {
	name string
	body []sibStmt
	ntok int
}

// sibContainer is one class, type, or module of the tree.
type sibContainer struct {
	fp        *Fingerprint
	path      string
	name      string
	kind      string
	lang      string
	names     []string    // every function the units list for it, sorted
	units     []int       // the unit of each name
	members   []sibMember // the ones of sibMinMember tokens or more, by name
	line, end int
	ntok      int
	read      bool
}

// sibAside reports whether the sibling pass leaves a file out: a test, a
// file of stories, a stand-alone directory, and the scripts, migrations, and
// seed files the case pass leaves out, each of which is written in parallel
// on purpose.
func sibAside(file string) bool {
	return caseAside(file)
}

// sibLang names the grammar family of a file: a .ts and a .tsx file are one.
func sibLang(file string) string {
	l := lang.ByPath(file)
	switch l {
	case nil:
		return ""
	case lang.TSX:
		return lang.TypeScript.ID
	}

	return l.ID
}

// sibContainers lists the classes, types, and modules of one file by the
// functions its units name. It reads no statement.
func sibContainers(file string, fp *Fingerprint) []*sibContainer {
	id := sibLang(file)
	if fp == nil || id == "" || len(fp.Frags) == 0 {
		return nil
	}
	classKind := SiblingClass
	if id == lang.Go.ID {
		classKind = SiblingType
	}
	module := &sibContainer{fp: fp, path: file, name: stem(file), kind: SiblingModule, lang: id}
	byOwner := map[string]*sibContainer{}
	var out []*sibContainer
	for i := range fp.Units {
		u := &fp.Units[i]
		if !u.Func || u.Name == "" {
			continue
		}
		c := module
		name := u.Name
		if dot := strings.LastIndexByte(u.Name, '.'); dot >= 0 {
			owner := u.Name[:dot]
			name = u.Name[dot+1:]
			c = byOwner[owner]
			if c == nil {
				c = &sibContainer{fp: fp, path: file, name: owner, kind: classKind, lang: id}
				byOwner[owner] = c
				out = append(out, c)
			}
		}
		c.names = append(c.names, name)
		c.units = append(c.units, i)
	}
	out = append(out, module)
	out = slices.DeleteFunc(out, func(c *sibContainer) bool { return len(c.names) < sibMinShared })
	for _, c := range out {
		c.settle()
	}

	return out
}

// settle sorts a container's functions by name, keeps the first of each
// name, and reads its lines off the units: those of the declaration when the
// file holds it, and otherwise from the first function to the last.
func (c *sibContainer) settle() {
	order := make([]int, len(c.names))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return strings.Compare(c.names[a], c.names[b]) })
	names, units := make([]string, 0, len(order)), make([]int, 0, len(order))
	for _, i := range order {
		if len(names) > 0 && names[len(names)-1] == c.names[i] {
			continue
		}
		names = append(names, c.names[i])
		units = append(units, c.units[i])
	}
	c.names, c.units = names, units
	for _, i := range c.units {
		u := &c.fp.Units[i]
		if c.line == 0 || u.Line < c.line {
			c.line = u.Line
		}
		c.end = max(c.end, u.End)
	}
	if c.kind == SiblingModule {
		return
	}
	for i := range c.fp.Units {
		if u := &c.fp.Units[i]; u.Name == c.name && u.Line <= c.line {
			c.line, c.end = u.Line, max(c.end, u.End)
		}
	}
}

// load reads the bodies of a container's functions off the file's
// statements and keeps the functions of sibMinMember tokens or more. A
// statement belongs to the smallest function that holds its line, and the
// line that declares a function belongs to none.
func (c *sibContainer) load(stmts []fragLine) {
	if c.read {
		return
	}
	c.read = true
	for i, unit := range c.units {
		u := &c.fp.Units[unit]
		from, _ := slices.BinarySearchFunc(stmts, u.Line, func(s fragLine, line int) int {
			return cmp.Compare(s.line, line)
		})
		m := sibMember{name: c.names[i]}
		for _, s := range stmts[from:] {
			if s.line > u.End {
				break
			}
			if s.header || s.ntok == 0 {
				continue
			}
			m.body = append(m.body, sibStmt{shape: s.shape, ntok: s.ntok})
			m.ntok += s.ntok
		}
		if m.ntok >= sibMinMember {
			c.members = append(c.members, m)
			c.ntok += m.ntok
		}
	}
}

// member answers the container's member of a name, or nil.
func (c *sibContainer) member(name string) *sibMember {
	i, ok := slices.BinarySearchFunc(c.members, name, func(m sibMember, name string) int {
		return strings.Compare(m.name, name)
	})
	if !ok {
		return nil
	}

	return &c.members[i]
}

// sibCommon answers the statements two bodies hold in the same order, shape
// for shape, that weigh most in tokens: a longest common subsequence. The
// statements both open with and both close with are taken first, and two
// bodies too long for a table are compared by those alone.
func sibCommon(a, b []sibStmt) []sibStmt {
	var head []sibStmt
	for len(a) > 0 && len(b) > 0 && a[0].shape == b[0].shape {
		head = append(head, a[0])
		a, b = a[1:], b[1:]
	}
	tail := 0
	for tail < len(a) && tail < len(b) && a[len(a)-1-tail].shape == b[len(b)-1-tail].shape {
		tail++
	}
	rest := a[len(a)-tail:]
	a, b = a[:len(a)-tail], b[:len(b)-tail]
	if len(a) > 0 && len(b) > 0 && len(a)*len(b) <= sibMaxCells {
		head = append(head, sibAlign(a, b)...)
	}

	return append(head, rest...)
}

// sibAlign fills the table of a longest common subsequence weighted by
// tokens and reads the statements back off it.
func sibAlign(a, b []sibStmt) []sibStmt {
	width := len(b) + 1
	best := make([]int32, (len(a)+1)*width)
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			at := i*width + j
			best[at] = max(best[at+width], best[at+1])
			if a[i].shape == b[j].shape {
				best[at] = max(best[at], best[at+width+1]+int32(a[i].ntok)) //nolint:gosec // a token count
			}
		}
	}
	var out []sibStmt
	for i, j := 0, 0; i < len(a) && j < len(b); {
		at := i*width + j
		switch {
		case a[i].shape == b[j].shape && best[at] == best[at+width+1]+int32(a[i].ntok): //nolint:gosec // as above
			out = append(out, a[i])
			i++
			j++
		case best[at] == best[at+width]:
			i++
		default:
			j++
		}
	}

	return out
}

// sibTokens sums the tokens of a run of statements.
func sibTokens(stmts []sibStmt) int {
	n := 0
	for i := range stmts {
		n += stmts[i].ntok
	}

	return n
}

// sibJoin is what two containers share: the members of one name whose bodies
// are in line, and the tokens in line over all of them.
type sibJoin struct {
	names  []string
	tokens int
}

// sibCompare lines the members of two containers up by name. Two members are
// in line when twice the tokens their bodies share reach sibAlikeNum parts in
// sibAlikeDen of the tokens of both.
func sibCompare(a, b *sibContainer) sibJoin {
	var j sibJoin
	for i := range a.members {
		m := &a.members[i]
		o := b.member(m.name)
		if o == nil {
			continue
		}
		shared := sibTokens(sibCommon(m.body, o.body))
		if 2*shared*sibAlikeDen >= (m.ntok+o.ntok)*sibAlikeNum {
			j.names = append(j.names, m.name)
			j.tokens += shared
		}
	}

	return j
}

// joins reports whether what two containers share makes them siblings:
// sibMinShared members in line, sibMinTokens tokens, and a sibCoverDen-th of
// the smaller one's tokens.
func (j *sibJoin) joins(a, b *sibContainer) bool {
	return len(j.names) >= sibMinShared && j.tokens >= sibMinTokens &&
		j.tokens*sibCoverDen >= min(a.ntok, b.ntok)
}

// sibTwins reports whether two containers are one thing kept in two
// flavors, which is parallel on purpose: two classes or types of one name,
// or of one name once "async" and "sync" are taken out of both, two modules
// of one name in one directory, and anything in two files of one name that
// the duplicate pass reads as kept in step.
func sibTwins(a, b *sibContainer) bool {
	if a.path != b.path && stem(a.path) == stem(b.path) &&
		mirrored(a.fp.Frags.hashes().exacts, b.fp.Frags.hashes().exacts) {
		return true
	}
	if a.kind == SiblingModule {
		return a.name == b.name && path.Dir(a.path) == path.Dir(b.path)
	}

	return sibFlavor(a.name) == sibFlavor(b.name)
}

// sibFlavor is a class name with what tells a flavor of it left out.
func sibFlavor(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, "async", "")

	return strings.ReplaceAll(name, "sync", "")
}

// sibPass is one run of the sibling pass over a tree.
type sibPass struct {
	all   []*sibContainer
	lines map[*Fingerprint][]fragLine
	joins map[[2]int]sibJoin
	edges [][]int // per container, the containers it joins, rising
}

// newSibPass lists the containers of a tree in path order, proposes the
// pairs that share sibMinShared names few containers hold, reads the bodies
// of those alone, and keeps the pairs that join.
func newSibPass(tree map[string]*Fingerprint) *sibPass {
	p := &sibPass{lines: map[*Fingerprint][]fragLine{}, joins: map[[2]int]sibJoin{}}
	for _, name := range slices.Sorted(maps.Keys(tree)) {
		if sibAside(name) {
			continue
		}
		p.all = append(p.all, sibContainers(name, tree[name])...)
	}
	p.edges = make([][]int, len(p.all))
	for _, pair := range p.proposals() {
		a, b := p.all[pair[0]], p.all[pair[1]]
		if sibTwins(a, b) {
			continue
		}
		p.load(a)
		p.load(b)
		if j := sibCompare(a, b); j.joins(a, b) {
			p.joins[pair] = j
			p.edges[pair[0]] = append(p.edges[pair[0]], pair[1])
			p.edges[pair[1]] = append(p.edges[pair[1]], pair[0])
		}
	}
	for i := range p.edges {
		slices.Sort(p.edges[i])
	}

	return p
}

// load reads a container's bodies, decoding its file's statements once.
func (p *sibPass) load(c *sibContainer) {
	if c.read {
		return
	}
	stmts, ok := p.lines[c.fp]
	if !ok {
		stmts = c.fp.Frags.lines()
		p.lines[c.fp] = stmts
	}
	c.load(stmts)
}

// proposals lists the pairs of containers of one kind and one language that
// share sibMinShared names, each held by at most sibMaxSpread containers, in
// order.
func (p *sibPass) proposals() [][2]int {
	holders := map[string][]int{}
	for i, c := range p.all {
		for _, name := range c.names {
			key := c.kind + "\x00" + c.lang + "\x00" + name
			holders[key] = append(holders[key], i)
		}
	}
	shared := map[[2]int]int{}
	for _, list := range holders {
		if len(list) > sibMaxSpread {
			continue
		}
		for i, a := range list {
			for _, b := range list[i+1:] {
				shared[[2]int{a, b}]++
			}
		}
	}
	var out [][2]int
	for pair, n := range shared {
		if n >= sibMinShared {
			out = append(out, pair)
		}
	}
	slices.SortFunc(out, func(a, b [2]int) int { return cmp.Or(cmp.Compare(a[0], b[0]), cmp.Compare(a[1], b[1])) })

	return out
}

// join answers what two containers share.
func (p *sibPass) join(a, b int) sibJoin {
	return p.joins[[2]int{min(a, b), max(a, b)}]
}

// sibSet is one set being gathered: a hub and the containers joined to it.
type sibSet struct {
	sites   []int     // the hub and the others, rising, which is path order
	members []sibPart // the shared members, by name
	hub     int
}

// sibPart is one shared member of a set: the statements every site that
// holds it has in line, and how many sites those are.
type sibPart struct {
	name      string
	common    []sibStmt
	sites     int
	differing int
}

func (s *sibSet) tokens() int {
	n := 0
	for i := range s.members {
		n += sibTokens(s.members[i].common)
	}

	return n
}

func (s *sibSet) rank() int {
	n := 0
	for i := range s.members {
		n += sibTokens(s.members[i].common) * (s.members[i].sites - 1)
	}

	return n
}

// sets gathers the sets of the tree. The container whose joins weigh most,
// with every container joined to it that no set holds yet, is a set, then
// the same over the containers left. Highest rank first.
func (p *sibPass) sets() []sibSet {
	weight := make([]int, len(p.all))
	order := make([]int, 0, len(p.all))
	for i, to := range p.edges {
		for _, o := range to {
			weight[i] += p.join(i, o).tokens
		}
		if len(to) > 0 {
			order = append(order, i)
		}
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(weight[b], weight[a]) })
	taken := make([]bool, len(p.all))
	var out []sibSet
	for _, hub := range order {
		if taken[hub] {
			continue
		}
		sites := []int{hub}
		for _, o := range p.edges[hub] {
			if !taken[o] {
				sites = append(sites, o)
			}
		}
		set, ok := p.gather(hub, sites)
		if !ok {
			continue
		}
		for _, s := range set.sites {
			taken[s] = true
		}
		out = append(out, set)
	}
	slices.SortStableFunc(out, func(a, b sibSet) int {
		return cmp.Or(
			cmp.Compare(b.rank(), a.rank()), cmp.Compare(len(b.sites), len(a.sites)),
			cmp.Compare(a.sites[0], b.sites[0]),
		)
	})

	return out
}

// gather settles one set around its hub. A member is shared when it is in
// line between the hub and enough of the others that half the sites hold it,
// and never fewer than two. A site in line on fewer than sibMinShared of the
// shared members is put back, and the members are counted again without it.
func (p *sibPass) gather(hub int, sites []int) (sibSet, bool) {
	for len(sites) >= sibMinSites {
		shared := p.sharedBy(hub, sites)
		kept := slices.DeleteFunc(slices.Clone(sites), func(s int) bool {
			return s != hub && p.inLine(hub, s, shared) < sibMinShared
		})
		if len(kept) == len(sites) {
			set := p.fold(hub, sites, slices.Sorted(maps.Keys(shared)))

			return set, len(set.members) >= sibMinShared && set.tokens() >= sibMinTokens
		}
		sites = kept
	}

	return sibSet{}, false
}

// sharedBy names the members in line between a hub and enough of the other
// sites that half of them all hold it, the hub counted.
func (p *sibPass) sharedBy(hub int, sites []int) map[string]bool {
	holds := map[string]int{}
	for _, s := range sites {
		if s == hub {
			continue
		}
		for _, name := range p.join(hub, s).names {
			holds[name]++
		}
	}
	need := max(sibMinSites, (len(sites)+1)/2) //nolint:mnd // half the sites, rounded up
	shared := map[string]bool{}
	for name, n := range holds {
		if n+1 >= need {
			shared[name] = true
		}
	}

	return shared
}

// inLine counts the shared members a site has in line with the hub.
func (p *sibPass) inLine(hub, site int, shared map[string]bool) int {
	n := 0
	for _, name := range p.join(hub, site).names {
		if shared[name] {
			n++
		}
	}

	return n
}

// fold reads, for each shared member, the statements in line at every site
// that holds it: the hub's body, cut down site by site in path order to what
// the next one also has.
func (p *sibPass) fold(hub int, sites []int, names []string) sibSet {
	slices.Sort(sites)
	set := sibSet{hub: hub, sites: sites}
	for _, name := range names {
		part := sibPart{name: name, common: p.all[hub].member(name).body, sites: 1}
		total := len(part.common)
		for _, s := range sites {
			if s == hub || !slices.Contains(p.join(hub, s).names, name) {
				continue
			}
			body := p.all[s].member(name).body
			part.common = sibCommon(part.common, body)
			part.sites++
			total += len(body)
		}
		part.differing = total - part.sites*len(part.common)
		if len(part.common) > 0 {
			set.members = append(set.members, part)
		}
	}

	return set
}

// SiblingSets lists the classes, types, and modules of a tree that are
// written in parallel, ranked by the tokens a shared base would take out.
// A container is a class with its methods, a Go type with the methods one
// file gives it, or the functions of a file. Two are siblings when
// sibMinShared of their members have one name and bodies in line with each
// other, the statements in line hold sibMinTokens tokens, and those are a
// third of the smaller one. Tests, stories, stand-alone directories, and
// scripts, migrations, and seed files are not read, and two flavors of one
// name are no pair.
func SiblingSets(tree map[string]*Fingerprint) []SiblingSet {
	p := newSibPass(tree)
	sets := p.sets()
	out := make([]SiblingSet, 0, len(sets))
	for i := range sets {
		out = append(out, p.export(&sets[i]))
	}

	return out
}

// export renders one set.
func (p *sibPass) export(s *sibSet) SiblingSet {
	out := SiblingSet{
		Kind: p.all[s.hub].kind, Tokens: s.tokens(), Rank: s.rank(),
		Sites: make([]SiblingSite, 0, len(s.sites)), Members: make([]string, 0, len(s.members)),
	}
	shared := map[string]bool{}
	for i := range s.members {
		m := &s.members[i]
		out.Members = append(out.Members, m.name)
		out.Statements += len(m.common)
		out.Differing += m.differing
		shared[m.name] = true
	}
	for _, at := range s.sites {
		c := p.all[at]
		site := SiblingSite{Path: c.path, Name: c.name, Line: c.line, EndLine: c.end}
		for i := range c.members {
			if !shared[c.members[i].name] {
				site.Others++
			}
		}
		out.Sites = append(out.Sites, site)
	}

	return out
}

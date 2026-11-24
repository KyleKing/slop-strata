package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"math"
	"os/exec"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/kyleking/aragonite/vcs"

	"github.com/kyleking/slop-strata/internal/archtrend"
	"github.com/kyleking/slop-strata/internal/depth"
	"github.com/kyleking/slop-strata/internal/engine"
	"github.com/kyleking/slop-strata/internal/intent"
	"github.com/kyleking/slop-strata/internal/lang"
	"github.com/kyleking/slop-strata/internal/lineage"
	"github.com/kyleking/slop-strata/internal/report"
	"github.com/kyleking/slop-strata/internal/rules"
)

// depthMovers caps the directories listed under depth_moved.
const depthMovers = 5

// depthMoveFloor is the smallest depth move worth a row.
const depthMoveFloor = 0.05

// depthScale rounds a depth move to the four decimals a depth has.
const depthScale = 1e4

// hiddenPairs caps the pairs listed under hidden_coupling.
const hiddenPairs = 10

// architecture is how the inferred import graph changed over a history walk.
// See Docs["architecture"].
type architecture struct {
	// Periods has one row per week with a commit, oldest first.
	Periods []archPeriod `json:"periods"`
	// DepthMoved lists the directories whose depth moved most from the first
	// period to the last. See Docs["depth_moved"].
	DepthMoved []depthMove `json:"depth_moved"`
	// Cycles lists the import cycles of the last period with where each came
	// from. See Docs["cycle_origin"].
	Cycles []cycleOrigin `json:"cycles"`
	// HiddenCoupling lists file pairs that change together with no import
	// between them. See Docs["hidden_coupling"].
	HiddenCoupling []archtrend.Pair `json:"hidden_coupling"`
}

// archPeriod is the import graph of the tree a week ended on.
type archPeriod struct {
	MedianDepth           *float64 `json:"median_depth"`
	MedianDepthAttributed *float64 `json:"median_depth_attributed"`
	Week                  string   `json:"week"`
	Rev                   string   `json:"rev"`
	Cycles                int      `json:"cycles"`
	LargestCycle          int      `json:"largest_cycle"`
	FilesInCycles         int      `json:"files_in_cycles"`
	DepthDirs             int      `json:"depth_dirs"`
}

// depthMove is one directory's depth at the first and the last period.
type depthMove struct {
	Dir            string  `json:"dir"`
	DepthFrom      float64 `json:"depth_from"`
	DepthTo        float64 `json:"depth_to"`
	AttributedFrom float64 `json:"depth_attributed_from"`
	AttributedTo   float64 `json:"depth_attributed_to"`
	Moved          float64 `json:"moved"`
}

// cycleOrigin is one import cycle of the last period and the commit it was
// first present after.
type cycleOrigin struct {
	Introduced   *cycleIntro `json:"introduced"`
	ID           string      `json:"id"`
	FirstWeek    string      `json:"first_week"`
	Members      []string    `json:"members"`
	Size         int         `json:"size"`
	FirstSize    int         `json:"first_size"`
	BeforeWindow bool        `json:"before_window"`
}

// cycleIntro is the commit a cycle was first present after.
type cycleIntro struct {
	Commit      string `json:"commit"`
	AuthorClass string `json:"author_class"`
	Intent      string `json:"intent"`
}

// archCommit is what the architecture pass keeps of one walked commit.
type archCommit struct {
	hash    string
	week    string
	klass   string
	kind    string
	renames [][2]string
	// graph is set when the commit changed a file the import graph reads.
	graph bool
}

// archWalk records, during the walk, the commits to sample trees at and the
// files that change together.
type archWalk struct {
	co *archtrend.CoChange
	// walked holds the blob ids the walk scored, as the log wrote them. The
	// log abbreviates an id and a tree spells it out, and the cache is keyed
	// by the id as written, so a tree's blob is looked up under the short id
	// the walk stored it by.
	walked  map[string]bool
	base    string
	week    string
	commits []archCommit
	// lengths are the distinct lengths of the ids in walked.
	lengths []int
}

func newArchWalk() *archWalk {
	return &archWalk{co: archtrend.NewCoChange(), walked: map[string]bool{}}
}

// note records a blob id the walk scored.
func (aw *archWalk) note(sha string) {
	if !isBlobSHA(sha) || aw.walked[sha] {
		return
	}
	aw.walked[sha] = true
	if !slices.Contains(aw.lengths, len(sha)) {
		aw.lengths = append(aw.lengths, len(sha))
	}
}

// cachedAs answers the id the walk scored a tree's blob under, or the tree's
// own id when the walk never saw the blob.
func (aw *archWalk) cachedAs(sha string) string {
	for _, n := range aw.lengths {
		if n < len(sha) && aw.walked[sha[:n]] {
			return sha[:n]
		}
	}

	return sha
}

// graphFile reports whether the import graph reads a path: a source file of a
// supported language, or a go.mod.
func graphFile(file string) bool {
	return lang.ByPath(file) != nil || lang.TextKind(file) == lang.TextManifest
}

// commit records one commit. Its week is its author week, or the latest week
// of the commits before it when it was authored earlier than they were, so
// the weeks run in walk order.
func (aw *archWalk) commit(rec *vcs.CommitRecord, klass, kind string, reports map[engine.Blob]*engine.FileReport) {
	if len(aw.commits) == 0 && len(rec.Parents) > 0 {
		aw.base = rec.Parents[0]
	}
	aw.week = max(aw.week, mondayOf(rec.AuthorTime))
	c := archCommit{hash: rec.Hash, week: aw.week, klass: klass, kind: kind}
	var paired []string
	for i := range rec.Files {
		f := &rec.Files[i]
		if f.Status == vcs.StatusRenamed && f.OldPath != "" {
			c.renames = append(c.renames, [2]string{f.OldPath, f.Path})
			aw.co.Rename(f.OldPath, f.Path)
			c.graph = c.graph || graphFile(f.OldPath)
		}
		if graphFile(f.Path) {
			c.graph = true
			aw.note(f.NewSHA)
			aw.note(f.OldSHA)
		}
		rep, ok := reports[engine.Blob{Path: f.Path, SHA: f.NewSHA}]
		if ok && rep.Skipped == "" && rep.Metrics.Lang != "" && !lang.IsTestPath(f.Path) {
			paired = append(paired, f.Path)
		}
	}
	if klass != botClass && kind != string(intent.Docs) && kind != string(intent.Chore) {
		aw.co.Commit(paired, len(rec.Files))
	}
	aw.commits = append(aw.commits, c)
}

// treeGraphs resolves the import graph of one revision after another. It
// holds the fingerprints of the last tree it read, so the next tree costs a
// cache read for the blobs that differ and a parse for the ones never scored.
type treeGraphs struct {
	s      *session
	reader *vcs.BlobReader
	held   map[engine.Blob]*rules.Fingerprint
	// cachedAs maps a tree's blob id to the id the cache holds it under.
	cachedAs func(sha string) string
	// paths is every path of the last tree read.
	paths []string
}

// imports resolves the import graph of rev's tree.
func (t *treeGraphs) imports(ctx context.Context, rev string) (*rules.Imports, error) {
	entries, err := t.s.ops.TreeEntries(ctx, t.s.root, rev)
	if err != nil {
		return nil, fmt.Errorf("listing tree at %s: %w", rev, err)
	}
	paths := make([]string, 0, len(entries))
	var reqs []engine.Request
	for _, e := range entries {
		paths = append(paths, e.Path)
		if graphFile(e.Path) {
			reqs = append(reqs, engine.Request{Path: e.Path, SHA: e.SHA, Text: lang.ByPath(e.Path) == nil})
		}
	}
	if err := t.hold(ctx, reqs); err != nil {
		return nil, err
	}
	t.paths = paths
	blobs := make([]rules.BlobPrint, 0, len(reqs))
	for _, r := range reqs {
		if fp := t.held[engine.Blob{Path: r.Path, SHA: r.SHA}]; fp != nil {
			blobs = append(blobs, rules.BlobPrint{Path: r.Path, Print: fp})
		}
	}

	return rules.ResolveImports(blobs, paths), nil
}

// hold replaces the held fingerprints with those of reqs, reading from the
// cache what the last tree did not hold and from git what the cache lacks.
func (t *treeGraphs) hold(ctx context.Context, reqs []engine.Request) error {
	next := make(map[engine.Blob]*rules.Fingerprint, len(reqs))
	var ask []engine.Request
	for _, r := range reqs {
		b := engine.Blob{Path: r.Path, SHA: r.SHA}
		if fp, ok := t.held[b]; ok {
			next[b] = fp
		} else {
			ask = append(ask, r)
		}
	}
	// A blob is cached under the id the walk scored it by, or under its own.
	ask = t.fromCache(ctx, ask, next, t.cachedAs)
	ask = t.fromCache(ctx, ask, next, func(sha string) string { return sha })
	shas := make([]string, 0, len(ask))
	for _, r := range ask {
		shas = append(shas, r.SHA)
	}
	content, err := t.reader.Read(shas)
	if err != nil {
		return fmt.Errorf("reading blobs: %w", err)
	}
	for i := range ask {
		ask[i].Content = content[ask[i].SHA]
	}
	ask = slices.DeleteFunc(ask, func(r engine.Request) bool { return r.Content == nil })
	for b, rep := range t.s.eng.AnalyzeAll(ctx, ask) {
		next[b] = rep.Print
	}
	t.held = next

	return nil
}

// fromCache fills into next the fingerprints the cache holds for ask, each
// looked up under the id that key gives its blob, and answers the requests
// the cache did not hold.
func (t *treeGraphs) fromCache(ctx context.Context, ask []engine.Request, next map[engine.Blob]*rules.Fingerprint,
	key func(sha string) string,
) []engine.Request {
	lookups := make([]engine.Request, len(ask))
	for i, r := range ask {
		lookups[i] = r
		lookups[i].SHA = key(r.SHA)
	}
	found := t.s.eng.LookupPrints(ctx, lookups)
	var missing []engine.Request
	for i, r := range ask {
		if rep, ok := found[engine.Blob{Path: r.Path, SHA: lookups[i].SHA}]; ok {
			next[engine.Blob{Path: r.Path, SHA: r.SHA}] = rep.Print
		} else {
			missing = append(missing, r)
		}
	}

	return missing
}

// archPass is the state of the pass that samples the walked commits.
type archPass struct {
	trees *treeGraphs
	// last is the graph of the last sampled tree.
	last *rules.Imports
	// intros maps a track's origin to the commit that introduced it.
	intros map[int]*cycleIntro
	// first and final are the module depths of the first and last samples.
	first map[string]*depth.Module
	final map[string]*depth.Module
	// files are the paths of the last sampled tree that have a fingerprint,
	// and paths is every path of that tree.
	files   map[string]bool
	paths   []string
	tracks  []*archtrend.Track
	periods []archPeriod
	tracker archtrend.Cycles
	// hasBase is set when the tree before the first commit could be read.
	hasBase bool
}

// architecture samples the tree each week of the walk ended on and rolls the
// samples up. A tree whose week changed no file the graph reads repeats the
// week before.
func (s *session) architecture(ctx context.Context, aw *archWalk, reader *vcs.BlobReader) (*architecture, error) {
	p := &archPass{
		trees: &treeGraphs{s: s, reader: reader, cachedAs: aw.cachedAs}, intros: map[int]*cycleIntro{},
	}
	res := &architecture{
		Periods: []archPeriod{}, DepthMoved: []depthMove{}, Cycles: []cycleOrigin{},
		HiddenCoupling: []archtrend.Pair{},
	}
	if len(aw.commits) == 0 {
		return res, nil
	}
	p.readBase(ctx, aw.base)
	start, changed := 0, true
	for i := range aw.commits {
		c := &aw.commits[i]
		for _, r := range c.renames {
			p.tracker.Rename(r[0], r[1])
		}
		changed = changed || c.graph
		if i+1 < len(aw.commits) && aw.commits[i+1].week == c.week {
			continue
		}
		if err := p.sample(ctx, aw.commits[start:i+1], changed); err != nil {
			return nil, err
		}
		start, changed = i+1, false
	}
	res.Periods = p.periods
	res.DepthMoved = depthMoves(p.first, p.final)
	res.Cycles = p.origins()
	res.HiddenCoupling = aw.co.Hidden(p.scope(), hiddenPairs)

	return res, nil
}

// readBase observes the cycles of the tree before the first walked commit, so
// a cycle older than the window is told from one its first week introduced. A
// root commit has an empty tree before it. A shallow clone cannot read that
// tree, whether its first commit names a parent git lacks or, at the clone's
// boundary, names none.
func (p *archPass) readBase(ctx context.Context, base string) {
	if base == "" {
		p.hasBase = !p.trees.s.shallow(ctx)

		return
	}
	im, err := p.trees.imports(ctx, base)
	if err != nil {
		return
	}
	p.hasBase = true
	p.tracker.Observe(-1, cycleSets(im))
}

// shallow reports whether the repository is a shallow clone, whose oldest
// commits read as parentless. It answers true when git cannot say.
func (s *session) shallow(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--is-shallow-repository")
	cmd.Dir = s.root
	out, err := cmd.Output()

	return err != nil || strings.TrimSpace(string(out)) != "false"
}

// cycleSets lists the member files of each cycle of a graph.
func cycleSets(im *rules.Imports) [][]string {
	cycles := im.Cycles()
	sets := make([][]string, 0, len(cycles))
	for i := range cycles {
		sets = append(sets, cycles[i].Members)
	}

	return sets
}

// sample reads the tree a week ended on, the last of its commits, and adds the
// week's row. Changed is false when no commit since the last sample changed a
// file the graph reads.
func (p *archPass) sample(ctx context.Context, week []archCommit, changed bool) error {
	end := &week[len(week)-1]
	if !changed && len(p.periods) > 0 {
		row := p.periods[len(p.periods)-1]
		row.Week, row.Rev = end.week, end.hash
		p.periods = append(p.periods, row)

		return nil
	}
	im, err := p.trees.imports(ctx, end.hash)
	if err != nil {
		return err
	}
	modules := im.Depth(lineage.DirOf)
	row := archPeriod{Week: end.week, Rev: end.hash}
	sets := cycleSets(im)
	row.Cycles = len(sets)
	for _, members := range sets {
		row.LargestCycle = max(row.LargestCycle, len(members))
		row.FilesInCycles += len(members)
	}
	row.DepthDirs, row.MedianDepth, row.MedianDepthAttributed = depthSummary(modules)
	index := len(p.periods)
	p.periods = append(p.periods, row)
	if p.first == nil {
		p.first = modules
	}
	p.last, p.final = im, modules
	p.paths, p.files = p.trees.paths, make(map[string]bool, len(p.trees.held))
	for b, fp := range p.trees.held {
		p.files[b.Path] = fp != nil
	}
	p.tracks = p.tracker.Observe(index, sets)
	for _, t := range p.tracks {
		if _, seen := p.intros[t.Origin]; seen || t.First != index || (index == 0 && !p.hasBase) {
			continue
		}
		intro, err := p.introducedBy(ctx, week, t.FirstMembers)
		if err != nil {
			return err
		}
		p.intros[t.Origin] = intro
	}

	return nil
}

// introducedBy finds, by bisection over one week's commits, the commit a
// cycle is first present after. The cycle is absent before the week and
// present at its end. A cycle that came and went inside the week is placed at
// one of the commits that brought it back.
func (p *archPass) introducedBy(ctx context.Context, week []archCommit, members []string) (*cycleIntro, error) {
	lo, hi := -1, len(week)-1
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2 //nolint:mnd // the midpoint
		im, err := p.trees.imports(ctx, week[mid].hash)
		if err != nil {
			return nil, err
		}
		present := false
		for _, set := range cycleSets(im) {
			for _, c := range week[mid+1:] {
				for _, r := range c.renames {
					set = archtrend.Renamed(set, r[0], r[1])
				}
			}
			present = present || archtrend.Continues(set, members)
		}
		if present {
			hi = mid
		} else {
			lo = mid
		}
	}
	c := &week[hi]

	return &cycleIntro{Commit: c.hash, AuthorClass: c.klass, Intent: c.kind}, nil
}

// origins lists the cycles of the last period, largest first.
func (p *archPass) origins() []cycleOrigin {
	out := make([]cycleOrigin, 0, len(p.tracks))
	for _, t := range p.tracks {
		id, key := p.last.CycleID(t.Members)
		o := cycleOrigin{
			ID: id, Members: key, Size: len(t.Members), FirstSize: len(t.FirstMembers),
			Introduced: p.intros[t.Origin], BeforeWindow: t.First < 0 || p.intros[t.Origin] == nil,
		}
		if !o.BeforeWindow {
			o.FirstWeek = p.periods[t.First].Week
		}
		out = append(out, o)
	}
	slices.SortFunc(out, func(a, b cycleOrigin) int {
		return cmp.Or(cmp.Compare(b.Size, a.Size), strings.Compare(a.ID, b.ID))
	})

	return out
}

// scope is what the hidden coupling pass reads of the last sampled tree: its
// graph, its scored production source files, and each file's package, the
// directory of the nearest package.json above it.
func (p *archPass) scope() archtrend.Scope {
	roots := map[string]bool{}
	for _, file := range p.paths {
		if path.Base(file) == "package.json" {
			roots[path.Dir(file)] = true
		}
	}

	return archtrend.Scope{
		Graph: p.last.Graph,
		Live: func(file string) bool {
			return p.files[file] && lang.ByPath(file) != nil && !lang.IsTestPath(file)
		},
		Package: func(file string) string {
			dir := path.Dir(file)
			for dir != "." && !roots[dir] {
				dir = path.Dir(dir)
			}

			return dir
		},
	}
}

// depthSummary answers how many modules have a depth read off enough names,
// and the median of their depth and of their attributed depth.
func depthSummary(modules map[string]*depth.Module) (int, *float64, *float64) {
	var lower, upper []float64
	for _, m := range modules {
		if m.Depth == nil || m.Provisional {
			continue
		}
		lower = append(lower, *m.Depth)
		if m.DepthAttributed != nil {
			upper = append(upper, *m.DepthAttributed)
		}
	}

	return len(lower), medianOrNil(lower), medianOrNil(upper)
}

func medianOrNil(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	m := math.Round(median(values)*depthScale) / depthScale

	return &m
}

// depthMoves lists the directories whose depth moved most between two trees.
// A directory is listed when it has a depth read off enough names in both, and
// its depth and its attributed depth moved the same way by depthMoveFloor or
// more: the smaller of the two moves is the one reported, so a method matched
// by name on one side alone never makes a row.
func depthMoves(first, last map[string]*depth.Module) []depthMove {
	out := []depthMove{}
	for dir, to := range last {
		from := first[dir]
		if from == nil || !solidDepth(from) || !solidDepth(to) {
			continue
		}
		lower, upper := *to.Depth-*from.Depth, *to.DepthAttributed-*from.DepthAttributed
		if lower*upper <= 0 {
			continue
		}
		moved := upper
		if (lower < 0 && lower > upper) || (lower > 0 && lower < upper) {
			moved = lower
		}
		if moved < depthMoveFloor && moved > -depthMoveFloor {
			continue
		}
		out = append(out, depthMove{
			Dir: dir, DepthFrom: *from.Depth, DepthTo: *to.Depth,
			AttributedFrom: *from.DepthAttributed, AttributedTo: *to.DepthAttributed,
			Moved: math.Round(moved*depthScale) / depthScale,
		})
	}
	slices.SortFunc(out, func(a, b depthMove) int {
		return cmp.Or(cmp.Compare(max(b.Moved, -b.Moved), max(a.Moved, -a.Moved)), strings.Compare(a.Dir, b.Dir))
	})

	return out[:min(len(out), depthMovers)]
}

// solidDepth reports whether a module has both depths, read off enough names.
func solidDepth(m *depth.Module) bool {
	return m.Depth != nil && m.DepthAttributed != nil && !m.Provisional
}

// pairPathWidth is the longest path a hidden coupling row prints per file.
const pairPathWidth = 28

// shortRev is how much of a commit id the text tables print.
const shortRev = 10

// printArchitecture renders the weekly graph rows that changed, the
// directories whose depth moved, where each cycle came from, and the pairs
// that change together with no import between them.
func printArchitecture(w io.Writer, arch *architecture) {
	if arch == nil || len(arch.Periods) == 0 {
		return
	}
	printArchPeriods(w, arch.Periods)
	if len(arch.DepthMoved) > 0 {
		rows := make([][]string, 0, len(arch.DepthMoved))
		for _, m := range arch.DepthMoved {
			rows = append(rows, []string{
				m.Dir, fmt.Sprintf("%.2f → %.2f", m.DepthFrom, m.DepthTo),
				fmt.Sprintf("%.2f → %.2f", m.AttributedFrom, m.AttributedTo),
			})
		}
		header := []string{"depth_moved", colDepth, "depth_attributed"}
		shortenFirstColumn(header, rows)
		report.Fprintln(w)
		report.Table(w, header, rows)
	}
	printCycleOrigins(w, arch.Cycles)
	printHiddenCoupling(w, arch.HiddenCoupling)
}

// printArchPeriods renders the first week, the last, and each week whose
// cycle counts differ from the week before. The JSON lists every week.
func printArchPeriods(w io.Writer, periods []archPeriod) {
	rows := make([][]string, 0, len(periods))
	var prev [3]int
	same := 0
	for i := range periods {
		p := &periods[i]
		counts := [...]int{p.Cycles, p.LargestCycle, p.FilesInCycles}
		if i > 0 && i < len(periods)-1 && counts == prev {
			same++

			continue
		}
		prev = counts
		depths := depthText(p.MedianDepth)
		if upper := depthText(p.MedianDepthAttributed); upper != depths {
			depths += "–" + upper
		}
		rows = append(rows, []string{
			p.Week, strconv.Itoa(p.Cycles), strconv.Itoa(p.LargestCycle), strconv.Itoa(p.FilesInCycles),
			strconv.Itoa(p.DepthDirs), depths,
		})
	}
	report.Fprintln(w)
	report.Table(w, []string{"architecture", "cycles", "largest", "in_cycles", "depth_dirs", "median_depth"}, rows)
	if same > 0 {
		report.Fprintf(w, "%d weeks with the cycle counts of the week before not shown\n", same)
	}
}

// printCycleOrigins renders where each cycle of the last week came from.
func printCycleOrigins(w io.Writer, cycles []cycleOrigin) {
	if len(cycles) == 0 {
		return
	}
	rows := make([][]string, 0, len(cycles))
	for i := range cycles {
		c := &cycles[i]
		row := []string{c.Members[0], c.ID, strconv.Itoa(c.Size), "before window", strconv.Itoa(c.FirstSize), "", ""}
		if in := c.Introduced; in != nil {
			row[3] = c.FirstWeek
			row[5], row[6] = in.Commit[:min(len(in.Commit), shortRev)], in.AuthorClass+" "+in.Intent
		}
		rows = append(rows, row)
	}
	header := []string{"cycle with", "id", "files", "first_seen", "then", "commit", "by"}
	shortenFirstColumn(header, rows)
	report.Fprintln(w)
	report.Table(w, header, rows)
}

// printHiddenCoupling renders the pairs that change together with no import
// between them.
func printHiddenCoupling(w io.Writer, pairs []archtrend.Pair) {
	if len(pairs) == 0 {
		return
	}
	rows := make([][]string, 0, len(pairs))
	for i := range pairs {
		p := &pairs[i]
		shared := ""
		if p.SharedImporter {
			shared = "yes"
		}
		rows = append(rows, []string{
			report.ElidePath(p.A, pairPathWidth), report.ElidePath(p.B, pairPathWidth), strconv.Itoa(p.CoChanges),
			fmt.Sprintf("%d/%d", p.ChangesA, p.ChangesB), fmt.Sprintf("%.2f", p.Support),
			fmt.Sprintf("%.2f", p.Confidence), shared,
		})
	}
	report.Fprintln(w)
	report.Table(w, []string{"hidden_coupling", "and", "together", "changes", "support", "conf", "shared"}, rows)
}

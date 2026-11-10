package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/kyleking/aragonite/vcs"

	"github.com/kyleking/slop-strata/internal/engine"
	"github.com/kyleking/slop-strata/internal/intent"
	"github.com/kyleking/slop-strata/internal/lang"
	"github.com/kyleking/slop-strata/internal/lineage"
	"github.com/kyleking/slop-strata/internal/reading"
	"github.com/kyleking/slop-strata/internal/report"
	"github.com/kyleking/slop-strata/internal/rules"
	"github.com/kyleking/slop-strata/internal/structure"
)

// defaultRev is the revision change and snapshot default to.
const defaultRev = "HEAD"

// emptyTree is git's well-known empty tree, the base a root commit diffs from.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// side is one version of a changed file.
type side struct {
	err     error
	content []byte
}

// changeResult is the change subcommand's JSON shape.
type changeResult struct {
	beforeByPath map[string]structure.FileMetrics
	Context      *contextPayload     `json:"context,omitempty"`
	Dirs         map[string]dirDelta `json:"dirs"`
	dirTouches   map[string]int
	afterByPath  map[string]structure.FileMetrics
	Base         string        `json:"base"`
	End          string        `json:"end"`
	Reading      string        `json:"reading"`
	Intent       intent.Intent `json:"intent"`
	Findings     []findingRow  `json:"findings"`
	Errors       []skippedRow  `json:"errors,omitempty"`
	Skipped      []skippedRow  `json:"skipped,omitempty"`
	Files        []fileDelta   `json:"files"`
	Delta        deltaSummary  `json:"delta"`
	highFuncs    funcCounts
}

type deltaSummary struct {
	MassAdded     float64 `json:"mass_added"`
	MassRemoved   float64 `json:"mass_removed"`
	MassNet       float64 `json:"mass_net"`
	MassGrown     float64 `json:"mass_grown"`
	OverLimit     float64 `json:"over_limit"`
	ErosionBefore float64 `json:"erosion_before"`
	ErosionAfter  float64 `json:"erosion_after"`
	LinesAdded    int     `json:"lines_added"`
	LinesDeleted  int     `json:"lines_deleted"`
}

type dirDelta struct {
	MassNet       float64 `json:"mass_net"`
	ErosionBefore float64 `json:"erosion_before"`
	ErosionAfter  float64 `json:"erosion_after"`
	Hotspot       bool    `json:"hotspot"`
}

type findingRow struct {
	Path  string `json:"path"`
	Rule  string `json:"rule"`
	Match string `json:"match,omitempty"`
	Line  int    `json:"line"`
	New   bool   `json:"new"`
}

type fileDelta struct {
	Path       string  `json:"path"`
	Status     string  `json:"status"`
	Lang       string  `json:"lang,omitempty"`
	MassBefore float64 `json:"mass_before"`
	MassAfter  float64 `json:"mass_after"`
	MassGrown  float64 `json:"mass_grown"`
	OverLimit  float64 `json:"over_limit"`
	Added      int     `json:"added"`
	Deleted    int     `json:"deleted"`
}

type skippedRow struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// percentScale converts a fraction to a percent for display.
const percentScale = 100

// parseErrorRow reports a blob tree-sitter could not fully parse, with the
// byte share under error nodes.
func parseErrorRow(path string, m structure.FileMetrics) skippedRow {
	return skippedRow{
		path,
		fmt.Sprintf("tree-sitter parse error (%.1f%% of bytes)", m.ErrorCoverage*percentScale),
	}
}

type contextPayload struct {
	Hunks      map[string][]vcs.DiffHunk `json:"hunks"`
	Snippets   map[string]map[int]string `json:"snippets"`        // path -> line -> text
	DirTouches map[string]int            `json:"dir_touches_90d"` // commits touching each dir in 90 days
	Crossed    []crossedFunc             `json:"crossed"`         // functions over CC 10 on either side
	Messages   []string                  `json:"messages"`
}

// crossedFunc is a function that sits over the complexity line on one side of
// the change — the functions a reader asks about first.
type crossedFunc struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	CC     int    `json:"cc"`
	Lines  int    `json:"lines"`
	Before bool   `json:"before,omitempty"`
	After  bool   `json:"after,omitempty"`
}

func (s *session) changeCmd(ctx context.Context, a *args, w io.Writer) error {
	end := a.rev
	if end == "" {
		end = defaultRev
	}
	base, err := s.changeBase(ctx, a.base, end)
	if err != nil {
		return err
	}

	var files []vcs.CommitFile
	if a.rev != "" {
		d, err := s.ops.DiffRefs(ctx, s.root, base, a.rev)
		if err != nil {
			return fmt.Errorf("diff %s..%s: %w", base, a.rev, err)
		}
		files = d
	} else {
		files, err = s.worktreeFiles(ctx, base)
		if err != nil {
			return err
		}
	}

	res, err := s.scoreChange(ctx, base, end, files, a.rev != "")
	if err != nil {
		return err
	}

	if a.json {
		if a.ctx {
			res.Context = s.buildContext(ctx, base, end, files, res)
		}
		if err := report.WriteJSON(w, "change", s.root, res, warnings(res)); err != nil {
			return fmt.Errorf("writing result: %w", err)
		}

		return nil
	}
	if a.ctx {
		report.Fprintln(w, "--context only applies to --json output")
	}

	printChange(w, res, a.all)

	return nil
}

// changeBase names the commit a change diffs from. A root commit has no parent
// to diff from, so its base is git's empty tree.
func (s *session) changeBase(ctx context.Context, base, end string) (string, error) {
	if base != "" {
		_, err := s.ops.ResolveRef(ctx, s.root, base)
		switch {
		case err == nil:
			return base, nil
		case s.isRootParent(ctx, base):
			return emptyTree, nil
		default:
			return "", fmt.Errorf("base %s: %w", base, err)
		}
	}
	branch, err := s.defaultBranch(ctx)
	if err != nil {
		return "", err
	}
	merged, err := s.ops.MergeBase(ctx, s.root, end, s.upstreamOf(ctx, branch))
	switch {
	case err == nil:
		return merged, nil
	case s.isRoot(ctx, end):
		return emptyTree, nil
	default:
		return "", fmt.Errorf("merge-base %s..%s: %w", end, branch, err)
	}
}

// upstreamOf names the remote-tracking branch behind a local default branch,
// so commits made locally and not yet pushed count as the change. It falls
// back to the local branch when none is set.
func (s *session) upstreamOf(ctx context.Context, branch string) string {
	if up, err := s.ops.ResolveRef(ctx, s.root, branch+"@{upstream}"); err == nil {
		return up
	}

	return branch
}

// isRootParent reports whether base names the parent of a root commit.
func (s *session) isRootParent(ctx context.Context, base string) bool {
	root, ok := strings.CutSuffix(base, "^")

	return ok && s.isRoot(ctx, root)
}

// isRoot reports whether rev names a commit with no parent.
func (s *session) isRoot(ctx context.Context, rev string) bool {
	if _, err := s.ops.ResolveRef(ctx, s.root, rev); err != nil {
		return false
	}
	_, err := s.ops.ResolveRef(ctx, s.root, rev+"^")

	return err != nil
}

// defaultBranch resolves the branch the change diffs against.
func (s *session) defaultBranch(ctx context.Context) (string, error) {
	if s.cfg.DefaultBranch != "" {
		return s.cfg.DefaultBranch, nil
	}
	name, err := s.ops.DefaultBranchName(ctx, s.root)
	if err != nil {
		return "", fmt.Errorf("resolving default branch: %w", err)
	}

	return name, nil
}

// worktreeFiles diffs base against the working tree and adds the untracked
// files as additions.
func (s *session) worktreeFiles(ctx context.Context, base string) ([]vcs.CommitFile, error) {
	files, err := s.ops.DiffWorktree(ctx, s.root, base)
	if err != nil {
		return nil, fmt.Errorf("diff %s..worktree: %w", base, err)
	}
	untracked, err := s.ops.UntrackedFiles(ctx, s.root)
	if err != nil {
		return nil, fmt.Errorf("listing untracked files: %w", err)
	}
	added, err := untrackedAsAdds(s.root, untracked)
	if err != nil {
		return nil, err
	}

	return append(files, added...), nil
}

// untrackedAsAdds turns untracked paths into added-file records whose hunk
// spans every line, so they count toward lines added. Their blobs are scored
// straight off disk.
func untrackedAsAdds(root string, paths []string) ([]vcs.CommitFile, error) {
	out := make([]vcs.CommitFile, 0, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(filepath.Join(root, p)) //nolint:gosec // p is git's own listing of the worktree
		if err != nil {
			return nil, fmt.Errorf("reading untracked %s: %w", p, err)
		}
		lines := bytes.Count(data, []byte{'\n'})
		if len(data) > 0 && data[len(data)-1] != '\n' {
			lines++
		}
		out = append(out, vcs.CommitFile{
			Path: p, Status: vcs.StatusAdded,
			Hunks: []vcs.DiffHunk{{NewStart: 1, NewLines: lines}},
		})
	}

	return out, nil
}

// scoredFile is one changed file's delta plus the metrics each side yielded.
type scoredFile struct {
	before      *structure.FileMetrics
	after       *structure.FileMetrics
	beforePrint *rules.Fingerprint
	afterPrint  *rules.Fingerprint
	delta       fileDelta
}

// scoreChange analyzes both sides of every changed file.
func (s *session) scoreChange(ctx context.Context, base, end string, files []vcs.CommitFile,
	committed bool,
) (*changeResult, error) {
	oldBlobs, err := s.oldBlobs(ctx, files)
	if err != nil {
		return nil, err
	}
	baseline, err := rules.ReadBaseline(filepath.Join(s.root, s.cfg.Baseline))
	if err != nil {
		return nil, fmt.Errorf("reading baseline: %w", err)
	}

	res := &changeResult{
		Base: base, End: end, Dirs: map[string]dirDelta{},
		beforeByPath: map[string]structure.FileMetrics{}, afterByPath: map[string]structure.FileMetrics{},
	}
	var before, after []structure.FileMetrics
	old := map[string]bool{}
	scored := make([]scoredFile, 0, len(files))
	for i := range files {
		sf, err := s.scoreFile(ctx, &files[i], oldBlobs, committed, baseline, res, old)
		if err != nil {
			return nil, err
		}
		scored = append(scored, sf)
		if sf.before != nil {
			before = append(before, *sf.before)
		}
		if sf.after != nil {
			after = append(after, *sf.after)
		}
		res.Files = append(res.Files, sf.delta)
	}
	// A function absent from its own file's before side falls back to an
	// exact (name, complexity, size) match anywhere on the old side, so code
	// moved between files does not read as fresh mass.
	moved := map[funcKey]float64{}
	for i := range scored {
		if scored[i].before == nil {
			continue
		}
		for _, fn := range scored[i].before.Funcs {
			moved[funcKey{fn.Name, fn.CC, fn.SLOC}] = structure.Mass(fn)
		}
	}
	for i := range scored {
		grown, over := funcGrowth(scored[i].before, scored[i].after, moved)
		res.Files[i].MassGrown = grown
		res.Files[i].OverLimit = over
		res.Delta.MassGrown += grown
		res.Delta.OverLimit += over
		res.highFuncs.add(highFuncChanges(scored[i].before, scored[i].after, moved))
	}

	markFindingsOld(old, res)
	s.lastRefs(ctx, base, end, files, scored, res)

	aggBefore := structure.Aggregate(before)
	aggAfter := structure.Aggregate(after)
	res.Delta.MassNet = res.Delta.MassAdded - res.Delta.MassRemoved
	res.Delta.ErosionBefore = aggBefore.Erosion
	res.Delta.ErosionAfter = aggAfter.Erosion

	branch, _ := s.ops.GetCurrentBranch(ctx, s.root) //nolint:errcheck // detached HEAD leaves intent to subjects
	res.Intent = s.changeIntent(ctx, base, end, branch)
	s.dirDeltas(ctx, res)
	res.Reading = readingFor(res, aggBefore, aggAfter)

	return res, nil
}

// oldBlobs batch-reads every old-side blob in the diff.
func (s *session) oldBlobs(ctx context.Context, files []vcs.CommitFile) (map[string][]byte, error) {
	var shas []string
	for i := range files {
		if files[i].OldSHA != "" {
			shas = append(shas, files[i].OldSHA)
		}
	}
	blobs, err := s.ops.BlobContents(ctx, s.root, shas)
	if err != nil {
		return nil, fmt.Errorf("reading blobs: %w", err)
	}

	return blobs, nil
}

// scoreFile analyzes one changed file on both sides and folds it into res;
// old collects the old side's (rule, match) pairs for markFindingsOld. A hit
// is new only when its line was added by the change.
func (s *session) scoreFile(ctx context.Context, f *vcs.CommitFile, oldBlobs map[string][]byte,
	committed bool, baseline rules.Baseline, res *changeResult, old map[string]bool,
) (scoredFile, error) {
	sf := scoredFile{delta: fileDelta{
		Path: f.Path, Status: f.Status.String(),
		Added: f.LinesAdded(), Deleted: f.LinesDeleted(),
	}}
	res.Delta.LinesAdded += f.LinesAdded()
	res.Delta.LinesDeleted += f.LinesDeleted()

	oldAt := map[string]bool{}
	if rep := s.oldReport(ctx, *f, oldBlobs); rep != nil && rep.Skipped == "" {
		sf.before = &rep.Metrics
		sf.beforePrint = rep.Print
		res.beforeByPath[oldPathOf(*f)] = rep.Metrics
		sf.delta.MassBefore = fileMass(rep.Metrics)
		res.Delta.MassRemoved += sf.delta.MassBefore
		for _, fn := range rep.Findings {
			old[fn.Rule+"\x00"+fn.Match] = true
			oldAt[ruleLine(fn.Rule, fn.Line)] = true
		}
	}

	newSide := s.newContent(ctx, *f, committed)
	if newSide.err != nil {
		return sf, newSide.err
	}
	if newSide.content == nil {
		return sf, nil
	}
	sha := f.NewSHA
	if sha == "" || strings.Trim(sha, "0") == "" {
		sha = vcs.BlobSHA(newSide.content)
	}
	rep := s.eng.Analyze(ctx, f.Path, sha, newSide.content)
	if rep.Skipped != "" {
		res.Skipped = append(res.Skipped, skippedRow{f.Path, rep.Skipped})

		return sf, nil
	}
	if rep.Metrics.ParseError {
		res.Errors = append(res.Errors, parseErrorRow(f.Path, rep.Metrics))
	}
	sf.after = &rep.Metrics
	sf.afterPrint = rep.Print
	res.afterByPath[f.Path] = rep.Metrics
	sf.delta.MassAfter = fileMass(rep.Metrics)
	sf.delta.Lang = rep.Lang
	res.Delta.MassAdded += sf.delta.MassAfter
	added := addedLines(f.Hunks)
	for _, fn := range baseline.Suppress(f.Path, rep.Findings) {
		row := findingRow{Path: f.Path, Rule: fn.Rule, Line: fn.Line, Match: fn.Match}
		if oldLine, ok := added[fn.Line]; ok {
			row.New = fn.Match != "" || !oldAt[ruleLine(fn.Rule, oldLine)]
		}
		res.Findings = append(res.Findings, row)
	}

	return sf, nil
}

// addedLines maps each line a file's hunks add to the old line it replaced, or
// to 0 for a pure addition.
func addedLines(hunks []vcs.DiffHunk) map[int]int {
	out := map[int]int{}
	for _, h := range hunks {
		for i := range h.NewLines {
			replaced := 0
			if i < h.OldLines {
				replaced = h.OldStart + i
			}
			out[h.NewStart+i] = replaced
		}
	}

	return out
}

// ruleLine keys a finding by its rule and line on one side of a file.
func ruleLine(rule string, line int) string {
	return rule + "\x00" + strconv.Itoa(line)
}

// funcKey identifies a moved function exactly: a move keeps its name and
// shape, so (name, complexity, size) matches it on the other side.
type funcKey struct {
	name string
	cc   int
	sloc int
}

// funcGrowth matches functions between the two sides of one file by funcKeys
// and returns the mass the change grew (per-function positive deltas summed)
// and the part of that growth sitting in functions over the CC limit. A
// function missing from its own file's before side falls back to an exact
// moved-code match; a nil before side counts every function not matched
// elsewhere.
func funcGrowth(before, after *structure.FileMetrics, moved map[funcKey]float64) (float64, float64) {
	if after == nil {
		return 0, 0
	}
	var grown, overLimit float64
	prev := map[string]float64{}
	if before != nil {
		for i, key := range funcKeys(before.Funcs) {
			prev[key] = structure.Mass(before.Funcs[i])
		}
	}
	for i, key := range funcKeys(after.Funcs) {
		fn := after.Funcs[i]
		matched, ok := prev[key]
		if !ok {
			matched = moved[funcKey{fn.Name, fn.CC, fn.SLOC}]
		}
		g := structure.Mass(fn) - matched
		if g <= 0 {
			continue
		}
		grown += g
		if fn.CC > structure.CCLimit {
			overLimit += g
		}
	}

	return grown, overLimit
}

// funcKeys names each function of one file side for matching. A function
// nested in another is keyed under its enclosing function, and an anonymous one
// by its order among the anonymous functions of that enclosing function.
func funcKeys(fns []structure.FuncMetrics) []string {
	keys := make([]string, len(fns))
	var open []int
	anonymous := map[string]int{}
	for i, fn := range fns {
		for len(open) > 0 && fn.EndLine > fns[open[len(open)-1]].EndLine {
			open = open[:len(open)-1]
		}
		parent := ""
		if len(open) > 0 {
			parent = keys[open[len(open)-1]]
		}
		name := fn.Name
		if name == lang.AnonymousName {
			anonymous[parent]++
			name = fmt.Sprintf("%s#%d", lang.AnonymousName, anonymous[parent])
		}
		keys[i] = name
		if parent != "" {
			keys[i] = parent + "/" + name
		}
		open = append(open, i)
	}

	return keys
}

// oldReport analyzes a file's before-side blob, when it has one.
func (s *session) oldReport(ctx context.Context, f vcs.CommitFile, oldBlobs map[string][]byte) *engine.FileReport {
	if f.OldSHA == "" || f.Binary {
		return nil
	}
	content, ok := oldBlobs[f.OldSHA]
	if !ok {
		return nil
	}

	return s.eng.Analyze(ctx, oldPathOf(f), f.OldSHA, content)
}

// oldPathOf names the path a file's before side lived at, which differs from
// f.Path after a rename.
func oldPathOf(f vcs.CommitFile) string {
	if f.OldPath != "" {
		return f.OldPath
	}

	return f.Path
}

// fileMass is one file's total complexity mass.
func fileMass(m structure.FileMetrics) float64 {
	total := 0.0
	for _, fn := range m.Funcs {
		total += structure.Mass(fn)
	}

	return total
}

// newContent reads a changed file's after-side bytes. A committed diff's new
// sha names a real object; a worktree diff's does not (it is only the hash of
// dirty content), so worktree files always come off disk.
func (s *session) newContent(ctx context.Context, f vcs.CommitFile, committed bool) side {
	if f.Status == vcs.StatusDeleted {
		return side{}
	}
	if committed && f.NewSHA != "" && strings.Trim(f.NewSHA, "0") != "" {
		content, err := s.ops.BlobContent(ctx, s.root, f.NewSHA)

		return side{content: content, err: err}
	}
	content, err := os.ReadFile(filepath.Join(s.root, f.Path))

	return side{content: content, err: err}
}

// changeIntent folds the commits on the range into one intent.
func (s *session) changeIntent(ctx context.Context, base, end, branch string) intent.Intent {
	var intents []intent.Intent
	for rec, err := range s.ops.LogHistory(ctx, s.root, vcs.LogOptions{Range: base + ".." + end, FirstParent: true}) {
		if err != nil {
			break
		}
		intents = append(intents, intent.Of(rec.Subject, branch, rec.Body))
	}

	return intent.ForChange(intents)
}

// readingFor composes the one-line reading.
func readingFor(res *changeResult, before, after structure.Aggregates) string {
	f := reading.Fields{
		Intent:          res.Intent,
		MassAdded:       res.Delta.MassAdded,
		MassRemoved:     res.Delta.MassRemoved,
		MassGrown:       res.Delta.MassGrown,
		ErosionBefore:   before.Erosion,
		ErosionAfter:    after.Erosion,
		FuncHighAdded:   res.highFuncs.added,
		FuncHighTouched: res.highFuncs.touched,
		FuncHighGone:    res.highFuncs.gone,
	}
	for _, fn := range res.Findings {
		if fn.New {
			f.FindingsNew++
		}
	}
	for _, d := range res.Dirs {
		if d.Hotspot {
			f.Hotspot = true
		}
	}
	for d := range res.Dirs {
		f.TouchedDirs = append(f.TouchedDirs, d)
	}
	sort.Strings(f.TouchedDirs)

	return f.Sentence()
}

// funcCounts tallies a change's functions over the CC limit by how the change
// reached them.
type funcCounts struct {
	added   int
	touched int
	gone    int
}

func (c *funcCounts) add(o funcCounts) {
	c.added += o.added
	c.touched += o.touched
	c.gone += o.gone
}

// highFuncChanges counts one file's functions over the CC limit: added when
// they match nothing on the before side, touched when they match and their
// shape changed, gone when a before-side function over the limit has no match
// after. A function matched by moved code is neither added nor touched.
func highFuncChanges(before, after *structure.FileMetrics, moved map[funcKey]float64) funcCounts {
	var c funcCounts
	prev := map[string]structure.FuncMetrics{}
	var beforeKeys []string
	if before != nil {
		beforeKeys = funcKeys(before.Funcs)
		for i, key := range beforeKeys {
			prev[key] = before.Funcs[i]
		}
	}
	matched := map[string]bool{}
	if after != nil {
		for i, key := range funcKeys(after.Funcs) {
			fn := after.Funcs[i]
			matched[key] = true
			old, ok := prev[key]
			_, isMoved := moved[funcKey{fn.Name, fn.CC, fn.SLOC}]
			switch {
			case fn.CC <= structure.CCLimit:
			case !ok && !isMoved:
				c.added++
			case ok && !sameShape(old, fn):
				c.touched++
			}
		}
	}
	for i, key := range beforeKeys {
		if before.Funcs[i].CC > structure.CCLimit && !matched[key] {
			c.gone++
		}
	}

	return c
}

// sameShape reports whether two versions of a function measure the same.
func sameShape(a, b structure.FuncMetrics) bool {
	return a.CC == b.CC && a.SLOC == b.SLOC && a.Cognitive == b.Cognitive &&
		a.Nesting == b.Nesting && a.Params == b.Params && a.Comments == b.Comments
}

// markFindingsOld clears New on hits whose (rule,match) pair exists on the
// old side; the old reports were already scored in scoreChange.
func markFindingsOld(old map[string]bool, res *changeResult) {
	for i := range res.Findings {
		key := res.Findings[i].Rule + "\x00" + res.Findings[i].Match
		if old[key] && res.Findings[i].Match != "" {
			res.Findings[i].New = false
		}
	}
}

// dirDeltas rolls the change up per directory and flags hotspots.
func (s *session) dirDeltas(ctx context.Context, res *changeResult) {
	// Roll per-dir mass deltas from the file deltas.
	massNet := map[string]float64{}
	for _, fd := range res.Files {
		d := lineage.DirOf(fd.Path)
		massNet[d] += fd.MassAfter - fd.MassBefore
	}
	hotspots, touches := s.hotspotDirs(ctx, mapKeys(massNet))
	res.dirTouches = touches
	for d, net := range massNet {
		res.Dirs[d] = dirDelta{MassNet: net, Hotspot: hotspots[d]}
	}
}

// hotspotDirs names the most-touched directories over the last 90 days among
// the dirs the change touched, and returns the touch counts behind them.
func (s *session) hotspotDirs(ctx context.Context, touched []string) (map[string]bool, map[string]int) {
	touches := map[string]int{}
	for rec, err := range s.ops.LogHistory(ctx, s.root,
		vcs.LogOptions{FirstParent: true, Since: "90 days ago"}) {
		if err != nil {
			break
		}
		dirs := map[string]bool{}
		for i := range rec.Files {
			dirs[lineage.DirOf(rec.Files[i].Path)] = true
		}
		for d := range dirs {
			touches[d]++
		}
	}
	hot := map[string]bool{}
	for _, d := range touched {
		if touches[d] >= 10 { //nolint:mnd // a working definition of "frequently touched"
			hot[d] = true
		}
	}

	return hot, touches
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	return keys
}

// warnings lists skip and parse-error reasons worth surfacing.
func warnings(res *changeResult) []string {
	out := make([]string, 0, len(res.Skipped)+len(res.Errors))
	for _, sk := range res.Skipped {
		out = append(out, "skipped "+sk.Path+": "+sk.Reason)
	}
	for _, er := range res.Errors {
		out = append(out, "error "+er.Path+": "+er.Reason)
	}

	return out
}

// buildContext assembles what a model would open next.
func (s *session) buildContext(ctx context.Context, base, end string, files []vcs.CommitFile,
	res *changeResult,
) *contextPayload {
	cp := &contextPayload{
		Hunks:    map[string][]vcs.DiffHunk{},
		Snippets: map[string]map[int]string{},
	}
	for i := range files {
		if len(files[i].Hunks) > 0 {
			cp.Hunks[files[i].Path] = files[i].Hunks
		}
	}
	for rec, err := range s.ops.LogHistory(ctx, s.root, vcs.LogOptions{Range: base + ".." + end, FirstParent: true}) {
		if err != nil {
			break
		}
		cp.Messages = append(cp.Messages, rec.Subject)
	}
	for _, fr := range res.Findings {
		if line := snippetLine(s.root, fr.Path, fr.Line); line != "" {
			if cp.Snippets[fr.Path] == nil {
				cp.Snippets[fr.Path] = map[int]string{}
			}
			cp.Snippets[fr.Path][fr.Line] = line
		}
	}
	for path, m := range res.beforeByPath {
		cp.Crossed = append(cp.Crossed, crossedFuncs(path, m, true)...)
	}
	for path, m := range res.afterByPath {
		cp.Crossed = append(cp.Crossed, crossedFuncs(path, m, false)...)
	}
	sort.Slice(cp.Crossed, func(i, j int) bool {
		if cp.Crossed[i].Path != cp.Crossed[j].Path {
			return cp.Crossed[i].Path < cp.Crossed[j].Path
		}

		return cp.Crossed[i].Name < cp.Crossed[j].Name
	})
	cp.DirTouches = res.dirTouches

	return cp
}

// crossedFuncs lists one file's functions over the CC limit; before marks
// which side of the change they were measured on.
func crossedFuncs(path string, m structure.FileMetrics, before bool) []crossedFunc {
	var out []crossedFunc
	for _, fn := range m.Funcs {
		if fn.CC <= structure.CCLimit {
			continue
		}
		cf := crossedFunc{Path: path, Name: fn.Name, CC: fn.CC, Lines: fn.SLOC}
		if before {
			cf.Before = true
		} else {
			cf.After = true
		}
		out = append(out, cf)
	}

	return out
}

// snippetLine reads one source line for a finding's context.
func snippetLine(root, path string, line int) string {
	content, err := os.ReadFile(filepath.Join(root, path)) //nolint:gosec // root is the caller's repo path
	if err != nil {
		return ""
	}
	lines := strings.Split(string(content), "\n")
	if line <= 0 || line > len(lines) {
		return ""
	}

	return lines[line-1]
}

// printChange renders the human form: reading, delta table, findings.
func printChange(w io.Writer, res *changeResult, all bool) {
	report.Fprintln(w, res.Reading)
	report.Fprintln(w)

	rows := [][]string{
		{"mass added", fmt.Sprintf("%.1f", res.Delta.MassAdded)},
		{"mass removed", fmt.Sprintf("%.1f", res.Delta.MassRemoved)},
		{"mass net", fmt.Sprintf("%+.1f", res.Delta.MassNet)},
		{"mass grown", fmt.Sprintf("%.1f", res.Delta.MassGrown)},
		{"grown over CC " + strconv.Itoa(structure.CCLimit), fmt.Sprintf("%.1f", res.Delta.OverLimit)},
		{"erosion", fmt.Sprintf("%.2f → %.2f", res.Delta.ErosionBefore, res.Delta.ErosionAfter)},
		{"lines", fmt.Sprintf("+%d −%d", res.Delta.LinesAdded, res.Delta.LinesDeleted)},
	}
	report.Table(w, []string{"delta", "value"}, rows)

	if len(res.Dirs) > 0 {
		report.Fprintln(w)
		rows := make([][]string, 0, len(res.Dirs))
		for _, d := range report.SortedKeys(res.Dirs) {
			dd := res.Dirs[d]
			hot := ""
			if dd.Hotspot {
				hot = "hotspot"
			}
			rows = append(rows, []string{d, fmt.Sprintf("%+.1f", dd.MassNet), hot})
		}
		report.Table(w, []string{colDir, "mass net", ""}, rows)
	}

	newOnly := res.Findings
	if !all {
		var kept []findingRow
		for _, f := range res.Findings {
			if f.New {
				kept = append(kept, f)
			}
		}
		newOnly = kept
	}
	if len(newOnly) > 0 {
		report.Fprintln(w)
		rows := make([][]string, 0, len(newOnly))
		for _, f := range newOnly {
			mark := ""
			if !f.New {
				mark = "(existing)"
			}
			rows = append(rows, []string{f.Rule, fmt.Sprintf("%s:%d", f.Path, f.Line), mark})
		}
		report.Table(w, []string{"rule", "at", ""}, rows)
	}
	for _, sk := range res.Skipped {
		report.Fprintf(w, "skipped %s: %s\n", sk.Path, sk.Reason)
	}
	for _, er := range res.Errors {
		report.Fprintf(w, "error %s: %s\n", er.Path, er.Reason)
	}
}

// lastRefs flags a symbol whose last live reference this change removed: its
// declaration still stands at the end of the change, but nothing uses it now.
// The pass is observational — a failed tree read skips it, never fails the run.
func (s *session) lastRefs(ctx context.Context, base, end string,
	files []vcs.CommitFile, scored []scoredFile, res *changeResult,
) {
	basePrints, err := s.treePrints(ctx, base)
	if err != nil {
		return
	}
	endPrints, err := s.treePrints(ctx, end)
	if err != nil {
		return
	}
	for i := range scored {
		if scored[i].afterPrint != nil {
			endPrints[scored[i].delta.Path] = scored[i].afterPrint
		}
	}
	usesBase := useCounts(basePrints)
	usesEnd := useCounts(endPrints)
	declared := declNames(endPrints)
	for i := range scored {
		bp := scored[i].beforePrint
		if bp == nil {
			continue
		}
		ap := scored[i].afterPrint
		for name := range bp.Referenced {
			if ap != nil && ap.Referenced[name] {
				continue
			}
			if usesBase[name] > 0 && usesEnd[name] == 0 && declared[name] {
				res.Findings = append(res.Findings, findingRow{
					Path: scored[i].delta.Path, Rule: "x/last-ref",
					Line: removedLine(&files[i], name), Match: name, New: true,
				})
			}
		}
	}
}

// treePrints fingerprints every code blob in a revision's tree.
func (s *session) treePrints(ctx context.Context, rev string) (map[string]*rules.Fingerprint, error) {
	entries, err := s.ops.TreeEntries(ctx, s.root, rev)
	if err != nil {
		return nil, fmt.Errorf("listing tree at %s: %w", rev, err)
	}
	var want []string
	for _, e := range entries {
		if lang.ByPath(e.Path) != nil {
			want = append(want, e.SHA)
		}
	}
	blobs, err := s.ops.BlobContents(ctx, s.root, want)
	if err != nil {
		return nil, fmt.Errorf("reading blobs: %w", err)
	}
	prints := map[string]*rules.Fingerprint{}
	for _, e := range entries {
		if lang.ByPath(e.Path) == nil {
			continue
		}
		if rep := s.eng.Analyze(ctx, e.Path, e.SHA, blobs[e.SHA]); rep.Print != nil {
			prints[e.Path] = rep.Print
		}
	}

	return prints, nil
}

// useCounts tallies how many files use each name, discounting a file's own
// declaration of it: a name with zero uses is dead even where it is declared.
func useCounts(prints map[string]*rules.Fingerprint) map[string]int {
	uses := map[string]int{}
	for _, fp := range prints {
		for n := range fp.Referenced {
			uses[n]++
		}
		for n := range fp.Declared {
			if fp.Referenced[n] {
				uses[n]--
			}
		}
		for n := range fp.Exported {
			if fp.Referenced[n] {
				uses[n]--
			}
		}
	}

	return uses
}

// declNames unions every declared or exported name across the prints.
func declNames(prints map[string]*rules.Fingerprint) map[string]bool {
	declared := map[string]bool{}
	for _, fp := range prints {
		for n := range fp.Declared {
			declared[n] = true
		}
		for n := range fp.Exported {
			declared[n] = true
		}
	}

	return declared
}

// removedLine locates the hunk line where a name's reference was deleted.
func removedLine(f *vcs.CommitFile, name string) int {
	pos := 0
	for _, h := range f.Hunks {
		for j := range h.OldLines {
			if pos+j < len(f.Removed) && strings.Contains(f.Removed[pos+j], name) {
				return h.OldStart + j
			}
		}
		pos += h.OldLines
	}

	return 0
}

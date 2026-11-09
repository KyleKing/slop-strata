package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kyleking/aragonite/vcs"

	"github.com/kyleking/slop-strata/internal/engine"
	"github.com/kyleking/slop-strata/internal/intent"
	"github.com/kyleking/slop-strata/internal/lineage"
	"github.com/kyleking/slop-strata/internal/reading"
	"github.com/kyleking/slop-strata/internal/report"
	"github.com/kyleking/slop-strata/internal/rules"
	"github.com/kyleking/slop-strata/internal/structure"
)

// defaultRev is the revision change and snapshot default to.
const defaultRev = "HEAD"

// side is one version of a changed file.
type side struct {
	report  *engine.FileReport
	content []byte
	err     error
}

// changeResult is the change subcommand's JSON shape.
type changeResult struct {
	Base     string              `json:"base"`
	End      string              `json:"end"`
	Reading  string              `json:"reading"`
	Intent   intent.Intent       `json:"intent"`
	Delta    deltaSummary        `json:"delta"`
	Dirs     map[string]dirDelta `json:"dirs"`
	Findings []findingRow        `json:"findings"`
	Files    []fileDelta         `json:"files"`
	Skipped  []skippedRow        `json:"skipped,omitempty"`
	Errors   []skippedRow        `json:"errors,omitempty"`
	Context  *contextPayload     `json:"context,omitempty"`

	beforeByPath map[string]structure.FileMetrics
	afterByPath  map[string]structure.FileMetrics
	dirTouches   map[string]int
}

type deltaSummary struct {
	MassAdded     float64 `json:"mass_added"`
	MassRemoved   float64 `json:"mass_removed"`
	MassNet       float64 `json:"mass_net"`
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
	Line  int    `json:"line"`
	Match string `json:"match,omitempty"`
	New   bool   `json:"new"`
}

type fileDelta struct {
	Path       string  `json:"path"`
	Status     string  `json:"status"`
	Lang       string  `json:"lang,omitempty"`
	MassBefore float64 `json:"mass_before"`
	MassAfter  float64 `json:"mass_after"`
	Added      int     `json:"added"`
	Deleted    int     `json:"deleted"`
}

type skippedRow struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type contextPayload struct {
	Hunks      map[string][]vcs.DiffHunk `json:"hunks"`
	Messages   []string                  `json:"messages"`
	Snippets   map[string]map[int]string `json:"snippets"`        // path -> line -> text
	Crossed    []crossedFunc             `json:"crossed"`         // functions over CC 10 on either side
	DirTouches map[string]int            `json:"dir_touches_90d"` // commits touching each dir in 90 days
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
	base := a.base
	if base == "" {
		branch, err := s.defaultBranch(ctx)
		if err != nil {
			return err
		}
		base, err = s.ops.MergeBase(ctx, s.root, end, branch)
		if err != nil {
			return fmt.Errorf("merge-base %s..%s: %w", end, branch, err)
		}
	}

	var files []vcs.CommitFile
	if a.rev != "" {
		d, err := s.ops.DiffRefs(ctx, s.root, base, a.rev)
		if err != nil {
			return err
		}
		files = d
	} else {
		d, err := s.ops.DiffWorktree(ctx, s.root, base)
		if err != nil {
			return err
		}
		untracked, err := s.ops.UntrackedFiles(ctx, s.root)
		if err != nil {
			return err
		}
		files = append(d, untrackedAsAdds(untracked)...)
	}

	res, err := s.scoreChange(ctx, base, end, files, a.rev != "")
	if err != nil {
		return err
	}

	if a.json {
		if a.ctx {
			res.Context = s.buildContext(ctx, base, end, files, res)
		}
		return report.WriteJSON(w, "change", s.root, res, s.warnings(res))
	}
	if a.ctx && !a.json {
		report.Fprintln(w, "--context only applies to --json output")
	}

	s.printChange(w, res, a.all)

	return nil
}

// defaultBranch resolves the branch the change diffs against.
func (s *session) defaultBranch(ctx context.Context) (string, error) {
	if s.cfg.DefaultBranch != "" {
		return s.cfg.DefaultBranch, nil
	}

	return s.ops.DefaultBranchName(ctx, s.root)
}

// untrackedAsAdds turns untracked paths into added-file records; their blobs
// are scored straight off disk.
func untrackedAsAdds(paths []string) []vcs.CommitFile {
	out := make([]vcs.CommitFile, 0, len(paths))
	for _, p := range paths {
		out = append(out, vcs.CommitFile{Path: p, Status: vcs.StatusAdded})
	}

	return out
}

// scoreChange analyzes both sides of every changed file.
func (s *session) scoreChange(ctx context.Context, base, end string, files []vcs.CommitFile, committed bool) (*changeResult, error) {
	// Batch-read old blobs.
	var oldSHAs []string
	for _, f := range files {
		if f.OldSHA != "" {
			oldSHAs = append(oldSHAs, f.OldSHA)
		}
	}
	oldBlobs, err := s.ops.BlobContents(ctx, s.root, oldSHAs)
	if err != nil {
		return nil, err
	}

	res := &changeResult{
		Base: base, End: end, Dirs: map[string]dirDelta{},
		beforeByPath: map[string]structure.FileMetrics{}, afterByPath: map[string]structure.FileMetrics{},
	}
	var before, after []structure.FileMetrics
	old := map[string]bool{}
	baseline, err := rules.ReadBaseline(filepath.Join(s.root, s.cfg.Baseline))
	if err != nil {
		return nil, err
	}

	for _, f := range files {
		fd := fileDelta{Path: f.Path, Status: f.Status.String(), Added: f.LinesAdded(), Deleted: f.LinesDeleted()}
		res.Delta.LinesAdded += f.LinesAdded()
		res.Delta.LinesDeleted += f.LinesDeleted()

		if f.OldSHA != "" && !f.Binary {
			if content, ok := oldBlobs[f.OldSHA]; ok {
				oldPath := f.Path
				if f.OldPath != "" {
					oldPath = f.OldPath
				}
				rep := s.eng.Analyze(oldPath, f.OldSHA, content)
				if rep.Skipped == "" {
					before = append(before, rep.Metrics)
					res.beforeByPath[oldPath] = rep.Metrics
					fd.MassBefore = fileMass(rep.Metrics)
					res.Delta.MassRemoved += fd.MassBefore
					for _, fn := range rep.Findings {
						old[fn.Rule+"\x00"+fn.Match] = true
					}
				}
			}
		}

		newSide := s.newContent(ctx, f, committed)
		if newSide.err != nil {
			return nil, newSide.err
		}
		if newSide.content != nil {
			sha := f.NewSHA
			if sha == "" || strings.Trim(sha, "0") == "" {
				sha = vcs.BlobSHA(newSide.content)
			}
			rep := s.eng.Analyze(f.Path, sha, newSide.content)
			if rep.Skipped != "" {
				res.Skipped = append(res.Skipped, skippedRow{f.Path, rep.Skipped})
			} else {
				if rep.Metrics.ParseError {
					res.Errors = append(res.Errors, skippedRow{
						f.Path,
						fmt.Sprintf("tree-sitter parse error (%.1f%% of bytes)", rep.Metrics.ErrorCoverage*100),
					})
				}
				after = append(after, rep.Metrics)
				res.afterByPath[f.Path] = rep.Metrics
				fd.MassAfter = fileMass(rep.Metrics)
				fd.Lang = rep.Lang
				res.Delta.MassAdded += fd.MassAfter
				for _, fn := range baseline.Suppress(f.Path, rep.Findings) {
					res.Findings = append(res.Findings, findingRow{Path: f.Path, Rule: fn.Rule, Line: fn.Line, Match: fn.Match, New: true})
				}
			}
		}
		res.Files = append(res.Files, fd)
	}

	aggBefore := structure.Aggregate(before)
	aggAfter := structure.Aggregate(after)
	res.Delta.MassNet = res.Delta.MassAdded - res.Delta.MassRemoved
	res.Delta.ErosionBefore = aggBefore.Erosion
	res.Delta.ErosionAfter = aggAfter.Erosion

	branch, _ := s.ops.GetCurrentBranch(ctx, s.root) //nolint:errcheck // a detached HEAD just leaves intent to the subjects
	res.Intent = s.changeIntent(ctx, base, end, branch)
	markFindingsOld(old, res)
	s.dirDeltas(ctx, res, files)
	res.Reading = s.readingFor(res, aggBefore, aggAfter, before, after)

	return res, nil
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
func (s *session) readingFor(res *changeResult, before, after structure.Aggregates,
	beforeFiles, afterFiles []structure.FileMetrics,
) string {
	f := reading.Fields{
		Intent:        res.Intent,
		MassAdded:     res.Delta.MassAdded,
		MassRemoved:   res.Delta.MassRemoved,
		ErosionBefore: before.Erosion,
		ErosionAfter:  after.Erosion,
	}
	for _, fn := range res.Findings {
		if fn.New {
			f.FindingsNew++
		}
	}
	for _, m := range beforeFiles {
		for _, fn := range m.Funcs {
			if fn.CC > structure.CCLimit {
				f.FuncHighGone++
			}
		}
	}
	for _, m := range afterFiles {
		for _, fn := range m.Funcs {
			if fn.CC > structure.CCLimit {
				f.FuncHighAdded++
			}
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
func (s *session) dirDeltas(ctx context.Context, res *changeResult, files []vcs.CommitFile) {
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
		for _, f := range rec.Files {
			touches[lineage.DirOf(f.Path)]++
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
func (s *session) warnings(res *changeResult) []string {
	var out []string
	for _, sk := range res.Skipped {
		out = append(out, "skipped "+sk.Path+": "+sk.Reason)
	}
	for _, er := range res.Errors {
		out = append(out, "error "+er.Path+": "+er.Reason)
	}

	return out
}

// buildContext assembles what a model would open next.
func (s *session) buildContext(ctx context.Context, base, end string, files []vcs.CommitFile, res *changeResult) *contextPayload {
	cp := &contextPayload{
		Hunks:    map[string][]vcs.DiffHunk{},
		Snippets: map[string]map[int]string{},
	}
	for _, f := range files {
		if len(f.Hunks) > 0 {
			cp.Hunks[f.Path] = f.Hunks
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
		for _, fn := range m.Funcs {
			if fn.CC > structure.CCLimit {
				cp.Crossed = append(cp.Crossed, crossedFunc{Path: path, Name: fn.Name, CC: fn.CC, Lines: fn.SLOC, Before: true})
			}
		}
	}
	for path, m := range res.afterByPath {
		for _, fn := range m.Funcs {
			if fn.CC > structure.CCLimit {
				cp.Crossed = append(cp.Crossed, crossedFunc{Path: path, Name: fn.Name, CC: fn.CC, Lines: fn.SLOC, After: true})
			}
		}
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

// snippetLine reads one source line for a finding's context.
func snippetLine(root, path string, line int) string {
	content, err := os.ReadFile(filepath.Join(root, path))
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
func (s *session) printChange(w io.Writer, res *changeResult, all bool) {
	report.Fprintln(w, res.Reading)
	report.Fprintln(w)

	rows := [][]string{
		{"mass added", fmt.Sprintf("%.1f", res.Delta.MassAdded)},
		{"mass removed", fmt.Sprintf("%.1f", res.Delta.MassRemoved)},
		{"mass net", fmt.Sprintf("%+.1f", res.Delta.MassNet)},
		{"erosion", fmt.Sprintf("%.2f → %.2f", res.Delta.ErosionBefore, res.Delta.ErosionAfter)},
		{"lines", fmt.Sprintf("+%d −%d", res.Delta.LinesAdded, res.Delta.LinesDeleted)},
	}
	report.Table(w, []string{"delta", "value"}, rows)

	if len(res.Dirs) > 0 {
		report.Fprintln(w)
		var rows [][]string
		for _, d := range report.SortedKeys(res.Dirs) {
			dd := res.Dirs[d]
			hot := ""
			if dd.Hotspot {
				hot = "hotspot"
			}
			rows = append(rows, []string{d, fmt.Sprintf("%+.1f", dd.MassNet), hot})
		}
		report.Table(w, []string{"dir", "mass net", ""}, rows)
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
		var rows [][]string
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

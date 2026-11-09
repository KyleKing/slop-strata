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
	err     error
	content []byte
}

// changeResult is the change subcommand's JSON shape.
type changeResult struct {
	Dirs     map[string]dirDelta `json:"dirs"`
	Context  *contextPayload     `json:"context,omitempty"`
	Intent   intent.Intent       `json:"intent"`
	Base     string              `json:"base"`
	End      string              `json:"end"`
	Reading  string              `json:"reading"`
	Findings []findingRow        `json:"findings"`
	Files    []fileDelta         `json:"files"`
	Skipped  []skippedRow        `json:"skipped,omitempty"`
	Delta    deltaSummary        `json:"delta"`
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
	Added      int     `json:"added"`
	Deleted    int     `json:"deleted"`
}

type skippedRow struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type contextPayload struct {
	Hunks    map[string][]vcs.DiffHunk `json:"hunks"`
	Snippets map[string]map[int]string `json:"snippets"` // path -> line -> text
	Messages []string                  `json:"messages"`
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
			return fmt.Errorf("diff %s..%s: %w", base, a.rev, err)
		}
		files = d
	} else {
		d, err := s.ops.DiffWorktree(ctx, s.root, base)
		if err != nil {
			return fmt.Errorf("diff %s..worktree: %w", base, err)
		}
		untracked, err := s.ops.UntrackedFiles(ctx, s.root)
		if err != nil {
			return fmt.Errorf("listing untracked files: %w", err)
		}
		files = append(files, d...)
		files = append(files, untrackedAsAdds(untracked)...)
	}

	res, err := s.scoreChange(ctx, base, end, files, a.rev != "")
	if err != nil {
		return err
	}

	if a.json {
		if a.ctx {
			res.Context = s.buildContext(ctx, base, files, res)
		}
		if err := report.WriteJSON(w, "change", s.root, res, warnings(res)); err != nil {
			return fmt.Errorf("writing result: %w", err)
		}

		return nil
	}

	printChange(w, res, a.all)

	return nil
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

// untrackedAsAdds turns untracked paths into added-file records; their blobs
// are scored straight off disk.
func untrackedAsAdds(paths []string) []vcs.CommitFile {
	out := make([]vcs.CommitFile, 0, len(paths))
	for _, p := range paths {
		out = append(out, vcs.CommitFile{Path: p, Status: vcs.StatusAdded})
	}

	return out
}

// scoredFile is one changed file's delta plus the metrics each side yielded.
type scoredFile struct {
	before *structure.FileMetrics
	after  *structure.FileMetrics
	delta  fileDelta
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

	res := &changeResult{Base: base, End: end, Dirs: map[string]dirDelta{}}
	var before, after []structure.FileMetrics
	for _, f := range files {
		sf, err := s.scoreFile(ctx, f, oldBlobs, committed, baseline, res)
		if err != nil {
			return nil, err
		}
		if sf.before != nil {
			before = append(before, *sf.before)
		}
		if sf.after != nil {
			after = append(after, *sf.after)
		}
		res.Files = append(res.Files, sf.delta)
	}

	aggBefore := structure.Aggregate(before)
	aggAfter := structure.Aggregate(after)
	res.Delta.MassNet = res.Delta.MassAdded - res.Delta.MassRemoved
	res.Delta.ErosionBefore = aggBefore.Erosion
	res.Delta.ErosionAfter = aggAfter.Erosion

	res.Intent = s.changeIntent(ctx, base)
	res.Reading = readingFor(res, aggBefore, aggAfter, before, after)
	s.markFindingsOld(ctx, files, res)
	s.dirDeltas(ctx, res)

	return res, nil
}

// oldBlobs batch-reads every old-side blob in the diff.
func (s *session) oldBlobs(ctx context.Context, files []vcs.CommitFile) (map[string][]byte, error) {
	var shas []string
	for _, f := range files {
		if f.OldSHA != "" {
			shas = append(shas, f.OldSHA)
		}
	}
	blobs, err := s.ops.BlobContents(ctx, s.root, shas)
	if err != nil {
		return nil, fmt.Errorf("reading blobs: %w", err)
	}

	return blobs, nil
}

// scoreFile analyzes one changed file on both sides and folds it into res.
func (s *session) scoreFile(ctx context.Context, f vcs.CommitFile, oldBlobs map[string][]byte,
	committed bool, baseline rules.Baseline, res *changeResult,
) (scoredFile, error) {
	sf := scoredFile{delta: fileDelta{
		Path: f.Path, Status: f.Status.String(),
		Added: f.LinesAdded(), Deleted: f.LinesDeleted(),
	}}
	res.Delta.LinesAdded += f.LinesAdded()
	res.Delta.LinesDeleted += f.LinesDeleted()

	if rep := s.oldReport(ctx, f, oldBlobs); rep != nil && rep.Skipped == "" {
		sf.before = &rep.Metrics
		sf.delta.MassBefore = fileMass(rep.Metrics)
		res.Delta.MassRemoved += sf.delta.MassBefore
	}

	newSide := s.newContent(ctx, f, committed)
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
	sf.after = &rep.Metrics
	sf.delta.MassAfter = fileMass(rep.Metrics)
	sf.delta.Lang = rep.Lang
	res.Delta.MassAdded += sf.delta.MassAfter
	for _, fn := range baseline.Suppress(f.Path, rep.Findings) {
		res.Findings = append(res.Findings, findingRow{
			Path: f.Path, Rule: fn.Rule, Line: fn.Line, Match: fn.Match, New: true,
		})
	}

	return sf, nil
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
	oldPath := f.Path
	if f.OldPath != "" {
		oldPath = f.OldPath
	}

	return s.eng.Analyze(ctx, oldPath, f.OldSHA, content)
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
func (s *session) changeIntent(ctx context.Context, base string) intent.Intent {
	var intents []intent.Intent
	for rec, err := range s.ops.LogHistory(ctx, s.root, vcs.LogOptions{Range: base + "..HEAD", FirstParent: true}) {
		if err != nil {
			break
		}
		intents = append(intents, intent.Of(rec.Subject, "", rec.Body))
	}

	return intent.ForChange(intents)
}

// readingFor composes the one-line reading.
func readingFor(res *changeResult, before, after structure.Aggregates,
	beforeFiles, afterFiles []structure.FileMetrics,
) string {
	f := reading.Fields{
		Intent:        res.Intent,
		MassAdded:     res.Delta.MassAdded,
		MassRemoved:   res.Delta.MassRemoved,
		ErosionBefore: before.Erosion,
		ErosionAfter:  after.Erosion,
		FindingsNew:   len(res.Findings),
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

// markFindingsOld clears New on hits already present on the old side.
func (s *session) markFindingsOld(ctx context.Context, files []vcs.CommitFile, res *changeResult) {
	// Findings whose (rule,match) pair exists on the old blob are carried, not new.
	old := map[string]bool{}
	blobs, err := s.oldBlobs(ctx, files)
	if err != nil {
		return
	}
	for _, f := range files {
		content, ok := blobs[f.OldSHA]
		if !ok {
			continue
		}
		p := f.Path
		if f.OldPath != "" {
			p = f.OldPath
		}
		rep := s.eng.Analyze(ctx, p, f.OldSHA, content)
		for _, fn := range rep.Findings {
			old[fn.Rule+"\x00"+fn.Match] = true
		}
	}
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
	hotspots := s.hotspotDirs(ctx, mapKeys(massNet))
	for d, net := range massNet {
		res.Dirs[d] = dirDelta{MassNet: net, Hotspot: hotspots[d]}
	}
}

// hotspotDirs names the most-touched directories over the last 90 days among
// the dirs the change touched.
func (s *session) hotspotDirs(ctx context.Context, touched []string) map[string]bool {
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

	return hot
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	return keys
}

// warnings lists skip reasons worth surfacing.
func warnings(res *changeResult) []string {
	out := make([]string, 0, len(res.Skipped))
	for _, sk := range res.Skipped {
		out = append(out, "skipped "+sk.Path+": "+sk.Reason)
	}

	return out
}

// buildContext assembles what a model would open next.
func (s *session) buildContext(ctx context.Context, base string, files []vcs.CommitFile,
	res *changeResult,
) *contextPayload {
	cp := &contextPayload{
		Hunks:    map[string][]vcs.DiffHunk{},
		Snippets: map[string]map[int]string{},
	}
	for _, f := range files {
		if len(f.Hunks) > 0 {
			cp.Hunks[f.Path] = f.Hunks
		}
	}
	for rec, err := range s.ops.LogHistory(ctx, s.root, vcs.LogOptions{Range: base + "..HEAD", FirstParent: true}) {
		if err != nil {
			break
		}
		cp.Messages = append(cp.Messages, rec.Subject)
	}
	for _, fr := range res.Findings {
		content, err := os.ReadFile(filepath.Join(s.root, fr.Path))
		if err != nil {
			continue
		}
		lines := strings.Split(string(content), "\n")
		if fr.Line <= len(lines) {
			if cp.Snippets[fr.Path] == nil {
				cp.Snippets[fr.Path] = map[int]string{}
			}
			cp.Snippets[fr.Path][fr.Line] = lines[fr.Line-1]
		}
	}

	return cp
}

// printChange renders the human form: reading, delta table, findings.
func printChange(w io.Writer, res *changeResult, all bool) {
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
}

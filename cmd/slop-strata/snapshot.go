package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/kyleking/aragonite/vcs"

	"github.com/kyleking/slop-strata/internal/engine"
	"github.com/kyleking/slop-strata/internal/lang"
	"github.com/kyleking/slop-strata/internal/lineage"
	"github.com/kyleking/slop-strata/internal/report"
	"github.com/kyleking/slop-strata/internal/rules"
	"github.com/kyleking/slop-strata/internal/structure"
)

// snapshotResult is the snapshot subcommand's JSON shape.
type snapshotResult struct {
	Findings map[string][]rules.Finding `json:"findings"`
	Context  *contextPayload            `json:"context,omitempty"`
	Rev      string                     `json:"rev"`
	Dirs     []dirSnapshot              `json:"dirs"`
	Skipped  []skippedRow               `json:"skipped,omitempty"`
	Errors   []skippedRow               `json:"errors,omitempty"`
	Total    structure.Aggregates       `json:"total"`
}

// dirSnapshot is one directory's rollup.
type dirSnapshot struct {
	Dir      string  `json:"dir"`
	Files    int     `json:"files"`
	Erosion  float64 `json:"erosion"`
	Mass     float64 `json:"mass"`
	Funcs    int     `json:"funcs"`
	Over50   int     `json:"over_50"`
	Hotspot  float64 `json:"hotspot"`
	Findings int     `json:"findings"`
}

func (s *session) snapshotCmd(ctx context.Context, a *args, w io.Writer) error {
	rev := a.rev
	if rev == "" {
		rev = defaultRev
	}
	entries, err := s.ops.TreeEntries(ctx, s.root, rev)
	if err != nil {
		return fmt.Errorf("listing tree at %s: %w", rev, err)
	}
	reports, err := s.treeReports(ctx, entries)
	if err != nil {
		return err
	}
	baseline, err := rules.ReadBaseline(filepath.Join(s.root, s.cfg.Baseline))
	if err != nil {
		return fmt.Errorf("reading baseline: %w", err)
	}
	res := s.snapshotRollup(ctx, rev, reports, baseline)

	if a.writeBaseline {
		if err := s.writeBaseline(reports); err != nil {
			return err
		}
	}
	if a.json {
		if a.ctx {
			res.Context = s.snapshotContext(res)
		}
		if err := report.WriteJSON(w, "snapshot", s.root, res, nil); err != nil {
			return fmt.Errorf("writing result: %w", err)
		}

		return nil
	}

	printSnapshot(w, res, a.all)

	return nil
}

// treeReports analyzes every supported blob in the tree.
func (s *session) treeReports(ctx context.Context, entries []vcs.TreeEntry) ([]*engine.FileReport, error) {
	// Keep only analyzable blobs, then batch-read them.
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

	var reports []*engine.FileReport
	for _, e := range entries {
		content, ok := blobs[e.SHA]
		if !ok || lang.ByPath(e.Path) == nil {
			continue
		}
		reports = append(reports, s.eng.Analyze(ctx, e.Path, e.SHA, content))
	}
	engine.CrossFile(reports, s.cfg)

	return reports, nil
}

// snapshotRollup folds the file reports into the snapshot result: skipped
// blobs, live findings, and the per-directory aggregates ranked by hotspot.
func (s *session) snapshotRollup(
	ctx context.Context, rev string, reports []*engine.FileReport, baseline rules.Baseline,
) *snapshotResult {
	res := &snapshotResult{Rev: rev, Findings: map[string][]rules.Finding{}}
	byDir := map[string][]structure.FileMetrics{}
	live := map[string][]rules.Finding{}
	var files []structure.FileMetrics
	for _, r := range reports {
		if r.Skipped != "" {
			res.Skipped = append(res.Skipped, skippedRow{r.Path, r.Skipped})

			continue
		}
		if r.Metrics.ParseError {
			res.Errors = append(res.Errors, parseErrorRow(r.Path, r.Metrics))
		}
		files = append(files, r.Metrics)
		byDir[lineage.DirOf(r.Path)] = append(byDir[lineage.DirOf(r.Path)], r.Metrics)
		fs := baseline.Suppress(r.Path, r.Findings)
		if len(fs) > 0 {
			res.Findings[r.Path] = fs
			live[r.Path] = fs
		}
	}

	s.astGrepFindings(ctx, rev, res, live, baseline)

	res.Total = structure.Aggregate(files)

	for _, d := range report.SortedKeys(byDir) {
		agg := structure.Aggregate(byDir[d])
		findings := 0
		for p, fs := range live {
			if lineage.DirOf(p) == d {
				findings += len(fs)
			}
		}
		res.Dirs = append(res.Dirs, dirSnapshot{
			Dir: d, Files: agg.Files, Erosion: agg.Erosion, Mass: agg.Mass,
			Funcs: agg.FuncCount, Over50: agg.Over50, Findings: findings,
			Hotspot: agg.Mass * float64(findings+1),
		})
	}
	sort.Slice(res.Dirs, func(i, j int) bool { return res.Dirs[i].Hotspot > res.Dirs[j].Hotspot })

	return res
}

// astGrepFindings folds external ast-grep pack findings into the result,
// suppressed by the baseline. Packs scan the working tree, so they only
// apply when the scored revision is what's checked out.
func (s *session) astGrepFindings(ctx context.Context, rev string, res *snapshotResult,
	live map[string][]rules.Finding, baseline rules.Baseline,
) {
	if len(s.cfg.AstGrep) == 0 || (rev != defaultRev && rev != "") {
		return
	}
	sgFindings, err := rules.RunAstGrep(ctx, s.root, s.cfg.AstGrep)
	if err != nil {
		res.Skipped = append(res.Skipped, skippedRow{"ast_grep", err.Error()})
	}
	for path, fs := range sgFindings {
		fs = baseline.Suppress(path, fs)
		if len(fs) > 0 {
			res.Findings[path] = append(res.Findings[path], fs...)
			live[path] = append(live[path], fs...)
		}
	}
}

// writeBaseline records the current findings as the accepted baseline.
func (s *session) writeBaseline(reports []*engine.FileReport) error {
	all := map[string][]rules.Finding{}
	for _, r := range reports {
		if len(r.Findings) > 0 {
			all[r.Path] = r.Findings
		}
	}
	if err := rules.WriteBaseline(filepath.Join(s.root, s.cfg.Baseline), all); err != nil {
		return fmt.Errorf("writing baseline: %w", err)
	}

	return nil
}

// snapshotContext fills the finding snippets a reader would open next.
func (s *session) snapshotContext(res *snapshotResult) *contextPayload {
	cp := &contextPayload{Snippets: map[string]map[int]string{}}
	for path, findings := range res.Findings {
		for _, f := range findings {
			if line := snippetLine(s.root, path, f.Line); line != "" {
				if cp.Snippets[path] == nil {
					cp.Snippets[path] = map[int]string{}
				}
				cp.Snippets[path][f.Line] = line
			}
		}
	}

	return cp
}

// printSnapshot renders the per-directory table ranked by hotspot score.
func printSnapshot(w io.Writer, res *snapshotResult, all bool) {
	report.Fprintf(w, "erosion %.2f · mass %.0f · %d funcs · %d over 50 lines\n\n",
		res.Total.Erosion, res.Total.Mass, res.Total.FuncCount, res.Total.Over50)
	rows := make([][]string, 0, len(res.Dirs))
	for _, d := range res.Dirs {
		rows = append(rows, []string{
			d.Dir, strconv.Itoa(d.Files), fmt.Sprintf("%.2f", d.Erosion),
			fmt.Sprintf("%.0f", d.Mass), strconv.Itoa(d.Over50), strconv.Itoa(d.Findings),
		})
	}
	report.Table(w, []string{colDir, "files", "erosion", "mass", ">50", "hits"}, rows)

	if all && len(res.Findings) > 0 {
		report.Fprintln(w)
		for _, p := range report.SortedKeys(res.Findings) {
			for _, f := range res.Findings[p] {
				report.Fprintf(w, "%s:%d  %s  %s\n", p, f.Line, f.Rule, strings.TrimSpace(f.Match))
			}
		}
	}
	for _, sk := range res.Skipped {
		report.Fprintf(w, "skipped %s: %s\n", sk.Path, sk.Reason)
	}
	for _, er := range res.Errors {
		report.Fprintf(w, "error %s: %s\n", er.Path, er.Reason)
	}
}

package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kyleking/slop-strata/internal/engine"
	"github.com/kyleking/slop-strata/internal/lang"
	"github.com/kyleking/slop-strata/internal/lineage"
	"github.com/kyleking/slop-strata/internal/report"
	"github.com/kyleking/slop-strata/internal/rules"
	"github.com/kyleking/slop-strata/internal/structure"
)

// snapshotResult is the snapshot subcommand's JSON shape.
type snapshotResult struct {
	Rev      string                     `json:"rev"`
	Total    structure.Aggregates       `json:"total"`
	Dirs     []dirSnapshot              `json:"dirs"`
	Findings map[string][]rules.Finding `json:"findings"`
	Skipped  []skippedRow               `json:"skipped,omitempty"`
	Errors   []skippedRow               `json:"errors,omitempty"`
	Context  *contextPayload            `json:"context,omitempty"`
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

	// Keep only analyzable blobs, then batch-read them.
	var want []string
	shaByPath := map[string]string{}
	for _, e := range entries {
		if lang.ByPath(e.Path) == nil {
			continue
		}
		want = append(want, e.SHA)
		shaByPath[e.Path] = e.SHA
	}
	blobs, err := s.ops.BlobContents(ctx, s.root, want)
	if err != nil {
		return err
	}

	var reports []*engine.FileReport
	for _, e := range entries {
		content, ok := blobs[e.SHA]
		if !ok || lang.ByPath(e.Path) == nil {
			continue
		}
		reports = append(reports, s.eng.Analyze(e.Path, e.SHA, content))
	}
	engine.CrossFile(reports)

	res := &snapshotResult{Rev: rev, Findings: map[string][]rules.Finding{}}
	baseline, err := rules.ReadBaseline(filepath.Join(s.root, s.cfg.Baseline))
	if err != nil {
		return err
	}

	byDir := map[string][]structure.FileMetrics{}
	live := map[string][]rules.Finding{}
	for _, r := range reports {
		if r.Skipped != "" {
			res.Skipped = append(res.Skipped, skippedRow{r.Path, r.Skipped})

			continue
		}
		if r.Metrics.ParseError {
			res.Errors = append(res.Errors, skippedRow{
				r.Path,
				fmt.Sprintf("tree-sitter parse error (%.1f%% of bytes)", r.Metrics.ErrorCoverage*100),
			})
		}
		byDir[lineage.DirOf(r.Path)] = append(byDir[lineage.DirOf(r.Path)], r.Metrics)
		fs := baseline.Suppress(r.Path, r.Findings)
		if len(fs) > 0 {
			res.Findings[r.Path] = fs
			live[r.Path] = fs
		}
	}

	// External ast-grep packs scan the working tree, so they only apply when
	// the scored revision is what's checked out.
	if len(s.cfg.AstGrep) > 0 && (rev == defaultRev || rev == "") {
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

	var files []structure.FileMetrics
	for _, r := range reports {
		if r.Skipped == "" {
			files = append(files, r.Metrics)
		}
	}
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

	if a.writeBaseline {
		all := map[string][]rules.Finding{}
		for _, r := range reports {
			if len(r.Findings) > 0 {
				all[r.Path] = r.Findings
			}
		}
		if err := rules.WriteBaseline(filepath.Join(s.root, s.cfg.Baseline), all); err != nil {
			return err
		}
	}

	if a.json {
		if a.ctx {
			res.Context = s.snapshotContext(res)
		}
		return report.WriteJSON(w, "snapshot", s.root, res, nil)
	}

	printSnapshot(w, res, a.all)

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
	var rows [][]string
	for _, d := range res.Dirs {
		rows = append(rows, []string{
			d.Dir, fmt.Sprint(d.Files), fmt.Sprintf("%.2f", d.Erosion),
			fmt.Sprintf("%.0f", d.Mass), fmt.Sprint(d.Over50), fmt.Sprint(d.Findings),
		})
	}
	report.Table(w, []string{"dir", "files", "erosion", "mass", ">50", "hits"}, rows)

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

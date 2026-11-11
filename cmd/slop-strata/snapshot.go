package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
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
	TestDirs []dirSnapshot              `json:"test_dirs,omitempty"`
	Files    []fileSnapshot             `json:"files"`
	Skipped  []skippedRow               `json:"skipped,omitempty"`
	Errors   []skippedRow               `json:"errors,omitempty"`
	Total    structure.Aggregates       `json:"total"`
}

// dirSnapshot is one directory's rollup.
type dirSnapshot struct {
	Dir         string  `json:"dir"`
	Files       int     `json:"files"`
	Erosion     float64 `json:"erosion"`
	Mass        float64 `json:"mass"`
	TableMass   float64 `json:"table_mass"`
	Funcs       int     `json:"funcs"`
	Over50      int     `json:"over_50"`
	Hotspot     float64 `json:"hotspot"`
	Findings    int     `json:"findings"`
	Provisional bool    `json:"provisional,omitempty"`
}

// fileSnapshot is one source file's rollup, so a hot directory can be traced
// to the file behind it.
type fileSnapshot struct {
	Path      string  `json:"path"`
	Test      bool    `json:"test,omitempty"`
	SLOC      int     `json:"sloc"`
	Funcs     int     `json:"funcs"`
	Mass      float64 `json:"mass"`
	Tables    int     `json:"tables"`
	TableMass float64 `json:"table_mass"`
	Erosion   float64 `json:"erosion"`
	Over50    int     `json:"over_50"`
	MaxCC     int     `json:"max_cc"`
	Findings  int     `json:"findings"`
	Hotspot   float64 `json:"hotspot"`
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
		if err := report.WriteJSON(w, "snapshot", s.root, res, countRows(res.Skipped, res.Errors), nil); err != nil {
			return fmt.Errorf("writing result: %w", err)
		}

		return nil
	}

	printSnapshot(w, res, a.all)
	report.WarnCounts(s.stderr, countRows(res.Skipped, res.Errors))

	return nil
}

// treeReports analyzes every supported blob in the tree, plus the prose and
// config blobs the cross-file passes read.
func (s *session) treeReports(ctx context.Context, entries []vcs.TreeEntry) ([]*engine.FileReport, error) {
	// Keep only analyzable blobs, then batch-read them.
	var want []string
	for _, e := range entries {
		if lang.ByPath(e.Path) != nil || lang.TextKind(e.Path) != "" {
			want = append(want, e.SHA)
		}
	}
	blobs, err := s.ops.BlobContents(ctx, s.root, want)
	if err != nil {
		return nil, fmt.Errorf("reading blobs: %w", err)
	}

	var reqs []engine.Request
	for _, e := range entries {
		content, ok := blobs[e.SHA]
		if !ok {
			continue
		}
		switch {
		case lang.ByPath(e.Path) != nil:
			reqs = append(reqs, engine.Request{Path: e.Path, SHA: e.SHA, Content: content})
		case lang.TextKind(e.Path) != "":
			reqs = append(reqs, engine.Request{Path: e.Path, SHA: e.SHA, Content: content, Text: true})
		}
	}
	s.eng.Prefetch(ctx, reqs)
	reports := make([]*engine.FileReport, 0, len(reqs))
	for _, r := range reqs {
		if r.Text {
			reports = append(reports, s.eng.AnalyzeText(ctx, r.Path, r.SHA, r.Content))
		} else {
			reports = append(reports, s.eng.Analyze(ctx, r.Path, r.SHA, r.Content))
		}
	}
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		paths = append(paths, e.Path)
	}
	engine.CrossFile(reports, paths, s.cfg)

	return reports, nil
}

// snapshotRollup folds the file reports into the snapshot result: skipped
// blobs, live findings, and the per-directory aggregates ranked by hotspot.
func (s *session) snapshotRollup(
	ctx context.Context, rev string, reports []*engine.FileReport, baseline rules.Baseline,
) *snapshotResult {
	res := &snapshotResult{Rev: rev, Findings: map[string][]rules.Finding{}}
	byDir := map[string][]structure.FileMetrics{}
	byTestDir := map[string][]structure.FileMetrics{}
	live := map[string][]rules.Finding{}
	var files []structure.FileMetrics
	metrics := map[string]structure.FileMetrics{}
	for _, r := range reports {
		if r.Skipped != "" {
			res.Skipped = append(res.Skipped, skippedRow{r.Path, r.Skipped})

			continue
		}
		if r.Metrics.ParseError {
			res.Errors = append(res.Errors, parseErrorRow(r.Path, r.Metrics))
		}
		if r.Metrics.Lang != "" {
			files = append(files, r.Metrics)
			metrics[r.Path] = r.Metrics
			if lang.IsTestPath(r.Path) {
				byTestDir[lineage.DirOf(r.Path)] = append(byTestDir[lineage.DirOf(r.Path)], r.Metrics)
			} else {
				byDir[lineage.DirOf(r.Path)] = append(byDir[lineage.DirOf(r.Path)], r.Metrics)
			}
		}
		fs := baseline.Suppress(r.Path, r.Findings)
		if len(fs) > 0 {
			res.Findings[r.Path] = fs
			live[r.Path] = fs
		}
	}

	s.astGrepFindings(ctx, rev, res, live, baseline)
	for _, fs := range res.Findings {
		slices.SortFunc(fs, func(a, b rules.Finding) int {
			return cmp.Or(
				cmp.Compare(a.Line, b.Line),
				strings.Compare(a.Rule, b.Rule),
				strings.Compare(a.Match, b.Match),
			)
		})
	}

	res.Total = structure.Aggregate(files)
	res.Dirs = rankDirs(byDir, live, false)
	res.TestDirs = rankDirs(byTestDir, live, true)
	res.Files = rankFiles(metrics, live)

	return res
}

// rankDirs rolls files up per directory, ranked by hotspot. Findings count
// only from files on the same side of the test split as the directory's files.
func rankDirs(
	byDir map[string][]structure.FileMetrics, live map[string][]rules.Finding, test bool,
) []dirSnapshot {
	dirs := make([]dirSnapshot, 0, len(byDir))
	for _, d := range report.SortedKeys(byDir) {
		agg := structure.Aggregate(byDir[d])
		findings := 0
		for p, fs := range live {
			if lineage.DirOf(p) == d && lang.IsTestPath(p) == test {
				findings += len(fs)
			}
		}
		dirs = append(dirs, dirSnapshot{
			Dir: d, Files: agg.Files, Erosion: agg.Erosion, Mass: agg.Mass, TableMass: agg.TableMass,
			Funcs: agg.FuncCount, Over50: agg.Over50, Findings: findings,
			Hotspot:     (agg.Mass + agg.TableMass) * float64(findings+1),
			Provisional: agg.FuncCount < structure.ProvisionalFuncs,
		})
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Hotspot > dirs[j].Hotspot })

	return dirs
}

// rankFiles scores each source file, production files first, ranked by hotspot.
func rankFiles(metrics map[string]structure.FileMetrics, live map[string][]rules.Finding) []fileSnapshot {
	files := make([]fileSnapshot, 0, len(metrics))
	for path, m := range metrics {
		agg := structure.Aggregate([]structure.FileMetrics{m})
		maxCC := 0
		for _, fn := range m.Funcs {
			maxCC = max(maxCC, fn.CC)
		}
		findings := len(live[path])
		files = append(files, fileSnapshot{
			Path: path, Test: lang.IsTestPath(path), SLOC: m.SLOC, Funcs: agg.FuncCount, Mass: agg.Mass,
			Tables: agg.Tables, TableMass: agg.TableMass, Erosion: agg.Erosion, Over50: agg.Over50,
			MaxCC: maxCC, Findings: findings,
			Hotspot: (agg.Mass + agg.TableMass) * float64(findings+1),
		})
	}
	slices.SortFunc(files, func(a, b fileSnapshot) int {
		return cmp.Or(
			cmp.Compare(btoi(a.Test), btoi(b.Test)),
			cmp.Compare(b.Hotspot, a.Hotspot),
			strings.Compare(a.Path, b.Path),
		)
	})

	return files
}

func btoi(b bool) int {
	if b {
		return 1
	}

	return 0
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
	printDirTable(w, res.Dirs)
	provisionalNote(w, res.Dirs, func(d dirSnapshot) bool { return d.Provisional }, structure.ProvisionalFuncs)
	printFileTable(w, res.Files, all)
	if len(res.TestDirs) > 0 {
		report.Fprintf(w, "\ntests, not ranked with the above\n")
		printDirTable(w, res.TestDirs)
	}

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

func printDirTable(w io.Writer, dirs []dirSnapshot) {
	rows := make([][]string, 0, len(dirs))
	for _, d := range dirs {
		rows = append(rows, []string{
			d.Dir, strconv.Itoa(d.Files), provisionalMarked(fmt.Sprintf("%.2f", d.Erosion), d.Provisional),
			fmt.Sprintf("%.0f", d.Mass), fmt.Sprintf("%.0f", d.TableMass),
			strconv.Itoa(d.Over50), strconv.Itoa(d.Findings),
		})
	}
	report.Table(w, []string{colDir, "files", "erosion", colMass, "table_mass", "over_50", "rule_hits"}, rows)
}

// topFiles is how many ranked files the text view lists without --all.
const topFiles = 10

// printFileTable lists the highest-hotspot production files, all of them with --all.
func printFileTable(w io.Writer, files []fileSnapshot, all bool) {
	var rows [][]string
	for _, f := range files {
		if f.Test || (!all && len(rows) >= topFiles) {
			continue
		}
		rows = append(rows, []string{
			f.Path, strconv.Itoa(f.SLOC), fmt.Sprintf("%.0f", f.Mass), fmt.Sprintf("%.0f", f.TableMass),
			strconv.Itoa(f.MaxCC), strconv.Itoa(f.Findings),
		})
	}
	if len(rows) == 0 {
		return
	}
	report.Fprintln(w)
	report.Table(w, []string{"file", "sloc", colMass, "table_mass", "max_cc", "rule_hits"}, rows)
}

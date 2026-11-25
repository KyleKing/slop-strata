package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/kyleking/aragonite/vcs"

	"github.com/kyleking/slop-strata/internal/depgraph"
	"github.com/kyleking/slop-strata/internal/depth"
	"github.com/kyleking/slop-strata/internal/engine"
	"github.com/kyleking/slop-strata/internal/lang"
	"github.com/kyleking/slop-strata/internal/lineage"
	"github.com/kyleking/slop-strata/internal/report"
	"github.com/kyleking/slop-strata/internal/rules"
	"github.com/kyleking/slop-strata/internal/structure"
)

// snapshotResult is the snapshot subcommand's JSON shape.
type snapshotResult struct {
	Findings   map[string][]rules.Finding `json:"findings"`
	Duplicates *duplicateList             `json:"duplicate_groups,omitempty"`
	Splits     *splitList                 `json:"split_candidates,omitempty"`
	Rev        string                     `json:"rev"`
	Dirs       []dirSnapshot              `json:"dirs"`
	TestDirs   []dirSnapshot              `json:"test_dirs,omitempty"`
	Files      []fileSnapshot             `json:"files"`
	TopFuncs   []snapFunc                 `json:"top_functions"`
	Skipped    []skippedRow               `json:"skipped,omitempty"`
	Errors     []skippedRow               `json:"errors,omitempty"`
	Total      structure.Aggregates       `json:"total"`
	Imports    importSummary              `json:"imports"`
}

// importSummary counts the resolved import graph of the scored tree.
type importSummary struct {
	Edges         int `json:"edges"`
	TypeEdges     int `json:"type_edges"`
	DeferredEdges int `json:"deferred_edges"`
	Cycles        int `json:"cycles"`
	LargestCycle  int `json:"largest_cycle"`
}

// dirSnapshot is one directory's rollup. The module depth fields are zero,
// with a null depth, on a test directory's row.
type dirSnapshot struct {
	Dir string `json:"dir"`
	depth.Module

	Files       int     `json:"files"`
	Erosion     float64 `json:"erosion"`
	Mass        float64 `json:"mass"`
	TableMass   float64 `json:"table_mass"`
	Funcs       int     `json:"funcs"`
	Over50      int     `json:"over_50"`
	Touches90d  int     `json:"touches_90d"`
	Hotspot     float64 `json:"hotspot"`
	Findings    int     `json:"findings"`
	FanIn       int     `json:"fan_in"`
	FanOut      int     `json:"fan_out"`
	Provisional bool    `json:"provisional,omitempty"`
}

// snapFunc is one function of the scored tree.
type snapFunc struct {
	File string  `json:"file"`
	Name string  `json:"name"`
	Line int     `json:"line"`
	CC   int     `json:"cc"`
	SLOC int     `json:"sloc"`
	Mass float64 `json:"mass"`
	Test bool    `json:"test"`
}

// fileSnapshot is one source file's rollup, so a hot directory can be traced
// to the file behind it.
type fileSnapshot struct {
	Path       string  `json:"path"`
	Test       bool    `json:"test,omitempty"`
	SLOC       int     `json:"sloc"`
	Funcs      int     `json:"funcs"`
	Mass       float64 `json:"mass"`
	Tables     int     `json:"tables"`
	TableMass  float64 `json:"table_mass"`
	Erosion    float64 `json:"erosion"`
	Over50     int     `json:"over_50"`
	MaxCC      int     `json:"max_cc"`
	Findings   int     `json:"findings"`
	Touches90d int     `json:"touches_90d"`
	Hotspot    float64 `json:"hotspot"`
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
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		paths = append(paths, e.Path)
	}
	sup, err := s.suppressor(a.writeBaseline)
	if err != nil {
		return err
	}
	touches, err := s.snapshotTouches(ctx, rev)
	if err != nil {
		return err
	}
	res := s.snapshotRollup(ctx, rev, reports, sup, touches)
	res.addImports(engine.ResolveImports(reports, paths))
	if s.cfg.Experimental {
		res.Duplicates = duplicateGroups(reports)
	}
	counts := countRows(res.Skipped, res.Errors)
	counts.Suppressed = suppressedCounts(sup)
	counts.StaleBaseline = sup.StaleBaseline(func(rule string) bool {
		return rules.SnapshotRule(rule, s.cfg.Experimental)
	})

	if a.writeBaseline {
		if err := s.writeBaseline(reports); err != nil {
			return err
		}
	}
	if a.json {
		if err := report.WriteJSON(w, "snapshot", s.root, res, counts, nil); err != nil {
			return fmt.Errorf("writing result: %w", err)
		}

		return nil
	}

	printSnapshot(w, res, a.all)
	report.WarnCounts(s.stderr, counts)

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

// addImports folds the import graph into the result: the totals, and each directory's fan-in and fan-out.
// A production directory counts the edges between production files. A test directory counts the edges that
// start or end at a test file.
func (res *snapshotResult) addImports(im *rules.Imports) {
	for _, e := range im.Graph.Edges() {
		res.Imports.Edges++
		switch e.Kind {
		case depgraph.TypeOnly:
			res.Imports.TypeEdges++
		case depgraph.Deferred, depgraph.MainGuard:
			res.Imports.DeferredEdges++
		case depgraph.Runtime:
		}
	}
	cycles := im.Cycles()
	res.Imports.Cycles = len(cycles)
	for i := range cycles {
		res.Imports.LargestCycle = max(res.Imports.LargestCycle, len(cycles[i].Members))
	}
	touchesTest := func(e depgraph.Edge) bool { return lang.IsTestPath(e.From) || lang.IsTestPath(e.To) }
	production := im.Graph.Filter(func(e depgraph.Edge) bool { return !touchesTest(e) }).Fans(lineage.DirOf)
	modules := im.Depth(lineage.DirOf)
	for i := range res.Dirs {
		d := &res.Dirs[i]
		d.FanIn, d.FanOut = production[d.Dir].In, production[d.Dir].Out
		if m := modules[d.Dir]; m != nil {
			d.Module = *m
		}
	}
	tests := im.Graph.Filter(touchesTest).Fans(lineage.DirOf)
	for i := range res.TestDirs {
		d := &res.TestDirs[i]
		d.FanIn, d.FanOut = tests[d.Dir].In, tests[d.Dir].Out
	}
}

// recentSnapshotDays is the window of the touches behind a snapshot's hotspot.
const recentSnapshotDays = 90

// touchCounts is the commits that touched each directory and file in a window.
type touchCounts struct {
	dirs  map[string]int
	files map[string]int
}

// snapshotTouches counts the commits touching each directory and file in the
// recentSnapshotDays before the scored commit.
func (s *session) snapshotTouches(ctx context.Context, rev string) (touchCounts, error) {
	cmd := exec.CommandContext(ctx, "git", "log", "-1", "--format=%ct", rev) // #nosec G204 -- rev is the caller's
	cmd.Dir = s.root
	out, err := cmd.Output()
	if err != nil {
		return touchCounts{}, fmt.Errorf("reading the commit date of %s: %w", rev, err)
	}
	stamp, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return touchCounts{}, fmt.Errorf("parsing the commit date of %s: %w", rev, err)
	}
	since := strconv.FormatInt(stamp-recentSnapshotDays*secondsPerDay, 10)
	commits, err := recentCommitPaths(ctx, s.root, rev, since)
	if err != nil {
		return touchCounts{}, err
	}
	dirs, files := countTouches(commits)

	return touchCounts{dirs: dirs, files: files}, nil
}

// secondsPerDay converts the touch window to seconds.
const secondsPerDay = 24 * 60 * 60

// snapshotRollup folds the file reports into the snapshot result: skipped
// blobs, live findings, and the per-directory aggregates ranked by hotspot.
func (s *session) snapshotRollup(
	ctx context.Context, rev string, reports []*engine.FileReport, sup *rules.Suppressor, touches touchCounts,
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
		fs := sup.Apply(r.Path, r.Print, r.Findings)
		if len(fs) > 0 {
			res.Findings[r.Path] = fs
			live[r.Path] = fs
		}
	}

	s.astGrepFindings(ctx, rev, res, live, sup)
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
	res.Dirs = rankDirs(byDir, live, touches.dirs, false)
	res.TestDirs = rankDirs(byTestDir, live, touches.dirs, true)
	res.Files = rankFiles(metrics, live, touches.files)
	res.TopFuncs = rankSnapFuncs(metrics)
	if s.cfg.Experimental {
		res.Splits = splitCandidates(metrics)
	}

	return res
}

// rankDirs rolls files up per directory, ranked by hotspot, then mass. Findings
// count only from files on the same side of the test split as the directory's files.
func rankDirs(
	byDir map[string][]structure.FileMetrics, live map[string][]rules.Finding, touches map[string]int, test bool,
) []dirSnapshot {
	findings := make(map[string]int, len(byDir))
	for p, fs := range live {
		if lang.IsTestPath(p) == test {
			findings[lineage.DirOf(p)] += len(fs)
		}
	}
	dirs := make([]dirSnapshot, 0, len(byDir))
	for _, d := range report.SortedKeys(byDir) {
		agg := structure.Aggregate(byDir[d])
		dirs = append(dirs, dirSnapshot{
			Dir: d, Files: agg.Files, Erosion: agg.Erosion, Mass: agg.Mass, TableMass: agg.TableMass,
			Funcs: agg.FuncCount, Over50: agg.Over50, Findings: findings[d],
			Touches90d: touches[d], Hotspot: float64(touches[d]) * agg.Mass,
			Provisional: agg.FuncCount < structure.ProvisionalFuncs,
		})
	}
	slices.SortStableFunc(dirs, func(a, b dirSnapshot) int {
		return cmp.Or(cmp.Compare(b.Hotspot, a.Hotspot), cmp.Compare(b.Mass, a.Mass))
	})

	return dirs
}

// rankFiles scores each source file, production files first, ranked by hotspot, then mass.
func rankFiles(
	metrics map[string]structure.FileMetrics, live map[string][]rules.Finding, touches map[string]int,
) []fileSnapshot {
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
			Touches90d: touches[path], Hotspot: float64(touches[path]) * agg.Mass,
		})
	}
	slices.SortFunc(files, func(a, b fileSnapshot) int {
		return cmp.Or(
			cmp.Compare(btoi(a.Test), btoi(b.Test)),
			cmp.Compare(b.Hotspot, a.Hotspot),
			cmp.Compare(b.Mass, a.Mass),
			strings.Compare(a.Path, b.Path),
		)
	})

	return files
}

// snapFuncLimit caps the top_functions list of a snapshot.
const snapFuncLimit = 10

// rankSnapFuncs lists the heaviest functions of the tree, tests included and flagged.
func rankSnapFuncs(metrics map[string]structure.FileMetrics) []snapFunc {
	out := []snapFunc{}
	for path, m := range metrics {
		test := lang.IsTestPath(path)
		for _, fn := range m.Funcs {
			out = append(out, snapFunc{
				File: path, Name: fn.Name, Line: fn.StartLine, CC: fn.CC, SLOC: fn.SLOC,
				Mass: structure.Mass(fn), Test: test,
			})
		}
	}
	slices.SortFunc(out, func(a, b snapFunc) int {
		return cmp.Or(
			cmp.Compare(b.Mass, a.Mass),
			strings.Compare(a.File, b.File),
			cmp.Compare(a.Line, b.Line),
			strings.Compare(a.Name, b.Name),
		)
	})

	return out[:min(len(out), snapFuncLimit)]
}

func btoi(b bool) int {
	if b {
		return 1
	}

	return 0
}

// astGrepFindings folds ast-grep findings in, set aside by the baseline and labels but not by inline ignores.
// Packs scan the working tree, so they apply only when the scored revision is checked out.
func (s *session) astGrepFindings(ctx context.Context, rev string, res *snapshotResult,
	live map[string][]rules.Finding, sup *rules.Suppressor,
) {
	if len(s.cfg.AstGrep) == 0 || (rev != defaultRev && rev != "") {
		return
	}
	sgFindings, err := rules.RunAstGrep(ctx, s.root, s.cfg.AstGrep)
	if err != nil {
		res.Skipped = append(res.Skipped, skippedRow{"ast_grep", err.Error()})
	}
	for path, fs := range sgFindings {
		fs = sup.Apply(path, nil, fs)
		if len(fs) > 0 {
			res.Findings[path] = append(res.Findings[path], fs...)
			live[path] = append(live[path], fs...)
		}
	}
}

// writeBaseline records the current findings. Ignored and fp-labeled ones stay out,
// so removing the mark brings the hit back.
func (s *session) writeBaseline(reports []*engine.FileReport) error {
	labels, err := s.readLabels()
	if err != nil {
		return err
	}
	live := rules.NewSuppressor(rules.Baseline{}, labels)
	all := map[string][]rules.Finding{}
	for _, r := range reports {
		if kept := live.Apply(r.Path, r.Print, r.Findings); len(kept) > 0 {
			all[r.Path] = kept
		}
	}
	if err := rules.WriteBaseline(filepath.Join(s.root, s.cfg.Baseline), all); err != nil {
		return fmt.Errorf("writing baseline: %w", err)
	}

	return nil
}

// printSnapshot renders the per-directory table ranked by hotspot, then mass.
func printSnapshot(w io.Writer, res *snapshotResult, all bool) {
	report.Fprintf(w, "erosion %.2f · mass %.0f · %d funcs · %d over 50 lines\n\n",
		res.Total.Erosion, res.Total.Mass, res.Total.FuncCount, res.Total.Over50)
	if im := res.Imports; im.Edges > 0 {
		report.Fprintf(w, "imports: %d edges · %d import_cycles · largest %d files\n\n",
			im.Edges, im.Cycles, im.LargestCycle)
	}
	printDirTable(w, res.Dirs)
	provisionalNote(w, res.Dirs, func(d dirSnapshot) bool { return d.Provisional }, structure.ProvisionalFuncs)
	printDepthTable(w, res.Dirs, all)
	printFileTable(w, res.Files, all)
	printSnapFuncs(w, res.TopFuncs)
	printSplits(w, res.Splits, all)
	printDuplicates(w, res.Duplicates, all)
	if len(res.TestDirs) > 0 {
		report.Fprintf(w, "\ntests, not ranked with the above\n")
		printDirTable(w, res.TestDirs)
	}

	if all && len(res.Findings) > 0 {
		report.Fprintln(w)
		for _, p := range report.SortedKeys(res.Findings) {
			for i := range res.Findings[p] {
				f := &res.Findings[p][i]
				report.Fprintf(w, "%s:%d  %s  %s  %s\n", p, f.Line, f.ID, f.Rule, strings.TrimSpace(f.Match))
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
	for i := range dirs {
		d := &dirs[i]
		rows = append(rows, []string{
			d.Dir, strconv.Itoa(d.Files), provisionalMarked(fmt.Sprintf("%.2f", d.Erosion), d.Provisional),
			fmt.Sprintf("%.0f", d.Mass), fmt.Sprintf("%.0f", d.TableMass),
			strconv.Itoa(d.Over50), strconv.Itoa(d.Findings), strconv.Itoa(d.FanIn), strconv.Itoa(d.FanOut),
			fmt.Sprintf("%.0f", d.Hotspot),
		})
	}
	header := []string{
		colDir, "files", colErosion, colMass, "table_mass", "over_50", "rule_hits", "fan_in", "fan_out", colHotspot,
	}
	report.Table(w, header, rows)
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
			strconv.Itoa(f.MaxCC), strconv.Itoa(f.Findings), fmt.Sprintf("%.0f", f.Hotspot),
		})
	}
	if len(rows) == 0 {
		return
	}
	report.Fprintln(w)
	report.Table(w, []string{"file", "sloc", colMass, "table_mass", "max_cc", "rule_hits", colHotspot}, rows)
}

// topSnapFuncs is how many production functions the text view lists.
const topSnapFuncs = 5

// printSnapFuncs lists the heaviest production functions of the tree.
func printSnapFuncs(w io.Writer, funcs []snapFunc) {
	var rows [][]string
	for _, f := range funcs {
		if f.Test || len(rows) >= topSnapFuncs {
			continue
		}
		rows = append(rows, []string{
			fmt.Sprintf("%s:%d", f.File, f.Line), f.Name, "cc " + strconv.Itoa(f.CC),
			strconv.Itoa(f.SLOC), fmt.Sprintf("%.1f", f.Mass),
		})
	}
	if len(rows) == 0 {
		return
	}
	report.Fprintln(w)
	report.Table(w, []string{"top functions", "", "", "sloc", colMass}, rows)
}

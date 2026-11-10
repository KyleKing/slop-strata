// Package engine ties parsing, rules, and the blob cache together: a file in
// (path, sha, content) becomes a scored report out, with every blob computed
// once ever.
package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/kyleking/slop-strata/internal/cache"
	"github.com/kyleking/slop-strata/internal/config"
	"github.com/kyleking/slop-strata/internal/lang"
	"github.com/kyleking/slop-strata/internal/rules"
	"github.com/kyleking/slop-strata/internal/structure"
)

// FileReport is one blob's analysis, or why it was skipped.
type FileReport struct {
	Print    *rules.Fingerprint    `json:"-"`
	Path     string                `json:"path"`
	SHA      string                `json:"sha"`
	Lang     string                `json:"lang,omitempty"`
	Skipped  string                `json:"skipped,omitempty"`
	Findings []rules.Finding       `json:"findings,omitempty"`
	Metrics  structure.FileMetrics `json:"metrics,omitempty"`
}

// cached is the stored payload per blob.
type cached struct {
	Print    *rules.Fingerprint    `json:"print"`
	Findings []rules.Finding       `json:"findings"`
	Metrics  structure.FileMetrics `json:"metrics"`
}

// Analyzer scores blobs through the cache. Prefetch scores a batch of blobs in
// parallel and holds the results until Analyze or AnalyzeText asks for them.
type Analyzer struct {
	memo     map[string]*FileReport
	parser   *lang.Parser
	store    *cache.Store
	cfg      *config.Config
	analysis string
}

// Request is one blob for Prefetch. Text marks a prose or config blob, which
// AnalyzeText scores; anything else is scored as code.
type Request struct {
	Path    string
	SHA     string
	Content []byte
	Text    bool
}

// job is a blob the cache could not answer: the report it fills and the bytes it
// scores.
type job struct {
	rep     *FileReport
	lang    *lang.Language
	sha     string
	class   string
	content []byte
}

// New builds an Analyzer. Store may be a no-op store.
func New(cfg *config.Config, store *cache.Store) *Analyzer {
	return &Analyzer{
		memo: map[string]*FileReport{}, store: store, cfg: cfg,
		analysis: analysisKey(cfg.Experimental),
	}
}

// Close releases the parser Analyze reuses between calls.
func (a *Analyzer) Close() {
	if a.parser != nil {
		a.parser.Close()
		a.parser = nil
	}
}

// ReasonUnsupported is the skip reason for a path no grammar or text kind covers.
// Such files are listed as skipped but not counted, since they are never meant
// to be scored.
const ReasonUnsupported = "unsupported"

// Skip reports whether a path is excluded by config or unsupported.
func (a *Analyzer) skip(path string) string {
	for _, glob := range a.cfg.Skip {
		if ok, err := matchGlob(glob, path); err == nil && ok {
			return "config-skip"
		}
	}
	if lang.ByPath(path) == nil {
		return ReasonUnsupported
	}

	return ""
}

// matchGlob applies config skip globs. It is deliberately small: "dir/**"
// prefixes a directory tree, everything else is filepath.Match against the
// path and its basename.
func matchGlob(glob, path string) (bool, error) {
	if prefix, ok := strings.CutSuffix(glob, "/**"); ok {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true, nil
		}
	}
	ok, err := filepath.Match(glob, path)
	if err != nil {
		return false, fmt.Errorf("matching %q: %w", glob, err)
	}
	if ok {
		return true, nil
	}
	ok, err = filepath.Match(glob, filepath.Base(path))
	if err != nil {
		return false, fmt.Errorf("matching %q: %w", glob, err)
	}

	return ok, nil
}

// Analyze scores one blob. Unsupported paths and config skips return a
// skipped report; oversize and minified blobs likewise, each with its reason.
func (a *Analyzer) Analyze(ctx context.Context, path, sha string, content []byte) *FileReport {
	if rep, ok := a.take(path, sha); ok {
		return rep
	}
	rep, j := a.begin(ctx, path, sha, content)
	if j == nil {
		return rep
	}
	if a.parser == nil {
		a.parser = lang.NewParser()
	}
	a.record(ctx, j, a.score(a.parser, j))

	return rep
}

// AnalyzeText fingerprints a prose or config blob: no parse, only the
// text-level facts the cross-file passes join on.
func (a *Analyzer) AnalyzeText(ctx context.Context, path, sha string, content []byte) *FileReport {
	if rep, ok := a.take(path, sha); ok {
		return rep
	}
	rep, j := a.beginText(ctx, path, sha, content)
	if j == nil {
		return rep
	}
	a.record(ctx, j, a.score(nil, j))

	return rep
}

// Prefetch scores the requests not yet held, one worker per CPU. The calling
// goroutine alone writes the cache and the hold, so no lock is needed.
func (a *Analyzer) Prefetch(ctx context.Context, reqs []Request) {
	var jobs []*job
	pending := map[string]bool{}
	for _, r := range reqs {
		key := memoKey(r.Path, r.SHA)
		if pending[key] {
			continue
		}
		pending[key] = true
		var (
			rep *FileReport
			j   *job
		)
		if r.Text {
			rep, j = a.beginText(ctx, r.Path, r.SHA, r.Content)
		} else {
			rep, j = a.begin(ctx, r.Path, r.SHA, r.Content)
		}
		if j == nil {
			a.memo[key] = rep

			continue
		}
		jobs = append(jobs, j)
	}
	a.run(ctx, jobs)
}

// run scores jobs on a worker pool and records each result as it arrives.
func (a *Analyzer) run(ctx context.Context, jobs []*job) {
	if len(jobs) == 0 {
		return
	}
	type scored struct {
		job     *job
		payload []byte
	}
	workers := min(runtime.GOMAXPROCS(0), len(jobs))
	in := make(chan *job)
	out := make(chan scored)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := lang.NewParser()
			defer p.Close()
			for j := range in {
				out <- scored{job: j, payload: a.score(p, j)}
			}
		}()
	}
	go func() {
		for _, j := range jobs {
			in <- j
		}
		close(in)
	}()
	go func() {
		wg.Wait()
		close(out)
	}()
	for s := range out {
		a.record(ctx, s.job, s.payload)
		a.memo[memoKey(s.job.rep.Path, s.job.sha)] = s.job.rep
	}
}

// take hands out a prefetched report, which is held for one call only.
func (a *Analyzer) take(path, sha string) (*FileReport, bool) {
	key := memoKey(path, sha)
	rep, ok := a.memo[key]
	if ok {
		delete(a.memo, key)
	}

	return rep, ok
}

func memoKey(path, sha string) string {
	return path + "\x00" + sha
}

// begin answers a code blob that needs no parse, either skipped or read from the
// cache, and otherwise returns the job that scores it.
func (a *Analyzer) begin(ctx context.Context, path, sha string, content []byte) (*FileReport, *job) {
	rep := &FileReport{Path: path, SHA: sha}
	if reason := a.skip(path); reason != "" {
		rep.Skipped = reason

		return rep, nil
	}
	l := lang.ByPath(path)
	if int64(len(content)) > a.cfg.MaxFileBytes {
		rep.Skipped = "oversize"

		return rep, nil
	}
	if bytes.IndexByte(content, 0) >= 0 {
		rep.Skipped = "binary"

		return rep, nil
	}
	if minified(content, a.cfg.MinifiedAvgLine) {
		rep.Skipped = "minified"

		return rep, nil
	}
	class := pathClass(path)
	if payload, ok := a.store.Get(ctx, sha, class, a.analysis); ok {
		var c cached
		if err := json.Unmarshal(payload, &c); err == nil {
			rep.Metrics = c.Metrics
			rep.Findings = c.Findings
			rep.Print = c.Print
			rep.Lang = c.Metrics.Lang
			a.addPathFindings(rep)

			return rep, nil
		}
	}

	return rep, &job{rep: rep, lang: l, sha: sha, class: class, content: content}
}

// beginText is begin for prose and config blobs, which have no parse and no
// minified or path-dependent findings.
func (a *Analyzer) beginText(ctx context.Context, path, sha string, content []byte) (*FileReport, *job) {
	rep := &FileReport{Path: path, SHA: sha, Lang: lang.TextKind(path)}
	for _, glob := range a.cfg.Skip {
		if ok, err := matchGlob(glob, path); err == nil && ok {
			rep.Skipped = "config-skip"

			return rep, nil
		}
	}
	if int64(len(content)) > a.cfg.MaxFileBytes {
		rep.Skipped = "oversize"

		return rep, nil
	}
	if bytes.IndexByte(content, 0) >= 0 {
		rep.Skipped = "binary"

		return rep, nil
	}
	class := pathClass(path)
	if payload, ok := a.store.Get(ctx, sha, class, a.analysis); ok {
		var c cached
		if err := json.Unmarshal(payload, &c); err == nil {
			rep.Print = c.Print

			return rep, nil
		}
	}

	return rep, &job{rep: rep, sha: sha, class: class, content: content}
}

// score computes a job's report and returns the payload to cache, or nil when it
// cannot be encoded. A nil parser means a text job.
func (a *Analyzer) score(p *lang.Parser, j *job) []byte {
	rep := j.rep
	if j.lang == nil {
		rep.Print = rules.CollectText(rep.Path, j.content)
		payload, err := json.Marshal(cached{Print: rep.Print})
		if err != nil {
			return nil
		}

		return payload
	}
	rep.Lang = j.lang.ID
	tree := p.Parse(j.lang, j.content)
	defer tree.Close()

	rep.Metrics = structure.Analyze(j.lang, tree, j.content)
	rctx := &rules.Ctx{
		Lang: j.lang, Root: tree.RootNode(), Src: j.content, Path: rep.Path,
		Test: j.lang.IsTestFile(rep.Path), Experimental: a.cfg.Experimental,
	}
	rep.Findings = rules.Run(rctx)
	rep.Print = rules.Collect(rctx)
	payload, err := json.Marshal(cached{Metrics: rep.Metrics, Findings: rep.Findings, Print: rep.Print})
	if err != nil {
		return nil
	}

	return payload
}

// record stores a scored job's payload, when it has one, and adds the findings
// that depend on the file name, which the cached payload never holds.
func (a *Analyzer) record(ctx context.Context, j *job, payload []byte) {
	if payload != nil {
		a.store.Put(ctx, j.sha, j.class, a.analysis, payload)
	}
	if j.lang != nil {
		a.addPathFindings(j.rep)
	}
}

// addPathFindings appends the findings that depend on the file name.
func (a *Analyzer) addPathFindings(rep *FileReport) {
	test := lang.IsTestPath(rep.Path)
	rep.Findings = append(rep.Findings, rules.PathFindings(rep.Path, test, a.cfg.Experimental)...)
}

// minified reports whether content looks machine-packed: very long average
// line, which is how vendored bundles arrive.
func minified(content []byte, avgLine int) bool {
	if avgLine <= 0 {
		return false
	}
	newlines := bytes.Count(content, []byte{'\n'})
	if newlines == 0 {
		return len(content) > avgLine
	}

	return len(content)/newlines > avgLine
}

// CrossFile runs the cross-file passes over analyzed reports and merges their
// findings into each file's report. TreePaths is every path in the tree.
func CrossFile(reports []*FileReport, treePaths []string, cfg *config.Config) {
	var blobs []rules.BlobPrint
	for _, r := range reports {
		if r.Print != nil {
			blobs = append(blobs, rules.BlobPrint{Path: r.Path, Print: r.Print})
		}
	}
	for path, findings := range rules.CrossFile(blobs, treePaths, cfg.EntryPoints, cfg.ConfigPaths, cfg.Experimental) {
		for _, r := range reports {
			if r.Path == path {
				r.Findings = append(r.Findings, findings...)
			}
		}
	}
}

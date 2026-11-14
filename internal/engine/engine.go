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

// cachedScores is cached with the fingerprint left undecoded.
type cachedScores struct {
	Print    *rules.Fingerprint    `json:"-"`
	Skipped  string                `json:"skipped,omitempty"`
	Findings []rules.Finding       `json:"findings"`
	Metrics  structure.FileMetrics `json:"metrics"`
}

// cached is the stored payload per blob: its analysis, or the reason its
// content was skipped.
type cached struct {
	Print    *rules.Fingerprint    `json:"print"`
	Skipped  string                `json:"skipped,omitempty"`
	Findings []rules.Finding       `json:"findings"`
	Metrics  structure.FileMetrics `json:"metrics"`
}

// Analyzer scores blobs through the cache. Prefetch scores a batch of blobs in
// parallel and holds the results until Analyze or AnalyzeText asks for them.
type Analyzer struct {
	memo      map[string]*FileReport
	parser    *lang.Parser
	store     *cache.Store
	cfg       *config.Config
	generated func(path string) bool
	analysis  string
}

// Option configures an Analyzer.
type Option func(*Analyzer)

// WithGenerated skips code paths for which isGenerated reports true, as generated.
func WithGenerated(isGenerated func(path string) bool) Option {
	return func(a *Analyzer) { a.generated = isGenerated }
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
func New(cfg *config.Config, store *cache.Store, opts ...Option) *Analyzer {
	a := &Analyzer{
		memo: map[string]*FileReport{}, store: store, cfg: cfg,
		analysis: analysisKey(cfg),
	}
	for _, opt := range opts {
		opt(a)
	}

	return a
}

// Close releases the parser Analyze reuses between calls.
func (a *Analyzer) Close() {
	if a.parser != nil {
		a.parser.Close()
		a.parser = nil
	}
}

// ReasonGenerated is the skip reason for a file a generator wrote, by its
// .gitattributes mark or a marker in its first lines.
const ReasonGenerated = "generated"

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
	return a.analyzeOne(ctx, Request{Path: path, SHA: sha, Content: content})
}

// AnalyzeText fingerprints a prose or config blob: no parse, only the
// text-level facts the cross-file passes join on.
func (a *Analyzer) AnalyzeText(ctx context.Context, path, sha string, content []byte) *FileReport {
	return a.analyzeOne(ctx, Request{Path: path, SHA: sha, Content: content, Text: true})
}

// analyzeOne answers one request from the hold, the cache, or a parse on the
// parser Analyze reuses.
func (a *Analyzer) analyzeOne(ctx context.Context, r Request) *FileReport {
	if rep, ok := a.take(r.Path, r.SHA); ok {
		return rep
	}
	rep, ok := a.front(r)
	if ok {
		return rep
	}
	class := pathClass(r.Path)
	if payload, ok := a.store.Get(ctx, r.SHA, class, a.analysis); ok && a.apply(rep, payload, r.Text, true) {
		return rep
	}
	reason, j := a.back(rep, r, class)
	if reason != "" {
		if e, ok := verdict(r.SHA, class, reason); ok {
			a.store.Put(ctx, e.SHA, e.Class, a.analysis, e.Payload)
		}

		return rep
	}
	var p *lang.Parser
	if j.lang != nil {
		if a.parser == nil {
			a.parser = lang.NewParser()
		}
		p = a.parser
	}
	if payload := a.score(p, j); payload != nil {
		a.store.Put(ctx, j.sha, j.class, a.analysis, payload)
	}
	a.finish(j)

	return rep
}

// Blob names a blob read at a path.
type Blob struct {
	Path string
	SHA  string
}

// Lookup answers the code blobs whose reports follow from their paths and the
// cache alone, without their content, reading the cache in one transaction. Its
// reports carry no Print, which only the cross-file passes read.
func (a *Analyzer) Lookup(ctx context.Context, blobs []Blob) map[Blob]*FileReport {
	reqs := make([]Request, len(blobs))
	for i, b := range blobs {
		reqs[i] = Request{Path: b.Path, SHA: b.SHA}
	}
	out, pending := a.answerAll(ctx, reqs, false)
	for _, p := range pending {
		delete(out, p.blob)
	}

	return out
}

// Prefetch scores the requests not yet held and holds each report until Analyze
// or AnalyzeText asks for it.
func (a *Analyzer) Prefetch(ctx context.Context, reqs []Request) {
	for b, rep := range a.AnalyzeAll(ctx, reqs) {
		a.memo[memoKey(b.Path, b.SHA)] = rep
	}
}

// AnalyzeAll answers every request's report, parsing the ones the cache cannot
// answer on one worker per CPU. The calling goroutine alone reads and writes the
// cache, one transaction each way, so no lock is needed.
func (a *Analyzer) AnalyzeAll(ctx context.Context, reqs []Request) map[Blob]*FileReport {
	out, pending := a.answerAll(ctx, reqs, true)
	var entries []cache.Entry
	var jobs []*job
	for _, p := range pending {
		reason, j := a.back(out[p.blob], p.req, p.class)
		switch {
		case j != nil:
			jobs = append(jobs, j)
		case reason != "":
			if e, ok := verdict(p.req.SHA, p.class, reason); ok {
				entries = append(entries, e)
			}
		}
	}
	entries = append(entries, a.run(jobs)...)
	a.store.PutMany(ctx, a.analysis, entries)

	return out
}

// pendingReq is a request its path and the cache could not answer.
type pendingReq struct {
	blob  Blob
	class string
	req   Request
}

// answerAll fills a report for every distinct request, answering what the path
// and the cache can and listing the rest, whose reports still need content.
func (a *Analyzer) answerAll(ctx context.Context, reqs []Request, withPrint bool,
) (map[Blob]*FileReport, []pendingReq) {
	out := make(map[Blob]*FileReport, len(reqs))
	var pending []pendingReq
	var keys []cache.Key
	for _, r := range reqs {
		b := Blob{Path: r.Path, SHA: r.SHA}
		if _, seen := out[b]; seen {
			continue
		}
		rep, ok := a.front(r)
		out[b] = rep
		if !ok {
			class := pathClass(r.Path)
			pending = append(pending, pendingReq{blob: b, req: r, class: class})
			keys = append(keys, cache.Key{SHA: r.SHA, Class: class})
		}
	}
	payloads := a.store.GetMany(ctx, a.analysis, keys)
	unanswered := pending[:0]
	for i, p := range pending {
		if payload, ok := payloads[keys[i]]; ok && a.apply(out[p.blob], payload, p.req.Text, withPrint) {
			continue
		}
		unanswered = append(unanswered, p)
	}

	return out, unanswered
}

// run scores jobs on a worker pool and answers the payloads to store.
func (a *Analyzer) run(jobs []*job) []cache.Entry {
	if len(jobs) == 0 {
		return nil
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
	entries := make([]cache.Entry, 0, len(jobs))
	for s := range out {
		if s.payload != nil {
			key := cache.Key{SHA: s.job.sha, Class: s.job.class}
			entries = append(entries, cache.Entry{Key: key, Payload: s.payload})
		}
		a.finish(s.job)
	}

	return entries
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

// front starts a request's report and answers it from the path alone when it
// can: config skips, unsupported code paths, and generated marks.
func (a *Analyzer) front(r Request) (*FileReport, bool) {
	rep := &FileReport{Path: r.Path, SHA: r.SHA}
	if r.Text {
		rep.Lang = lang.TextKind(r.Path)
		for _, glob := range a.cfg.Skip {
			if ok, err := matchGlob(glob, r.Path); err == nil && ok {
				rep.Skipped = "config-skip"

				return rep, true
			}
		}

		return rep, false
	}
	if reason := a.skip(r.Path); reason != "" {
		rep.Skipped = reason

		return rep, true
	}
	if a.generated != nil && a.generated(r.Path) {
		rep.Skipped = ReasonGenerated

		return rep, true
	}

	return rep, false
}

// apply fills a report from a stored payload, a skip verdict or an analysis, and
// reports whether the payload decoded. Without withPrint the fingerprint, most of
// a payload's bytes, is skipped undecoded.
func (a *Analyzer) apply(rep *FileReport, payload []byte, text, withPrint bool) bool {
	var c cached
	var err error
	if withPrint {
		err = json.Unmarshal(payload, &c)
	} else {
		err = json.Unmarshal(payload, (*cachedScores)(&c))
	}
	if err != nil {
		return false
	}
	rep.Skipped = c.Skipped
	rep.Print = c.Print
	if text || c.Skipped != "" {
		return true
	}
	rep.Metrics = c.Metrics
	rep.Findings = c.Findings
	rep.Lang = c.Metrics.Lang
	a.addPathFindings(rep)

	return true
}

// back answers a request from its content: why it is skipped, setting the
// report's reason, or the job that scores it.
func (a *Analyzer) back(rep *FileReport, r Request, class string) (string, *job) {
	reason := a.contentSkip(r.Content, r.Text)
	if reason != "" {
		rep.Skipped = reason

		return reason, nil
	}
	j := &job{rep: rep, sha: r.SHA, class: class, content: r.Content}
	if !r.Text {
		j.lang = lang.ByPath(r.Path)
	}

	return "", j
}

// contentSkip names why a blob's content is not scored, or "" when it is. Prose
// and config are only ever skipped for size or binary bytes.
func (a *Analyzer) contentSkip(content []byte, text bool) string {
	switch {
	case int64(len(content)) > a.cfg.MaxFileBytes:
		return "oversize"
	case bytes.IndexByte(content, 0) >= 0:
		return "binary"
	case text:
		return ""
	case minified(content, a.cfg.MinifiedAvgLine):
		return "minified"
	case generatedMarker(content):
		return ReasonGenerated
	default:
		return ""
	}
}

// verdict is the entry that stores why a blob's content was skipped, so a later
// run skips it without reading it.
func verdict(sha, class, reason string) (cache.Entry, bool) {
	payload, err := json.Marshal(cached{Skipped: reason})
	if err != nil {
		return cache.Entry{}, false
	}

	return cache.Entry{Key: cache.Key{SHA: sha, Class: class}, Payload: payload}, true
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

// finish adds the findings that depend on the file name, which a stored
// payload never holds, to a scored code job's report.
func (a *Analyzer) finish(j *job) {
	if j.lang != nil {
		a.addPathFindings(j.rep)
	}
}

// addPathFindings appends the findings that depend on the file name.
func (a *Analyzer) addPathFindings(rep *FileReport) {
	test := lang.IsTestPath(rep.Path)
	rep.Findings = append(rep.Findings, rules.PathFindings(rep.Path, test, a.cfg.Experimental)...)
}

// markerLines is how far down a file a generated-code marker may sit.
const markerLines = 5

// generatedMarker reports whether a file's first lines carry a code generator's
// marker, such as "@generated" or "Code generated ... DO NOT EDIT".
func generatedMarker(content []byte) bool {
	head := bytes.SplitN(content, []byte{'\n'}, markerLines+1)
	if len(head) > markerLines {
		head = head[:markerLines]
	}
	for _, line := range head {
		if bytes.Contains(line, []byte("@generated")) || bytes.Contains(line, []byte("DO NOT EDIT")) {
			return true
		}
	}

	return false
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

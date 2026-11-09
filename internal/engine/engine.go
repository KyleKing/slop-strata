// Package engine ties parsing, rules, and the blob cache together: a file in
// (path, sha, content) becomes a scored report out, with every blob computed
// once ever.
package engine

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/kyleking/slop-strata/internal/cache"
	"github.com/kyleking/slop-strata/internal/config"
	"github.com/kyleking/slop-strata/internal/lang"
	"github.com/kyleking/slop-strata/internal/rules"
	"github.com/kyleking/slop-strata/internal/structure"
)

// FileReport is one blob's analysis, or why it was skipped.
type FileReport struct {
	Path     string                `json:"path"`
	SHA      string                `json:"sha"`
	Lang     string                `json:"lang,omitempty"`
	Skipped  string                `json:"skipped,omitempty"`
	Metrics  structure.FileMetrics `json:"metrics,omitempty"`
	Findings []rules.Finding       `json:"findings,omitempty"`
	Print    *rules.Fingerprint    `json:"-"`
}

// cached is the stored payload per blob.
type cached struct {
	Metrics  structure.FileMetrics `json:"metrics"`
	Findings []rules.Finding       `json:"findings"`
	Print    *rules.Fingerprint    `json:"print"`
}

// Analyzer scores blobs through the cache.
type Analyzer struct {
	pool  *lang.Pool
	store *cache.Store
	cfg   *config.Config
}

// New builds an Analyzer. store may be a no-op store.
func New(cfg *config.Config, store *cache.Store) *Analyzer {
	return &Analyzer{pool: lang.NewPool(), store: store, cfg: cfg}
}

// Skip reports whether a path is excluded by config or unsupported.
func (a *Analyzer) skip(path string) string {
	for _, glob := range a.cfg.Skip {
		if ok, err := matchGlob(glob, path); err == nil && ok {
			return "config-skip"
		}
	}
	if lang.ByPath(path) == nil {
		return "unsupported"
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
	if ok, err := filepath.Match(glob, path); err != nil || ok {
		return ok, err
	}

	return filepath.Match(glob, filepath.Base(path))
}

// Analyze scores one blob. Unsupported paths and config skips return a
// skipped report; oversize and minified blobs likewise, each with its reason.
func (a *Analyzer) Analyze(path, sha string, content []byte) *FileReport {
	rep := &FileReport{Path: path, SHA: sha}
	if reason := a.skip(path); reason != "" {
		rep.Skipped = reason

		return rep
	}
	l := lang.ByPath(path)
	if int64(len(content)) > a.cfg.MaxFileBytes {
		rep.Skipped = "oversize"

		return rep
	}
	if bytes.IndexByte(content, 0) >= 0 {
		rep.Skipped = "binary"

		return rep
	}
	if minified(content, a.cfg.MinifiedAvgLine) {
		rep.Skipped = "minified"

		return rep
	}

	if payload, ok := a.store.Get(sha, lang.GrammarVersion, rules.Version); ok {
		var c cached
		if err := json.Unmarshal(payload, &c); err == nil {
			rep.Metrics = c.Metrics
			rep.Findings = c.Findings
			rep.Print = c.Print
			rep.Lang = c.Metrics.Lang

			return rep
		}
	}

	rep.Lang = l.ID
	tree := a.pool.Parse(l, content)
	defer tree.Close()

	rep.Metrics = structure.Analyze(l, tree, content)
	ctx := &rules.Ctx{Lang: l, Root: tree.RootNode(), Src: content, Path: path, Test: l.IsTestFile(path)}
	rep.Findings = rules.Run(ctx)
	rep.Print = rules.Collect(ctx)

	if payload, err := json.Marshal(cached{Metrics: rep.Metrics, Findings: rep.Findings, Print: rep.Print}); err == nil {
		a.store.Put(sha, lang.GrammarVersion, rules.Version, payload)
	}

	return rep
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
// findings into each file's report.
func CrossFile(reports []*FileReport) {
	var blobs []rules.BlobPrint
	for _, r := range reports {
		if r.Print != nil {
			blobs = append(blobs, rules.BlobPrint{Path: r.Path, Print: r.Print})
		}
	}
	for path, findings := range rules.CrossFile(blobs) {
		for _, r := range reports {
			if r.Path == path {
				r.Findings = append(r.Findings, findings...)
			}
		}
	}
}

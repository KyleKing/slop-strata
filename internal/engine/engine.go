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
	"strings"

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

// Analyzer scores blobs through the cache.
type Analyzer struct {
	pool  *lang.Pool
	store *cache.Store
	cfg   *config.Config
}

// New builds an Analyzer. Store may be a no-op store.
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

	if payload, ok := a.store.Get(ctx, sha, lang.GrammarVersion, rules.Version); ok {
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
	rctx := &rules.Ctx{Lang: l, Root: tree.RootNode(), Src: content, Path: path, Test: l.IsTestFile(path)}
	rep.Findings = rules.Run(rctx)
	rep.Print = rules.Collect(rctx)

	payload, err := json.Marshal(cached{Metrics: rep.Metrics, Findings: rep.Findings, Print: rep.Print})
	if err == nil {
		a.store.Put(ctx, sha, lang.GrammarVersion, rules.Version, payload)
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
func CrossFile(reports []*FileReport, cfg *config.Config) {
	var blobs []rules.BlobPrint
	for _, r := range reports {
		if r.Print != nil {
			blobs = append(blobs, rules.BlobPrint{Path: r.Path, Print: r.Print})
		}
	}
	for path, findings := range rules.CrossFile(blobs, cfg.EntryPoints) {
		for _, r := range reports {
			if r.Path == path {
				r.Findings = append(r.Findings, findings...)
			}
		}
	}
}

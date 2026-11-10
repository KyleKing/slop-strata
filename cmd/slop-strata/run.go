package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/kyleking/aragonite/vcs"

	"github.com/kyleking/slop-strata/internal/cache"
	"github.com/kyleking/slop-strata/internal/config"
	"github.com/kyleking/slop-strata/internal/engine"
	"github.com/kyleking/slop-strata/internal/gitattr"
	"github.com/kyleking/slop-strata/internal/report"
)

// errUsage marks a bad command line.
var errUsage = errors.New("usage error")

// errNotMetric marks an unknown --explain name.
var errNotMetric = errors.New("no such metric")

// errNotRepo marks a path that is not a repository.
var errNotRepo = errors.New("not a repository")

// errUnknownCmd marks a name that is not a subcommand.
var errUnknownCmd = errors.New("unknown subcommand")

// Table column headers shared across subcommand output.
const (
	colDir   = "dir"
	colAdded = "added"
)

// session bundles what every subcommand needs.
type session struct {
	ops    *vcs.GitOperations
	cfg    *config.Config
	eng    *engine.Analyzer
	store  *cache.Store
	stderr io.Writer
	root   string
	trees  []memoTree
}

// countRows tallies skipped and errored files for an envelope. A row whose
// reason is an unsupported file type is listed but not counted.
func countRows(skipped, errs []skippedRow) report.Counts {
	c := report.Counts{Errors: len(errs)}
	for _, sk := range skipped {
		if sk.Reason != engine.ReasonUnsupported {
			c.Skipped++
		}
	}

	return c
}

func newSession(ctx context.Context, repoPath string, experimental bool, stderr io.Writer) (*session, error) {
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("resolving repo path: %w", err)
	}
	if !vcs.IsRepo(abs) {
		return nil, fmt.Errorf("%s: %w", abs, errNotRepo)
	}
	cfg, err := config.Load(abs)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	cfg.Experimental = cfg.Experimental || experimental
	attrs, err := gitattr.Load(abs)
	if err != nil {
		return nil, fmt.Errorf("loading attributes: %w", err)
	}
	cachePath, err := cache.Path(version)
	if err != nil {
		cachePath = ""
	}
	store, err := cache.Open(ctx, cachePath)
	if err != nil {
		return nil, fmt.Errorf("opening cache: %w", err)
	}

	return &session{
		ops: vcs.NewGitOperations(), cfg: cfg,
		eng: engine.New(cfg, store, engine.WithGenerated(attrs.Generated)), store: store, stderr: stderr, root: abs,
	}, nil
}

func (s *session) close() {
	s.eng.Close()
	if s.store == nil {
		return
	}
	// A failed close loses nothing; the cache only ever held memos.
	if err := s.store.Close(); err != nil {
		return
	}
}

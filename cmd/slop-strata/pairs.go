package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kyleking/slop-strata/internal/report"
)

// errPairs marks a batch in which at least one pair failed; every pair still
// wrote its envelope.
var errPairs = errors.New("pairs failed")

// stdinPath names standard input as a --pairs source.
const stdinPath = "-"

// pair is one "<base> <rev>" line, labeled by its text. A line that does not
// parse carries its error and is reported in place, so the batch keeps its order.
type pair struct {
	err   error
	label string
	base  string
	rev   string
}

// changePairs scores each pair of --pairs and writes one envelope per pair as
// JSON Lines. A failed pair writes an envelope with its error and the batch goes
// on; the command then exits non-zero.
func (s *session) changePairs(ctx context.Context, a *args, w io.Writer) error {
	if !a.json {
		return fmt.Errorf("--pairs writes JSON Lines and needs --json: %w", errUsage)
	}
	if a.rev != "" || a.base != "" {
		return fmt.Errorf("--pairs takes its revisions from the file, not --base or --rev: %w", errUsage)
	}
	pairs, err := readPairs(a.pairs)
	if err != nil {
		return err
	}
	failed := 0
	for _, p := range pairs {
		env, ok := s.pairEnvelope(ctx, p, a.ctx)
		if !ok {
			failed++
		}
		if err := report.Encode(w, env, false); err != nil {
			return fmt.Errorf("writing result: %w", err)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d pairs failed: %w", failed, len(pairs), errPairs)
	}

	return nil
}

// pairEnvelope scores one pair into the envelope it writes, and reports whether
// it scored. A failed pair carries its error in place of a result.
func (s *session) pairEnvelope(ctx context.Context, p pair, withContext bool) (report.Envelope, bool) {
	if p.err == nil {
		res, err := s.scorePair(ctx, p, withContext)
		if err == nil {
			return report.NewEnvelope("change", s.root, res, countRows(res.Skipped, res.Errors), warnings(res)), true
		}
		p.err = err
	}
	env := report.NewEnvelope("change", s.root, nil, report.Counts{}, nil)
	env.Error = fmt.Sprintf("%s: %v", p.label, p.err)

	return env, false
}

// scorePair scores the change from a pair's base to its rev, with the context
// block when withContext is set.
func (s *session) scorePair(ctx context.Context, p pair, withContext bool) (*changeResult, error) {
	base, err := s.changeBase(ctx, p.base, p.rev)
	if err != nil {
		return nil, err
	}
	res, files, err := s.scoreCommits(ctx, base, p.rev)
	if err != nil {
		return nil, err
	}
	if withContext {
		res.Context = s.buildContext(ctx, base, p.rev, files, res)
	}

	return res, nil
}

// readPairs reads the pairs file, or standard input for "-". Blank lines and
// lines starting with # are skipped.
func readPairs(path string) ([]pair, error) {
	src := io.Reader(os.Stdin)
	if path != stdinPath {
		data, err := os.ReadFile(path) //nolint:gosec // the path is the user's own --pairs argument
		if err != nil {
			return nil, fmt.Errorf("reading pairs: %w", err)
		}
		src = bytes.NewReader(data)
	}
	var pairs []pair
	sc := bufio.NewScanner(src)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 { //nolint:mnd // a pair is exactly two revisions
			err := fmt.Errorf("want \"<base> <rev>\": %w", errUsage)
			pairs = append(pairs, pair{err: err, label: line})

			continue
		}
		pairs = append(pairs, pair{label: line, base: fields[0], rev: fields[1]})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading pairs: %w", err)
	}

	return pairs, nil
}

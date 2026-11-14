package rules

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
)

// baselineVersion is the on-disk format: version 2 keys findings by ID.
const baselineVersion = 2

// ErrBaselineFormat marks a baseline file written by an older format.
var ErrBaselineFormat = errors.New("baseline format is out of date")

// BaselineFile is the on-disk shape of the baseline: a JSON list of accepted
// findings.
type BaselineFile struct {
	Entries []BaselineEntry `json:"entries"`
	Version int             `json:"version"`
}

// BaselineEntry is one accepted finding. ID is the key and the rest says what
// it was, for the person reading the file.
type BaselineEntry struct {
	ID    string `json:"id"`
	Path  string `json:"path"`
	Rule  string `json:"rule"`
	Scope string `json:"scope,omitempty"`
	Match string `json:"match,omitempty"`
}

// Baseline is the set of finding IDs a repository already accepted.
type Baseline map[string]bool

// baselinePerm is the mode for the baseline file: owner-only.
const baselinePerm = 0o600

// ReadBaseline loads a baseline file; a missing file is an empty baseline. A
// file in an older format is an error, since reading it as empty would report
// every accepted finding as new.
func ReadBaseline(path string) (Baseline, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the path is the repo's own config'd baseline file
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Baseline{}, nil
		}

		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var bf BaselineFile
	if err := json.Unmarshal(data, &bf); err != nil {
		return nil, fmt.Errorf("parsing baseline %s: %w", path, err)
	}
	if bf.Version != baselineVersion {
		return nil, fmt.Errorf("%w: %s is version %d and this build reads version %d, "+
			"so run `slop-strata snapshot --write-baseline` to rewrite it",
			ErrBaselineFormat, path, bf.Version, baselineVersion)
	}
	b := Baseline{}
	for _, e := range bf.Entries {
		b[e.ID] = true
	}

	return b, nil
}

// WriteBaseline renders findings, which must carry IDs, as a new baseline file.
func WriteBaseline(path string, files map[string][]Finding) error {
	bf := BaselineFile{Version: baselineVersion, Entries: Entries(files)}
	data, err := json.MarshalIndent(bf, "", "  ")
	if err != nil {
		return fmt.Errorf("rendering baseline: %w", err)
	}

	if err := os.WriteFile(path, append(data, '\n'), baselinePerm); err != nil {
		return fmt.Errorf("writing baseline: %w", err)
	}

	return nil
}

// Entries renders a finding set into baseline entries ordered by path, rule,
// then ID, so rewriting an unchanged baseline changes no byte.
func Entries(files map[string][]Finding) []BaselineEntry {
	out := []BaselineEntry{}
	for path, found := range files {
		for _, f := range found {
			out = append(out, BaselineEntry{ID: f.ID, Path: path, Rule: f.Rule, Scope: f.Scope, Match: f.Match})
		}
	}
	slices.SortFunc(out, func(a, b BaselineEntry) int {
		return strings.Compare(a.Path+"\x00"+a.Rule+"\x00"+a.ID, b.Path+"\x00"+b.Rule+"\x00"+b.ID)
	})

	return out
}

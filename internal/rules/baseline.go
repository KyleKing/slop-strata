package rules

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// baselineVersion is the on-disk format this build writes. Version 2 keys
// findings by ID. Version 3 adds members to the entries of set-shaped findings
// and is otherwise the same, so a version 2 file still loads.
const baselineVersion = 3

// oldestBaseline is the oldest format ReadBaseline accepts.
const oldestBaseline = 2

// ErrBaselineFormat marks a baseline file in a format this build cannot read.
var ErrBaselineFormat = errors.New("baseline format is out of date")

// BaselineFile is the on-disk shape of the baseline: a JSON list of accepted
// findings.
type BaselineFile struct {
	Entries []BaselineEntry `json:"entries"`
	Version int             `json:"version"`
}

// BaselineEntry is one accepted finding. ID is the key and the rest says what
// it was, for the person reading the file. Members, on a set-shaped finding
// such as an import cycle, also widen the entry to every subset of them.
type BaselineEntry struct {
	ID      string   `json:"id"`
	Path    string   `json:"path"`
	Rule    string   `json:"rule"`
	Scope   string   `json:"scope,omitempty"`
	Match   string   `json:"match,omitempty"`
	Members []string `json:"members,omitempty"`
}

// Baseline is the findings a repository already accepted. Its zero value is empty.
type Baseline struct {
	ids     map[string]bool
	entries []BaselineEntry
}

// Covers reports whether the baseline accepts a finding: by its ID, or, for a
// set-shaped finding, because an accepted entry of the same rule holds every
// one of its members. A cycle that shrank is therefore still accepted, and
// one that grew or is new is not.
func (b Baseline) Covers(f *Finding) bool {
	if b.ids[f.ID] {
		return true
	}
	if len(f.Members) == 0 {
		return false
	}
	for _, e := range b.entries {
		if e.Rule == f.Rule && len(e.Members) > 0 && subset(f.Members, e.Members) {
			return true
		}
	}

	return false
}

// baselinePerm is the mode for the baseline file: owner-only.
const baselinePerm = 0o600

// ReadBaseline loads a baseline file; a missing file is empty. A format older
// than version 2, or newer than this build writes, is an error, so accepted
// findings never read as new.
func ReadBaseline(path string) (Baseline, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the path is the repo's own config'd baseline file
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Baseline{}, nil
		}

		return Baseline{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var bf BaselineFile
	if err := json.Unmarshal(data, &bf); err != nil {
		return Baseline{}, fmt.Errorf("parsing baseline %s: %w", path, err)
	}
	if bf.Version < oldestBaseline || bf.Version > baselineVersion {
		return Baseline{}, fmt.Errorf("%w: %s is version %d and this build reads versions %d to %d, "+
			"so run `slop-strata snapshot --write-baseline` to rewrite it",
			ErrBaselineFormat, path, bf.Version, oldestBaseline, baselineVersion)
	}
	b := Baseline{ids: map[string]bool{}, entries: bf.Entries}
	for i := range bf.Entries {
		slices.Sort(bf.Entries[i].Members)
		b.ids[bf.Entries[i].ID] = true
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

	if err := writeFileAtomic(path, append(data, '\n'), baselinePerm); err != nil {
		return fmt.Errorf("writing baseline: %w", err)
	}

	return nil
}

// writeFileAtomic writes through a temp file in the same directory and renames
// it over path, so a failed write leaves the old file intact.
func writeFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.Remove(tmp.Name()))
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return errors.Join(fmt.Errorf("writing %s: %w", tmp.Name(), err), tmp.Close())
	}
	if err = tmp.Chmod(perm); err != nil {
		return errors.Join(fmt.Errorf("setting mode of %s: %w", tmp.Name(), err), tmp.Close())
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmp.Name(), err)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}

	return nil
}

// Entries renders a finding set into baseline entries ordered by path, rule,
// then ID, so rewriting an unchanged baseline changes no byte.
func Entries(files map[string][]Finding) []BaselineEntry {
	out := []BaselineEntry{}
	for path, found := range files {
		for i := range found {
			f := &found[i]
			out = append(out, BaselineEntry{
				ID: f.ID, Path: path, Rule: f.Rule, Scope: f.Scope, Match: f.Match, Members: f.Members,
			})
		}
	}
	slices.SortFunc(out, func(a, b BaselineEntry) int {
		return strings.Compare(a.Path+"\x00"+a.Rule+"\x00"+a.ID, b.Path+"\x00"+b.Rule+"\x00"+b.ID)
	})

	return out
}

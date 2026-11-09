package rules

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// BaselineFile is the on-disk shape of the baseline: a JSON list of accepted
// findings.
type BaselineFile struct {
	Version int             `json:"version"`
	Entries []BaselineEntry `json:"entries"`
}

// ReadBaseline loads a baseline file; a missing file is an empty baseline.
func ReadBaseline(path string) (Baseline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Baseline{}, nil
		}

		return nil, fmt.Errorf("reading baseline: %w", err)
	}
	var bf BaselineFile
	if err := json.Unmarshal(data, &bf); err != nil {
		return nil, fmt.Errorf("parsing baseline %s: %w", path, err)
	}

	return LoadBaseline(bf.Entries), nil
}

// WriteBaseline renders the finding set as a new baseline file.
func WriteBaseline(path string, files map[string][]Finding) error {
	bf := BaselineFile{Version: 1, Entries: Entries(files)}
	data, err := json.MarshalIndent(bf, "", "  ")
	if err != nil {
		return fmt.Errorf("rendering baseline: %w", err)
	}

	return os.WriteFile(path, append(data, '\n'), 0o600)
}

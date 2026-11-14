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

// labelsVersion is the on-disk format of the labels file.
const labelsVersion = 1

var errLabelsFormat = errors.New("labels format is not supported")

// Verdicts a person gives a finding.
const (
	VerdictFalsePositive = "fp"
	VerdictTruePositive  = "tp"
)

// ProvisionalLabels is the label count under which a rule's precision is a
// thin sample.
const ProvisionalLabels = 10

// Label is one person's verdict on one finding. It records what the finding
// was, so the verdict stays readable after the code around it changes.
type Label struct {
	ID          string `json:"id"`
	Rule        string `json:"rule"`
	Verdict     string `json:"verdict"`
	Path        string `json:"path"`
	Match       string `json:"match,omitempty"`
	Note        string `json:"note,omitempty"`
	ToolVersion string `json:"tool_version"`
}

// Labels holds the verdicts of a repository by finding ID.
type Labels map[string]Label

// LabelsFile is the on-disk shape of the labels file.
type LabelsFile struct {
	Labels  []Label `json:"labels"`
	Version int     `json:"version"`
}

// labelsPerm is the mode for the labels file: owner-only.
const labelsPerm = 0o600

// ReadLabels loads a labels file; a missing file holds no labels.
func ReadLabels(path string) (Labels, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the path is the repo's own config'd labels file
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Labels{}, nil
		}

		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var lf LabelsFile
	if err := json.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("parsing labels %s: %w", path, err)
	}
	if lf.Version != labelsVersion {
		return nil, fmt.Errorf("%w: %s is version %d and this build reads version %d",
			errLabelsFormat, path, lf.Version, labelsVersion)
	}
	labels := Labels{}
	for _, l := range lf.Labels {
		labels[l.ID] = l
	}

	return labels, nil
}

// WriteLabels renders the labels sorted by rule, path, then ID, so adding one
// label changes one block of the file.
func WriteLabels(path string, labels Labels) error {
	data, err := json.MarshalIndent(LabelsFile{Version: labelsVersion, Labels: labels.Sorted()}, "", "  ")
	if err != nil {
		return fmt.Errorf("rendering labels: %w", err)
	}
	if err := writeFileAtomic(path, append(data, '\n'), labelsPerm); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}

// Sorted lists the labels by rule, path, then ID.
func (l Labels) Sorted() []Label {
	out := make([]Label, 0, len(l))
	for _, label := range l {
		out = append(out, label)
	}
	slices.SortFunc(out, func(a, b Label) int {
		return strings.Compare(a.Rule+"\x00"+a.Path+"\x00"+a.ID, b.Rule+"\x00"+b.Path+"\x00"+b.ID)
	})

	return out
}

// IsFalsePositive reports whether a finding ID carries an fp verdict.
func (l Labels) IsFalsePositive(id string) bool {
	return l[id].Verdict == VerdictFalsePositive
}

// Precision tallies one rule's verdicts.
type Precision struct {
	Rule string
	TP   int
	FP   int
}

// N is how many findings of the rule carry a verdict.
func (p Precision) N() int { return p.TP + p.FP }

// Rate is the share of labeled findings that were real, false when none is labeled.
func (p Precision) Rate() (float64, bool) {
	if p.N() == 0 {
		return 0, false
	}

	return float64(p.TP) / float64(p.N()), true
}

// Provisional reports whether the sample is under ProvisionalLabels.
func (p Precision) Provisional() bool { return p.N() < ProvisionalLabels }

// Precision tallies the verdicts per rule.
func (l Labels) Precision() map[string]Precision {
	out := map[string]Precision{}
	for _, label := range l {
		p := out[label.Rule]
		p.Rule = label.Rule
		if label.Verdict == VerdictTruePositive {
			p.TP++
		} else {
			p.FP++
		}
		out[label.Rule] = p
	}

	return out
}

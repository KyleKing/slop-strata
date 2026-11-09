package rules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
)

// sgMatch is one ast-grep JSON match with --include-metadata.
type sgMatch struct {
	RuleID  string `json:"ruleId"`
	File    string `json:"file"`
	Text    string `json:"text"`
	Message string `json:"message"`
	Range   struct {
		Start struct {
			Line int `json:"line"`
		} `json:"start"`
	} `json:"range"`
}

// ErrNoSg reports a configured ast-grep pack with no sg binary on PATH.
var ErrNoSg = errors.New("ast_grep configured but sg is not on PATH")

// RunAstGrep scans the working tree with each configured ast-grep YAML pack
// and maps matches to findings keyed by repo-relative path. The pack set is
// the optional external rule source; sg scans the worktree, so callers use
// this only when the scored revision is the checked-out one.
func RunAstGrep(ctx context.Context, root string, packs []string) (map[string][]Finding, error) {
	if len(packs) == 0 {
		return nil, nil
	}
	if _, err := exec.LookPath("sg"); err != nil {
		return nil, ErrNoSg
	}
	out := map[string][]Finding{}
	for _, pack := range packs {
		matches, err := sgScan(ctx, root, pack)
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			// sg emits the file relative to the scanned root; normalize anyway
			// in case a version answers absolute paths.
			rel := m.File
			if filepath.IsAbs(rel) {
				if r, err := filepath.Rel(root, rel); err == nil {
					rel = r
				}
			}
			out[rel] = append(out[rel], Finding{
				Rule:  "sg/" + m.RuleID,
				Line:  m.Range.Start.Line + 1,
				Match: m.Text,
				Note:  m.Message,
			})
		}
	}

	return out, nil
}

// sgScan runs one pack over the tree and parses its compact JSON matches.
func sgScan(ctx context.Context, root, pack string) ([]sgMatch, error) {
	rule := pack
	if !filepath.IsAbs(rule) {
		rule = filepath.Join(root, rule)
	}
	cmd := exec.CommandContext(ctx, "sg", "scan", "--rule", rule,
		"--json=compact", "--include-metadata", root) // #nosec G204 -- argv is configured, not user input
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("sg scan %s: %w", pack, err)
	}
	var matches []sgMatch
	if err := json.Unmarshal(raw, &matches); err != nil {
		return nil, fmt.Errorf("parsing sg output for %s: %w", pack, err)
	}

	return matches, nil
}

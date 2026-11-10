package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kyleking/aragonite/vcs"

	"github.com/kyleking/slop-strata/internal/config"
	"github.com/kyleking/slop-strata/internal/report"
)

// initScanCap bounds the history walk init uses to detect bot authors.
const initScanCap = 500

// botMarker names the login suffix GitHub gives app accounts.
const botMarker = "[bot]"

// configPerm is the mode init writes .slop-strata.toml with: owner-only.
const configPerm = 0o600

// initCmd writes a starter .slop-strata.toml when none exists — bot author
// classes detected from recent history — and records the current findings as
// the baseline. An existing config is never overwritten.
func (s *session) initCmd(ctx context.Context, a *args, w io.Writer) error {
	path := filepath.Join(s.root, config.FileName)
	if _, err := os.Stat(path); err != nil {
		if err := s.writeConfig(ctx, path); err != nil {
			return err
		}
		report.Fprintf(w, "wrote %s\n", config.FileName)
	}
	sub := *a
	sub.json, sub.writeBaseline = false, true

	return s.snapshotCmd(ctx, &sub, w)
}

// writeConfig renders .slop-strata.toml with the bot logins seen in recent
// first-parent history plus the default co-author trailer class.
func (s *session) writeConfig(ctx context.Context, path string) error {
	logins := map[string]bool{}
	n := 0
	for rec, err := range s.ops.LogHistory(ctx, s.root, vcs.LogOptions{FirstParent: true}) {
		if err != nil || n >= initScanCap {
			break
		}
		n++
		for _, id := range []string{rec.AuthorName, rec.AuthorEmail, rec.CommitterName, rec.CommitterEmail} {
			if strings.Contains(id, botMarker) {
				logins[id] = true
			}
		}
	}
	var b strings.Builder
	// Top-level keys must precede [[author_class]] or TOML attaches them to the
	// last table element.
	b.WriteString("# slop-strata configuration\n\n# skip = [\"vendor/*\", \"*.min.js\"]\n\n[[author_class]]\nname = \"bot\"\n")
	if len(logins) > 0 {
		sorted := make([]string, 0, len(logins))
		for l := range logins {
			sorted = append(sorted, l)
		}
		sort.Strings(sorted)
		quoted := make([]string, 0, len(sorted))
		for _, l := range sorted {
			quoted = append(quoted, fmt.Sprintf("%q", l))
		}
		fmt.Fprintf(&b, "logins = [%s]\n", strings.Join(quoted, ", "))
	}
	b.WriteString("trailers = ['(?i)co-authored-by:.*\\[bot\\]']\n")

	if err := os.WriteFile(path, []byte(b.String()), configPerm); err != nil {
		return fmt.Errorf("writing %s: %w", config.FileName, err)
	}

	return nil
}

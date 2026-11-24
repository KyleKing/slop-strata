package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/kyleking/slop-strata/internal/report"
	"github.com/kyleking/slop-strata/internal/rules"
)

// checkLabelArgs rejects a label invocation that names neither a verdict nor --list.
func checkLabelArgs(a *args) error {
	if a.list {
		if len(a.positional) > 0 || a.note != "" {
			return fmt.Errorf("%s takes no finding id, verdict, or %s: %w", flagList, flagNote, errUsage)
		}

		return nil
	}
	if len(a.positional) != 2 { //nolint:mnd // a finding id and a verdict
		return fmt.Errorf("label needs a finding id and a verdict (tp or fp), or %s: %w", flagList, errUsage)
	}
	if v := a.positional[1]; v != rules.VerdictTruePositive && v != rules.VerdictFalsePositive {
		return fmt.Errorf("verdict %q is not tp or fp: %w", v, errUsage)
	}

	return nil
}

// labelCmd runs every rule, experimental ones included, because the labels exist to measure them.
func (s *session) labelCmd(ctx context.Context, a *args, w io.Writer) error {
	labels, err := s.readLabels()
	if err != nil {
		return err
	}
	if a.list {
		printLabels(w, labels)

		return nil
	}
	id, verdict := a.positional[0], a.positional[1]
	rev := cmp.Or(a.rev, defaultRev)
	path, found, err := s.findingByID(ctx, rev, id)
	if err != nil {
		return err
	}
	labels[id] = rules.Label{
		ID: id, Rule: found.Rule, Verdict: verdict, Path: path, Match: found.Match, Note: a.note, ToolVersion: version,
	}
	if err := rules.WriteLabels(filepath.Join(s.root, s.cfg.Labels), labels); err != nil {
		return fmt.Errorf("saving the verdict: %w", err)
	}
	report.Fprintf(w, "labeled %s %s: %s in %s\n", id, verdict, found.Rule, path)

	return nil
}

// findingByID finds the finding of the tree at rev that carries id.
func (s *session) findingByID(ctx context.Context, rev, id string) (string, rules.Finding, error) {
	entries, err := s.ops.TreeEntries(ctx, s.root, rev)
	if err != nil {
		return "", rules.Finding{}, fmt.Errorf("listing tree at %s: %w", rev, err)
	}
	reports, err := s.treeReports(ctx, entries)
	if err != nil {
		return "", rules.Finding{}, err
	}
	for _, r := range reports {
		found := rules.Identify(r.Path, r.Findings)
		for i := range found {
			if found[i].ID == id {
				return r.Path, found[i], nil
			}
		}
	}

	return "", rules.Finding{}, fmt.Errorf("%s at %s: %w (snapshot --json lists the ids, "+
		"and an x/last-ref or x/new-duplicate id that only change reports cannot be labeled)", id, rev, errNoFinding)
}

// printLabels lists the verdicts, one per row.
func printLabels(w io.Writer, labels rules.Labels) {
	if len(labels) == 0 {
		report.Fprintln(w, "no labels")

		return
	}
	rows := make([][]string, 0, len(labels))
	for _, l := range labels.Sorted() {
		rows = append(rows, []string{l.ID, l.Verdict, l.Rule, l.Path, l.Match, l.Note})
	}
	report.Table(w, []string{"id", "verdict", colRule, "path", "match", "note"}, rows)
}

package main

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/kyleking/slop-strata/internal/config"
	"github.com/kyleking/slop-strata/internal/report"
	"github.com/kyleking/slop-strata/internal/rules"
)

// Tiers a rule runs in.
const (
	tierDefault      = "default"
	tierExperimental = "experimental"
	tierExternal     = "external"
)

// rulesResult is the rules subcommand's JSON shape.
type rulesResult struct {
	Labels string    `json:"labels"`
	Rules  []ruleRow `json:"rules"`
}

// ruleRow is one rule with the verdicts recorded for it.
type ruleRow struct {
	Precision   *float64 `json:"precision"`
	Rule        string   `json:"rule"`
	Tier        string   `json:"tier"`
	TP          int      `json:"tp"`
	FP          int      `json:"fp"`
	N           int      `json:"n"`
	Provisional bool     `json:"provisional"`
}

// rulesCmd lists every rule with its tier and the precision the labels file
// records for it.
func (s *session) rulesCmd(a *args, w io.Writer) error {
	labels, err := s.readLabels()
	if err != nil {
		return err
	}
	res := rulesResult{Labels: s.cfg.Labels, Rules: ruleRows(labels)}
	if a.json {
		if err := report.WriteJSON(w, cmdRules, s.root, res, report.Counts{}, nil); err != nil {
			return fmt.Errorf("writing result: %w", err)
		}

		return nil
	}
	printRuleRows(w, res.Rules)

	return nil
}

// explainRules prints the rule table under --explain experimental, from the
// labels file of the repository in the current directory.
func explainRules(w io.Writer) error {
	cfg, err := config.Load(".")
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	labels, err := rules.ReadLabels(filepath.Join(".", cfg.Labels))
	if err != nil {
		return fmt.Errorf("loading labels: %w", err)
	}
	report.Fprintln(w)
	printRuleRows(w, ruleRows(labels))

	return nil
}

// ruleRows joins the rule catalog with the labeled precision, adding rules that
// only the labels file knows, such as ast-grep rules.
func ruleRows(labels rules.Labels) []ruleRow {
	precision := labels.Precision()
	var rows []ruleRow
	known := map[string]bool{}
	for _, info := range rules.Catalog() {
		known[info.ID] = true
		tier := tierDefault
		if info.Experimental {
			tier = tierExperimental
		}
		rows = append(rows, newRuleRow(info.ID, tier, precision[info.ID]))
	}
	for id, p := range precision {
		if !known[id] {
			rows = append(rows, newRuleRow(id, tierExternal, p))
		}
	}
	slices.SortFunc(rows, func(a, b ruleRow) int { return strings.Compare(a.Rule, b.Rule) })

	return rows
}

func newRuleRow(id, tier string, p rules.Precision) ruleRow {
	row := ruleRow{Rule: id, Tier: tier, TP: p.TP, FP: p.FP, N: p.N(), Provisional: p.Provisional()}
	if rate, ok := p.Rate(); ok {
		row.Precision = &rate
	}

	return row
}

// printRuleRows renders the rule table, marking a precision read from fewer
// than rules.ProvisionalLabels labels.
func printRuleRows(w io.Writer, rows []ruleRow) {
	table := make([][]string, 0, len(rows))
	thin := false
	for _, r := range rows {
		cell := "-"
		if r.Precision != nil {
			cell = provisionalMarked(fmt.Sprintf("%.0f%%", *r.Precision*percentScale), r.Provisional) +
				fmt.Sprintf(" (n=%d)", r.N)
			thin = thin || r.Provisional
		}
		table = append(table, []string{r.Rule, r.Tier, strconv.Itoa(r.TP), strconv.Itoa(r.FP), cell})
	}
	report.Table(w, []string{colRule, "tier", "tp", "fp", "precision"}, table)
	if thin {
		report.Fprintf(w, "%s fewer than %d labels\n", provisionalMark, rules.ProvisionalLabels)
	}
}

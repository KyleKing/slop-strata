// Package report renders results: JSON on --json, and the human table forms
// otherwise. Every metric carries its own doc, printed beside the number and
// answered by --explain.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Version is the JSON schema version; bump on any field change.
const Version = 2

// Counts tallies the files a run could not score, so a consumer never reads a
// file that was not analyzed as zero mass.
type Counts struct {
	Errors  int `json:"errors"`
	Skipped int `json:"skipped"`
}

// Envelope wraps every subcommand's JSON output.
type Envelope struct {
	Result   any               `json:"result"`
	Docs     map[string]string `json:"docs"`
	Tool     string            `json:"tool"`
	Command  string            `json:"command"`
	Repo     string            `json:"repo"`
	Error    string            `json:"error,omitempty"`
	At       string            `json:"at"`
	Warnings []string          `json:"warnings,omitempty"`
	Counts   Counts            `json:"counts"`
	Schema   int               `json:"schema"`
}

// Fprint writes to an output stream, converting every print call's noise.
// Broken-pipe failures surface at the next write or the process's exit.
//
//nolint:errcheck // output to a terminal/pipe that went away is not recoverable per-line
func Fprint(w io.Writer, args ...any) {
	fmt.Fprint(w, args...)
}

// Fprintf is Fprint with a format string.
//
//nolint:errcheck // see Fprint
func Fprintf(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, format, args...)
}

// NewEnvelope stamps a command's result with the tool, schema version, and time.
func NewEnvelope(command, repo string, result any, counts Counts, warnings []string) Envelope {
	return Envelope{
		Schema: Version, Tool: "slop-strata", Command: command, Repo: repo,
		At: time.Now().UTC().Format(time.RFC3339), Result: result,
		Docs: Docs, Warnings: warnings, Counts: counts,
	}
}

// WarnCounts prints one line to w when a run skipped or failed to parse any file,
// so text output says what its numbers leave out.
func WarnCounts(w io.Writer, c Counts) {
	if c.Skipped == 0 && c.Errors == 0 {
		return
	}
	Fprintf(w, "slop-strata: %d skipped, %d with parse errors (--json names each)\n", c.Skipped, c.Errors)
}

// Encode writes one envelope, indented for a person or on a single line for
// JSON Lines.
func Encode(w io.Writer, env Envelope, indent bool) error {
	enc := json.NewEncoder(w)
	if indent {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(env); err != nil {
		return fmt.Errorf("encoding envelope: %w", err)
	}

	return nil
}

// WriteJSON emits the envelope for a command's result.
func WriteJSON(w io.Writer, command, repo string, result any, counts Counts, warnings []string) error {
	return Encode(w, NewEnvelope(command, repo, result, counts, warnings), true)
}

// Docs is the per-metric reference, the same text --explain prints and the
// JSON carries. Keep each entry to what the number counts, its threshold, and
// the behavior it should change.
//
//nolint:gochecknoglobals // the metric glossary is a constant table
var Docs = map[string]string{
	"erosion": "Share of complexity mass (CC x sqrt(SLOC) per function) held by functions with " +
		"cyclomatic complexity over 10, per arXiv 2603.24755. No target; watch direction per " +
		"directory. When it rises on routine work, the complex functions are getting more of them.",
	"mass": "Total complexity mass: sum over functions of CC x sqrt(SLOC). " +
		"Gross added/removed/net are reported separately; a refactor that removes mass differs " +
		"from one that adds it.",
	"mass_8w": "Net mass per week for the last eight weeks of the range, oldest left. Each row " +
		"scales to its own minimum and maximum, so it shows shape, not sign or size.",
	"mass_grown": "Function-matched growth: for each function present after the change, " +
		"max(0, mass_after - mass_before) summed, matched by name within the same file. New " +
		"functions count in full. This is the mass a change actually adds, as distinct from " +
		"mass_added which totals the after-side mass of every touched file.",
	"over_limit": "The share of mass_grown sitting in functions whose cyclomatic complexity " +
		"exceeds 10. A change that concentrates its growth in already-complex functions is " +
		"the shape erosion measures.",
	"p90_len": "90th percentile function length in lines. Long functions carry context a reader " +
		"must hold; watch the max, not the mean.",
	"max_len": "Longest function in lines.",
	"p90_nesting": "90th percentile control-nesting depth. Deep nesting is the shape refactors " +
		"actually need to fix.",
	"max_nesting": "Deepest control nesting in any function.",
	"p90_params": "90th percentile parameter count. Many parameters signal a missing abstraction " +
		"or a config object.",
	"max_params": "Largest parameter count on any function.",
	"p90_cognitive": "90th percentile cognitive complexity (nesting-weighted decision count, " +
		"approximating the SonarSource definition).",
	"max_cognitive": "Highest cognitive complexity in any function.",
	"over_50":       "Functions longer than 50 lines. Each is a candidate to split, not a violation.",
	"churn_14d": "Lines added in the range that were modified or deleted within 14 days of " +
		"landing. High churn reads as work that did not settle.",
	"churn_30d": "Lines added in the range that were modified or deleted within 30 days.",
	"rework":    "Young lines (<=30 days) removed by fix commits: fixes to things just built.",
	"reverts":   "Commits in the range that revert another change.",
	"survival": "Kaplan-Meier share of a cohort's added lines still alive at 30 and 90 days, " +
		"per monthly cohort. At-risk is approximated at month granularity. Null when too few lines " +
		"in the cohort have run as long as the horizon.",
	"concentration": "Largest single author's share of each directory's live lines. Reported per " +
		"directory, never as a person ranking.",
	"coupling": "File pairs changed in the same commit 3+ times, skipping commits over 30 files " +
		"(code-maat's definition of co-change).",
	"hotspot": "Directories where recent touches meet high complexity mass: churn x complexity.",
	"rule_hits": "Slop-rule findings. Each carries the rule id, file, and line; a baseline file " +
		"holds the ones already accepted. Experimental rules report only with --experimental.",
	"experimental": "Rules not yet shown to clear 80% precision on the corpus, among them the " +
		"narrating-comment, dup-literal, dup-test-case, dead-export, and stale-prose rules and the type-any and " +
		"test-reads-source families. Enable them per run with --experimental, or for a repository " +
		"with experimental = true in .slop-strata.toml. A rule leaves this list when a labels file " +
		"shows it clears the bar.",
	"findings": "New rule hits attributable to this change, after the baseline and pre-existing " +
		"hits are excluded.",
	"virality": "For each rule: the first commit that hit it, then how hits spread afterward per " +
		"author class and across directories (spread_dirs). Answers whether a pattern gets " +
		"copied once it lands.",
	"test_survival": "Share of test files added in the range that were deleted within 30 and 90 " +
		"days, split by the adding commit's author class. Only adds observed inside the range count. " +
		"Live files younger than a horizon are left out, and the share is null when none is old enough.",
	"pairing": "Author classes compared on the same week and directory: net mass moved and " +
		"commits each. A cell is provisional under 5 commits.",
	"errors": "Files the parser could not fully read, with the byte share under error nodes. " +
		"Metrics still count what parsed; the row is a warning, not an abort.",
	"skipped": "Files excluded by config, size, binary content, minified shape, or generated code, " +
		"each named with its reason. Generated means a generator marker in the first five lines, or " +
		"linguist-generated in the working tree's root .gitattributes.",
	"counts": "Files the run could not score: skipped (excluded by config, size, binary content, " +
		"minified or generated shape) and errors (a parse that left error nodes). Metrics exclude " +
		"them, so a non-zero count means the numbers cover less of the tree. Unsupported file " +
		"types are listed as skipped but not counted.",
	"intent": "The change's stated purpose read off commit metadata: Conventional Commit type, " +
		"branch name, then issue keywords, plus revert detection.",
}

// Explain prints a metric's doc for --explain.
func Explain(w io.Writer, name string) bool {
	doc, ok := Docs[name]
	if !ok {
		return false
	}
	Fprintf(w, "%s\n%s\n", name, doc)

	return true
}

// Fprintln is Fprint with a trailing newline.
//
//nolint:errcheck // see Fprint
func Fprintln(w io.Writer, args ...any) {
	fmt.Fprintln(w, args...)
}

// Table renders aligned rows for the text forms.
func Table(w io.Writer, header []string, rows [][]string) {
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, r := range rows {
		for i, c := range r {
			widths[i] = max(widths[i], utf8.RuneCountInString(c))
		}
	}
	line := func(cols []string) {
		parts := make([]string, len(cols))
		for i, c := range cols {
			parts[i] = c + strings.Repeat(" ", widths[i]-utf8.RuneCountInString(c))
		}
		Fprintln(w, strings.TrimRight(strings.Join(parts, "  "), " "))
	}
	line(header)
	for _, r := range rows {
		line(r)
	}
}

// spark ticks for trend sparklines.
var spark = []rune("▁▂▃▄▅▆▇█")

// Sparkline renders values as ticks scaled between their own minimum and maximum,
// so it shows the shape of a series and not its sign or size.
func Sparkline(values []float64) string {
	if len(values) == 0 {
		return ""
	}
	lo, hi := values[0], values[0]
	for _, v := range values[1:] {
		lo = min(lo, v)
		hi = max(hi, v)
	}
	var b strings.Builder
	for _, v := range values {
		idx := 0
		if hi > lo {
			idx = int(math.Round((v - lo) / (hi - lo) * float64(len(spark)-1)))
		}
		b.WriteRune(spark[idx])
	}

	return b.String()
}

// SortedKeys is a helper for deterministic output.
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	return keys
}

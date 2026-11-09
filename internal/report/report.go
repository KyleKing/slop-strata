// Package report renders results: JSON on --json, and the human table forms
// otherwise. Every metric carries its own doc, printed beside the number and
// answered by --explain.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// Version is the JSON schema version; bump on any field change.
const Version = 1

// Envelope wraps every subcommand's JSON output.
type Envelope struct {
	Schema   int               `json:"schema"`
	Tool     string            `json:"tool"`
	Command  string            `json:"command"`
	Repo     string            `json:"repo"`
	At       string            `json:"at"`
	Result   any               `json:"result"`
	Docs     map[string]string `json:"docs"`
	Warnings []string          `json:"warnings,omitempty"`
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

// WriteJSON emits the envelope for a command's result.
func WriteJSON(w io.Writer, command, repo string, result any, warnings []string) error {
	env := Envelope{
		Schema: Version, Tool: "slop-strata", Command: command, Repo: repo,
		At: time.Now().UTC().Format(time.RFC3339), Result: result,
		Docs: Docs, Warnings: warnings,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	return enc.Encode(env)
}

// Docs is the per-metric reference, the same text --explain prints and the
// JSON carries. Keep each entry to what the number counts, its threshold, and
// the behavior it should change.
//
//nolint:gochecknoglobals // the metric glossary is a constant table
var Docs = map[string]string{
	"erosion":       "Share of complexity mass (CC x sqrt(SLOC) per function) held by functions with cyclomatic complexity over 10, per arXiv 2603.24755. No target; watch direction per directory. When it rises on routine work, the complex functions are getting more of them.",
	"mass":          "Total complexity mass: sum over functions of CC x sqrt(SLOC). Gross added/removed/net are reported separately; a refactor that removes mass differs from one that adds it.",
	"p90_len":       "90th percentile function length in lines. Long functions carry context a reader must hold; watch the max, not the mean.",
	"max_len":       "Longest function in lines.",
	"p90_nesting":   "90th percentile control-nesting depth. Deep nesting is the shape refactors actually need to fix.",
	"max_nesting":   "Deepest control nesting in any function.",
	"p90_params":    "90th percentile parameter count. Many parameters signal a missing abstraction or a config object.",
	"max_params":    "Largest parameter count on any function.",
	"p90_cognitive": "90th percentile cognitive complexity (nesting-weighted decision count, approximating the SonarSource definition).",
	"max_cognitive": "Highest cognitive complexity in any function.",
	"over_50":       "Functions longer than 50 lines. Each is a candidate to split, not a violation.",
	"churn_14d":     "Lines added in the range that were modified or deleted within 14 days of landing. High churn reads as work that did not settle.",
	"churn_30d":     "Lines added in the range that were modified or deleted within 30 days.",
	"rework":        "Young lines (<=30 days) removed by fix commits: fixes to things just built.",
	"reverts":       "Commits in the range that revert another change.",
	"survival":      "Kaplan-Meier share of a cohort's added lines still alive at 30 and 90 days, per monthly cohort. At-risk is approximated at month granularity.",
	"concentration": "Largest single author's share of each directory's live lines. Reported per directory, never as a person ranking.",
	"coupling":      "File pairs changed in the same commit 3+ times, skipping commits over 30 files (code-maat's definition of co-change).",
	"hotspot":       "Directories where recent touches meet high complexity mass: churn x complexity.",
	"rule_hits":     "Slop-rule findings. Each carries the rule id, file, and line; a baseline file holds the ones already accepted.",
	"findings":      "New rule hits attributable to this change, after the baseline and pre-existing hits are excluded.",
	"virality":      "For each rule: the first commit that hit it, then how hits spread afterward per author class. Answers whether a pattern gets copied once it lands.",
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
		widths[i] = len(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}
	line := func(cols []string) {
		parts := make([]string, len(cols))
		for i, c := range cols {
			parts[i] = c + strings.Repeat(" ", widths[i]-len(c))
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

// Sparkline renders values as a bar sparkline.
func Sparkline(values []float64) string {
	if len(values) == 0 {
		return ""
	}
	hi := 0.0
	for _, v := range values {
		if v > hi {
			hi = v
		}
	}
	if hi == 0 {
		return strings.Repeat(string(spark[0]), len(values))
	}
	var b strings.Builder
	for _, v := range values {
		idx := int(v / hi * float64(len(spark)-1))
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

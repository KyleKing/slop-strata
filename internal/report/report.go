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
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Version is the JSON schema version; bump on any field change.
const Version = 3

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
	"mass": "Total complexity mass: sum over functions of CC x sqrt(SLOC). A change reports the " +
		"mass it grew and the mass it removed apart, each for production and for tests, so a " +
		"refactor that removes mass reads differently from one that adds it.",
	"table_mass": "Mass of case tables, kept apart from mass and erosion: a map literal of 15+ " +
		"structured entries scores as a switch with one branch per entry, entries x sqrt(lines). " +
		"A snapshot's hotspot leaves table_mass out, so a hand-written case table never ranks a directory.",
	"mass_8w": "Net mass per week for the last eight weeks of the range, oldest left. Each row " +
		"scales to its own minimum and maximum, so it shows shape, not sign or size.",
	"mass_grown": "Gross mass the change added: for each function present after the change, " +
		"max(0, mass_after - mass_before) summed, matched by name within the same file or, for " +
		"code moved between files, by name, CC, and size. New functions count in full. Production " +
		"files and test files are summed apart (test_mass_grown). A change that grows mass in " +
		"many small functions differs from one that grows a single function.",
	"mass_removed": "Gross mass the change removed: for each function present before the change, " +
		"max(0, mass_before - mass_after) summed, with deleted functions counted in full and moved " +
		"code not counted. mass_net is mass_grown minus mass_removed. Production and test files " +
		"are summed apart (test_mass_removed). A refactor should show mass_removed near or above " +
		"mass_grown.",
	"over_limit": "The share of mass_grown sitting in functions whose cyclomatic complexity " +
		"exceeds 10, for production files (test_over_limit for tests). A change that concentrates " +
		"its growth in already-complex functions is the shape erosion measures. Split the " +
		"function or reduce its branches before adding to it.",
	"top_functions": "On change, up to 10 functions the change grew most, largest mass_grown first, " +
		"with file, start line, CC and mass before and after, and whether the file is a test file. " +
		"A new function has cc_before 0. Open these first in review: they hold the growth. On " +
		"snapshot, the 10 heaviest functions of the tree by mass, with file, start line, CC, SLOC, " +
		"mass, and test; the text view lists the top 5 production functions. Split the top one " +
		"before adding to it.",
	"next": "result.next: up to three places to look at, in a fixed order: production functions over CC 10 " +
		"that grew or were added (largest mass grown first), then new rule hits not in the " +
		"baseline (by file and line), then a stated intent the numbers argue against (the " +
		"\"reads as\" clause). A change is worth a look exactly when the list is not empty. " +
		"change --brief prints the reading and these lines, and nothing when the list is empty. " +
		"With --exit-code the status is 1 when it would print and 0 when it would not. Look at " +
		"the first item before you merge or commit. With --json --context, snippets carry the " +
		"after-side source lines of each next function, up to 80 lines each.",
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
	"deaths_30d": "How the lines counted in churn_30d died, per directory (credited to the directory the " +
		"line was written in) and overall, as counts: total, self, pure, whole_file, in_revert, three age " +
		"buckets (age_le_1d, age_2_7d, age_8_30d), and by_class (birth author class, then the class of the " +
		"removing commit). Each split is a count of total, so a consumer can add directories. Moved lines " +
		"are not deaths. Squash merges hide whatever happened inside a branch, so a repository that squashes " +
		"shows only the survivors of each branch. Read the parts side by side and never as one score: " +
		"pruning and flapping look alike in churn_30d and differ here.",
	"readd": "Share of young deaths that came back: a later commit, within 30 days of the death, added a " +
		"line to the same file with the same trimmed text. deaths_30d carries the counts: readd_at_risk, " +
		"readded, and readded_by_revert, overall and per directory, and the text table prints readded over " +
		"readd_at_risk. Only lines of 30 or more bytes match, because shorter ones such as \"return nil\" " +
		"recur by habit, and readd_at_risk counts only such deaths that had 30 full days of walk after them, " +
		"the way survival leaves out a line too young to reach its horizon, so a window under 30 days old " +
		"shows a dash. A line removed and added in one commit is a move or an edit and never counts, and each " +
		"death and each added line match once. A death in a revert commit, or a line re-added by one, counts " +
		"in readded_by_revert instead, since undoing a revert is deliberate. A high share means code was cut " +
		"and then rebuilt in the same place: agree on the design before writing it. A low share beside high " +
		"pure and whole_file means pruned code stayed gone. It misreads formatting commits that rewrap lines, " +
		"regenerated fixtures and golden files, a red-green test loop that deletes and restores a line, and " +
		"squash merges that hide what happened inside a branch. Code that reappears in a file with another " +
		"name, or in 29 bytes or fewer, is not seen.",
	"oscillating_funcs": "Functions whose body changed in at least 3 separate commits within any 14 days, " +
		"keyed by file path and function name, counted per directory and overall. oscillating_test_funcs " +
		"counts functions in test files apart, and the JSON lists the 10 busiest production functions with " +
		"path, name, line, changes, and span_days. A commit changes a function when a diff hunk adds a line " +
		"inside it or removes one from between its lines, and the function already existed. Commits of the " +
		"bot author class do not count. A function rewritten again and again in two weeks means its shape " +
		"was not settled before the code: decide the interface first, or split the function so the unstable " +
		"part is small. A formatting commit can supply all three changes, a red-green test loop on one " +
		"function reads as oscillation though it is deliberate, a squash merge hides the changes inside a " +
		"branch, and a small function has little to change except all of it. A file renamed through git's " +
		"rename detection keeps its history, and a function renamed or moved to another file starts again. " +
		"Same-named functions in one file count once per commit, and anonymous functions are not followed.",
	"self": "Share of young deaths removed by the author who wrote the line, compared by commit author " +
		"email. High self means people clean up their own work. A high share of other-author removals on " +
		"agent-born lines (by_class) means cleanup is left to someone else: review agent output before it " +
		"lands. With one author in the window self is always 1.0, so text output hides the column. " +
		"A person with two emails reads as two authors, and a rebase that rewrites authors hides the original.",
	"pure": "Share of young deaths in a hunk that removed lines and added none, against a hunk that " +
		"replaced them in place. A deleted file counts as pure, and a rewrite whose removal and addition fall in " +
		"separate hunks reads as pure. High pure with high whole_file and " +
		"self reads as experiments pruned by whoever ran them. Low pure with high within_7d reads as young " +
		"code rewritten in place: settle the design before writing the code.",
	"whole_file": "Share of young deaths that died because their file was deleted, against a partial edit " +
		"to a file that lives on. High whole_file means whole attempts are discarded, which is pruning. A " +
		"file renamed or split with its lines kept does not count, because moved lines are not deaths. A " +
		"file deleted and recreated with changes reads as whole_file for the old lines.",
	"within_7d": "Share of young deaths within 7 days of the line being written: age_le_1d plus age_2_7d " +
		"over total. A high share with low pure means code rewritten in place within a week: agree on the " +
		"shape of the change before writing it. A repository that commits rarely has few deaths in the first day, " +
		"so read the count with the share.",
	"in_revert": "Young deaths in commits that revert another change. A revert is neither pruning nor " +
		"flapping by these measures, so it is counted apart and still included in total.",
	"reverts": "Commits in the range that revert another change.",
	"s30_ci": "Greenwood 95% confidence interval for a cohort's survival at 30 days, as [low, high] " +
		"clipped to 0 and 1. It widens when few lines reach the horizon, so read a wide interval as a weak estimate.",
	"s90_ci": "Greenwood 95% confidence interval for a cohort's survival at 90 days, on the same basis as s30_ci.",
	"noise_floor": "Median absolute deviation of weekly net mass across the history window. A week " +
		"whose net sits within one floor of the median week shows no measurable change, so each " +
		"is marked within_noise and printed with a ~.",
	"within_noise": "Set on a week whose net mass lies within one noise_floor of the window's median week.",
	"s_30d": "Survival at 30 days: the Kaplan-Meier share of lines still alive at 30 days (cohort " +
		"rows) or of test files not deleted within 30 days (class rows). Null when too few reached " +
		"30 days; see survival and test_survival.",
	"s_90d": "Survival at 90 days, on the same basis as s_30d.",
	"survival": "Kaplan-Meier share of a cohort's added lines still alive at 30 and 90 days, " +
		"per monthly cohort. At-risk is approximated at month granularity. Null when too few lines " +
		"in the cohort have run as long as the horizon.",
	"concentration": "Largest single author's share of each directory's live lines. Reported per " +
		"directory, never as a person ranking. A directory with no live lines reads as a dash in text " +
		"and null in JSON.",
	"coupling": "File pairs changed in the same commit 3+ times, skipping commits over 30 files " +
		"(code-maat's definition of co-change).",
	"hotspot": "Where recent edits meet complexity mass. On snapshot, for each directory and file, " +
		"touches_90d times mass: the commits that touched it in the 90 days ending at the scored " +
		"revision's commit date, times its production mass (table_mass left out). Tests rank apart, " +
		"and ties rank by mass. On history, 30-day churned lines times live mass per directory. Read " +
		"the top rows first when you choose what to simplify, and a directory still being built will " +
		"score high.",
	"touches_90d": "Commits that touched a directory or file in the 90 days ending at the scored " +
		"revision's commit date, merges counted once through their first parent. It is the " +
		"multiplier in a snapshot's hotspot. A change counts the last 90 days from today for its " +
		"hotspot flag, which marks a directory with 10 or more touches.",
	"provisional": "Set on a survival cohort, test class, or directory whose rate rests on too few " +
		"samples: under 30 lines for a cohort, under 5 test files for a test class, under 5 functions " +
		"for a directory's erosion (on the smaller side of a change). The rate still prints, " +
		"marked * in text output.",
	"rule_hits": "Slop-rule findings. Each carries the rule id, file, and line; a baseline file " +
		"holds the ones already accepted. Experimental rules report only with --experimental.",
	"experimental": "Rules not yet shown to clear 80% precision on the corpus, among them the " +
		"narrating-comment, dup-literal, dup-test-case, dead-export, and snapshot stale-prose rules and " +
		"the type-any and test-reads-source families. Enable them per run with --experimental, or for a " +
		"repository " +
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
		"branch name, then issue keywords, plus revert detection. Kinds are feat, fix, refactor, perf, " +
		"test, docs, chore, and unknown. Only refactor, fix, and test make a claim the numbers can " +
		"contradict, so a reading that says \"reads as\" questions that claim. perf, docs, and chore " +
		"never do. Use it to judge whether a mismatch between label and numbers is worth a question in review.",
}

// Names lists every metric --explain answers, sorted.
func Names() []string {
	return SortedKeys(Docs)
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

// DropZeroColumns removes every column after the first whose cells are all
// blank or zero, read as numbers split on spaces and arrows, and returns the remaining header and rows.
func DropZeroColumns(header []string, rows [][]string) ([]string, [][]string) {
	keep := []int{0}
	for c := 1; c < len(header); c++ {
		if !zeroColumn(rows, c) {
			keep = append(keep, c)
		}
	}
	pick := func(cols []string) []string {
		out := make([]string, len(keep))
		for i, c := range keep {
			out[i] = cols[c]
		}

		return out
	}
	trimmed := make([][]string, len(rows))
	for i, r := range rows {
		trimmed[i] = pick(r)
	}

	return pick(header), trimmed
}

func zeroColumn(rows [][]string, col int) bool {
	isSeparator := func(c rune) bool { return c == ' ' || c == '→' || c == '*' }
	for _, r := range rows {
		for _, field := range strings.FieldsFunc(r[col], isSeparator) {
			if v, err := strconv.ParseFloat(strings.TrimPrefix(field, "+"), 64); err != nil || v != 0 {
				return false
			}
		}
	}

	return true
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

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
const Version = 6

// Counts tallies the files a run could not score, so a consumer never reads a
// file that was not analyzed as zero mass.
type Counts struct {
	Suppressed Suppressed `json:"suppressed"`
	Errors     int        `json:"errors"`
	Skipped    int        `json:"skipped"`
	// StaleBaseline is the baseline entries no finding of a snapshot matched. See Docs["stale_baseline"].
	StaleBaseline int `json:"stale_baseline"`
}

// Suppressed tallies the findings a run set aside: baseline, inline ignore, or false-positive label.
type Suppressed struct {
	Baseline  int `json:"baseline"`
	Ignored   int `json:"ignored"`
	LabeledFP int `json:"labeled_fp"`
}

// Total is every finding set aside.
func (s Suppressed) Total() int { return s.Baseline + s.Ignored + s.LabeledFP }

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

// WarnCounts prints a line when a run skipped or failed a file, and another when it set findings aside.
func WarnCounts(w io.Writer, c Counts) {
	if c.Skipped != 0 || c.Errors != 0 {
		Fprintf(w, "slop-strata: %d skipped, %d with parse errors (--json names each)\n", c.Skipped, c.Errors)
	}
	if s := c.Suppressed; s.Total() != 0 {
		Fprintf(w, "slop-strata: %d findings set aside (%d in the baseline, %d ignored inline, %d labeled false "+
			"positive)\n", s.Total(), s.Baseline, s.Ignored, s.LabeledFP)
	}
	if c.StaleBaseline != 0 {
		Fprintf(w, "slop-strata: %d baseline entries match no finding now (fixed, edited, or a cycle that shrank); "+
			"run `slop-strata snapshot --write-baseline` to tighten the baseline\n", c.StaleBaseline)
	}
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
		"function reads as oscillation though it is deliberate, a function that gains one entry per feature, " +
		"such as a key map or a command table, counts on every addition, a squash merge hides the changes " +
		"inside a branch, and a small function has little to change except all of it. A file renamed through git's " +
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
		"narrating-comment, dup-literal, dup-test-case, dead-export, import-cycle, and snapshot stale-prose rules " +
		"and the type-any and test-reads-source families. Enable them per run with --experimental, or for a " +
		"repository with experimental = true in .slop-strata.toml. The owner decides when a rule leaves " +
		"this list. Verdicts recorded with slop-strata label give each rule a measured precision for this " +
		"repository, which slop-strata rules prints and the table below repeats from the labels file of the " +
		"current directory. Labels never promote or demote a rule on their own, and one repository's labels " +
		"are not the corpus the 80% bar is set on.",
	"findings": "New rule hits attributable to this change, after the baseline, ignored, and labeled " +
		"hits and the pre-existing hits are excluded. Each has an id, a short hash of the rule, path, " +
		"enclosing function, matched text, and line text, so it survives line moves and new hits above it, and " +
		"a scope naming the function. Editing the line changes the id. An x/import-cycle finding is keyed by " +
		"its member files instead; see import_cycles. " +
		"To suppress a hit inline, write slop-strata:ignore RULE-ID REASON in a comment on its line or the " +
		"line above, in the file's comment syntax: // or # in code, # in TOML and YAML, and an HTML comment " +
		"in Markdown outside code blocks. In Go, //nolint:errcheck // REASON also suppresses go/silent-error. " +
		"The reason is required. An ignore without one suppresses nothing and is reported as " +
		"x/ignore-without-reason. JSON files and ast-grep findings take no inline ignore. Suppressed hits are " +
		"counted in counts.suppressed. To dispute a hit without touching the code, run slop-strata label ID fp. " +
		"The one exception is x/last-ref, which only change reports: label cannot find its id, so accept it " +
		"with a baseline or labels file entry for the id.",
	"suppressed": "In counts.suppressed, the findings a run set aside: baseline (accepted in the baseline " +
		"file), ignored (an inline ignore or nolint comment with a reason), and labeled_fp (a false-positive " +
		"label). A finding counts once, under the first of ignored, labeled_fp, and baseline. A count that " +
		"grows faster than the hits get fixed means findings are being muted: read the ignore reasons and " +
		"labels in review.",
	"stale_baseline": "In counts.stale_baseline, on snapshot: baseline entries that no finding of the scored " +
		"tree carries the id of. The finding was fixed, its line or matched text was edited, or it is an import " +
		"cycle that shrank, which the entry still covers by its members. Entries of rules this run did not " +
		"execute (experimental rules without --experimental, ast-grep packs, x/last-ref) are not counted. " +
		"Run slop-strata snapshot --write-baseline to drop them, so a fixed finding that comes back reads as " +
		"new. It never changes the exit status. change and history do not score the whole tree against the " +
		"baseline and always report 0.",
	"import_edges": "In result.imports on snapshot: edges counts every import the tool resolved from one " +
		"file of the tree to another, type_edges the ones only a type checker reads (TypeScript import type, " +
		"Python under TYPE_CHECKING, any import of or from a .pyi or .d.ts file), and deferred_edges the ones " +
		"that do not run when the file loads (inside a function body, a dynamic import(), or under Python's " +
		"if __name__ == \"__main__\"). The rest run at module level. Python resolves absolute and relative " +
		"imports under any source root and drops a module name that two roots answer to. TypeScript resolves " +
		"relative specifiers only, so path aliases and workspace package names add no edge. Go resolves " +
		"through the module path of each go.mod. Imports of installed packages are outside the graph. No " +
		"target; the numbers say how much of the tree fan_in, fan_out, and import_cycles rest on.",
	"fan_in": "Per directory on snapshot: the distinct other directories with a file that imports one of " +
		"this directory's files, over resolved edges of every kind (see import_edges). A production row counts " +
		"edges between production files, and a test row counts edges that start or end at a test file. High " +
		"fan_in means many places break when this directory changes: keep its surface small and stable. " +
		"Imports the tool cannot resolve are not counted, so a directory reached only through a path alias " +
		"reads low.",
	"fan_out": "Per directory on snapshot: the distinct other directories its files import, on the same " +
		"basis as fan_in. High fan_out means the directory knows about much of the tree. A directory high on " +
		"both is a hub: a change there is felt everywhere and it is hard to test alone, so look there first " +
		"when you split responsibilities.",
	"import_cycles": "result.imports.cycles on snapshot: groups of two or more non-test files where each " +
		"imports every other, directly or through the others, over module-level runtime imports. " +
		"largest_cycle is the file count of the biggest group. Imports a type checker alone reads, imports " +
		"inside a function body, and imports under a main guard are the usual ways around a cycle and do not " +
		"count, and neither does a cycle that needs a test file to close. Go forbids package cycles, so Go " +
		"files never form one. The numbers are counted before the baseline. With --experimental each group " +
		"is one x/import-cycle finding on the import line of its cheapest cut, the edge importing the fewest " +
		"names, with the shortest cycle through that edge in the note. The finding's id comes from the rule " +
		"and the sorted member files (members), leaving out files that only re-export when two others " +
		"remain, so it holds while imports move and changes when a file joins or leaves. A baseline entry " +
		"for a cycle also covers any cycle made only of its members, so shrinking a cycle is never a new " +
		"finding, and a new or grown cycle is. change reports a cycle the change created or grew. Move the " +
		"names the cheapest cut imports into a module both sides can import.",
	"architecture": "result.architecture on history: how the inferred import graph changed over the window. " +
		"periods has one row per week with a commit, read from the tree after the week's last first-parent " +
		"commit (rev). A commit authored before the commits it follows counts in their week, so the rows run " +
		"in walk order. cycles and largest_cycle are counted as import_cycles counts them, and " +
		"files_in_cycles, depth_dirs, and median_depth have their own entries. A week that changed no source " +
		"file and no go.mod repeats the week before. Each tree is resolved from cached fingerprints, so the " +
		"first run parses the files the window never touched and later runs parse nothing. The text table " +
		"prints the first week, the last, and each week whose cycle counts changed. Nothing here is a " +
		"finding: there is no rule id, no baseline, and no gate. See depth_moved, cycle_origin, and " +
		"hidden_coupling for the three lists beside periods.",
	"files_in_cycles": "Per week in architecture.periods: how many files sit in any import cycle, the sum " +
		"of the sizes of the week's cycles. A count that rises while cycles stays flat means a cycle is " +
		"growing.",
	"depth_dirs": "Per week in architecture.periods: the directories that other directories use names of " +
		"and that declare at least 5 names, which are the ones median_depth is taken over. A directory " +
		"under 5 names has a provisional depth and is left out.",
	"median_depth": "Per week in architecture.periods: the median depth of the week's depth_dirs, and " +
		"median_depth_attributed the median of their depth_attributed. depth counts a method matched by " +
		"name as shown and depth_attributed counts it as hidden, so the true median lies between the two, " +
		"and the text prints them as a range when they differ. Null when the week has no such directory. " +
		"A median over a handful of directories jumps when one directory crosses the 5-name line, so read " +
		"it with depth_dirs.",
	"depth_moved": "architecture.depth_moved: up to 5 directories whose module depth moved most between " +
		"the first and the last week of the window. A directory is listed when it has a depth off at least " +
		"5 names in both weeks and both depth and depth_attributed moved the same way by 0.05 or more. " +
		"moved is the smaller of the two moves, so a method matched by name on one side alone cannot make a " +
		"row. A directory that only one of the two weeks has is not listed. A fall means other directories " +
		"came to use more of what the directory holds.",
	"cycle_origin": "architecture.cycles: each import cycle of the last week, with id and members as its " +
		"x/import-cycle finding has them and size counting every file. A cycle continues an earlier one " +
		"when the two share at least two files, after following renames: two files that import each other " +
		"in both weeks are one dependency that held, which includes the grown and the shrunk cycle a " +
		"baseline entry covers. first_week and first_size are the week and size at which that line of " +
		"cycles began, and a cycle that went away and came back begins again. introduced is the first-parent " +
		"commit the cycle is first present after, found by bisecting that week's commits over cached " +
		"fingerprints, with the commit's author class and intent. It is exact unless the cycle came and went " +
		"inside the week, where it names one of the commits that brought it back, and a merge commit stands " +
		"for its whole branch. before_window is set, with introduced null, when the cycle is already in the " +
		"tree before the window's first commit, or when a shallow clone cannot read that tree. The author " +
		"class is the configured class of the commit and never a person.",
	"hidden_coupling": "architecture.hidden_coupling: up to 10 pairs of production source files that " +
		"change together though no chain of imports leads from either to the other in the last week's " +
		"graph, over imports of every kind. co_changes counts the commits that changed both files, less the " +
		"ones left out below, changes_a and changes_b count the commits that changed each, and support and " +
		"confidence have their own entries. A " +
		"pair needs 4 co_changes, a support of 1.5, and a confidence of 0.5. Left out of the counting: " +
		"commits that touch more than 30 files, bot commits, docs and chore commits (ci, build, style, and " +
		"dependency bumps read as chore), test files, files the " +
		"engine does not score (generated, minified, oversized, unsupported), and any commit that also " +
		"changed a file both files import, since that import explains the change. Left out as pairs: two Go " +
		"files of one directory, which are one package, and two TypeScript files under different " +
		"package.json files, which may import each other by a package name the graph does not resolve. " +
		"shared_importer says some file imports both. A file keeps its counts through a rename. On twelve " +
		"public repositories 7 pairs cleared these floors and all 7 read as real when checked by hand, " +
		"against 20 of 30 at looser floors, so expect few rows. A pair listed here is a rule the code does " +
		"not state: name it in a shared module or a test, or accept it.",
	"support": "Per pair in architecture.hidden_coupling: the sum, over the commits counted in co_changes, " +
		"of one over the number of other source files the commit changed. A commit of just the two files " +
		"adds 1 and a sweep over twenty-one files adds a twentieth, so many wide commits weigh less than a " +
		"few narrow ones. Pairs are ranked by it.",
	"confidence": "Per pair in architecture.hidden_coupling: co_changes over the smaller of changes_a and " +
		"changes_b. At 1 the file that changes less never changed without the other.",
	"shown": "Per directory on snapshot: the directory's declared names that a production file of another " +
		"directory uses. A name is a function, type or class, constant, or variable at file scope, or a method " +
		"(Type.method). Use decides it and visibility does not: export, a capital letter, a leading underscore, " +
		"and __all__ change nothing. A top-level name is attributed: resolved imports tie the use to the file " +
		"that declares it, through re-exporting files, so a same-named symbol elsewhere is not counted, and an " +
		"import that is bound and never used, or a re-export nobody consumes, is not a use. A method cannot be " +
		"attributed, so it is matched by name alone; see shown_by_name for how many of shown are such guesses. " +
		"Any other directory is outside, a parent or a subdirectory included, as for fan_in. Test files " +
		"neither count as users nor have their names counted. A name the world uses and no file of the tree " +
		"does (a library's public API) reads hidden. Imports the tool cannot resolve (see import_edges) are " +
		"not uses.",
	"shown_by_name": "Per directory on snapshot (by_name in the table): how many of shown are methods matched " +
		"by name alone, and shown_mass_by_name how much of shown_mass. There is no type inference, so a method " +
		"counts as shown when any file of another directory reads a member of that name off a value that is " +
		"not an imported module, once anything outside imports the directory. A method named like an unrelated " +
		"member (get, String, value) is counted too: on a hand-checked sample about six in ten were real uses, " +
		"while every method left hidden was truly unused outside. Read it as how soft shown is: shown minus " +
		"shown_by_name is certain, and the rest is an upper bound. These names are never listed anywhere.",
	"hidden": "Per directory on snapshot: the declared names (see shown) that no production file of another " +
		"directory uses, whether the directory uses them itself or nothing does. Pure forwarders are left out " +
		"(see forwarders).",
	"shown_mass": "Per directory on snapshot: the complexity mass (CC x sqrt(SLOC), see mass) of the " +
		"functions behind the shown names. A function's mass, with its closures', goes to the declaration " +
		"that holds it, and a method's to the method. A constant or a type has none.",
	"hidden_mass": "Per directory on snapshot: the complexity mass behind the hidden names, plus the mass " +
		"of functions no declaration holds, such as a callback passed at file scope. Ten shown one-liners over " +
		"a large hidden engine read deep by mass and shallow by count, which is why depth uses mass.",
	"forwarders": "Per directory on snapshot, in the JSON: functions and methods whose body is one statement, " +
		"a call whose every argument is one of the function's own parameters (a docstring and comments aside). " +
		"A forwarder counts in neither shown nor hidden, so pass-through wrappers cannot pad a directory into " +
		"looking deep. When another directory uses a forwarder, the name it forwards to is shown in its place " +
		"if the same directory declares it: a function by name, or a method of the same receiver.",
	"depth": "Per directory on snapshot: hidden_mass / (shown_mass + hidden_mass), or by name count, " +
		"hidden / (shown + hidden), when neither side has a function to weigh (depth_by_mass false, # in the " +
		"table). After Ousterhout's deep modules: near 1 the directory hides much more than callers must " +
		"learn, near 0 callers see nearly all of it. A ratio with no target and no gate. It is null (- in the " +
		"table) when shown is 0: nothing outside uses the directory, so it is an entry point or a leaf, not a " +
		"module with everything hidden. depth counts methods matched by name as shown, which makes it a lower " +
		"bound, and depth_attributed is the upper one; the table prints both as a range when they differ. " +
		"depth_provisional (*) marks fewer than 5 names. The table lists shallowest first and leaves the " +
		"masses to the JSON. When a directory you expected to hide its workings reads shallow, look at " +
		"widest_surface and ask which of those names callers need.",
	"depth_attributed": "Per directory on snapshot: depth with every method matched by name counted as hidden, " +
		"so only uses a resolved import attributes count as shown. No method can be attributed, so this is the " +
		"upper bound and depth the lower: uses the tool cannot see aside, the true ratio lies between them. It is " +
		"null when no use is " +
		"attributed. A narrow range means the number is solid, and a wide one means the directory is mostly " +
		"classes whose methods the tool cannot follow. On directories checked by hand, depth was the closer " +
		"of the two wherever methods carry the mass.",
	"widest_surface": "Per directory on snapshot: widest_file is the file with the most shown names and " +
		"widest_shown is how many. One long list is harder to learn than the same count spread over several " +
		"short ones, so this names where a wide surface sits.",
	"surface_widened": "result.surface_widened on change: each directory, present before and after, whose " +
		"count of attributed shown names rose (see shown), with shown_before and shown_after, up to five of " +
		"the names other directories use now and did not before (newly_shown, with newly_shown_count for all " +
		"of them), and depth_before and depth_after, which are depth_attributed on each side. Methods matched " +
		"by name alone never open a row and are never listed, because the match may be another type's method: " +
		"newly_shown_by_name only counts them, as an unverified upper bound. Both trees are measured whole, so " +
		"the cause may be a new use in a file of another directory. A rename that swaps one shown name for " +
		"another is not reported. Information only: it adds nothing to next, and --brief and --exit-code " +
		"ignore it. When a name you meant to keep internal appears here, the change made another directory " +
		"depend on it.",
	"labels": "Verdicts recorded with slop-strata label ID tp or fp, in the labels file (default " +
		".slop-strata-labels.json, set by labels in .slop-strata.toml). A finding labeled fp is suppressed " +
		"and counted in counts.suppressed.labeled_fp, and one labeled tp stays. Each entry holds the rule, " +
		"verdict, path, matched text, note, and the tool version, and no author. The file is evidence of " +
		"this repository's precision per rule. It says nothing about a rule's precision on other " +
		"repositories, which the 80% gate measures. Label hits as you review them, then read slop-strata rules.",
	"precision": "Per rule, tp over tp plus fp from the labels file, with n the number of labels. Under " +
		"10 labels the figure is provisional and printed with *. The tier column says whether the rule runs " +
		"by default or only with --experimental. No rule changes tier from this number: the 80% bar is the " +
		"owner's call, and the number is evidence for it from one repository.",
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
		"types are listed as skipped but not counted. suppressed counts the findings set aside; see " +
		"suppressed. stale_baseline counts baseline entries that match nothing; see stale_baseline.",
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

// ElidePath shortens a slash-separated path to at most width runes by
// replacing its middle segments with an ellipsis: the first segment and as
// many of the last as fit are kept. A path with nothing left to drop is cut
// from the left.
func ElidePath(p string, width int) string {
	if utf8.RuneCountInString(p) <= width {
		return p
	}
	segments := strings.Split(p, "/")
	for keep := len(segments) - 1 - 1; keep >= 1; keep-- { // all but the first, less at least one dropped
		short := segments[0] + "/…/" + strings.Join(segments[len(segments)-keep:], "/")
		if utf8.RuneCountInString(short) <= width {
			return short
		}
	}
	runes := []rune(segments[len(segments)-1])
	if len(segments) > 1 && len(runes)+len("…/") <= width {
		return "…/" + string(runes)
	}
	if width < 1 {
		return ""
	}

	return "…" + string(runes[max(0, len(runes)-width+1):])
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

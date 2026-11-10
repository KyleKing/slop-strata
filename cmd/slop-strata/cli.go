package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kyleking/slop-strata/internal/cache"
	"github.com/kyleking/slop-strata/internal/report"
)

// Flag names, spelled once for the command tables, the parser, and the usage.
const (
	flagAll           = "--all"
	flagBase          = "--base"
	flagContext       = "--context"
	flagExperimental  = "--experimental"
	flagHelp          = "--help"
	flagJSON          = "--json"
	flagPairs         = "--pairs"
	flagRepo          = "--repo"
	flagRev           = "--rev"
	flagSince         = "--since"
	flagWriteBaseline = "--write-baseline"
)

// args is one invocation's parsed flags.
type args struct {
	repo          string
	rev           string
	base          string
	since         string
	pairs         string
	json          bool
	ctx           bool
	all           bool
	writeBaseline bool
	experimental  bool
	help          bool
}

// command is one subcommand: its summary and the flags it accepts, in the order
// its usage prints them.
type command struct {
	name    string
	summary string
	flags   []string
}

// subcommands lists the subcommands in the order the top-level help prints them.
//
//nolint:gochecknoglobals // the command table is fixed at compile time
var subcommands = []command{
	{
		name:    "change",
		summary: "score the change at hand against its merge base",
		flags: []string{
			flagRev, flagBase, flagJSON, flagContext, flagAll, flagExperimental,
			flagPairs, flagRepo,
		},
	},
	{
		name:    "snapshot",
		summary: "score the whole tree at a revision",
		flags: []string{
			flagRev, flagJSON, flagContext, flagAll, flagWriteBaseline, flagExperimental,
			flagRepo,
		},
	},
	{
		name:    "history",
		summary: "walk first-parent merges over a range and report trends",
		flags:   []string{flagRev, flagSince, flagJSON, flagExperimental, flagRepo},
	},
	{
		name:    "init",
		summary: "write .slop-strata.toml and record the current baseline",
		flags:   []string{flagRev, flagAll, flagExperimental, flagRepo},
	},
}

// flagUsage documents each flag: its value placeholder, empty for a switch, and
// what it does.
//
//nolint:gochecknoglobals // the flag vocabulary is fixed at compile time
var flagUsage = map[string][2]string{
	flagRev:           {"R", "revision to score (default HEAD; change reads the working tree when unset)"},
	flagBase:          {"B", "base revision to diff from (default merge-base with the default branch)"},
	flagSince:         {"S", `history window, e.g. "180 days ago" (default "180 days ago")`},
	flagPairs:         {"FILE", `score each "<base> <rev>" line of FILE (- for stdin), one JSON envelope per pair`},
	flagRepo:          {"PATH", "repository root (default .)"},
	flagJSON:          {"", "emit JSON, the integration surface"},
	flagContext:       {"", "add the hunks, source lines, and messages a model needs (JSON only)"},
	flagAll:           {"", "show every number, not only what moved"},
	flagWriteBaseline: {"", "record current findings as the baseline"},
	flagExperimental:  {"", "also run rules below the 80% precision bar (see --explain experimental)"},
	flagHelp:          {"", "show this help"},
}

// findCommand returns the subcommand named name.
func findCommand(name string) (command, bool) {
	for _, c := range subcommands {
		if c.name == name {
			return c, true
		}
	}

	return command{}, false
}

// parseFlags reads one subcommand's flags from argv into a. A flag the command
// does not list, a positional argument, or a value flag without a value is a
// usage error.
func parseFlags(cmd command, argv []string, a *args) error {
	for len(argv) > 0 {
		var err error
		argv, err = parseFlag(cmd, argv, a)
		if err != nil {
			return err
		}
	}

	return nil
}

// parseFlag reads the flag at the head of argv and returns what follows it.
func parseFlag(cmd command, argv []string, a *args) ([]string, error) {
	name, inline, hasInline := strings.Cut(argv[0], "=")
	rest := argv[1:]
	if name == "-h" {
		name = flagHelp
	}
	if !slices.Contains(cmd.flags, name) && name != flagHelp {
		if strings.HasPrefix(argv[0], "-") {
			return nil, fmt.Errorf("unknown flag %s for %s: %w", name, cmd.name, errUsage)
		}

		return nil, fmt.Errorf("unexpected argument %s: %w", argv[0], errUsage)
	}
	if name == flagHelp {
		a.help = true

		return rest, nil
	}
	if dst, ok := switchFlags(a)[name]; ok {
		if hasInline {
			return nil, fmt.Errorf("%s takes no value: %w", name, errUsage)
		}
		*dst = true

		return rest, nil
	}
	value := inline
	if !hasInline {
		if len(rest) == 0 {
			return nil, fmt.Errorf("%s needs a value: %w", name, errUsage)
		}
		value, rest = rest[0], rest[1:]
	}
	if value == "" {
		return nil, fmt.Errorf("%s needs a non-empty value: %w", name, errUsage)
	}
	*valueFlags(a)[name] = value

	return rest, nil
}

// switchFlags binds each boolean flag to its field of a.
func switchFlags(a *args) map[string]*bool {
	return map[string]*bool{
		flagJSON: &a.json, flagContext: &a.ctx, flagAll: &a.all,
		flagWriteBaseline: &a.writeBaseline, flagExperimental: &a.experimental,
	}
}

// valueFlags binds each flag that takes a value to its field of a.
func valueFlags(a *args) map[string]*string {
	return map[string]*string{
		flagRev: &a.rev, flagBase: &a.base, flagSince: &a.since,
		flagRepo: &a.repo, flagPairs: &a.pairs,
	}
}

// printHelp writes the top-level usage.
func printHelp(w io.Writer) {
	report.Fprint(w, `slop-strata — score code quality on a change and over history

Usage: slop-strata <command> [flags]   (try "change" first)

Commands:
`)
	for _, c := range subcommands {
		report.Fprintf(w, "  %-10s %s\n", c.name, c.summary)
	}
	report.Fprint(w, `
Run "slop-strata <command> --help" for that command's flags.

Global:
  --explain NAME   print a metric's doc: erosion, mass, churn_30d, survival, ...
  -v, --version    print the version (with --json: {"version", "cache"})
  -h, --help       this text

Every number is a guardrail with no target and no gate.
`)
}

// printCommandHelp writes one subcommand's usage with only its flags.
func printCommandHelp(w io.Writer, cmd command) {
	report.Fprintf(w, "slop-strata %s — %s\n\nUsage: slop-strata %s [flags]\n\nFlags:\n",
		cmd.name, cmd.summary, cmd.name)
	for _, name := range append(slices.Clone(cmd.flags), "--help") {
		info := flagUsage[name]
		left := name
		if info[0] != "" {
			left += " " + info[0]
		}
		report.Fprintf(w, "  %-22s %s\n", left, info[1])
	}
}

// printVersion writes the version, as text or with --json as the version and the
// cache file name a run would use.
func printVersion(argv []string, w io.Writer) error {
	asJSON := false
	for _, arg := range argv {
		if arg != "--json" {
			return fmt.Errorf("--version takes only --json, got %s: %w", arg, errUsage)
		}
		asJSON = true
	}
	if !asJSON {
		report.Fprintf(w, "slop-strata %s (commit: %s, built: %s)\n", version, commit, date)

		return nil
	}
	path, err := cache.Path(version)
	if err != nil {
		return fmt.Errorf("locating cache: %w", err)
	}

	if err := json.NewEncoder(w).Encode(map[string]string{
		"version": version, "cache": filepath.Base(path),
	}); err != nil {
		return fmt.Errorf("writing version: %w", err)
	}

	return nil
}

// run dispatches argv: global flags first, then one subcommand. Usage errors are
// all checked before the repository is opened.
func run(ctx context.Context, argv []string, stdout, stderr io.Writer) error {
	if len(argv) == 0 {
		printHelp(stdout)

		return nil
	}
	switch argv[0] {
	case "-h", "--help", "help":
		printHelp(stdout)

		return nil
	case "-v", "--version":
		return printVersion(argv[1:], stdout)
	}
	if strings.HasPrefix(argv[0], "--explain") {
		return runExplain(argv, stdout)
	}
	if strings.HasPrefix(argv[0], "-") {
		return fmt.Errorf("unknown flag %s before the subcommand: %w", argv[0], errUsage)
	}
	cmd, ok := findCommand(argv[0])
	if !ok {
		return fmt.Errorf("%q: %w: %w", argv[0], errUnknownCmd, errUsage)
	}
	a := &args{repo: "."}
	if err := parseFlags(cmd, argv[1:], a); err != nil {
		return err
	}
	if a.help {
		printCommandHelp(stdout, cmd)

		return nil
	}

	s, err := newSession(ctx, a.repo, a.experimental, stderr)
	if err != nil {
		return err
	}
	defer s.close()

	switch cmd.name {
	case "change":
		return s.changeCmd(ctx, a, stdout)
	case "snapshot":
		return s.snapshotCmd(ctx, a, stdout)
	case "history":
		return s.historyCmd(ctx, a, stdout)
	default:
		return s.initCmd(ctx, a, stdout)
	}
}

// runExplain prints one metric's doc for "--explain NAME" or "--explain=NAME".
func runExplain(argv []string, stdout io.Writer) error {
	name, inline, hasInline := strings.Cut(argv[0], "=")
	if name != "--explain" {
		return fmt.Errorf("unknown flag %s before the subcommand: %w", argv[0], errUsage)
	}
	rest := argv[1:]
	value := inline
	if !hasInline {
		if len(rest) == 0 {
			return fmt.Errorf("--explain needs a metric name: %w", errUsage)
		}
		value, rest = rest[0], rest[1:]
	}
	if len(rest) > 0 {
		return fmt.Errorf("unexpected argument %s: %w", rest[0], errUsage)
	}
	if value == "" {
		return fmt.Errorf("--explain needs a non-empty metric name: %w", errUsage)
	}
	if !report.Explain(stdout, value) {
		return fmt.Errorf("%q: %w", value, errNotMetric)
	}

	return nil
}

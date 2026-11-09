package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/kyleking/aragonite/vcs"

	"github.com/kyleking/slop-strata/internal/cache"
	"github.com/kyleking/slop-strata/internal/config"
	"github.com/kyleking/slop-strata/internal/engine"
	"github.com/kyleking/slop-strata/internal/report"
)

// errUsage marks a bad command line.
var errUsage = errors.New("usage error")

// errNotMetric marks an unknown --explain name.
var errNotMetric = errors.New("no such metric")

// errNotRepo marks a path that is not a repository.
var errNotRepo = errors.New("not a repository")

// errUnknownCmd marks a name that is not a subcommand.
var errUnknownCmd = errors.New("unknown subcommand")

// args is one invocation's parsed flags.
type args struct {
	cmd           string
	repo          string
	rev           string
	base          string
	since         string
	json          bool
	ctx           bool
	all           bool
	writeBaseline bool
	explain       string
	help          bool
}

// parseArgs scans flags positionally; every flag takes --name or --name=value,
// and --rev/--base/--since take a following value too.
func parseArgs(argv []string) (*args, error) {
	a := &args{cmd: "", repo: "."}
	rest := argv
	value := func(i *int, inline string) (string, error) {
		if inline != "" {
			return inline, nil
		}
		if *i+1 >= len(rest) {
			return "", fmt.Errorf("flag needs a value: %w", errUsage)
		}
		*i++

		return rest[*i], nil
	}
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		name, inline, _ := strings.Cut(arg, "=")
		switch name {
		case "--json":
			a.json = true
		case "--context":
			a.ctx = true
		case "--all":
			a.all = true
		case "--write-baseline":
			a.writeBaseline = true
		case "-h", "--help", "help":
			a.help = true
		case "--rev":
			v, err := value(&i, inline)
			if err != nil {
				return nil, fmt.Errorf("--rev: %w", err)
			}
			a.rev = v
		case "--base":
			v, err := value(&i, inline)
			if err != nil {
				return nil, fmt.Errorf("--base: %w", err)
			}
			a.base = v
		case "--since":
			v, err := value(&i, inline)
			if err != nil {
				return nil, fmt.Errorf("--since: %w", err)
			}
			a.since = v
		case "--repo":
			v, err := value(&i, inline)
			if err != nil {
				return nil, fmt.Errorf("--repo: %w", err)
			}
			a.repo = v
		case "--explain":
			v, err := value(&i, inline)
			if err != nil {
				return nil, fmt.Errorf("--explain: %w", err)
			}
			a.explain = v
		default:
			if strings.HasPrefix(arg, "--") {
				return nil, fmt.Errorf("unknown flag %s: %w", arg, errUsage)
			}
			if a.cmd == "" {
				a.cmd = arg
			} else {
				return nil, fmt.Errorf("unexpected argument %s: %w", arg, errUsage)
			}
		}
	}

	return a, nil
}

// session bundles what every subcommand needs.
type session struct {
	ops   *vcs.GitOperations
	cfg   *config.Config
	eng   *engine.Analyzer
	store *cache.Store
	root  string
}

func newSession(ctx context.Context, repoPath string) (*session, error) {
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, err
	}
	if !vcs.IsRepo(abs) {
		return nil, fmt.Errorf("%s: %w", abs, errNotRepo)
	}
	cfg, err := config.Load(abs)
	if err != nil {
		return nil, err
	}
	cachePath, err := cache.Path(version)
	if err != nil {
		cachePath = ""
	}
	store, err := cache.Open(cachePath)
	if err != nil {
		return nil, err
	}

	return &session{
		ops: vcs.NewGitOperations(), cfg: cfg,
		eng: engine.New(cfg, store), store: store, root: abs,
	}, nil
}

func (s *session) close() {
	if s.store != nil {
		s.store.Close() //nolint:errcheck // close failure loses nothing
	}
}

func run(ctx context.Context, argv []string, stdout io.Writer) error {
	a, err := parseArgs(argv)
	if err != nil {
		return err
	}
	if a.explain != "" {
		if !report.Explain(stdout, a.explain) {
			return fmt.Errorf("%q: %w", a.explain, errNotMetric)
		}

		return nil
	}
	if a.help || a.cmd == "" {
		printHelp(stdout)

		return nil
	}

	s, err := newSession(ctx, a.repo)
	if err != nil {
		return err
	}
	defer s.close()

	switch a.cmd {
	case "change":
		return s.changeCmd(ctx, a, stdout)
	case "snapshot":
		return s.snapshotCmd(ctx, a, stdout)
	case "history":
		return s.historyCmd(ctx, a, stdout)
	default:
		return fmt.Errorf("%q: %w", a.cmd, errUnknownCmd)
	}
}

func printHelp(w io.Writer) {
	report.Fprint(w, `slop-strata — score code quality on a change and over history

Usage: slop-strata <command> [flags]   (try "change" first)

Commands:
  change     score the change at hand against its merge base
  snapshot   score the whole tree at a revision
  history    walk first-parent merges over a range and report trends

Flags:
  --rev R          end revision (change/snapshot; default HEAD or worktree)
  --base B         base revision (change; default merge-base of default branch)
  --since S        history window, e.g. "180 days ago"
  --repo PATH      repository root (default .)
  --json           emit JSON (the integration surface)
  --context        include the diff hunks, source lines, and messages a model
                   would need to explain the result (JSON mode)
  --all            show every number, not only what moved
  --write-baseline record current findings as the baseline (snapshot)
  --explain NAME   print a metric's doc: erosion, mass, churn_30d, survival, ...
  -h, --help       this text
  -v, --version    version

Every number is a guardrail with no target and no gate.
`)
}

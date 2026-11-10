// Package config reads .slop-strata.toml, the per-repository settings every
// subcommand shares.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// FileName is the config file every subcommand looks for at the repo root.
const FileName = ".slop-strata.toml"

// BaselineFileName is the default file holding findings already accepted, so a
// ruleset tightened after code landed does not re-report it.
const BaselineFileName = ".slop-strata-baseline.json"

const (
	// The byte cap past which a blob is skipped and reported by name.
	defaultMaxFileBytes = 1 << 20
	// The average line length past which a blob reads as minified.
	defaultMinifiedAvgLine = 200
)

// AuthorClass groups commits by who wrote them, so every metric can split
// agent-authored from person-authored work. Classes are tried in order and the
// first match wins; a commit matching none is "person".
type AuthorClass struct {
	// Name is the label reported in output, e.g. "bot" or "agent".
	Name string `toml:"name"`
	// Logins are author and committer names or emails that mark the class. A
	// login starting with `*` matches an identity whose name or email local part
	// ends with the rest, e.g. "*[bot]".
	Logins []string `toml:"logins"`
	// Trailer patterns match against the commit message body, one regexp per
	// entry. They cover co-author trailers a bot leaves, e.g.
	// `Co-Authored-By:.*\[bot\]`.
	Trailers []string `toml:"trailers"`

	trailerRe []*regexp.Regexp
}

// Matches reports whether a commit belongs to the class.
func (a *AuthorClass) Matches(author, committer, body string) bool {
	for _, login := range a.Logins {
		if identityMatches(author, login) || identityMatches(committer, login) {
			return true
		}
	}
	for _, re := range a.trailerRe {
		if re.MatchString(body) {
			return true
		}
	}

	return false
}

// identityMatches reports whether a login names an identity written as
// `Name <email>`, ignoring case.
func identityMatches(identity, login string) bool {
	name, email := identity, ""
	if i := strings.LastIndex(identity, " <"); i >= 0 && strings.HasSuffix(identity, ">") {
		name, email = identity[:i], identity[i+len(" <"):len(identity)-1]
	}
	if suffix, ok := strings.CutPrefix(login, "*"); ok {
		local, _, _ := strings.Cut(email, "@")

		return hasSuffixFold(name, suffix) || hasSuffixFold(local, suffix)
	}

	return strings.EqualFold(name, login) || strings.EqualFold(email, login)
}

func hasSuffixFold(s, suffix string) bool {
	return strings.HasSuffix(strings.ToLower(s), strings.ToLower(suffix))
}

// Config is the parsed .slop-strata.toml.
type Config struct {
	// Baseline names the file of accepted findings relative to the repo root.
	Baseline string `toml:"baseline"`
	// DefaultBranch overrides the detected branch a change diffs against.
	DefaultBranch string `toml:"default_branch"`
	// Skip lists path globs (filepath.Match syntax) excluded from every
	// analysis: vendored trees, generated code, lockfiles.
	Skip []string `toml:"skip"`
	// AuthorClasses classifies commits in order; first match wins.
	AuthorClasses []AuthorClass `toml:"author_class"`
	// AstGrep lists ast-grep YAML rule files or directories, relative to the
	// repository root, loaded through `sg` as an optional external rule source.
	AstGrep []string `toml:"ast_grep"`
	// EntryPoints lists path globs (filepath.Match syntax) whose exports a
	// dead-export read never reports: CLI mains, package entry modules,
	// plugin registrations.
	EntryPoints []string `toml:"entry_points"`
	// ConfigPaths lists path globs (filepath.Match syntax) naming the files
	// the repository treats as application config. Only they feed the
	// stale-config read, so tool and CI config stays out of it.
	ConfigPaths []string `toml:"config_paths"`
	// MaxFileBytes skips blobs above the cap; minified bundles and vendored
	// output trip it and are reported by name rather than parsed.
	MaxFileBytes int64 `toml:"max_file_bytes"`
	// MinifiedAvgLine marks a blob minified when its average line length
	// passes this; those are skipped with the same warning as oversize files.
	MinifiedAvgLine int `toml:"minified_avg_line"`
}

// Default returns the configuration a repository gets with no file.
func Default() *Config {
	return &Config{
		MaxFileBytes:    defaultMaxFileBytes,
		MinifiedAvgLine: defaultMinifiedAvgLine,
		Baseline:        BaselineFileName,
	}
}

// defaultAuthorClasses are tried after the repository's own classes, so a
// repository can claim a login for a different class.
func defaultAuthorClasses() []AuthorClass {
	return []AuthorClass{
		{
			Name:     "agent",
			Trailers: []string{`(?im)^co-authored-by:.*(claude|copilot|cursor|codex|devin|aider)`},
		},
		{
			Name:     "bot",
			Logins:   []string{"*[bot]"},
			Trailers: []string{`(?i)co-authored-by:.*\[bot\]`},
		},
	}
}

// Load reads .slop-strata.toml under root, falling back to Default when the
// file does not exist. The default author classes follow the file's own.
func Load(root string) (*Config, error) {
	cfg := Default()

	data, err := os.ReadFile(filepath.Join(root, FileName)) //nolint:gosec // root is the caller's repo path
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("reading %s: %w", FileName, err)
	default:
		dec := toml.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(cfg); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", FileName, err)
		}
	}
	cfg.AuthorClasses = append(cfg.AuthorClasses, defaultAuthorClasses()...)

	cerr := cfg.compile()

	return cfg, cerr
}

func (c *Config) compile() error {
	for i := range c.AuthorClasses {
		for _, pattern := range c.AuthorClasses[i].Trailers {
			re, err := regexp.Compile(pattern)
			if err != nil {
				return fmt.Errorf("author_class %q trailer %q: %w", c.AuthorClasses[i].Name, pattern, err)
			}
			c.AuthorClasses[i].trailerRe = append(c.AuthorClasses[i].trailerRe, re)
		}
	}

	return nil
}

// Classify returns the author class name for a commit, or "person".
func (c *Config) Classify(author, committer, body string) string {
	for i := range c.AuthorClasses {
		if c.AuthorClasses[i].Matches(author, committer, body) {
			return c.AuthorClasses[i].Name
		}
	}

	return "person"
}

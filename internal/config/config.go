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

	"github.com/pelletier/go-toml/v2"
)

// FileName is the config file every subcommand looks for at the repo root.
const FileName = ".slop-strata.toml"

// BaselineFileName is the default file holding findings already accepted, so a
// ruleset tightened after code landed does not re-report it.
const BaselineFileName = ".slop-strata-baseline.json"

// AuthorClass groups commits by who wrote them, so every metric can split
// agent-authored from person-authored work. Classes are tried in order and the
// first match wins; a commit matching none is "person".
type AuthorClass struct {
	// Name is the label reported in output, e.g. "bot" or "agent".
	Name string `toml:"name"`
	// Logins are author and committer names or emails that mark the class.
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
		if author == login || committer == login {
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

// Config is the parsed .slop-strata.toml.
type Config struct {
	// MaxFileBytes skips blobs above the cap; minified bundles and vendored
	// output trip it and are reported by name rather than parsed.
	MaxFileBytes int64 `toml:"max_file_bytes"`
	// MinifiedAvgLine marks a blob minified when its average line length
	// passes this; those are skipped with the same warning as oversize files.
	MinifiedAvgLine int `toml:"minified_avg_line"`
	// Skip lists path globs (filepath.Match syntax) excluded from every
	// analysis: vendored trees, generated code, lockfiles.
	Skip []string `toml:"skip"`
	// Baseline names the file of accepted findings relative to the repo root.
	Baseline string `toml:"baseline"`
	// DefaultBranch overrides the detected branch a change diffs against.
	DefaultBranch string `toml:"default_branch"`
	// AuthorClasses classifies commits in order; first match wins.
	AuthorClasses []AuthorClass `toml:"author_class"`
	// AstGrep lists ast-grep YAML rule files or directories, relative to the
	// repository root, loaded through `sg` as an optional external rule source.
	AstGrep []string `toml:"ast_grep"`
}

// Default returns the configuration a repository gets with no file.
func Default() *Config {
	return &Config{
		MaxFileBytes:    1 << 20,
		MinifiedAvgLine: 200,
		Baseline:        BaselineFileName,
		AuthorClasses: []AuthorClass{
			{Name: "bot", Logins: nil, Trailers: []string{`(?i)co-authored-by:.*\[bot\]`}},
		},
	}
}

// Load reads .slop-strata.toml under root, falling back to Default when the
// file does not exist.
func Load(root string) (*Config, error) {
	cfg := Default()

	data, err := os.ReadFile(filepath.Join(root, FileName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cfg, cfg.compile()
		}

		return nil, fmt.Errorf("reading %s: %w", FileName, err)
	}

	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", FileName, err)
	}

	return cfg, cfg.compile()
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

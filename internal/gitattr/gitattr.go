// Package gitattr reads the linguist-generated attribute from a repository's
// root .gitattributes, which marks files a generator wrote.
package gitattr

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// fileName is the attributes file read from the repository root.
const fileName = ".gitattributes"

// generatedAttr is the attribute name linguist reads.
const generatedAttr = "linguist-generated"

// rule is one .gitattributes line that sets the attribute on the paths it matches.
type rule struct {
	pattern  string
	basename bool
	set      bool
}

// Matcher answers whether a path carries linguist-generated.
type Matcher struct {
	rules []rule
}

// Load reads the root .gitattributes of the repository at root. A repository
// without one has no generated paths.
func Load(root string) (*Matcher, error) {
	src, err := os.ReadFile(filepath.Join(root, fileName)) //nolint:gosec // root is the repository the user named
	if errors.Is(err, os.ErrNotExist) {
		return &Matcher{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", fileName, err)
	}

	return Parse(src)
}

// Parse reads .gitattributes content. Patterns take the subset of git's globs
// that name a file or a root-relative path: a pattern without a slash, or
// prefixed with **/, matches the file name at any depth.
func Parse(src []byte) (*Matcher, error) {
	var m Matcher
	sc := bufio.NewScanner(bytes.NewReader(src))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		r, ok, err := parseRule(fields[0], fields[1:])
		if err != nil {
			return nil, err
		}
		if ok {
			m.rules = append(m.rules, r)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", fileName, err)
	}

	return &m, nil
}

// parseRule reads one line's attributes. The last linguist-generated token on the
// line decides the rule, and a line without one yields no rule.
func parseRule(pattern string, attrs []string) (rule, bool, error) {
	var set, found bool
	for _, a := range attrs {
		switch a {
		case generatedAttr, generatedAttr + "=true":
			set, found = true, true
		case "-" + generatedAttr, "!" + generatedAttr, generatedAttr + "=false":
			set, found = false, true
		}
	}
	if !found {
		return rule{}, false, nil
	}
	r := rule{set: set}
	switch rest, deep := strings.CutPrefix(pattern, "**/"); {
	case deep:
		r.pattern, r.basename = rest, true
	case strings.Contains(pattern, "/"):
		r.pattern = strings.TrimPrefix(pattern, "/")
	default:
		r.pattern, r.basename = pattern, true
	}
	if _, err := path.Match(r.pattern, ""); err != nil {
		return rule{}, false, fmt.Errorf("pattern %q in %s: %w", pattern, fileName, err)
	}

	return r, true, nil
}

// Generated reports whether the last rule matching p sets linguist-generated.
func (m *Matcher) Generated(p string) bool {
	generated := false
	for _, r := range m.rules {
		name := p
		if r.basename {
			name = path.Base(p)
		}
		if ok, err := path.Match(r.pattern, name); err == nil && ok {
			generated = r.set
		}
	}

	return generated
}

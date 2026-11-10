package rules_test

import (
	"testing"

	"github.com/kyleking/slop-strata/internal/lang"
	"github.com/kyleking/slop-strata/internal/rules"
)

// findingsOf parses one snippet as path's language and runs every per-blob rule
// on it, as the engine does.
func findingsOf(t *testing.T, path, src string, experimental bool) []rules.Finding {
	t.Helper()
	l := lang.ByPath(path)
	if l == nil {
		t.Fatalf("no grammar for %s", path)
	}
	content := []byte(src)
	tree := lang.NewPool().Parse(l, content)
	defer tree.Close()

	return rules.Run(&rules.Ctx{
		Lang: l, Root: tree.RootNode(), Src: content, Path: path,
		Test: l.IsTestFile(path), Experimental: experimental,
	})
}

// printOf fingerprints one file the way the engine does before the cross-file passes.
func printOf(t *testing.T, path, src string) *rules.Fingerprint {
	t.Helper()
	l := lang.ByPath(path)
	if l == nil {
		return rules.CollectText(path, []byte(src))
	}
	content := []byte(src)
	tree := lang.NewPool().Parse(l, content)
	defer tree.Close()

	return rules.Collect(&rules.Ctx{
		Lang: l, Root: tree.RootNode(), Src: content, Path: path, Test: l.IsTestFile(path),
	})
}

// crossFileOf runs the cross-file passes over a set of files and returns the
// findings for each path.
func crossFileOf(t *testing.T, files map[string]string, experimental bool) map[string][]rules.Finding {
	t.Helper()
	blobs := make([]rules.BlobPrint, 0, len(files))
	treePaths := make([]string, 0, len(files))
	for path, src := range files {
		treePaths = append(treePaths, path)
		blobs = append(blobs, rules.BlobPrint{Path: path, Print: printOf(t, path, src)})
	}

	return rules.CrossFile(blobs, treePaths, nil, nil, experimental)
}

// countRule counts the findings carrying one rule id.
func countRule(fs []rules.Finding, rule string) int {
	n := 0
	for _, f := range fs {
		if f.Rule == rule {
			n++
		}
	}

	return n
}

type gateCase struct {
	name         string
	rule         string
	experimental bool
	want         int
}

func TestExperimentalRulesWaitForFlag(t *testing.T) {
	t.Parallel()
	const src = "from typing import Any\n\n\ndef f(x: Any) -> None:\n" +
		"    try:\n        g(x)\n    except ValueError:\n        pass\n"
	cases := []gateCase{
		{name: "experimental type-any off by default", rule: "py/type-any", experimental: false, want: 0},
		{name: "experimental type-any on with flag", rule: "py/type-any", experimental: true, want: 1},
		{name: "stable silent-except runs by default", rule: "py/silent-except", experimental: false, want: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := countRule(findingsOf(t, "f.py", src, tc.experimental), tc.rule)
			if got != tc.want {
				t.Errorf("%s hits = %d, want %d", tc.rule, got, tc.want)
			}
		})
	}
}

func TestExperimentalCrossFileWaitsForFlag(t *testing.T) {
	t.Parallel()
	files := map[string]string{"pkg/mod.go": "package pkg\n\nfunc Unused() {}\n"}
	cases := []gateCase{
		{name: "dead export off by default", rule: "x/dead-export", experimental: false, want: 0},
		{name: "dead export on with flag", rule: "x/dead-export", experimental: true, want: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := countRule(flatten(crossFileOf(t, files, tc.experimental)), tc.rule)
			if got != tc.want {
				t.Errorf("%s hits = %d, want %d", tc.rule, got, tc.want)
			}
		})
	}
}

// flatten joins per-path findings into one slice.
func flatten(byPath map[string][]rules.Finding) []rules.Finding {
	var out []rules.Finding
	for _, fs := range byPath {
		out = append(out, fs...)
	}

	return out
}

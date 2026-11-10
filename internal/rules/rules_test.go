package rules_test

import (
	"fmt"
	"slices"
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

// hitCase is one snippet and how many findings of one rule it must produce.
type hitCase struct {
	name string
	path string
	src  string
	rule string
	want int
}

func TestRuleHits(t *testing.T) {
	t.Parallel()
	for _, tc := range slices.Concat(
		goHitCases(), goReadsCases(), pyOrDefaultCases(), pyReadsCases(), tsHitCases(),
	) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := countRule(findingsOf(t, tc.path, tc.src, true), tc.rule)
			if got != tc.want {
				t.Errorf("%s hits = %d, want %d", tc.rule, got, tc.want)
			}
		})
	}
}

func goHitCases() []hitCase {
	const silent = "go/silent-error"

	return []hitCase{
		{
			name: "discarded error from a call fires",
			path: "remove.go",
			src:  "package p\n\nfunc g(name string) {\n\t_ = os.Remove(name)\n}\n",
			rule: silent, want: 1,
		},
		{
			name: "discarded close is allowed",
			path: "close.go",
			src:  "package p\n\nfunc g(conn io.Closer) {\n\t_ = conn.Close()\n}\n",
			rule: silent, want: 0,
		},
		{
			name: "stat checked in an if init stays quiet",
			path: "stat.go",
			src: "package p\n\nfunc g(p string) bool {\n\tif _, err := os.Stat(p); err == nil {\n" +
				"\t\treturn true\n\t}\n\treturn false\n}\n",
			rule: silent, want: 0,
		},
		{
			name: "strings.Cut bool in the blank stays quiet",
			path: "cut.go",
			src:  "package p\n\nfunc g(s string) string {\n\tv, _ := strings.Cut(s, \":\")\n\treturn v\n}\n",
			rule: silent, want: 0,
		},
		{
			name: "atoi error in the blank fires",
			path: "atoi.go",
			src:  "package p\n\nfunc g(s string) int {\n\tn, _ := strconv.Atoi(s)\n\treturn n\n}\n",
			rule: silent, want: 1,
		},
		{
			name: "blank in a non-last position stays quiet",
			path: "first.go",
			src:  "package p\n\nfunc g() int {\n\t_, n := f()\n\treturn n\n}\n",
			rule: silent, want: 0,
		},
		{
			name: "map index with blank stays quiet",
			path: "index.go",
			src:  "package p\n\nfunc g(m map[string]int) int {\n\tv, _ := m[\"k\"]\n\treturn v\n}\n",
			rule: silent, want: 0,
		},
		{
			name: "channel receive with blank stays quiet",
			path: "recv.go",
			src:  "package p\n\nfunc g(ch chan int) int {\n\tv, _ := <-ch\n\treturn v\n}\n",
			rule: silent, want: 0,
		},
		{
			name: "type assertion with blank stays quiet",
			path: "assert.go",
			src:  "package p\n\nfunc g(v any) string {\n\ts, _ := v.(string)\n\treturn s\n}\n",
			rule: silent, want: 0,
		},
		{
			name: "blank beside a bound err stays quiet",
			path: "open.go",
			src:  "package p\n\nfunc g(p string) error {\n\t_, err := os.Open(p)\n\treturn err\n}\n",
			rule: silent, want: 0,
		},
		{
			name: "all blank targets fire",
			path: "both.go",
			src:  "package p\n\nfunc g() {\n\t_, _ = f()\n}\n",
			rule: silent, want: 1,
		},
	}
}

func goReadsCases() []hitCase {
	const reads = "go/test-reads-source"

	return []hitCase{
		{
			name: "go repo file read through a parent path fires",
			path: "read_test.go",
			src: "package p\n\nfunc TestRead(t *testing.T) {\n" +
				"\t_, _ = os.ReadFile(\"../../internal/rules/rules.go\")\n}\n",
			rule: reads, want: 1,
		},
		{
			name: "go testdata read stays quiet",
			path: "testdata_test.go",
			src: "package p\n\nfunc TestRead(t *testing.T) {\n" +
				"\t_, _ = os.ReadFile(filepath.Join(\"testdata\", \"case.json\"))\n}\n",
			rule: reads, want: 0,
		},
		{
			name: "go temp dir read stays quiet",
			path: "temp_test.go",
			src: "package p\n\nfunc TestRead(t *testing.T) {\n" +
				"\t_, _ = os.ReadFile(filepath.Join(t.TempDir(), \"case.json\"))\n}\n",
			rule: reads, want: 0,
		},
		{
			name: "go root helper read fires",
			path: "root_test.go",
			src:  "package p\n\nfunc TestRead(t *testing.T) {\n\t_, _ = os.ReadFile(root + \"/go.mod\")\n}\n",
			rule: reads, want: 1,
		},
		{
			name: "go stream read stays quiet",
			path: "stream_test.go",
			src:  "package p\n\nfunc TestRead(t *testing.T) {\n\t_, _ = io.ReadAll(resp.Body)\n}\n",
			rule: reads, want: 0,
		},
	}
}

// crossCase is a set of files and how many findings of one cross-file rule the
// passes must report across them.
type crossCase struct {
	name  string
	files map[string]string
	rule  string
	want  int
}

func TestCrossFileHits(t *testing.T) {
	t.Parallel()
	for _, tc := range slices.Concat(crossHitCases(), staleProseCases()) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := countRule(flatten(crossFileOf(t, tc.files, true)), tc.rule)
			if got != tc.want {
				t.Errorf("%s hits = %d, want %d", tc.rule, got, tc.want)
			}
		})
	}
}

// repeated builds n files that each carry the same snippet body.
func repeated(n int, body string) map[string]string {
	files := make(map[string]string, n)
	for i := range n {
		files[fmt.Sprintf("pkg%d/file.go", i)] = "package p\n\nfunc g() {\n" + body + "}\n"
	}

	return files
}

func crossHitCases() []crossCase {
	const dup = "x/dup-literal"
	const call = "\tuse(\"https://example.org/feed/updates\")\n"

	return []crossCase{
		{
			name:  "literal in three files once each stays quiet",
			files: repeated(3, call),
			rule:  dup, want: 0,
		},
		{
			name:  "literal in three files twice each fires per file",
			files: repeated(3, call+call),
			rule:  dup, want: 3,
		},
		{
			name:  "literal of exactly the minimum content length fires",
			files: repeated(4, "\tuse(\"01234567\")\n"),
			rule:  dup, want: 4,
		},
		{
			name:  "literal under the minimum content length stays quiet",
			files: repeated(4, "\tuse(\"0123456\")\n"),
			rule:  dup, want: 0,
		},
		{
			name: "go import path repeated across files stays quiet",
			files: map[string]string{
				"a/one.go":   "package p\n\nimport \"github.com/example/project/internal/store\"\n",
				"a/two.go":   "package p\n\nimport \"github.com/example/project/internal/store\"\n",
				"a/three.go": "package p\n\nimport \"github.com/example/project/internal/store\"\n",
				"a/four.go":  "package p\n\nimport \"github.com/example/project/internal/store\"\n",
			},
			rule: dup, want: 0,
		},
		{
			name: "python import and dict key repeated across files stay quiet",
			files: map[string]string{
				"a/one.py":   "from pkg.reporting import run\nDEFAULTS = {\"reporting_window_days\": 30}\n",
				"a/two.py":   "from pkg.reporting import run\nDEFAULTS = {\"reporting_window_days\": 30}\n",
				"a/three.py": "from pkg.reporting import run\nDEFAULTS = {\"reporting_window_days\": 30}\n",
				"a/four.py":  "from pkg.reporting import run\nDEFAULTS = {\"reporting_window_days\": 30}\n",
			},
			rule: dup, want: 0,
		},
		{
			name: "typescript require and go map key repeated across files stay quiet",
			files: map[string]string{
				"a/one.ts":   "const m = require(\"./modules/reporting-window\")\n",
				"a/two.ts":   "const m = require(\"./modules/reporting-window\")\n",
				"a/three.ts": "const m = require(\"./modules/reporting-window\")\n",
				"a/four.ts":  "const m = require(\"./modules/reporting-window\")\n",
			},
			rule: dup, want: 0,
		},
	}
}

func pyOrDefaultCases() []hitCase {
	const orDefault = "py/or-default"

	return []hitCase{
		{
			name: "none default replaced by an empty list fires",
			path: "items.py",
			src:  "def f(items=None):\n    return items or []\n",
			rule: orDefault, want: 1,
		},
		{
			name: "none default replaced by an empty dict fires",
			path: "mapping.py",
			src:  "def f(mapping=None):\n    return mapping or {}\n",
			rule: orDefault, want: 1,
		},
		{
			name: "get fallback to an empty string stays quiet",
			path: "get.py",
			src:  "def f(d, k):\n    return d.get(k) or \"\"\n",
			rule: orDefault, want: 0,
		},
		{
			name: "string literal fallback on a parameter stays quiet",
			path: "name.py",
			src:  "def f(name):\n    return name or \"unknown\"\n",
			rule: orDefault, want: 0,
		},
		{
			name: "zero fallback on a parameter stays quiet",
			path: "count.py",
			src:  "def f(n):\n    return n or 0\n",
			rule: orDefault, want: 0,
		},
		{
			name: "call result fallback stays quiet",
			path: "call.py",
			src:  "def f():\n    total = compute() or []\n    return total\n",
			rule: orDefault, want: 0,
		},
		{
			name: "local variable fallback stays quiet",
			path: "local.py",
			src:  "def f():\n    rows = []\n    return rows or []\n",
			rule: orDefault, want: 0,
		},
	}
}

func pyReadsCases() []hitCase {
	const reads = "py/test-reads-source"

	return []hitCase{
		{
			name: "repo file read through __file__ fires",
			path: "test_pkg.py",
			src: "from pathlib import Path\n\n\ndef test_x():\n" +
				"    text = (Path(__file__).parent.parent / \"pyproject.toml\").read_text()\n    assert text\n",
			rule: reads, want: 1,
		},
		{
			name: "fixture under testdata stays quiet",
			path: "test_fixture.py",
			src: "from pathlib import Path\n\n\ndef test_x():\n" +
				"    text = (Path(__file__).parent / \"testdata\" / \"case.json\").read_text()\n    assert text\n",
			rule: reads, want: 0,
		},
		{
			name: "tmp_path output stays quiet",
			path: "test_tmp.py",
			src:  "def test_x(tmp_path):\n    assert (tmp_path / \"out.py\").read_text()\n",
			rule: reads, want: 0,
		},
		{
			name: "parent-relative open fires",
			path: "test_docs.py",
			src:  "def test_x():\n    with open(\"../README.md\") as f:\n        assert f.read()\n",
			rule: reads, want: 1,
		},
		{
			name: "write mode open stays quiet",
			path: "test_write.py",
			src:  "def test_x():\n    with open(\"config.toml\", \"w\") as f:\n        f.write(\"\")\n",
			rule: reads, want: 0,
		},
		{
			name: "path passed in as a parameter stays quiet",
			path: "test_param.py",
			src:  "def test_x(path):\n    assert open(path).read()\n",
			rule: reads, want: 0,
		},
		{
			name: "ast.parse of a computed module source fires",
			path: "test_ast.py",
			src:  "import ast\n\n\ndef test_x(source):\n    assert ast.parse(source)\n",
			rule: reads, want: 1,
		},
		{
			name: "ast.parse of a literal snippet stays quiet",
			path: "test_snippet.py",
			src:  "import ast\n\n\ndef test_x():\n    assert ast.parse(\"x = 1\")\n",
			rule: reads, want: 0,
		},
		{
			name: "inspect.getsource fires",
			path: "test_inspect.py",
			src:  "import inspect\n\n\ndef test_x(mod):\n    assert inspect.getsource(mod)\n",
			rule: reads, want: 1,
		},
		{
			name: "data file outside the source extensions stays quiet",
			path: "test_csv.py",
			src:  "def test_x():\n    assert open(\"data.csv\").read()\n",
			rule: reads, want: 0,
		},
	}
}

func tsHitCases() []hitCase {
	const reads = "ts/test-reads-source"
	return []hitCase{
		{
			name: "config read through __dirname fires",
			path: "reader.test.ts",
			src: "import { readFileSync } from \"fs\";\nimport path from \"path\";\n" +
				"test(\"reads\", () => {\n  readFileSync(path.join(__dirname, \"../config.json\"), \"utf8\");\n});\n",
			rule: reads, want: 1,
		},
		{
			name: "temp directory read stays quiet",
			path: "temp.test.ts",
			src: "import { readFileSync } from \"fs\";\n" +
				"test(\"reads\", () => {\n  readFileSync(path.join(tmpDir, \"case.json\"));\n});\n",
			rule: reads, want: 0,
		},
		{
			name: "fixture read stays quiet",
			path: "fixture.test.ts",
			src: "import { readFileSync } from \"fs\";\n" +
				"test(\"reads\", () => {\n  readFileSync(\"fixtures/case.json\");\n});\n",
			rule: reads, want: 0,
		},
		{
			name: "source file read by literal path fires",
			path: "literal.test.ts",
			src: "import { readFileSync } from \"fs\";\n" +
				"test(\"reads\", () => {\n  readFileSync(\"src/config/app.yaml\");\n});\n",
			rule: reads, want: 1,
		},
	}
}

func staleProseCases() []crossCase {
	const stale = "x/stale-prose"
	const prose = "Call `fetchRecords()` to load the page.\n"

	return []crossCase{
		{
			name:  "prose name nothing in the tree declares fires",
			files: map[string]string{"README.md": prose, "store.go": "package p\n\nfunc loadPage() {}\n"},
			rule:  stale, want: 1,
		},
		{
			name:  "prose name declared in the tree stays quiet",
			files: map[string]string{"README.md": prose, "store.go": "package p\n\nfunc fetchRecords() {}\n"},
			rule:  stale, want: 0,
		},
		{
			name: "markdown link destination stays quiet",
			files: map[string]string{
				"README.md": "See [the guide](https://example.com/docs/fetchRecords.md) for details.\n",
			},
			rule: stale, want: 0,
		},
		{
			name:  "autolink stays quiet",
			files: map[string]string{"README.md": "Docs live at <https://example.com/fetchRecords>.\n"},
			rule:  stale, want: 0,
		},
		{
			name: "bare host and path stays quiet",
			files: map[string]string{
				"README.md": "Source is github.com/example/tool/blob/main/fetchRecords.md today.\n",
			},
			rule: stale, want: 0,
		},
		{
			name:  "domain token stays quiet",
			files: map[string]string{"README.md": "Hosted at `example.com` today.\n"},
			rule:  stale, want: 0,
		},
		{
			name:  "changelog stays quiet",
			files: map[string]string{"CHANGELOG.md": "Renamed away from `fetchRecords()`.\n"},
			rule:  stale, want: 0,
		},
		{
			name:  "adr stays quiet",
			files: map[string]string{"docs/adr/0001-store.md": "Chose `fetchRecords()` for loads.\n"},
			rule:  stale, want: 0,
		},
		{
			name:  "research folder stays quiet",
			files: map[string]string{"storage-research/notes.md": "Tried `fetchRecords()` first.\n"},
			rule:  stale, want: 0,
		},
	}
}

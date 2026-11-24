package main

import (
	"encoding/json"
	"testing"

	"github.com/kyleking/slop-strata/internal/fixturetest"
)

// lastRefIDs runs change over the last commit of a written history and
// answers the ids of its x/last-ref findings.
func lastRefIDs(t *testing.T) fixturetest.Eval {
	t.Helper()

	return func(tree fixturetest.Tree) []string {
		repo := historyRepo(t, tree)
		out, err := runCLI(t, repo, "change", "--base", "HEAD~1", "--rev", "HEAD", "--json")
		if err != nil {
			t.Fatalf("change: %v", err)
		}
		var env struct {
			Result struct {
				Findings []findingRow `json:"findings"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("parsing JSON: %v\n%s", err, out)
		}
		var ids []string
		for _, f := range env.Result.Findings {
			if f.Rule == "x/last-ref" {
				ids = append(ids, f.ID)
			}
		}

		return ids
	}
}

// The two commits of a last-ref fixture: the tree as it stood, and the change.
const (
	before = "1.0 feat: start/"
	change = "1.1 fix: stop calling it/"
)

// edit is a variant that replaces or adds files of the change.
func edit(name string, files fixturetest.Tree) fixturetest.Variant {
	out := fixturetest.Tree{}
	for file, body := range files {
		out[change+file] = body
	}

	return fixturetest.Variant{Name: name, Files: out}
}

func goLastRefCase() fixturetest.Case {
	return fixturetest.Case{
		Name: "go: a change removes the last use of a constant",
		Base: fixturetest.Tree{
			before + "store/limits.go": "package store\n\nconst pageSize = 50\n",
			before + "store/list.go":   "package store\n\nfunc list() int { return pageSize }\n",
			change + "store/list.go":   "package store\n\nfunc list() int { return 20 }\n",
		},
		Dodges: []fixturetest.Variant{
			edit("mention the name in a comment the change adds", fixturetest.Tree{
				"store/list.go": "package store\n\n// list no longer reads pageSize.\nfunc list() int { return 20 }\n",
			}),
			edit("add an unrelated file in the same change", fixturetest.Tree{
				"store/extra.go": "package store\n\nfunc extra() int { return 1 }\n",
			}),
		},
		Persists: []fixturetest.Variant{
			edit("delete the file that held the last use", fixturetest.Tree{"store/list.go": removed}),
		},
		Gaps: []fixturetest.Variant{
			edit("leave a test reading it", fixturetest.Tree{
				"store/limits_test.go": "package store\n" +
					"\n" +
					"import \"testing\"\n" +
					"\n" +
					"func TestPage(t *testing.T) { _ = pageSize }\n",
			}),
			edit("name an unrelated local the same", fixturetest.Tree{
				"store/other.go": "package store\n\nfunc other() int {\n\tpageSize := 1\n\n\treturn pageSize\n}\n",
			}),
		},
		Fixes: []fixturetest.Variant{
			edit("delete the constant in the same change", fixturetest.Tree{"store/limits.go": "package store\n"}),
			{Name: "its own file went on using it all along", Files: fixturetest.Tree{
				before + "store/limits.go": "package store\n" +
					"\n" +
					"const pageSize = 50\n" +
					"\n" +
					"func pages(n int) int { return n / pageSize }\n",
			}},
		},
	}
}

func pythonLastRefCase() fixturetest.Case {
	return fixturetest.Case{
		Name: "python: a change removes the last use of a module constant",
		Base: fixturetest.Tree{
			before + "app/limits.py": "PAGE_SIZE = 50\n",
			before + "app/list.py":   "from app.limits import PAGE_SIZE\n\n\ndef rows():\n    return PAGE_SIZE\n",
			change + "app/list.py":   "def rows():\n    return 20\n",
		},
		Dodges: []fixturetest.Variant{
			edit("mention the name in a comment the change adds", fixturetest.Tree{
				"app/list.py": "# rows no longer reads PAGE_SIZE\ndef rows():\n    return 20\n",
			}),
		},
		Persists: []fixturetest.Variant{
			edit("delete the file that held the last use", fixturetest.Tree{"app/list.py": removed}),
		},
		Gaps: []fixturetest.Variant{
			edit("leave the import and drop the use", fixturetest.Tree{
				"app/list.py": "from app.limits import PAGE_SIZE\n\n\ndef rows():\n    return 20\n",
			}),
			{Name: "list it in __all__, which publishes it", Files: fixturetest.Tree{
				before + "app/limits.py": "__all__ = [\"PAGE_SIZE\"]\n\nPAGE_SIZE = 50\n",
			}},
		},
		Fixes: []fixturetest.Variant{
			edit("delete the constant in the same change", fixturetest.Tree{"app/limits.py": removed}),
		},
	}
}

func typescriptLastRefCase() fixturetest.Case {
	return fixturetest.Case{
		Name: "typescript: a change removes the last use of an exported constant",
		Base: fixturetest.Tree{
			before + "src/limits.ts": "export const PAGE_SIZE = 50\n",
			before + "src/list.ts":   "import { PAGE_SIZE } from \"./limits\"\n\nexport const rows = () => PAGE_SIZE\n",
			before + "src/main.ts":   "import { rows } from \"./list\"\n\nconsole.log(rows())\n",
			change + "src/list.ts":   "export const rows = () => 20\n",
		},
		Dodges: []fixturetest.Variant{
			edit("mention the name in a comment the change adds", fixturetest.Tree{
				"src/list.ts": "// rows no longer reads PAGE_SIZE\nexport const rows = () => 20\n",
			}),
		},
		Persists: []fixturetest.Variant{
			edit("delete the file that held the last use", fixturetest.Tree{
				"src/list.ts": removed, "src/main.ts": "console.log(20)\n",
			}),
		},
		Gaps: []fixturetest.Variant{
			edit("leave the import and drop the use", fixturetest.Tree{
				"src/list.ts": "import { PAGE_SIZE } from \"./limits\"\n\nexport const rows = () => 20\n",
			}),
		},
		Fixes: []fixturetest.Variant{
			edit("delete the constant in the same change", fixturetest.Tree{"src/limits.ts": removed}),
		},
	}
}

// TestLastRefHoldsUnderAdversarialEdits is x/last-ref's share of the
// adversarial fixtures: the rule reads two trees, so each variant is a
// two-commit history and the command scores its last commit.
func TestLastRefHoldsUnderAdversarialEdits(t *testing.T) {
	t.Parallel()
	fixturetest.Run(t, []fixturetest.Case{goLastRefCase(), pythonLastRefCase(), typescriptLastRefCase()},
		lastRefIDs(t))
}

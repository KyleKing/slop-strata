package archtrend_test

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/archtrend"
	"github.com/kyleking/slop-strata/internal/depgraph"
)

// history is a list of commits, each the files it changed. A commit of one
// "old>new" entry is a rename.
type history [][]string

// times repeats one commit.
func times(n int, files ...string) history {
	out := make(history, n)
	for i := range out {
		out[i] = files
	}

	return out
}

// shortList caps the pairs a case renders.
const shortList = 3

// hidden records a history, then renders the pairs it hides from a graph with
// the given edges as "a|b co/changesA/changesB support confidence [shared]".
func hidden(commits history, edges ...depgraph.Edge) string {
	co := archtrend.NewCoChange()
	for _, files := range commits {
		if from, to, ok := strings.Cut(files[0], ">"); ok {
			co.Rename(from, to)

			continue
		}
		co.Commit(files, len(files))
	}
	scope := archtrend.Scope{
		Graph: depgraph.New(nil, edges),
		Live:  func(file string) bool { return !strings.HasPrefix(file, "gone/") },
		Package: func(file string) string {
			first, _, _ := strings.Cut(path.Dir(file), "/")

			return first
		},
	}
	pairs := co.Hidden(scope, shortList)
	rows := make([]string, 0, len(pairs))
	for _, p := range pairs {
		row := fmt.Sprintf("%s|%s %d/%d/%d %.2f %.2f", p.A, p.B, p.CoChanges, p.ChangesA, p.ChangesB, p.Support,
			p.Confidence)
		if p.SharedImporter {
			row += " shared"
		}
		rows = append(rows, row)
	}

	return strings.Join(rows, "; ")
}

func edge(from, to string, kind depgraph.Kind) depgraph.Edge {
	return depgraph.Edge{From: from, To: to, Kind: kind}
}

// hiddenCase is one history, the edges of the graph it is judged against, and
// the pairs it must hide.
type hiddenCase struct {
	name    string
	want    string
	commits history
	edges   []depgraph.Edge
}

func runHidden(t *testing.T, cases []hiddenCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := hidden(tc.commits, tc.edges...); got != tc.want {
				t.Errorf("hidden pairs = %q, want %q", got, tc.want)
			}
		})
	}
}

// both is each of a.py and b.py importing one file.
func both(to string) []depgraph.Edge {
	return []depgraph.Edge{edge("p/a.py", to, depgraph.Runtime), edge("p/b.py", to, depgraph.Runtime)}
}

func TestHiddenNeedsNoChainOfImportsBetweenTheFiles(t *testing.T) {
	t.Parallel()
	together := times(archtrend.MinCoChanges, "p/a.py", "p/b.py")
	withBase := times(6, "p/a.py", "p/b.py", "p/base.py")
	runHidden(t, []hiddenCase{
		{name: "no import", commits: together, want: "p/a.py|p/b.py 4/4/4 4.00 1.00"},
		{name: "a runtime import one way", commits: together, edges: []depgraph.Edge{
			edge("p/a.py", "p/b.py", depgraph.Runtime),
		}, want: ""},
		{name: "a type import the other way", commits: together, edges: []depgraph.Edge{
			edge("p/b.py", "p/a.py", depgraph.TypeOnly),
		}, want: ""},
		{name: "a deferred import", commits: together, edges: []depgraph.Edge{
			edge("p/a.py", "p/b.py", depgraph.Deferred),
		}, want: ""},
		{name: "an import through a barrel", commits: together, edges: []depgraph.Edge{
			edge("p/a.py", "p/index.py", depgraph.Runtime), edge("p/index.py", "p/b.py", depgraph.Runtime),
		}, want: ""},
		{name: "a file that imports both", commits: together, edges: []depgraph.Edge{
			edge("p/main.py", "p/a.py", depgraph.Runtime), edge("p/main.py", "p/b.py", depgraph.Runtime),
		}, want: "p/a.py|p/b.py 4/4/4 4.00 1.00 shared"},
		{
			name: "a file both import that did not change", commits: together, edges: both("p/base.py"),
			want: "p/a.py|p/b.py 4/4/4 4.00 1.00",
		},
		{name: "a file both import that changed with them", commits: withBase, edges: both("p/base.py"), want: ""},
		{
			name: "a file both import through another that changed with them", commits: withBase,
			edges: append(both("p/mid.py"), edge("p/mid.py", "p/base.py", depgraph.Runtime)), want: "",
		},
		{
			name:    "only the commits a shared import does not explain count",
			commits: append(times(3, "p/a.py", "p/b.py", "p/base.py"), together...), edges: both("p/base.py"),
			want: "p/a.py|p/b.py 4/7/7 4.00 0.57",
		},
	})
}

func TestHiddenWeighsACommitByHowFewFilesItChanged(t *testing.T) {
	t.Parallel()
	together := times(archtrend.MinCoChanges, "p/a.py", "p/b.py")
	bulk := make([]string, archtrend.MaxCommitFiles+1)
	for i := range bulk {
		bulk[i] = fmt.Sprintf("p/f%02d.py", i)
	}
	sweep := []string{"p/a.py", "p/b.py", "p/c.py", "p/d.py", "p/e.py"}
	alone := append(times(5, "p/a.py"), times(5, "p/b.py")...)
	runHidden(t, []hiddenCase{
		{name: "changed together at the floor", commits: together, want: "p/a.py|p/b.py 4/4/4 4.00 1.00"},
		{name: "one commit under the floor", commits: together[1:], want: ""},
		{
			name: "a third file that neither imports", commits: times(6, "p/a.py", "p/b.py", "p/c.py"),
			want: "p/a.py|p/b.py 6/6/6 3.00 1.00; p/a.py|p/c.py 6/6/6 3.00 1.00; p/b.py|p/c.py 6/6/6 3.00 1.00",
		},
		{
			name: "one file also changes alone", commits: append(times(4, "p/a.py"), together...),
			want: "p/a.py|p/b.py 4/8/4 4.00 1.00",
		},
		{name: "both change alone more than together", commits: append(slices.Clone(alone), together...), want: ""},
		{name: "sweeps alone are under the support floor", commits: times(5, sweep...), want: ""},
		{
			name:    "sweeps add little to two commits of the pair",
			commits: append(times(2, sweep...), times(2, "p/a.py", "p/b.py")...),
			want:    "p/a.py|p/b.py 4/4/4 2.50 1.00",
		},
		{
			name:    "a bulk commit counts for nothing",
			commits: append(times(archtrend.MinCoChanges, bulk...), together...),
			want:    "p/a.py|p/b.py 4/4/4 4.00 1.00",
		},
	})
}

func TestHiddenLeavesOutPairsTheGraphCannotVouchFor(t *testing.T) {
	t.Parallel()
	runHidden(t, []hiddenCase{
		{name: "a file gone from the tree", commits: times(4, "gone/a.py", "p/b.py"), want: ""},
		{name: "two Go files of one directory", commits: times(4, "p/a.go", "p/b.go"), want: ""},
		{
			name: "two Go files of two directories", commits: times(4, "p/a.go", "q/b.go"),
			want: "p/a.go|q/b.go 4/4/4 4.00 1.00",
		},
		{name: "TypeScript in two packages", commits: times(4, "p/src/a.ts", "q/src/b.tsx"), want: ""},
		{
			name: "TypeScript in one package", commits: times(4, "p/src/a.ts", "p/lib/b.tsx"),
			want: "p/lib/b.tsx|p/src/a.ts 4/4/4 4.00 1.00",
		},
		{
			name: "Python and TypeScript in two packages", commits: times(4, "p/a.py", "q/b.ts"),
			want: "p/a.py|q/b.ts 4/4/4 4.00 1.00",
		},
		{
			name: "a rename carries the record", commits: append(append(times(2, "p/a.py", "p/b.py"),
				[]string{"p/b.py>p/c.py"}), times(2, "p/a.py", "p/c.py")...),
			want: "p/a.py|p/c.py 4/4/4 4.00 1.00",
		},
		{
			name: "best supported first, capped", commits: append(append(append(times(4, "p/a.py", "p/b.py"),
				times(7, "p/c.py", "p/d.py")...), times(6, "p/e.py", "p/f.py")...), times(5, "p/g.py", "p/h.py")...),
			want: "p/c.py|p/d.py 7/7/7 7.00 1.00; p/e.py|p/f.py 6/6/6 6.00 1.00; p/g.py|p/h.py 5/5/5 5.00 1.00",
		},
	})
}

func TestHiddenAnswersAnEmptyListAndNeverNil(t *testing.T) {
	t.Parallel()
	scope := archtrend.Scope{
		Graph: depgraph.New(nil, nil), Live: func(string) bool { return true },
		Package: func(string) string { return "" },
	}
	if got := archtrend.NewCoChange().Hidden(scope, 1); got == nil || len(got) != 0 {
		t.Errorf("Hidden of no history = %#v, want an empty list that encodes as []", got)
	}
}

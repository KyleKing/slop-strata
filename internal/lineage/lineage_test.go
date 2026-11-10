package lineage_test

import (
	"testing"
	"time"

	"github.com/kyleking/aragonite/vcs"

	"github.com/kyleking/slop-strata/internal/lineage"
)

func TestMovedLinesKeepTheirCohort(t *testing.T) {
	t.Parallel()
	w := lineage.NewWalker(func(_, _, _ string) string { return "person" })
	at := time.Date(2025, 3, 10, 0, 0, 0, 0, time.UTC)
	src := []string{
		"func alpha() int {",
		"	result := compute(alpha)",
		"	result += adjust(result)",
		"	return result * 2",
		"}",
	}
	w.Commit(vcs.CommitRecord{
		AuthorTime: at, AuthorName: "Dev", AuthorEmail: "dev@x",
		CommitterName: "Dev", CommitterEmail: "dev@x",
		Files: []vcs.CommitFile{{
			Path: "a.go", Status: vcs.StatusAdded, Added: src,
			Hunks: []vcs.DiffHunk{{NewStart: 1, NewLines: len(src)}},
		}},
	}, false, false)

	moved := src[1:4]
	w.Commit(vcs.CommitRecord{
		AuthorTime: at.Add(24 * time.Hour), AuthorName: "Dev", AuthorEmail: "dev@x",
		CommitterName: "Dev", CommitterEmail: "dev@x",
		Files: []vcs.CommitFile{
			{
				Path: "a.go", Removed: moved,
				Hunks: []vcs.DiffHunk{{OldStart: 2, OldLines: 3, NewStart: 2}},
			},
			{
				Path: "b.go", Status: vcs.StatusAdded, Added: moved,
				Hunks: []vcs.DiffHunk{{NewStart: 1, NewLines: len(moved)}},
			},
		},
	}, false, false)
	res := w.Finish()

	var cohort lineage.Survival
	for _, s := range res.Survival {
		if s.Cohort == "2025-03" {
			cohort = s
		}
	}
	if cohort.Surviving != len(src) {
		t.Fatalf("moved lines keep their cohort: surviving = %d, want %d", cohort.Surviving, len(src))
	}
	if res.Dirs["."].Removed != 0 {
		t.Fatalf("a move is not a death: removed = %d", res.Dirs["."].Removed)
	}
	if res.Dirs["."].Churn30 != 0 {
		t.Fatalf("moved lines are not churn: churn30 = %d", res.Dirs["."].Churn30)
	}
}

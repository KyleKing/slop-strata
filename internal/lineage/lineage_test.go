package lineage_test

import (
	"fmt"
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

func TestPureInsertionHunkLandsAfterAnchor(t *testing.T) {
	t.Parallel()
	w := lineage.NewWalker(func(_, _, _ string) string { return "person" })
	at := time.Date(2025, 3, 10, 0, 0, 0, 0, time.UTC)
	base := make([]string, 10)
	for i := range base {
		base[i] = fmt.Sprintf("original line %d", i+1)
	}
	signature := func(when time.Time, files ...vcs.CommitFile) vcs.CommitRecord {
		return vcs.CommitRecord{
			AuthorTime: when, AuthorName: "Dev", AuthorEmail: "dev@x",
			CommitterName: "Dev", CommitterEmail: "dev@x", Files: files,
		}
	}
	w.Commit(signature(at, vcs.CommitFile{
		Path: "a.go", Status: vcs.StatusAdded, Added: base,
		Hunks: []vcs.DiffHunk{{NewStart: 1, NewLines: len(base)}},
	}), false, false)

	inserted := []string{"inserted line one", "inserted line two"}
	w.Commit(signature(at.Add(60*24*time.Hour), vcs.CommitFile{
		Path: "a.go", Added: inserted,
		Hunks: []vcs.DiffHunk{{OldStart: 6, NewStart: 6, NewLines: len(inserted)}},
	}), false, false)

	w.Commit(signature(at.Add(62*24*time.Hour), vcs.CommitFile{
		Path: "a.go", Removed: inserted,
		Hunks: []vcs.DiffHunk{{OldStart: 6, OldLines: len(inserted), NewStart: 6}},
	}), false, false)

	res := w.Finish()
	if got := res.Dirs["."].Churn14; got != len(inserted) {
		t.Fatalf("inserted lines killed within 14 days: churn_14d = %d, want %d", got, len(inserted))
	}
	if got := res.Dirs["."].Removed; got != len(inserted) {
		t.Fatalf("removed = %d, want only the inserted lines (%d)", got, len(inserted))
	}
}

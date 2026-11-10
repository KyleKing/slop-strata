package lineage_test

import (
	"fmt"
	"math"
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
		AuthorTime: at, CommitTime: at, AuthorName: "Dev", AuthorEmail: "dev@x",
		CommitterName: "Dev", CommitterEmail: "dev@x",
		Files: []vcs.CommitFile{{
			Path: "a.go", Status: vcs.StatusAdded, Added: src,
			Hunks: []vcs.DiffHunk{{NewStart: 1, NewLines: len(src)}},
		}},
	}, false, false)

	moved := src[1:4]
	w.Commit(vcs.CommitRecord{
		AuthorTime: at.Add(24 * time.Hour), CommitTime: at.Add(24 * time.Hour), AuthorName: "Dev", AuthorEmail: "dev@x",
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
			AuthorTime: when, CommitTime: when, AuthorName: "Dev", AuthorEmail: "dev@x",
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

func TestChurnIsCreditedToTheDirectoryLinesWereAddedIn(t *testing.T) {
	t.Parallel()
	w := lineage.NewWalker(func(_, _, _ string) string { return "person" })
	src := []string{"first line", "second line", "third line"}
	w.Commit(commitOn(0, vcs.CommitFile{
		Path: "src/a.go", Status: vcs.StatusAdded, Added: src,
		Hunks: []vcs.DiffHunk{{NewStart: 1, NewLines: len(src)}},
	}), false, false)
	w.Commit(commitOn(2, vcs.CommitFile{
		Path: "lib/a.go", OldPath: "src/a.go", Status: vcs.StatusRenamed, Removed: []string{"second line"},
		Hunks: []vcs.DiffHunk{{OldStart: 2, OldLines: 1, NewStart: 2}},
	}), false, false)

	res := w.Finish()
	for dir, ds := range res.Dirs {
		if ds.Churn30 > ds.Added {
			t.Errorf("%s: churn_30d %d exceeds added %d", dir, ds.Churn30, ds.Added)
		}
	}
	if got := res.Dirs["src"].Churn14; got != 1 {
		t.Errorf("the line dies within 14 days of its birth in src: churn_14d = %d, want 1", got)
	}
	if got := res.Dirs["lib"].Churn30; got != 0 {
		t.Errorf("a renamed file does not own its lines' churn: lib churn_30d = %d, want 0", got)
	}
}

var walkEpoch = time.Date(2025, 3, 10, 0, 0, 0, 0, time.UTC)

func commitOn(day int, files ...vcs.CommitFile) vcs.CommitRecord {
	at := walkEpoch.Add(time.Duration(day) * 24 * time.Hour)

	return vcs.CommitRecord{
		AuthorTime: at, CommitTime: at, AuthorName: "Dev", AuthorEmail: "dev@x",
		CommitterName: "Dev", CommitterEmail: "dev@x", Files: files,
	}
}

func share(v float64) *float64 { return &v }

func TestSurvivalNullBeyondExposure(t *testing.T) {
	t.Parallel()
	cases := []struct {
		wantS30  *float64
		wantS90  *float64
		name     string
		walkDays int
	}{
		{name: "walk shorter than both horizons", walkDays: 2},
		{name: "walk past 30 days but not 90", walkDays: 45, wantS30: share(1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := lineage.NewWalker(func(_, _, _ string) string { return "person" })
			w.Commit(commitOn(0, addedFile("a.go", 40)), false, false)
			w.Commit(commitOn(tc.walkDays, addedFile("b.go", 1)), false, false)

			var cohort lineage.Survival
			for _, s := range w.Finish().Survival {
				if s.Cohort == "2025-03" {
					cohort = s
				}
			}
			if !sameShare(cohort.S30, tc.wantS30) || !sameShare(cohort.S90, tc.wantS90) {
				t.Fatalf("s30 = %v, s90 = %v; want s30 = %v, s90 = %v",
					fmtShare(cohort.S30), fmtShare(cohort.S90), fmtShare(tc.wantS30), fmtShare(tc.wantS90))
			}
		})
	}
}

func TestTestSurvivalCensorsYoungFiles(t *testing.T) {
	t.Parallel()
	cases := []struct {
		wantS30   *float64
		wantS90   *float64
		name      string
		deleteDay int // negative keeps the file alive to the end of the walk
		endDay    int
	}{
		{
			name: "never deleted, observed past both horizons", deleteDay: -1, endDay: 100,
			wantS30: share(1), wantS90: share(1),
		},
		{name: "deleted inside 30 days", deleteDay: 10, endDay: 100, wantS30: share(0), wantS90: share(0)},
		{name: "deleted between 30 and 90 days", deleteDay: 50, endDay: 100, wantS30: share(1), wantS90: share(0)},
		{name: "alive but younger than 30 days", deleteDay: -1, endDay: 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := lineage.NewWalker(func(_, _, _ string) string { return "person" })
			w.Commit(commitOn(0, vcs.CommitFile{
				Path: "pkg/a_test.go", Status: vcs.StatusAdded, Added: []string{"func TestA(t *testing.T) {}"},
				Hunks: []vcs.DiffHunk{{NewStart: 1, NewLines: 1}},
			}), false, false)
			if tc.deleteDay >= 0 {
				deleted := vcs.CommitFile{Path: "pkg/a_test.go", Status: vcs.StatusDeleted}
				w.Commit(commitOn(tc.deleteDay, deleted), false, false)
			}
			w.Commit(commitOn(tc.endDay, vcs.CommitFile{
				Path: "pkg/other.go", Status: vcs.StatusAdded, Added: []string{"func other() {}"},
				Hunks: []vcs.DiffHunk{{NewStart: 1, NewLines: 1}},
			}), false, false)

			classes := w.Finish().TestSurvival
			if len(classes) != 1 {
				t.Fatalf("test survival classes = %d, want 1", len(classes))
			}
			if !sameShare(classes[0].S30, tc.wantS30) || !sameShare(classes[0].S90, tc.wantS90) {
				t.Fatalf("s30 = %v, s90 = %v; want s30 = %v, s90 = %v",
					fmtShare(classes[0].S30), fmtShare(classes[0].S90), fmtShare(tc.wantS30), fmtShare(tc.wantS90))
			}
		})
	}
}

func sameShare(got, want *float64) bool {
	if got == nil || want == nil {
		return got == want
	}

	return *got == *want
}

func fmtShare(v *float64) string {
	if v == nil {
		return "null"
	}

	return fmt.Sprintf("%.2f", *v)
}

func TestCohortFollowsCommitterDateInUTC(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		authorTime time.Time
		commitTime time.Time
		want       string
	}{
		{
			name:       "committer date picks the month, not the author date",
			authorTime: time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC),
			commitTime: time.Date(2025, 3, 10, 0, 0, 0, 0, time.UTC),
			want:       "2025-03",
		},
		{
			name:       "month is taken in UTC",
			authorTime: time.Date(2025, 1, 31, 23, 30, 0, 0, time.FixedZone("EST", -5*60*60)),
			commitTime: time.Date(2025, 1, 31, 23, 30, 0, 0, time.FixedZone("EST", -5*60*60)),
			want:       "2025-02",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := lineage.NewWalker(func(_, _, _ string) string { return "person" })
			rec := commitOn(0, vcs.CommitFile{
				Path: "a.go", Status: vcs.StatusAdded, Added: []string{"func a() {}"},
				Hunks: []vcs.DiffHunk{{NewStart: 1, NewLines: 1}},
			})
			rec.AuthorTime, rec.CommitTime = tc.authorTime, tc.commitTime
			w.Commit(rec, false, false)

			survival := w.Finish().Survival
			if len(survival) != 1 || survival[0].Cohort != tc.want {
				t.Fatalf("cohorts = %+v, want one cohort %q", survival, tc.want)
			}
		})
	}
}

func addedFile(path string, lines int) vcs.CommitFile {
	added := make([]string, lines)
	for i := range added {
		added[i] = fmt.Sprintf("line %s %d", path, i)
	}

	return vcs.CommitFile{
		Path: path, Status: vcs.StatusAdded, Added: added,
		Hunks: []vcs.DiffHunk{{NewStart: 1, NewLines: lines}},
	}
}

func TestSurvivalNeedsEnoughLinesAtHorizon(t *testing.T) {
	t.Parallel()
	cases := []struct {
		build   func(w *lineage.Walker)
		wantS30 *float64
		name    string
	}{
		{
			name: "most lines censored young with a few deaths at 30 days",
			build: func(w *lineage.Walker) {
				w.Commit(commitOn(0, addedFile("dies.go", 16)), false, false)
				w.Commit(commitOn(15, addedFile("young.go", 100)), false, false)
				w.Commit(commitOn(30, vcs.CommitFile{Path: "dies.go", Status: vcs.StatusDeleted}), false, false)
			},
		},
		{
			name: "enough lines reach 30 days",
			build: func(w *lineage.Walker) {
				w.Commit(commitOn(0, addedFile("stays.go", 40), addedFile("dies.go", 4)), false, false)
				w.Commit(commitOn(30, vcs.CommitFile{Path: "dies.go", Status: vcs.StatusDeleted}), false, false)
			},
			wantS30: share(40.0 / 44.0),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := lineage.NewWalker(func(_, _, _ string) string { return "person" })
			tc.build(w)

			var cohort lineage.Survival
			for _, s := range w.Finish().Survival {
				if s.Cohort == "2025-03" {
					cohort = s
				}
			}
			if !sameShare(cohort.S30, tc.wantS30) {
				t.Fatalf("s30 = %v, want %v", fmtShare(cohort.S30), fmtShare(tc.wantS30))
			}
		})
	}
}

func TestThinSamplesAreProvisional(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name            string
		cohortLines     int
		wantProvisional bool
	}{
		{name: "cohort under the line floor", cohortLines: lineage.ProvisionalLines - 1, wantProvisional: true},
		{name: "cohort at the line floor", cohortLines: lineage.ProvisionalLines, wantProvisional: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := lineage.NewWalker(func(_, _, _ string) string { return "person" })
			// The test file's own line is one of the cohort's births.
			w.Commit(commitOn(0, addedFile("pkg/a.go", tc.cohortLines-1), vcs.CommitFile{
				Path: "pkg/a_test.go", Status: vcs.StatusAdded, Added: []string{"func TestA(t *testing.T) {}"},
				Hunks: []vcs.DiffHunk{{NewStart: 1, NewLines: 1}},
			}), false, false)
			res := w.Finish()

			if len(res.Survival) != 1 || res.Survival[0].Provisional != tc.wantProvisional {
				t.Fatalf("survival provisional = %+v, want %v", res.Survival, tc.wantProvisional)
			}
			if len(res.TestSurvival) != 1 || !res.TestSurvival[0].Provisional {
				t.Fatalf("test survival of one file must be provisional: %+v", res.TestSurvival)
			}
		})
	}
}

func TestGreenwoodIntervalAroundHalfSurviving(t *testing.T) {
	t.Parallel()
	w := lineage.NewWalker(func(_, _, _ string) string { return "person" })
	w.Commit(commitOn(0, addedFile("dies.go", 40), addedFile("stays.go", 40)), false, false)
	w.Commit(commitOn(10, vcs.CommitFile{Path: "dies.go", Status: vcs.StatusDeleted}), false, false)
	w.Commit(commitOn(100, addedFile("late.go", 1)), false, false)

	var cohort lineage.Survival
	for _, s := range w.Finish().Survival {
		if s.Cohort == "2025-03" {
			cohort = s
		}
	}
	// Half of 80 lines die at day 10: S = 0.5 and Greenwood's sum is 40/(80*40),
	// so the 95% interval is 0.5 +/- 1.96 * 0.5 * sqrt(0.0125).
	const want = 0.1095653
	if cohort.S30 == nil || cohort.S30CI == nil {
		t.Fatalf("s30 and its interval are reported: s30 = %v, ci = %v", fmtShare(cohort.S30), cohort.S30CI)
	}
	if math.Abs(*cohort.S30-0.5) > 1e-9 || math.Abs(cohort.S30CI[0]-(0.5-want)) > 1e-6 ||
		math.Abs(cohort.S30CI[1]-(0.5+want)) > 1e-6 {
		t.Fatalf("s30 = %v, ci = %v; want 0.5 with interval 0.5 +/- %.6f", *cohort.S30, *cohort.S30CI, want)
	}
}

package rules_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/rules"
)

// writeBaselineFile writes raw baseline JSON to a temp file and answers its path.
func writeBaselineFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestReadBaselineAcceptsVersionsTwoAndThree(t *testing.T) {
	t.Parallel()
	const entries = `"entries":[{"id":"abc","path":"a.py","rule":"py/x"}]}`
	cases := []struct {
		wantErr error
		name    string
		body    string
		covers  bool
	}{
		{name: "version 2 loads", body: `{"version":2,` + entries, covers: true},
		{name: "version 3 loads", body: `{"version":3,` + entries, covers: true},
		{name: "version 1 is out of date", body: `{"version":1,` + entries, wantErr: rules.ErrBaselineFormat},
		{name: "a newer version is refused", body: `{"version":4,` + entries, wantErr: rules.ErrBaselineFormat},
		{name: "no version is refused", body: `{` + entries, wantErr: rules.ErrBaselineFormat},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, err := rules.ReadBaseline(writeBaselineFile(t, tc.body))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ReadBaseline error = %v, want %v", err, tc.wantErr)
			}
			if got := b.Covers(&rules.Finding{ID: "abc", Rule: "py/x"}); got != tc.covers {
				t.Errorf("Covers = %v, want %v", got, tc.covers)
			}
		})
	}
	if _, err := rules.ReadBaseline(writeBaselineFile(t, "not json")); err == nil {
		t.Error("ReadBaseline of malformed JSON returned no error")
	}
	missing, err := rules.ReadBaseline(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil || missing.Covers(&rules.Finding{ID: "abc"}) {
		t.Errorf("a missing baseline must read as empty: %v", err)
	}
}

func TestBaselineCoversAShrunkCycleAndNotAGrownOne(t *testing.T) {
	t.Parallel()
	const cycle = "x/import-cycle"
	accepted := []string{"a.py", "b.py", "c.py"}
	// Members are written out of order on purpose: the reader sorts them.
	b, err := rules.ReadBaseline(writeBaselineFile(t, `{"version":3,"entries":[
		{"id":"`+rules.SetID(cycle, accepted)+`","path":"b.py","rule":"`+cycle+`","members":["c.py","a.py","b.py"]},
		{"id":"plain","path":"a.py","rule":"py/x"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		rule    string
		members []string
		want    bool
	}{
		{name: "the accepted cycle", rule: cycle, members: accepted, want: true},
		{name: "a cycle that shrank", rule: cycle, members: []string{"a.py", "c.py"}, want: true},
		{name: "a cycle that grew", rule: cycle, members: []string{"a.py", "b.py", "c.py", "d.py"}},
		{name: "a cycle that swapped a member", rule: cycle, members: []string{"a.py", "d.py"}},
		{name: "a new cycle elsewhere", rule: cycle, members: []string{"x.py", "y.py"}},
		{name: "the same members under another rule", rule: "x/other-set", members: []string{"a.py", "b.py"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := rules.Identify("b.py", []rules.Finding{{Rule: tc.rule, Members: tc.members}})[0]
			if got := b.Covers(&f); got != tc.want {
				t.Errorf("Covers(%v) = %v, want %v", tc.members, got, tc.want)
			}
		})
	}
	if b.Covers(&rules.Finding{ID: "other", Rule: "py/x"}) {
		t.Error("a finding with no members must match by ID alone")
	}
}

func TestWriteBaselineRoundTripsMembers(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "baseline.json")
	found := map[string][]rules.Finding{"b.py": rules.Identify("b.py", []rules.Finding{
		{Rule: "x/import-cycle", Line: 3, Match: "a.py -> b.py -> a.py", Members: []string{"a.py", "b.py"}},
		{Rule: "py/x", Line: 9, Match: "m"},
	})}
	if err := rules.WriteBaseline(path, found); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path) //nolint:gosec // a test-owned temp path
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"version": 3`) || strings.Count(string(data), `"members"`) != 1 {
		t.Errorf("baseline file must be version 3 with members on the cycle alone:\n%s", data)
	}
	b, err := rules.ReadBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range found["b.py"] {
		if !b.Covers(&found["b.py"][i]) {
			t.Errorf("round-tripped baseline does not cover %+v", found["b.py"][i])
		}
	}
}

func TestSuppressorCountsStaleBaselineEntries(t *testing.T) {
	t.Parallel()
	const cycle = "x/import-cycle"
	trio := []string{"a.py", "b.py", "c.py"}
	b, err := rules.ReadBaseline(writeBaselineFile(t, `{"version":3,"entries":[
		{"id":"`+rules.SetID(cycle, trio)+`","path":"b.py","rule":"`+cycle+`","members":["a.py","b.py","c.py"]},
		{"id":"`+rules.FindingID("py/x", "a.py", "", "kept", "", 0)+`","path":"a.py","rule":"py/x"},
		{"id":"gone","path":"a.py","rule":"py/x"},
		{"id":"other","path":"a.py","rule":"sg/pack-rule"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	ran := func(rule string) bool { return rule != "sg/pack-rule" }
	cases := []struct {
		name      string
		members   []string
		wantStale int
	}{
		{name: "the cycle as accepted leaves only the fixed entry stale", members: trio, wantStale: 1},
		{name: "a shrunk cycle is covered and its entry reads stale", members: []string{"a.py", "b.py"}, wantStale: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sup := rules.NewSuppressor(b, nil)
			kept := sup.Apply("a.py", nil, []rules.Finding{
				{Rule: "py/x", Line: 1, Match: "kept"}, {Rule: cycle, Line: 2, Members: tc.members},
			})
			if len(kept) != 0 || sup.Baselined != 2 {
				t.Fatalf("kept %+v with %d baselined, want both findings set aside", kept, sup.Baselined)
			}
			if got := sup.StaleBaseline(ran); got != tc.wantStale {
				t.Errorf("StaleBaseline = %d, want %d", got, tc.wantStale)
			}
		})
	}
}

func TestSnapshotRuleNamesTheRulesASnapshotRuns(t *testing.T) {
	t.Parallel()
	cases := []struct {
		rule         string
		experimental bool
		want         bool
	}{
		{rule: "py/silent-except", want: true},
		{rule: "x/import-cycle", want: false},
		{rule: "x/import-cycle", experimental: true, want: true},
		{rule: "x/last-ref", experimental: true, want: false},
		{rule: "sg/pack-rule", experimental: true, want: false},
	}
	for _, tc := range cases {
		if got := rules.SnapshotRule(tc.rule, tc.experimental); got != tc.want {
			t.Errorf("SnapshotRule(%s, %v) = %v, want %v", tc.rule, tc.experimental, got, tc.want)
		}
	}
}

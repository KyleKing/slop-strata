package report_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/report"
)

func TestSparklineSpansItsOwnRange(t *testing.T) {
	t.Parallel()
	if got := report.Sparkline([]float64{-4, 0, 4}); got != "▁▅█" {
		t.Errorf("Sparkline with negatives = %q, want %q", got, "▁▅█")
	}
}

func TestTableAlignsMultibyteCells(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	report.Table(&buf, []string{"cohort", "s30", "s90"}, [][]string{
		{"2026-08", "0.00", "—"},
		{"2026-09", "—", "—"},
	})
	const thirdColumn = 15
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if runes := []rune(line); len(runes) <= thirdColumn || runes[thirdColumn] == ' ' {
			t.Fatalf("third column not at rune %d in %q:\n%s", thirdColumn, line, buf.String())
		}
	}
}

func TestDropZeroColumnsKeepsTheFirstAndAnyColumnWithSignal(t *testing.T) {
	t.Parallel()
	header, rows := report.DropZeroColumns(
		[]string{"dir", "net", "erosion", "flag", "other"},
		[][]string{
			{"a", "+0.0", "0.00 → 0.00*", "", "0.00 → 0.20"},
			{"b", "0.0", "0.00 → 0.00", "", "—"},
		},
	)
	if got := strings.Join(header, ","); got != "dir,other" {
		t.Fatalf("header = %q, want dir,other", got)
	}
	if len(rows[0]) != 2 || rows[1][1] != "—" {
		t.Fatalf("rows = %v", rows)
	}
}

func TestElidePathKeepsTheEndsOfALongPath(t *testing.T) {
	t.Parallel()
	const long = "packages/vite/src/node/server/middlewares"
	cases := []struct {
		path  string
		want  string
		width int
	}{
		{path: "internal/rules", width: 14, want: "internal/rules"},
		{path: long, width: 41, want: long},
		{path: long, width: 40, want: "packages/…/src/node/server/middlewares"},
		{path: long, width: 30, want: "packages/…/server/middlewares"},
		{path: long, width: 22, want: "packages/…/middlewares"},
		{path: long, width: 15, want: "…/middlewares"},
		{path: long, width: 8, want: "…lewares"},
		{path: "averyveryverylongname", width: 6, want: "…gname"},
		{path: long, width: 0, want: ""},
	}
	for _, tc := range cases {
		if got := report.ElidePath(tc.path, tc.width); got != tc.want {
			t.Errorf("ElidePath(%q, %d) = %q, want %q", tc.path, tc.width, got, tc.want)
		}
	}
}

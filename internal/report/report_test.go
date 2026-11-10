package report_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/report"
)

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

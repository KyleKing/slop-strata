package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kyleking/slop-strata/internal/report"
)

// progressAfter is how long a walk runs before it shows progress, so a quick
// run prints nothing.
const progressAfter = 2 * time.Second

// progressEvery is the fewest time between two redraws of the progress line.
const progressEvery = 100 * time.Millisecond

// progress keeps one updating "done/total commits" line on a terminal. The zero
// value, for output that is not a terminal, prints nothing.
type progress struct {
	start time.Time
	drawn time.Time
	w     io.Writer
	total int
	done  int
	width int
}

// newProgress starts a progress line on a terminal w toward total commits.
func newProgress(w io.Writer, total int) *progress {
	return &progress{w: w, total: total, start: time.Now()}
}

// isTerminal reports whether w writes to a character device, such as a terminal.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()

	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// step counts one commit done and redraws the line once the walk has run long
// enough.
func (p *progress) step() {
	p.done++
	if p.w == nil {
		return
	}
	now := time.Now()
	if now.Sub(p.start) < progressAfter || now.Sub(p.drawn) < progressEvery {
		return
	}
	p.drawn = now
	line := fmt.Sprintf("history: %d/%d commits", p.done, p.total)
	report.Fprintf(p.w, "\r%s", line)
	p.width = len(line)
}

// clear erases the line, when one was drawn, so later output starts clean.
func (p *progress) clear() {
	if p.width > 0 {
		report.Fprintf(p.w, "\r%s\r", strings.Repeat(" ", p.width))
		p.width = 0
	}
}

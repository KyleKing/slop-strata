package main

import (
	"cmp"
	"fmt"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kyleking/slop-strata/internal/depth"
	"github.com/kyleking/slop-strata/internal/report"
)

// noDepth is what the depth column reads for a directory nothing outside uses.
const noDepth = "-"

// depthText renders a module's depth, or noDepth when it has none.
func depthText(ratio *float64) string {
	if ratio == nil {
		return noDepth
	}

	return fmt.Sprintf("%.2f", *ratio)
}

// countMark follows a depth read off name counts, where neither side has a
// function to weigh.
const countMark = "#"

// depthTableWidth is how wide the depth table may grow before it shortens
// directory paths.
const depthTableWidth = 100

// widestNameCap is the longest file name the widest_surface column prints.
const widestNameCap = 24

// printDepthTable lists the directories other directories use names of,
// shallowest first, with the provisional ones after the rest. With all it
// adds the directories nothing outside uses, which have no depth. Masses and
// forwarders are in the JSON: the directory table above carries each
// directory's mass, and the table has to fit depthTableWidth columns.
func printDepthTable(w io.Writer, dirs []dirSnapshot, all bool) {
	ranked := make([]dirSnapshot, 0, len(dirs))
	for i := range dirs {
		if d := &dirs[i]; d.Depth != nil || (all && d.Shown+d.Hidden+d.Forwarders > 0) {
			ranked = append(ranked, *d)
		}
	}
	if len(ranked) == 0 {
		return
	}
	slices.SortStableFunc(ranked, compareDepth)
	header := []string{colDir, "shown", "by_name", "hidden", colDepth, "widest_surface"}
	rows := make([][]string, 0, len(ranked))
	for i := range ranked {
		rows = append(rows, depthRow(&ranked[i]))
	}
	shortenFirstColumn(header, rows, depthTableWidth)
	report.Fprintf(w, "\nmodule depth, shallowest first\n")
	report.Table(w, header, rows)
	provisionalNote(w, ranked, func(d dirSnapshot) bool { return d.Module.Provisional }, depth.ProvisionalNames)
	if slices.ContainsFunc(ranked, func(d dirSnapshot) bool { return d.Depth != nil && !d.ByMass }) {
		report.Fprintf(w, "%s by name count: no function on either side to weigh\n", countMark)
	}
}

// shortenFirstColumn elides the paths of a table's first column so that the
// table, with its other columns as wide as they are, fits width columns.
func shortenFirstColumn(header []string, rows [][]string, width int) {
	for col := 1; col < len(header); col++ {
		widest := utf8.RuneCountInString(header[col])
		for _, r := range rows {
			widest = max(widest, utf8.RuneCountInString(r[col]))
		}
		width -= widest + len("  ")
	}
	for _, r := range rows {
		r[0] = report.ElidePath(r[0], width)
	}
}

// depthRange renders a module's depth, and after a dash its attributed depth
// when names matched by name alone make the two differ.
func depthRange(m *depth.Module) string {
	text := depthText(m.Depth)
	if upper := depthText(m.DepthAttributed); m.ShownByName > 0 && upper != text {
		text += "–" + upper
	}
	text = provisionalMarked(text, m.Provisional)
	if m.Depth != nil && !m.ByMass {
		text += countMark
	}

	return text
}

// depthRow renders one directory's line of the depth table.
func depthRow(d *dirSnapshot) []string {
	widest := ""
	if d.WidestShown > 0 {
		name := []rune(path.Base(d.WidestFile))
		if len(name) > widestNameCap {
			name = append(name[:widestNameCap-1], '…')
		}
		widest = fmt.Sprintf("%s (%d)", string(name), d.WidestShown)
	}

	return []string{
		d.Dir, strconv.Itoa(d.Shown), strconv.Itoa(d.ShownByName), strconv.Itoa(d.Hidden),
		depthRange(&d.Module), widest,
	}
}

// compareDepth orders the depth table: directories with a depth before those
// without, solid before provisional, then shallowest first.
func compareDepth(a, b dirSnapshot) int {
	if a.Depth == nil || b.Depth == nil {
		return cmp.Or(cmp.Compare(btoi(a.Depth == nil), btoi(b.Depth == nil)), strings.Compare(a.Dir, b.Dir))
	}

	return cmp.Or(
		cmp.Compare(btoi(a.Module.Provisional), btoi(b.Module.Provisional)),
		cmp.Compare(*a.Depth, *b.Depth), cmp.Compare(b.Shown, a.Shown), strings.Compare(a.Dir, b.Dir),
	)
}

// printSurface lists the directories whose surface the change widened, with
// the names attributed uses newly reach and the attributed depth on each
// side. Methods matched by name alone are counted and never named.
func printSurface(w io.Writer, widened []depth.Widening) {
	if len(widened) == 0 {
		return
	}
	rows := make([][]string, 0, len(widened))
	for i := range widened {
		d := &widened[i]
		names := strings.Join(d.NewlyShown, ", ")
		if d.NewlyShownCount > len(d.NewlyShown) {
			names += fmt.Sprintf(", … (%d)", d.NewlyShownCount)
		}
		if d.NewlyShownByName > 0 {
			names += fmt.Sprintf("; %d more by name", d.NewlyShownByName)
		}
		rows = append(rows, []string{
			d.Dir, fmt.Sprintf("%d → %d", d.ShownBefore, d.ShownAfter),
			depthText(d.DepthBefore) + " → " + depthText(d.DepthAfter), names,
		})
	}
	report.Fprintln(w)
	report.Table(w, []string{"surface_widened", "shown", colDepth, "newly used from outside"}, rows)
}

package main

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/kyleking/slop-strata/internal/engine"
	"github.com/kyleking/slop-strata/internal/report"
	"github.com/kyleking/slop-strata/internal/rules"
	"github.com/kyleking/slop-strata/internal/structure"
)

// duplicateLimit caps the duplicate groups a snapshot lists.
const duplicateLimit = 20

// duplicateList is the repeated code of a tree: how many groups there are,
// what merging all of them would remove, and the heaviest of them.
type duplicateList struct {
	Groups    []rules.DupGroup `json:"groups"`
	Total     int              `json:"total"`
	Removable int              `json:"removable"`
}

// duplicateGroups runs the duplicate pass over a snapshot's reports.
func duplicateGroups(reports []*engine.FileReport) *duplicateList {
	tree := make(map[string]*rules.Fingerprint, len(reports))
	for _, r := range reports {
		if r.Skipped == "" && r.Print != nil {
			tree[r.Path] = r.Print
		}
	}
	groups := rules.DuplicateGroups(tree)
	out := &duplicateList{Total: len(groups), Groups: groups[:min(len(groups), duplicateLimit)]}
	for i := range groups {
		out.Removable += groups[i].Removable
	}
	if out.Groups == nil {
		out.Groups = []rules.DupGroup{}
	}

	return out
}

// splitLimit caps the split candidates a snapshot lists.
const splitLimit = 20

// splitList is the functions of a tree, tests and examples aside, that hold a
// block worth moving out: how many there are, and the ones a split would relieve most.
type splitList struct {
	Functions []splitFunc `json:"functions"`
	Total     int         `json:"total"`
}

// splitFunc is one function and the block it could lose.
type splitFunc struct {
	File      string          `json:"file"`
	Name      string          `json:"name"`
	Split     structure.Split `json:"split"`
	Line      int             `json:"line"`
	EndLine   int             `json:"end_line"`
	Cognitive int             `json:"cognitive"`
	CC        int             `json:"cc"`
}

// splitCandidates ranks the production functions that carry a split by what
// it takes off them.
func splitCandidates(metrics map[string]structure.FileMetrics) *splitList {
	all := []splitFunc{}
	for path, m := range metrics {
		if rules.Aside(path) {
			continue
		}
		for _, fn := range m.Funcs {
			if fn.Split != nil {
				all = append(all, splitFunc{
					File: path, Name: fn.Name, Line: fn.StartLine, EndLine: fn.EndLine,
					Cognitive: fn.Cognitive, CC: fn.CC, Split: *fn.Split,
				})
			}
		}
	}
	slices.SortFunc(all, func(a, b splitFunc) int {
		return cmp.Or(
			cmp.Compare(b.Split.Drop, a.Split.Drop), strings.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line),
		)
	})

	return &splitList{Total: len(all), Functions: all[:min(len(all), splitLimit)]}
}

// Rows the text view lists of each table without --all.
const (
	topSplits     = 5
	topDuplicates = 5
)

// printSplits lists the functions a split would relieve most, all the result
// holds with --all. Cyclomatic complexity before and after is in the JSON:
// the table has to fit tableWidth columns.
func printSplits(w io.Writer, list *splitList, all bool) {
	if list == nil || len(list.Functions) == 0 {
		return
	}
	shown := list.Functions
	if !all {
		shown = shown[:min(len(shown), topSplits)]
	}
	rows := make([][]string, 0, len(shown))
	for i := range shown {
		f := &shown[i]
		sp := &f.Split
		rows = append(rows, []string{
			fmt.Sprintf("%s:%d", f.File, f.Line), clip(f.Name, maxUnitShown),
			fmt.Sprintf("%d-%d", sp.StartLine, sp.EndLine),
			fmt.Sprintf("%d → %d + %d", f.Cognitive, sp.Remainder, sp.Cognitive),
			strconv.Itoa(sp.Inputs), strconv.Itoa(sp.Outputs), strconv.Itoa(sp.Exits),
		})
	}
	header := []string{"function", "", "block", "cognitive", "in", "out", "exits"}
	shortenFirstColumn(header, rows)
	report.Fprintf(w, "\nsplit candidates, %d of %d (cognitive: before → rest + block)\n", len(shown), list.Total)
	report.Table(w, header, rows)
}

// maxSitesShown is how many sites of a duplicate group the text view lists.
const maxSitesShown = 3

// printDuplicates lists the heaviest duplicate groups, each with its sites,
// all the result holds with --all.
func printDuplicates(w io.Writer, list *duplicateList, all bool) {
	if list == nil || len(list.Groups) == 0 {
		return
	}
	shown := list.Groups
	if !all {
		shown = shown[:min(len(shown), topDuplicates)]
	}
	var rows [][]string
	for i := range shown {
		rows = append(rows, duplicateRows(&shown[i])...)
	}
	header := []string{"site", "", "removable", "sites", "tokens", "differs"}
	shortenFirstColumn(header, rows)
	report.Fprintf(w, "\nduplicate groups, %d of %d (%d tokens removable in all)\n",
		len(shown), list.Total, list.Removable)
	report.Table(w, header, rows)
}

// maxDiffersShown and maxUnitShown cap the width of the differs column and
// of a function's name.
const (
	maxDiffersShown = 20
	maxUnitShown    = 24
)

// duplicateRows renders one group: its numbers beside the first site, then a
// row for each further site up to maxSitesShown and a count of the rest. How
// many statements a site is, and whether it is an expression, is in the JSON.
func duplicateRows(g *rules.DupGroup) [][]string {
	differs := make([]string, 0, len(g.Parameters))
	for _, p := range g.Parameters {
		differs = append(differs, strings.Join(p, "/"))
	}
	rows := make([][]string, 0, maxSitesShown+1)
	for i := range g.Sites[:min(len(g.Sites), maxSitesShown)] {
		s := &g.Sites[i]
		row := []string{fmt.Sprintf("%s:%d", s.Path, s.Line), clip(s.Unit, maxUnitShown), "", "", "", ""}
		if i == 0 {
			row = []string{
				row[0], row[1], strconv.Itoa(g.Removable), strconv.Itoa(len(g.Sites)), strconv.Itoa(g.Tokens),
				clip(strings.Join(differs, "; "), maxDiffersShown),
			}
		}
		rows = append(rows, row)
	}
	if more := len(g.Sites) - maxSitesShown; more > 0 {
		rows = append(rows, []string{fmt.Sprintf("… and %d more", more), "", "", "", "", ""})
	}

	return rows
}

// clip shortens text to width runes, ending in an ellipsis when cut.
func clip(text string, width int) string {
	runes := []rune(text)
	if len(runes) <= width {
		return text
	}

	return string(runes[:width-1]) + "…"
}

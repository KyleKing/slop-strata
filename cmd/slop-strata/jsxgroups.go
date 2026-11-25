package main

import (
	"fmt"
	"io"
	"strconv"

	"github.com/kyleking/slop-strata/internal/engine"
	"github.com/kyleking/slop-strata/internal/report"
	"github.com/kyleking/slop-strata/internal/rules"
)

// jsxGroupLimit caps the JSX groups a snapshot lists.
const jsxGroupLimit = 20

// jsxGroupList is the blocks of markup a tree holds more than once: how many
// groups there are, and the ones that rank highest.
type jsxGroupList struct {
	Groups []rules.JSXGroup `json:"groups"`
	Total  int              `json:"total"`
}

// jsxGroups runs the JSX pass over a snapshot's reports.
func jsxGroups(reports []*engine.FileReport) *jsxGroupList {
	groups := rules.JSXGroups(scoredPrints(reports))
	out := &jsxGroupList{Total: len(groups), Groups: groups[:min(len(groups), jsxGroupLimit)]}
	if out.Groups == nil {
		out.Groups = []rules.JSXGroup{}
	}

	return out
}

// topJSXGroups is how many groups the text view lists without --all.
const topJSXGroups = 5

// maxJSXUnitShown is the width the text view clips a site's unit to.
const maxJSXUnitShown = 28

// printJSXGroups lists the highest-ranked JSX groups, each with its sites,
// all the result holds with --all.
func printJSXGroups(w io.Writer, list *jsxGroupList, all bool) {
	if list == nil || len(list.Groups) == 0 {
		return
	}
	shown := headOf(list.Groups, topJSXGroups, all)
	heading := fmt.Sprintf("JSX groups, %d of %d (rank: elements × (sites − 1) − holes)", len(shown), list.Total)
	header := []string{colSite, "", colRank, colSites, "blocks", "elements", "holes"}
	printSiteTable(w, heading, header, shown, jsxGroupRows)
}

// headOf is the part of a ranked list the text view shows: its first top
// items, and all of it with --all.
func headOf[T any](items []T, top int, all bool) []T {
	if all {
		return items
	}

	return items[:min(len(items), top)]
}

// printSiteTable prints a heading and, under it, one table of the rows each
// item of a list renders to, site first.
func printSiteTable[T any](w io.Writer, heading string, header []string, shown []T, rowsOf func(*T) [][]string) {
	rows := make([][]string, 0, len(shown))
	for i := range shown {
		rows = append(rows, rowsOf(&shown[i])...)
	}
	shortenFirstColumn(header, rows)
	report.Fprintf(w, "\n%s\n", heading)
	report.Table(w, header, rows)
}

// jsxGroupRows renders one group: its numbers beside the first site, then a
// row for each further site up to maxSitesShown and a count of the rest. A
// site's last line is in the JSON.
func jsxGroupRows(g *rules.JSXGroup) [][]string {
	rows := make([][]string, 0, maxSitesShown+1)
	for i := range g.Sites[:min(len(g.Sites), maxSitesShown)] {
		s := &g.Sites[i]
		row := []string{fmt.Sprintf("%s:%d", s.Path, s.Line), clip(s.Unit, maxJSXUnitShown), "", "", "", "", ""}
		if i == 0 {
			row[2] = strconv.Itoa(g.Rank)
			row[3] = strconv.Itoa(len(g.Sites))
			row[4] = strconv.Itoa(g.Blocks)
			row[5] = strconv.Itoa(g.Elements)
			row[6] = strconv.Itoa(g.Holes)
		}
		rows = append(rows, row)
	}
	if more := len(g.Sites) - maxSitesShown; more > 0 {
		rows = append(rows, []string{fmt.Sprintf("… and %d more", more), "", "", "", "", "", ""})
	}

	return rows
}

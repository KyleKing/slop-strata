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
	shown := list.Groups
	if !all {
		shown = shown[:min(len(shown), topJSXGroups)]
	}
	var rows [][]string
	for i := range shown {
		rows = append(rows, jsxGroupRows(&shown[i])...)
	}
	header := []string{colSite, "", "rank", colSites, "blocks", "elements", "holes"}
	shortenFirstColumn(header, rows)
	report.Fprintf(w, "\nJSX groups, %d of %d (rank: elements × (sites − 1) − holes)\n", len(shown), list.Total)
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

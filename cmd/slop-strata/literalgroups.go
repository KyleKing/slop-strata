package main

import (
	"fmt"
	"io"
	"strconv"

	"github.com/kyleking/slop-strata/internal/engine"
	"github.com/kyleking/slop-strata/internal/rules"
)

// literalGroupLimit caps the literal groups a snapshot lists.
const literalGroupLimit = 20

// literalGroupList is the strings a tree writes out in several files where a
// name would do: how many groups there are, and the ones that rank highest.
type literalGroupList struct {
	Groups []rules.LiteralGroup `json:"groups"`
	Total  int                  `json:"total"`
}

// literalGroups runs the literal pass over a snapshot's reports.
func literalGroups(reports []*engine.FileReport) *literalGroupList {
	groups := rules.LiteralGroups(scoredPrints(reports))
	out := &literalGroupList{Total: len(groups), Groups: groups[:min(len(groups), literalGroupLimit)]}
	if out.Groups == nil {
		out.Groups = []rules.LiteralGroup{}
	}

	return out
}

// topLiteralGroups is how many groups the text view lists without --all.
const topLiteralGroups = 5

// Widths the text view clips a group's columns to.
const (
	maxLiteralValueShown = 32
	maxLiteralNameShown  = 28
)

// printLiteralGroups lists the highest-ranked literal groups, each with its
// first files, all the result holds with --all.
func printLiteralGroups(w io.Writer, list *literalGroupList, all bool) {
	if list == nil || len(list.Groups) == 0 {
		return
	}
	shown := headOf(list.Groups, topLiteralGroups, all)
	heading := fmt.Sprintf("literal groups, %d of %d (rank: files × share of places that test or key on the value)",
		len(shown), list.Total)
	header := []string{colSite, "value", colRank, colFiles, "written", "uses", "constant"}
	printSiteTable(w, heading, header, shown, literalGroupRows)
}

// literalGroupRows renders one group: its numbers beside the first file, then
// a row for each further file up to maxSitesShown and a count of the rest.
// How often each file writes the value is in the JSON.
func literalGroupRows(g *rules.LiteralGroup) [][]string {
	rows := make([][]string, 0, maxSitesShown+1)
	for i := range g.Sites[:min(len(g.Sites), maxSitesShown)] {
		s := &g.Sites[i]
		row := []string{fmt.Sprintf("%s:%d", s.Path, s.Line), "", "", "", "", "", ""}
		if i == 0 {
			row[1] = clip(strconv.Quote(g.Value), maxLiteralValueShown)
			row[2] = strconv.FormatFloat(g.Rank, 'f', 1, 64)
			row[3] = strconv.Itoa(g.Files)
			row[4] = strconv.Itoa(g.Written)
			row[5] = strconv.Itoa(g.Uses)
			if g.Constant != nil {
				row[6] = clip(g.Constant.Name, maxLiteralNameShown)
			}
		}
		rows = append(rows, row)
	}
	if more := g.Files - maxSitesShown; more > 0 {
		rows = append(rows, []string{fmt.Sprintf("… and %d more", more), "", "", "", "", "", ""})
	}

	return rows
}

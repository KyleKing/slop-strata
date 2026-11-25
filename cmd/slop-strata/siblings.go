package main

import (
	"fmt"
	"io"
	"strconv"

	"github.com/kyleking/slop-strata/internal/engine"
	"github.com/kyleking/slop-strata/internal/rules"
)

// siblingSetLimit caps the sibling sets a snapshot lists.
const siblingSetLimit = 20

// siblingSetList is the classes, types, and modules a tree holds in
// parallel: how many sets there are, and the ones that rank highest.
type siblingSetList struct {
	Sets  []rules.SiblingSet `json:"sets"`
	Total int                `json:"total"`
}

// siblingSets runs the sibling pass over a snapshot's reports.
func siblingSets(reports []*engine.FileReport) *siblingSetList {
	sets := rules.SiblingSets(scoredPrints(reports))
	out := &siblingSetList{Total: len(sets), Sets: sets[:min(len(sets), siblingSetLimit)]}
	if out.Sets == nil {
		out.Sets = []rules.SiblingSet{}
	}

	return out
}

// topSiblingSets is how many sets the text view lists without --all.
const topSiblingSets = 5

// Widths the text view clips a set's columns to.
const (
	maxSiblingNameShown    = 24
	maxSiblingMembersShown = 26
)

// printSiblingSets lists the highest-ranked sibling sets, each with its
// sites, all the result holds with --all.
func printSiblingSets(w io.Writer, list *siblingSetList, all bool) {
	if list == nil || len(list.Sets) == 0 {
		return
	}
	shown := headOf(list.Sets, topSiblingSets, all)
	heading := fmt.Sprintf("sibling sets, %d of %d (rank: tokens in line × (sites − 1), per shared member)",
		len(shown), list.Total)
	header := []string{colSite, "", colRank, colSites, "members", "statements", "differing"}
	printSiteTable(w, heading, header, shown, siblingSetRows)
}

// siblingSetRows renders one set: its numbers beside the first site, then a
// row for each further site up to maxSitesShown and a count of the rest. A
// site's last line, its kind, the tokens in line, and each site's own members
// are in the JSON.
func siblingSetRows(s *rules.SiblingSet) [][]string {
	rows := make([][]string, 0, maxSitesShown+1)
	for i := range s.Sites[:min(len(s.Sites), maxSitesShown)] {
		site := &s.Sites[i]
		row := []string{
			fmt.Sprintf("%s:%d", site.Path, site.Line), clip(site.Name, maxSiblingNameShown), "", "", "", "", "",
		}
		if i == 0 {
			row[2] = strconv.Itoa(s.Rank)
			row[3] = strconv.Itoa(len(s.Sites))
			row[4] = counted(s.Members, maxSiblingMembersShown)
			row[5] = strconv.Itoa(s.Statements)
			row[6] = strconv.Itoa(s.Differing)
		}
		rows = append(rows, row)
	}
	if more := len(s.Sites) - maxSitesShown; more > 0 {
		rows = append(rows, []string{fmt.Sprintf("… and %d more", more), "", "", "", "", "", ""})
	}

	return rows
}

package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/kyleking/slop-strata/internal/engine"
	"github.com/kyleking/slop-strata/internal/report"
	"github.com/kyleking/slop-strata/internal/rules"
)

// caseSetLimit caps the case-set families a snapshot lists.
const caseSetLimit = 20

// caseSetList is the sets of cases a tree enumerates in more than one place:
// how many families there are, and the ones that rank highest.
type caseSetList struct {
	Families []rules.CaseFamily `json:"families"`
	Total    int                `json:"total"`
}

// caseSets runs the case pass over a snapshot's reports.
func caseSets(reports []*engine.FileReport) *caseSetList {
	families := rules.CaseFamilies(scoredPrints(reports))
	out := &caseSetList{Total: len(families), Families: families[:min(len(families), caseSetLimit)]}
	if out.Families == nil {
		out.Families = []rules.CaseFamily{}
	}

	return out
}

// topCaseSets is how many families the text view lists without --all.
const topCaseSets = 5

// Widths the text view clips a family's columns to.
const (
	maxCaseUnitShown = 20
	maxCasesShown    = 22
	maxLacksShown    = 16
)

// printCaseSets lists the highest-ranked case-set families, each with its
// sites, all the result holds with --all.
func printCaseSets(w io.Writer, list *caseSetList, all bool) {
	if list == nil || len(list.Families) == 0 {
		return
	}
	shown := list.Families
	if !all {
		shown = shown[:min(len(shown), topCaseSets)]
	}
	var rows [][]string
	for i := range shown {
		rows = append(rows, caseSetRows(&shown[i])...)
	}
	header := []string{colSite, "", colRank, colSites, "cases", "lacks"}
	shortenFirstColumn(header, rows)
	report.Fprintf(w, "\ncase sets, %d of %d (rank: sites × weight of the cases they share)\n", len(shown), list.Total)
	report.Table(w, header, rows)
}

// counted renders a list as its length and its first names, clipped.
func counted(names []string, width int) string {
	if len(names) == 0 {
		return ""
	}

	return clip(strconv.Itoa(len(names))+": "+strings.Join(names, ", "), width)
}

// caseSetRows renders one family: its numbers beside the first site, then a
// row for each further site up to maxSitesShown and a count of the rest.
// Each row ends with the family's cases the site does not name. A site's
// kind, its last line, and whether it has a default arm are in the JSON.
func caseSetRows(f *rules.CaseFamily) [][]string {
	rows := make([][]string, 0, maxSitesShown+1)
	for i := range f.Sites[:min(len(f.Sites), maxSitesShown)] {
		s := &f.Sites[i]
		row := []string{
			fmt.Sprintf("%s:%d", s.Path, s.Line), clip(s.Unit, maxCaseUnitShown), "", "", "",
			counted(s.Missing, maxLacksShown),
		}
		if i == 0 {
			row[2] = strconv.FormatFloat(f.Rank, 'f', 1, 64)
			row[3] = strconv.Itoa(len(f.Sites))
			row[4] = counted(f.Cases, maxCasesShown)
		}
		rows = append(rows, row)
	}
	if more := len(f.Sites) - maxSitesShown; more > 0 {
		rows = append(rows, []string{fmt.Sprintf("… and %d more", more), "", "", "", "", ""})
	}

	return rows
}

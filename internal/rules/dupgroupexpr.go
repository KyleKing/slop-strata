package rules

import (
	"cmp"
	"slices"
)

// exprShape is one expression shape the tree holds more than once, with the
// most its group could score.
type exprShape struct {
	sites  []shapeAt
	weight float64
	ntok   int
}

// expressionGroups lists the expressions that repeat inside statements no
// group of statements holds, the longest first, so an expression is reported
// and the smaller ones inside it are not.
func (p *groupPass) expressionGroups() []DupGroup {
	shapes := p.exprShapes()
	slices.SortStableFunc(shapes, func(a, b exprShape) int {
		return cmp.Or(cmp.Compare(b.ntok, a.ntok), cmp.Compare(a.sites[0].shape, b.sites[0].shape))
	})
	var out []DupGroup
	for i := range shapes {
		sites := p.gatherExprs(&shapes[i])
		if len(sites) < 1+1 {
			continue
		}
		for k := range sites {
			take(sites[k].file, sites[k].at, 1)
		}
		if isData(sites[0].cols) {
			continue
		}
		out = append(out, groupOf(sites, 1, shapes[i].ntok, true))
	}

	return out
}

// exprShapes lists the expression shapes that two or more places hold and
// that are rare enough to meet the floor.
func (p *groupPass) exprShapes() []exprShape {
	var out []exprShape
	for lo := 0; lo < len(p.subs); {
		hi := lo + 1
		for hi < len(p.subs) && p.subs[hi].shape == p.subs[lo].shape {
			hi++
		}
		sites := p.subs[lo:hi]
		lo = hi
		weigh := rarity(len(sites))
		if len(sites) < 1+1 || weigh == 0 {
			continue
		}
		f := p.all[sites[0].file]
		f.decoded()
		if int(sites[0].idx) >= len(f.subs) {
			continue // a payload cut short
		}
		ntok := f.subs[sites[0].idx].ntok
		if weight := float64(ntok) * weigh; weight >= dupExprMin {
			out = append(out, exprShape{sites: sites, weight: weight, ntok: ntok})
		}
	}

	return out
}

// gatherExprs lists the sites of one expression's group: the first place
// that may hold one, then the others, fewest differences first, while the
// group stays within its parameter budget.
func (p *groupPass) gatherExprs(shape *exprShape) []groupSite {
	var sites, rest []groupSite
	for _, at := range shape.sites {
		cand, ok := p.exprSite(at, shape.ntok)
		switch {
		case !ok:
		case len(sites) == 0:
			sites = append(sites, cand)
		case len(cand.cols) == len(sites[0].cols) && diffCols(sites[0].cols, cand.cols) <= dupMaxParams:
			rest = append(rest, cand)
		}
	}
	if len(sites) == 0 {
		return nil
	}
	first := sites[0]
	slices.SortStableFunc(rest, func(a, b groupSite) int {
		return cmp.Compare(diffCols(first.cols, a.cols), diffCols(first.cols, b.cols))
	})
	budget := paramBudget(shape.weight)
	for _, cand := range rest {
		if !p.fitsExpr(sites, cand) {
			continue
		}
		if sites = append(sites, cand); len(paramCols(sites)) > budget {
			sites = sites[:len(sites)-1]
		}
	}

	return sites
}

// exprSite answers one expression as a site, when it may be one: in
// production code, outside a signature, and in a statement no group holds.
func (p *groupPass) exprSite(at shapeAt, ntok int) (groupSite, bool) {
	f := p.all[at.file]
	if f.aside {
		return groupSite{}, false
	}
	f.decoded()
	if int(at.idx) >= len(f.subs) {
		return groupSite{}, false // a payload cut short
	}
	sub := &f.subs[at.idx]
	st := &f.stmts[sub.stmt]
	if sub.ntok != ntok || st.header || anyTaken(f, sub.stmt, 1) {
		return groupSite{}, false
	}
	cols := make([]string, 0, sub.nslots)
	for _, id := range st.slots[sub.slot : sub.slot+sub.nslots] {
		cols = append(cols, f.names[id])
	}

	return groupSite{file: f, at: sub.stmt, sub: int(at.idx), cols: cols}, true
}

// fitsExpr reports whether an expression may join the sites gathered so far,
// under the rule a change is held to: within one file each sits in a function
// of its own, since an expression twice in one function is a local variable
// its author chose not to make, and no two sit in files kept in step.
func (p *groupPass) fitsExpr(sites []groupSite, cand groupSite) bool {
	unit := unitAt(cand.file.fp, cand.file.stmts[cand.at].line)
	for i := range sites {
		s := &sites[i]
		if s.file != cand.file {
			if p.mirror(s.file, cand.file) {
				return false
			}

			continue
		}
		if held := unitAt(s.file.fp, s.file.stmts[s.at].line); unit == "" || held == "" || unit == held {
			return false
		}
	}

	return true
}

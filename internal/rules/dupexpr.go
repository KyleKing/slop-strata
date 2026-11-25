package rules

import "cmp"

// dupExprMin is the least an expression must score to be reported: its
// tokens, weighted by how rare its shape is among the tree's expressions,
// less dupParamCost for every parameter. It is the floor a run of statements
// meets, so an expression under subMinTokens tokens could never be reported.
const dupExprMin = dupMinScore

// exprs lists the expressions of q that repeat one elsewhere: per new
// statement no hit covers, the expression that scores highest, with every
// site that holds it.
func (p *dupPass) exprs(q *dupFile, covered []bool) []dupHit {
	best := map[int]dupHit{}
	var order []int
	for i := range q.subs {
		sub := &q.subs[i]
		if covered[sub.stmt] || !q.fresh[sub.stmt] || q.stmts[sub.stmt].header ||
			p.net.subExact[sub.exact] <= 0 || p.net.subShape[sub.shape] <= 0 {
			continue
		}
		hit, ok := p.exprHit(q, i)
		if !ok {
			continue
		}
		cur, seen := best[sub.stmt]
		if !seen {
			order = append(order, sub.stmt)
		}
		if !seen || hit.runs[0].score > cur.runs[0].score {
			best[sub.stmt] = hit
		}
	}
	out := make([]dupHit, 0, len(order))
	for _, stmt := range order {
		out = append(out, best[stmt])
	}

	return out
}

// exprHit gathers the sites that repeat expression i of q and meet the floor.
func (p *dupPass) exprHit(q *dupFile, i int) (dupHit, bool) {
	sub := &q.subs[i]
	weight := float64(sub.ntok) * rarity(p.subDF[sub.shape])
	if weight < dupExprMin {
		return dupHit{}, false
	}
	var runs []dupRun
	for _, site := range p.subOcc[sub.shape] {
		c := site.file
		c.decoded()
		if site.idx >= len(c.subs) || !p.pairsExpr(q, i, site) {
			continue // the first is a payload cut short
		}
		run := dupRun{
			file: q, with: c, q: sub.stmt, c: c.subs[site.idx].stmt, n: 1, qsub: i, csub: site.idx,
			tokens: sub.ntok, weight: weight, expr: true,
		}
		if !run.diffSubs() {
			continue
		}
		run.score = weight - dupParamCost*float64(len(run.pairs))
		if run.score >= dupExprMin {
			runs = append(runs, run)
		}
	}
	if len(runs) == 0 {
		return dupHit{}, false
	}
	sortRuns(runs)
	hit := dupHit{}
	for i := range runs {
		if !hit.has(&runs[i]) {
			hit.runs = append(hit.runs, runs[i])
		}
	}

	return hit, true
}

// pairsExpr reports whether expression i of q may be matched with a site,
// under the rules for statements: never in a signature, not with a copy the
// change added further on, and not across files the pass keeps apart. Within
// one file the two must sit in different functions, since an expression twice
// in one function is a local variable its author chose not to make.
func (p *dupPass) pairsExpr(q *dupFile, i int, site dupSite) bool {
	c := site.file
	mine, theirs := &q.subs[i], &c.subs[site.idx]
	if mine.ntok != theirs.ntok || mine.nslots != theirs.nslots || c.stmts[theirs.stmt].header {
		return false
	}
	if c != q {
		return p.pairsFile(q, c, c.wasAdded(theirs.stmt))
	}
	if q.wasAdded(theirs.stmt) && site.idx > i {
		return false
	}
	here, there := unitAt(q.fp, q.stmts[mine.stmt].line), unitAt(q.fp, q.stmts[theirs.stmt].line)

	return here != there && here != "" && there != ""
}

// diffSubs fills in the texts the two expressions of a run differ in, and
// reports whether they are few enough. The two hold as many slots as each
// other: pairsExpr turns away two shapes that share a hash.
func (r *dupRun) diffSubs() bool {
	a, b := &r.file.subs[r.qsub], &r.with.subs[r.csub]
	mine := r.file.stmts[a.stmt].slots[a.slot : a.slot+a.nslots]
	theirs := r.with.stmts[b.stmt].slots[b.slot : b.slot+b.nslots]
	seen := map[[2]string]bool{}
	for k := range mine {
		x, y := r.file.names[mine[k]], r.with.names[theirs[k]]
		if pair := [2]string{y, x}; x != y && !seen[pair] {
			seen[pair] = true
			r.pairs = append(r.pairs, pair)
		}
	}

	return len(r.pairs) <= dupMaxParams
}

// byLine orders hits by where they start in the changed file.
func byLine(a, b dupHit) int { return cmp.Compare(a.runs[0].qLine(), b.runs[0].qLine()) }

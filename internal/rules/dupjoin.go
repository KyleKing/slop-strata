package rules

import (
	"cmp"
	"slices"
)

// dupJoinMin is the least the stretches two functions share must add up to,
// when none of them meets the floor alone.
const dupJoinMin = 20

// joinKey names the two functions a stretch runs between.
type joinKey struct {
	with       *dupFile
	here, held string
}

// joins adds up the stretches too short to report alone: the ones q's
// function shares with one other function, in any order and with other
// statements between them. That is a copy with a statement added, dropped,
// reshaped, or moved. The stretches may not overlap, differ together in no
// more names than one run may, and are scored as one run. Stretches inside
// one function are left alone: those are the arms of a branch, which sit
// together for a reader to compare. So are the stretches between two
// functions a hit already names, which has said where the copy is.
func joins(q *dupFile, parts []dupRun, hits []dupHit) []dupHit {
	covered := coveredBy(q, hits)
	named := map[joinKey]bool{}
	for i := range hits {
		named[hits[i].runs[0].between()] = true
	}
	groups := map[joinKey][]dupRun{}
	var keys []joinKey
	for i := range parts {
		r := &parts[i]
		key := r.between()
		if key.here == "" || key.held == "" || (r.with == q && key.here == key.held) || named[key] ||
			r.anyCovered(covered) {
			continue
		}
		if _, ok := groups[key]; !ok {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], *r)
	}

	return pickJoins(groups, keys, covered)
}

// pickJoins joins each pair of functions and keeps the joins heaviest first,
// each over statements no other has taken: a stretch three functions share
// is reported once, against the one that shares the most with it, and the
// other function is one more site of that hit.
func pickJoins(groups map[joinKey][]dupRun, keys []joinKey, covered []bool) []dupHit {
	joined := make([]dupRun, 0, len(keys))
	for _, key := range keys {
		if run, ok := joinRuns(groups[key]); ok {
			joined = append(joined, run)
		}
	}
	sortRuns(joined)
	var out []dupHit
	for i := range joined {
		run := &joined[i]
		if run.takes(covered) {
			out = append(out, dupHit{runs: []dupRun{*run}})

			continue
		}
		for k := range out {
			if sharesCopy(out[k].runs[0].parts, run.parts) {
				out[k].runs = append(out[k].runs, *run)

				break
			}
		}
	}

	return out
}

// takes marks the statements of a joined run as reported, unless a hit
// already reports one of them, and reports whether it marked them.
func (r *dupRun) takes(covered []bool) bool {
	for j := range r.parts {
		if r.parts[j].anyCovered(covered) {
			return false
		}
	}
	for j := range r.parts {
		for k := r.parts[j].q; k < r.parts[j].q+r.parts[j].n; k++ {
			covered[k] = true
		}
	}

	return true
}

// between names the two functions a run of statements sits in.
func (r *dupRun) between() joinKey {
	return joinKey{
		with: r.with,
		here: unitAt(r.file.fp, r.file.stmts[r.q].line),
		held: unitAt(r.with.fp, r.with.decoded()[r.c].line),
	}
}

// anyCovered reports whether a hit already reports a statement of the run.
func (r *dupRun) anyCovered(covered []bool) bool {
	for k := r.q; k < r.q+r.n; k++ {
		if covered[k] {
			return true
		}
	}

	return false
}

// joinRuns picks, heaviest first, the stretches between two functions that
// do not overlap and stay within the parameter budget together, and answers
// them as one run when there are two or more and they meet the floor.
func joinRuns(runs []dupRun) (dupRun, bool) {
	sortRuns(runs)
	var parts []dupRun
	var pairs [][2]string
	weight, tokens := 0.0, 0
	for i := range runs {
		r := &runs[i]
		merged := unionPairs(pairs, r.pairs)
		if len(merged) > dupMaxParams || overlapsAny(parts, r) {
			continue
		}
		parts, pairs = append(parts, *r), merged
		weight += r.weight
		tokens += r.tokens
	}
	if len(parts) < 1+1 {
		return dupRun{}, false
	}
	slices.SortFunc(parts, func(a, b dupRun) int { return cmp.Compare(a.q, b.q) })
	run := parts[0]
	run.parts, run.pairs, run.weight, run.tokens = parts, pairs, weight, tokens
	run.score = weight - dupParamCost*float64(len(pairs))

	return run, run.score >= dupJoinMin
}

// sharesCopy reports whether two joins hold a statement of the changed file
// in common.
func sharesCopy(a, b []dupRun) bool {
	for i := range a {
		for j := range b {
			if a[i].q < b[j].q+b[j].n && b[j].q < a[i].q+a[i].n {
				return true
			}
		}
	}

	return false
}

// unionPairs answers the distinct pairs of both lists.
func unionPairs(a, b [][2]string) [][2]string {
	out := slices.Clone(a)
	for _, pair := range b {
		if !slices.Contains(out, pair) {
			out = append(out, pair)
		}
	}

	return out
}

// overlapsAny reports whether a run shares a statement with a chosen one, on
// either side.
func overlapsAny(parts []dupRun, r *dupRun) bool {
	for i := range parts {
		o := &parts[i]
		if (o.q < r.q+r.n && r.q < o.q+o.n) || (o.c < r.c+r.n && r.c < o.c+o.n) {
			return true
		}
	}

	return false
}

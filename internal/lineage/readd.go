package lineage

import (
	"hash/maphash"
	"strings"

	"github.com/kyleking/aragonite/vcs"
)

// ReaddDays is how long after a young death a matching added line still
// counts as the line coming back.
const ReaddDays = 30

// ReaddTextFloor is the fewest trimmed bytes a line needs before an added line
// matching a dead one reads as a re-addition. Below it, idioms like "return nil"
// recur across a file by habit.
const ReaddTextFloor = 30

// pendingDeath is a young death still open to a re-addition, credited to the
// directory the line was written in.
type pendingDeath struct {
	dir    string
	day    int
	revert bool
}

// pendingRef orders pendingDeath records by death day for eviction.
type pendingRef struct {
	key uint64
	day int
}

// readdDay is the re-addition tally of the young deaths on one day.
type readdDay struct {
	atRisk, readded, byRevert int
}

// readdTracker matches added lines against the trailing 30 days of young
// deaths, holding each as a 64-bit hash of its file path and trimmed text.
type readdTracker struct {
	pending map[uint64][]pendingDeath
	days    map[string]map[int]*readdDay
	queue   []pendingRef
	seed    maphash.Seed
}

func newReaddTracker() *readdTracker {
	return &readdTracker{
		pending: map[uint64][]pendingDeath{}, days: map[string]map[int]*readdDay{}, seed: maphash.MakeSeed(),
	}
}

// readdText trims a line and reports whether it is long enough to match.
func readdText(line string) (string, bool) {
	text := strings.TrimSpace(line)

	return text, len(text) >= ReaddTextFloor
}

func (r *readdTracker) key(path, text string) uint64 {
	return maphash.Comparable(r.seed, [2]string{path, text})
}

// evict drops the deaths too old for a commit on day to match.
func (r *readdTracker) evict(day int) {
	cutoff := day - ReaddDays
	n := 0
	for n < len(r.queue) && r.queue[n].day < cutoff {
		open := r.pending[r.queue[n].key]
		for len(open) > 0 && open[0].day < cutoff {
			open = open[1:]
		}
		if len(open) == 0 {
			delete(r.pending, r.queue[n].key)
		} else {
			r.pending[r.queue[n].key] = open
		}
		n++
	}
	r.queue = r.queue[n:]
}

func (r *readdTracker) tally(dir string, day int) *readdDay {
	if r.days[dir] == nil {
		r.days[dir] = map[int]*readdDay{}
	}
	if r.days[dir][day] == nil {
		r.days[dir][day] = &readdDay{}
	}

	return r.days[dir][day]
}

// register opens a young death to a later re-addition.
func (r *readdTracker) register(path, line, dir string, dc deathCtx) {
	text, ok := readdText(line)
	if !ok {
		return
	}
	key := r.key(path, text)
	r.pending[key] = append(r.pending[key], pendingDeath{dir: dir, day: dc.day, revert: dc.revert})
	r.queue = append(r.queue, pendingRef{key: key, day: dc.day})
	r.tally(dir, dc.day).atRisk++
}

// match closes the oldest open death of the same file and text, if any, and
// credits it as re-added by the commit on day.
func (r *readdTracker) match(path, line string, day int, revert bool) {
	text, ok := readdText(line)
	if !ok {
		return
	}
	key := r.key(path, text)
	open := r.pending[key]
	if len(open) == 0 || open[0].day > day || day-open[0].day > ReaddDays {
		return
	}
	d := open[0]
	if len(open) == 1 {
		delete(r.pending, key)
	} else {
		r.pending[key] = open[1:]
	}
	t := r.tally(d.dir, d.day)
	if d.revert || revert {
		t.byRevert++
	} else {
		t.readded++
	}
}

// matchReadds closes the open deaths a commit's added lines bring back. It runs
// before the commit's own deaths open, so a line removed and added in one commit
// is never a re-addition, and a moved line is never one either.
func (w *Walker) matchReadds(rec vcs.CommitRecord, mv *moves, day int, revert bool) {
	w.readd.evict(day)
	if len(w.readd.pending) == 0 {
		return
	}
	for i := range rec.Files {
		f := &rec.Files[i]
		if f.Status == vcs.StatusDeleted {
			continue
		}
		for j, line := range f.Added {
			if _, moved := mv.tags[addedLine{f.Path, j}]; !moved {
				w.readd.match(f.Path, line, day, revert)
			}
		}
	}
}

// foldReadds adds the re-addition tallies of the deaths that had ReaddDays of
// walk after them to their directories' deaths. A death nearer the walk's end
// is left out of every count, the way survival leaves out a line too young to
// have reached its horizon.
func (w *Walker) foldReadds() {
	for dir, byDay := range w.readd.days {
		for day, t := range byDay {
			if day+ReaddDays > w.lastDay {
				continue
			}
			d := &w.dirStats(dir).Deaths30
			d.ReaddAtRisk += t.atRisk
			d.Readded += t.readded
			d.ReaddedByRevert += t.byRevert
		}
	}
}

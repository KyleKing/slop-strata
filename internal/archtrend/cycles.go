// Package archtrend follows an inferred architecture across the revisions a
// history walk samples: which import cycle of one sample continues a cycle of
// the sample before, and which files change together with no import between
// them. It reads member lists and a resolved graph, and never a blob.
package archtrend

import "slices"

// sharedToContinue is how many files two cycles must share for one to continue
// the other: the smallest cycle there is.
const sharedToContinue = 2

// Track is one import cycle followed from the sample it first appeared in.
type Track struct {
	// Members are the cycle's files at the latest sample that held it, sorted.
	Members []string
	// FirstMembers are its files at the first sample, under the paths they
	// had then.
	FirstMembers []string
	// First is the index of the sample the cycle first appeared in.
	First int
	// Origin numbers the earliest cycle this one continues, so a cycle keeps
	// its number as it grows, shrinks, merges, and splits.
	Origin int
}

// Cycles follows import cycles across samples taken in order.
type Cycles struct {
	live []*Track
	next int
}

// Continues reports whether two cycles, each a sorted member list, share at
// least two files. Two files that import each other in both are a dependency
// that held, however many files joined or left around them, so this includes
// the subset relation a baseline entry covers a cycle by.
func Continues(a, b []string) bool {
	shared := 0
	for i, j := 0, 0; i < len(a) && j < len(b); {
		switch {
		case a[i] < b[j]:
			i++
		case a[i] > b[j]:
			j++
		default:
			shared++
			i++
			j++
		}
	}

	return shared >= sharedToContinue
}

// Rename moves a file of the cycles being followed to a new path, so a rename
// between two samples breaks no cycle's history.
func (c *Cycles) Rename(from, to string) {
	for _, t := range c.live {
		t.Members = Renamed(t.Members, from, to)
	}
}

// Renamed answers members with one path replaced, sorted. It answers members
// itself when from is not among them.
func Renamed(members []string, from, to string) []string {
	i, ok := slices.BinarySearch(members, from)
	if !ok {
		return members
	}
	out := slices.Clone(members)
	out[i] = to
	slices.Sort(out)

	return slices.Compact(out)
}

// Observe takes the cycles of the next sample, each a sorted member list, and
// answers their tracks in the same order. A cycle continues the oldest earlier
// cycle it shares two files with, and starts a track when there is none. A
// cycle of the sample before that nothing continues is dropped, so one that
// goes away and comes back starts again.
func (c *Cycles) Observe(sample int, cycles [][]string) []*Track {
	now := make([]*Track, 0, len(cycles))
	for _, members := range cycles {
		t := &Track{Members: members, FirstMembers: members, First: sample, Origin: -1}
		for _, old := range c.live {
			if Continues(old.Members, members) && (t.Origin < 0 || older(old, t)) {
				t.FirstMembers, t.First, t.Origin = old.FirstMembers, old.First, old.Origin
			}
		}
		if t.Origin < 0 {
			t.Origin = c.next
			c.next++
		}
		now = append(now, t)
	}
	c.live = now

	return now
}

// older orders two tracks by first sample, then by origin, so a merge of two
// cycles inherits the same one on every run.
func older(a, b *Track) bool {
	if a.First != b.First {
		return a.First < b.First
	}

	return a.Origin < b.Origin
}

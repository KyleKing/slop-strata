package depth

import "slices"

// maxListed caps the names a Widening lists.
const maxListed = 5

// Widening is one module whose surface grew between two trees: other modules
// use more of its names than they did. Only attributed uses count, since a
// method matched by name may be another type's.
type Widening struct {
	// DepthBefore and DepthAfter are the module's attributed depth on each
	// side, nil where no attributed use reached it.
	DepthBefore *float64 `json:"depth_before"`
	DepthAfter  *float64 `json:"depth_after"`
	Dir         string   `json:"dir"`
	// NewlyShown lists up to five of the names attributed uses reach now and
	// did not before, sorted, and NewlyShownCount counts them all.
	NewlyShown      []string `json:"newly_shown"`
	NewlyShownCount int      `json:"newly_shown_count"`
	// NewlyShownByName counts the methods matched by name now and not
	// before. They are unverified, so they are counted and never listed.
	NewlyShownByName int `json:"newly_shown_by_name"`
	// ShownBefore and ShownAfter count the attributed shown names.
	ShownBefore int `json:"shown_before"`
	ShownAfter  int `json:"shown_after"`
}

// Widened lists the modules present on both sides whose count of attributed
// shown names rose, sorted by name. A module the change created has no
// surface to widen, a rename that swaps one shown name for another widens
// nothing, and neither does a method that only a name match shows.
func Widened(before, after map[string]*Module) []Widening {
	out := []Widening{}
	for dir, now := range after {
		was := before[dir]
		if was == nil || now.Attributed() <= was.Attributed() {
			continue
		}
		added := newNames(was.ShownNames, now.ShownNames)
		if len(added) == 0 {
			continue
		}
		out = append(out, Widening{
			Dir: dir, NewlyShown: added[:min(len(added), maxListed)], NewlyShownCount: len(added),
			NewlyShownByName: len(newNames(was.NameMatched, now.NameMatched)),
			ShownBefore:      was.Attributed(), ShownAfter: now.Attributed(),
			DepthBefore: was.DepthAttributed, DepthAfter: now.DepthAttributed,
		})
	}
	slices.SortFunc(out, func(a, b Widening) int {
		switch {
		case a.Dir < b.Dir:
			return -1
		case a.Dir > b.Dir:
			return 1
		}

		return 0
	})

	return out
}

// newNames lists the names of now that was lacks, each once. Both are sorted.
func newNames(was, now []string) []string {
	var added []string
	for _, name := range now {
		if _, had := slices.BinarySearch(was, name); !had {
			added = append(added, name)
		}
	}

	return slices.Compact(added)
}

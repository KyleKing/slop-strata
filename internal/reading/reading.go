// Package reading renders a scored change as one deterministic sentence, so
// the same numbers always say the same thing.
package reading

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/kyleking/slop-strata/internal/intent"
)

// Dir is one touched directory's share of a change.
type Dir struct {
	Name      string
	MassNet   float64
	MassGrown float64
	Hotspot   bool
}

func (d Dir) moved() bool {
	return d.MassNet != 0 || d.MassGrown != 0
}

// Fields is what a reading is built from.
type Fields struct {
	Intent          intent.Intent
	Dirs            []Dir
	MassAdded       float64
	MassRemoved     float64
	MassGrown       float64
	ErosionBefore   float64
	ErosionAfter    float64
	FuncHighAdded   int // functions over CC 10 that the change introduced
	FuncHighTouched int // functions over CC 10 that existed before and changed
	FuncHighGone    int // functions over CC 10 that disappeared
	FindingsNew     int
	FindingsGone    int
}

// Disagreement reports whether the numbers argue against the stated intent.
func (f Fields) Disagreement() string {
	switch {
	case f.Intent.Kind == intent.Refactor && f.MassAdded > f.MassRemoved:
		return "a refactor that adds mass"
	case f.Intent.Kind == intent.Refactor && f.ErosionAfter > f.ErosionBefore:
		return "a refactor that raises erosion"
	case f.Intent.Kind == intent.Fix && f.MassAdded > 2*f.MassRemoved && f.MassRemoved > 0:
		return "a fix that adds far more than it touches"
	case f.Intent.Kind == intent.Test && f.MassAdded > 0:
		return "a test change that adds production mass"
	default:
		return ""
	}
}

// primaryDir picks the directory the change mostly happened in: the largest
// absolute net mass, then the most mass grown, then the name.
func primaryDir(dirs []Dir) (Dir, bool) {
	if len(dirs) == 0 {
		return Dir{}, false
	}
	best := slices.MaxFunc(dirs, func(a, b Dir) int {
		return cmp.Or(
			cmp.Compare(math.Abs(a.MassNet), math.Abs(b.MassNet)),
			cmp.Compare(a.MassGrown, b.MassGrown),
			cmp.Compare(b.Name, a.Name),
		)
	})

	return best, true
}

// functions counts functions with the right noun.
func functions(n int) string {
	if n == 1 {
		return "1 function"
	}

	return fmt.Sprintf("%d functions", n)
}

// Sentence renders the one-line reading.
func (f Fields) Sentence() string {
	var b strings.Builder

	where := ""
	if dir, ok := primaryDir(f.Dirs); ok {
		where = " in `" + dir.Name + "`"
		if dir.Hotspot && dir.moved() {
			where += " (a hotspot)"
		}
	}
	label := string(f.Intent.Kind)
	if label == "unknown" {
		label = "change"
	}
	fmt.Fprintf(&b, "%s%s: ", strings.ToUpper(label[:1])+label[1:], where)

	if f.MassAdded == 0 && f.MassRemoved == 0 {
		b.WriteString("no complexity mass moved")
	} else {
		fmt.Fprintf(&b, "net %+.1f mass, %.1f grown", f.MassAdded-f.MassRemoved, f.MassGrown)
	}

	if f.FuncHighAdded > 0 {
		fmt.Fprintf(&b, "; added %s over CC 10", functions(f.FuncHighAdded))
	}
	if f.FuncHighTouched > 0 {
		fmt.Fprintf(&b, "; touched %s over CC 10", functions(f.FuncHighTouched))
	}
	if f.FuncHighGone > 0 {
		fmt.Fprintf(&b, "; removed %s over CC 10", functions(f.FuncHighGone))
	}

	if f.ErosionBefore != f.ErosionAfter {
		fmt.Fprintf(&b, "; erosion %.2f → %.2f", f.ErosionBefore, f.ErosionAfter)
	}
	if f.FindingsNew > 0 {
		fmt.Fprintf(&b, "; %d new rule hits", f.FindingsNew)
	}
	if d := f.Disagreement(); d != "" {
		fmt.Fprintf(&b, " — reads as %s", d)
	}

	return b.String()
}

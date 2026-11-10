// Package reading renders a scored change as one deterministic sentence, so
// the same numbers always say the same thing.
package reading

import (
	"fmt"
	"strings"

	"github.com/kyleking/slop-strata/internal/intent"
)

// Fields is what a reading is built from.
type Fields struct {
	Intent          intent.Intent
	TouchedDirs     []string
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
	Hotspot         bool
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
	if len(f.TouchedDirs) > 0 {
		where = " in `" + f.TouchedDirs[0] + "`"
	}
	label := string(f.Intent.Kind)
	if label == "unknown" {
		label = "change"
	}
	if f.Hotspot {
		where += " (a hotspot)"
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

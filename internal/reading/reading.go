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
	Intent        intent.Intent
	TouchedDirs   []string
	MassAdded     float64
	MassRemoved   float64
	ErosionBefore float64
	ErosionAfter  float64
	FuncHighAdded int // functions crossing CC 10 that appeared
	FuncHighGone  int // functions over CC 10 that disappeared
	FindingsNew   int
	FindingsGone  int
	Hotspot       bool
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

	switch {
	case f.MassAdded == 0 && f.MassRemoved == 0:
		b.WriteString("no complexity mass moved")
	case f.MassAdded >= f.MassRemoved:
		fmt.Fprintf(&b, "added %.1f mass, removed %.1f", f.MassAdded, f.MassRemoved)
	default:
		fmt.Fprintf(&b, "removed %.1f mass, added %.1f", f.MassRemoved, f.MassAdded)
	}

	switch {
	case f.FuncHighGone > 0 && f.FuncHighAdded == 0:
		fmt.Fprintf(&b, "; removed %d functions over CC 10", f.FuncHighGone)
	case f.FuncHighAdded > 0:
		fmt.Fprintf(&b, "; added %d functions over CC 10", f.FuncHighAdded)
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

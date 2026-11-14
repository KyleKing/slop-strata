package rules

// Suppressor sets findings aside and counts why: a false-positive label or the
// baseline. Each finding counts once, under the first reason in that order.
type Suppressor struct {
	baseline Baseline
	labels   Labels

	// Labeled and Baselined count the findings set aside so far.
	Labeled   int
	Baselined int
}

// NewSuppressor builds a Suppressor over a baseline and the labels file.
func NewSuppressor(baseline Baseline, labels Labels) *Suppressor {
	return &Suppressor{baseline: baseline, labels: labels}
}

// Apply gives one file's findings their IDs and drops the ones set aside.
func (s *Suppressor) Apply(path string, fs []Finding) []Finding {
	fs = Identify(path, fs)
	var kept []Finding
	for _, f := range fs {
		switch {
		case s.labels.IsFalsePositive(f.ID):
			s.Labeled++
		case s.baseline[f.ID]:
			s.Baselined++
		default:
			kept = append(kept, f)
		}
	}

	return kept
}

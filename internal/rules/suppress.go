package rules

// Suppressor sets findings aside and counts each once: ignore first, then label, then baseline.
type Suppressor struct {
	baseline Baseline
	labels   Labels

	// Ignored, Labeled, and Baselined count the findings set aside so far.
	Ignored   int
	Labeled   int
	Baselined int
}

// NewSuppressor builds a Suppressor over a baseline and the labels file.
func NewSuppressor(baseline Baseline, labels Labels) *Suppressor {
	return &Suppressor{baseline: baseline, labels: labels}
}

// Apply gives one file's findings their IDs and drops the ones set aside. The
// fingerprint, when present, carries the file's inline directives.
func (s *Suppressor) Apply(path string, fp *Fingerprint, fs []Finding) []Finding {
	fs = Identify(path, fs)
	var directives []Directive
	if fp != nil {
		directives = fp.Directives
	}
	var kept []Finding
	for _, f := range fs {
		switch {
		case covered(directives, f):
			s.Ignored++
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

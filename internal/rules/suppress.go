package rules

// Suppressor sets findings aside and counts each once: ignore first, then label, then baseline.
type Suppressor struct {
	labels   Labels
	seen     map[string]bool
	baseline Baseline

	// Ignored, Labeled, and Baselined count the findings set aside so far.
	Ignored   int
	Labeled   int
	Baselined int
}

// NewSuppressor builds a Suppressor over a baseline and the labels file.
func NewSuppressor(baseline Baseline, labels Labels) *Suppressor {
	return &Suppressor{baseline: baseline, labels: labels, seen: map[string]bool{}}
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
	for i := range fs {
		f := &fs[i]
		s.seen[f.ID] = true
		switch {
		case covered(directives, *f):
			s.Ignored++
		case s.labels.IsFalsePositive(f.ID):
			s.Labeled++
		case s.baseline.Covers(f):
			s.Baselined++
		default:
			kept = append(kept, *f)
		}
	}

	return kept
}

// StaleBaseline counts the baseline entries no finding seen so far carries
// the ID of: the finding was fixed, its line was edited, or, for a set-shaped
// finding, it shrank and the entry now covers more than is there. It is only
// meaningful once every file of the tree went through Apply. An entry whose
// rule ran reports false for is left out, since a rule that did not run
// cannot say whether its findings are gone.
func (s *Suppressor) StaleBaseline(ran func(rule string) bool) int {
	stale := 0
	for _, e := range s.baseline.entries {
		if !s.seen[e.ID] && ran(e.Rule) {
			stale++
		}
	}

	return stale
}

// Package intent answers what a change set out to do, read off the commit
// metadata rather than inferred from the diff.
package intent

import (
	"regexp"
	"strings"
)

// Kind names the change's stated purpose.
type Kind string

// Kinds.
const (
	Feat     Kind = "feat"
	Fix      Kind = "fix"
	Refactor Kind = "refactor"
	Test     Kind = "test"
	Chore    Kind = "chore"
	Unknown  Kind = "unknown"
)

// ccPattern parses a Conventional Commit subject.
var ccPattern = regexp.MustCompile(`^(\w+)(?:\(([^)]*)\))?(!)?:\s*(.+)`) //nolint:gochecknoglobals // a compiled pattern is a constant

// revertPattern spots a revert by subject or body.
var revertPattern = regexp.MustCompile(`(?i)\brevert\b`) //nolint:gochecknoglobals // a compiled pattern is a constant

// Intent is a change's stated purpose plus how it was read.
type Intent struct {
	Kind    Kind   `json:"kind"`
	Scope   string `json:"scope,omitempty"`
	Revert  bool   `json:"revert,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// Of reads the intent from a commit subject plus the branch name, falling back
// to issue keywords when both are silent.
func Of(subject, branch, body string) Intent {
	in := Intent{}
	if m := ccPattern.FindStringSubmatch(strings.TrimSpace(subject)); m != nil {
		in.Summary = m[4]
		switch Kind(strings.ToLower(m[1])) {
		case Feat:
			in.Kind = Feat
		case Fix:
			in.Kind = Fix
		case Refactor, "perf", "style", "cleanup", "chore-cleanup":
			in.Kind = Refactor
		case Test, "docs":
			in.Kind = Test
		case Chore, "ci", "build", "bump", "deps":
			in.Kind = Chore
		default:
			in.Kind = Unknown
		}
		in.Scope = m[2]
	} else {
		in.Kind = Unknown
		in.Summary = strings.TrimSpace(subject)
	}

	if revertPattern.MatchString(subject) || strings.Contains(body, "This reverts commit") {
		in.Revert = true
	}

	// A branch name refines an unknown or ambiguous reading: "fix/login-loop"
	// says what the merge did not.
	if in.Kind == Unknown && branch != "" {
		first, _, _ := strings.Cut(strings.ToLower(branch), "/")
		switch first {
		case "feat", "feature":
			in.Kind = Feat
		case "fix", "bug", "hotfix":
			in.Kind = Fix
		case "refactor", "cleanup":
			in.Kind = Refactor
		case "test":
			in.Kind = Test
		case "chore":
			in.Kind = Chore
		}
	}

	return in
}

// ForChange folds the intents of a change's commits into one: the majority
// kind, reverting if any commit reverts.
func ForChange(intents []Intent) Intent {
	if len(intents) == 0 {
		return Intent{Kind: Unknown}
	}
	counts := map[Kind]int{}
	out := intents[0]
	for _, in := range intents {
		counts[in.Kind]++
		if in.Revert {
			out.Revert = true
		}
	}
	best, n := Unknown, 0
	for k, c := range counts {
		if c > n && k != Unknown {
			best, n = k, c
		}
	}
	if n > 0 {
		out.Kind = best
	}

	return out
}

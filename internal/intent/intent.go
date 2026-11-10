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
//
//nolint:gochecknoglobals // a compiled pattern is a constant
var ccPattern = regexp.MustCompile(`^(\w+)(?:\(([^)]*)\))?(!)?:\s*(.+)`)

// revertPattern spots a revert by subject or body.
var revertPattern = regexp.MustCompile(`(?i)\brevert\b`) //nolint:gochecknoglobals // a compiled pattern is a constant

// Intent is a change's stated purpose plus how it was read.
type Intent struct {
	Kind    Kind   `json:"kind"`
	Scope   string `json:"scope,omitempty"`
	Summary string `json:"summary,omitempty"`
	Revert  bool   `json:"revert,omitempty"`
}

// Of reads the intent from a commit subject plus the branch name, falling back
// to issue keywords when both are silent.
func Of(subject, branch, body string) Intent {
	in := Intent{Kind: Unknown, Summary: strings.TrimSpace(subject)}
	if m := ccPattern.FindStringSubmatch(strings.TrimSpace(subject)); m != nil {
		in.Kind = subjectKind(m[1])
		in.Scope = m[2]
		in.Summary = m[4]
	}

	if revertPattern.MatchString(subject) || strings.Contains(body, "This reverts commit") {
		in.Revert = true
	}

	// A branch name refines an unknown or ambiguous reading: "fix/login-loop"
	// says what the merge did not.
	if in.Kind == Unknown {
		in.Kind = branchKind(branch)
	}

	return in
}

// subjectKind maps a Conventional Commit type to a Kind.
func subjectKind(typ string) Kind {
	switch Kind(strings.ToLower(typ)) {
	case Feat:
		return Feat
	case Fix:
		return Fix
	case Refactor, "perf", "style", "cleanup", "chore-cleanup":
		return Refactor
	case Test, "docs":
		return Test
	case Chore, "ci", "build", "bump", "deps":
		return Chore
	default:
		return Unknown
	}
}

// branchKind reads a Kind off a branch prefix like "fix/login-loop"; anything
// unrecognized is Unknown.
func branchKind(branch string) Kind {
	first, _, _ := strings.Cut(strings.ToLower(branch), "/")
	switch first {
	case "feat", "feature":
		return Feat
	case "fix", "bug", "hotfix":
		return Fix
	case "refactor", "cleanup":
		return Refactor
	case "test":
		return Test
	case "chore":
		return Chore
	default:
		return Unknown
	}
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
	for _, in := range intents {
		if c := counts[in.Kind]; in.Kind != Unknown && c > n {
			best, n = in.Kind, c
		}
	}
	if n > 0 {
		out.Kind = best
	}

	return out
}

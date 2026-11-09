package rules

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kyleking/slop-strata/internal/lang"
)

// BlobPrint pairs one blob's fingerprint with where it came from.
type BlobPrint struct {
	Path  string
	Print *Fingerprint
}

// CrossFile runs the passes that join fingerprints across the tree, returning
// findings per path. These never re-parse; they only read what Collect stored.
func CrossFile(blobs []BlobPrint) map[string][]Finding {
	out := map[string][]Finding{}
	dupLiterals(blobs, out)
	clones(blobs, out)
	singleImplIfaces(blobs, out)
	unusedOptions(blobs, out)

	return out
}

// dupLiterals flags a literal carried by minDupFiles or more files.
func dupLiterals(blobs []BlobPrint, out map[string][]Finding) {
	byLiteral := map[string][]BlobPrint{}
	for _, b := range blobs {
		for lit := range b.Print.Literals {
			byLiteral[lit] = append(byLiteral[lit], b)
		}
	}
	for lit, owners := range byLiteral {
		if len(owners) < minDupFiles {
			continue
		}
		short := lit
		if len(short) > 60 { //nolint:mnd // a display cap, not a rule
			short = short[:60] + "…"
		}
		for _, b := range owners {
			out[b.Path] = append(out[b.Path], Finding{
				Rule: "x/dup-literal", Line: b.Print.Literals[lit],
				Match: short,
				Note:  fmt.Sprintf("shared by %d files", len(owners)),
			})
		}
	}
}

// clones flags normalized-identical function bodies living in different files.
func clones(blobs []BlobPrint, out map[string][]Finding) {
	type owner struct {
		path string
		unit CloneUnit
	}
	byHash := map[string][]owner{}
	for _, b := range blobs {
		for _, u := range b.Print.Clones {
			byHash[u.Hash] = append(byHash[u.Hash], owner{b.Path, u})
		}
	}
	for hash, owners := range byHash {
		files := map[string]bool{}
		for _, o := range owners {
			files[o.path] = true
		}
		if len(files) < 2 {
			// Same-file duplicates in a test file are duplicate cases: bodies
			// that differ only in fixture data.
			if len(owners) > 1 {
				for _, o := range owners {
					if lang.IsTestPath(o.path) {
						out[o.path] = append(out[o.path], Finding{
							Rule: "x/dup-test-case", Line: o.unit.Line,
							Match: o.unit.Name,
							Note:  fmt.Sprintf("same normalized body as %d tests in this file (%s)", len(owners), hash[:8]),
						})
					}
				}
			}

			continue
		}
		for _, o := range owners {
			out[o.path] = append(out[o.path], Finding{
				Rule: "x/clone-function", Line: o.unit.Line,
				Match: o.unit.Name,
				Note:  fmt.Sprintf("same normalized body in %d files (%s)", len(files), hash[:8]),
			})
		}
	}
}

// singleImplIfaces flags an interface with exactly one required method
// implemented by exactly one type: a seam for a variation that does not exist.
func singleImplIfaces(blobs []BlobPrint, out map[string][]Finding) {
	// Go: method sets per receiver type; TS: implements clauses per name.
	implsByMethod := map[string]map[string]int{} // method -> implementing type count (go)
	implementsByName := map[string]int{}         // interface -> implementor count (ts)
	for _, b := range blobs {
		for typ, methods := range b.Print.MethodImpls {
			for _, m := range methods {
				if implsByMethod[m] == nil {
					implsByMethod[m] = map[string]int{}
				}
				implsByMethod[m][typ]++
			}
		}
		for name, lines := range b.Print.Implements {
			implementsByName[name] += len(lines)
		}
	}

	for _, b := range blobs {
		for _, iface := range b.Print.Ifaces {
			if len(iface.Methods) != 1 {
				continue
			}
			implementors := len(implsByMethod[iface.Methods[0]]) + implementsByName[iface.Name]
			if implementors == 1 {
				out[b.Path] = append(out[b.Path], Finding{
					Rule: "x/single-impl-iface", Line: iface.Line,
					Match: iface.Name, Note: "exactly one implementation in the tree",
				})
			}
		}
	}
}

// unusedOptions flags a WithX Option helper no file references: an option no
// caller passes.
func unusedOptions(blobs []BlobPrint, out map[string][]Finding) {
	referenced := map[string]bool{}
	for _, b := range blobs {
		for name := range b.Print.Referenced {
			referenced[name] = true
		}
	}
	for _, b := range blobs {
		for name, line := range b.Print.Options {
			// The declaration site references its own name, so absence of a
			// second file referencing it means no caller exists.
			uses := 0
			for _, other := range blobs {
				if other.Print.Referenced[name] {
					uses++
				}
			}
			if uses <= 1 {
				out[b.Path] = append(out[b.Path], Finding{
					Rule: "x/unused-option", Line: line,
					Match: name, Note: "declared, never passed",
				})
			}
		}
	}
}

// Baseline is the set of findings a repository already accepted, keyed so a
// suppressed hit stays suppressed until the code around it moves.
type Baseline map[string]bool

// baselineKey identifies one finding for the baseline file.
func baselineKey(path, rule string, line int, match string) string {
	return strings.Join([]string{path, rule, fmt.Sprint(line), match}, "\x00")
}

// LoadBaseline reads a baseline file: a JSON list of [path, rule, line, match]
// tuples written by the tool's own baseline-write path.
func LoadBaseline(entries []BaselineEntry) Baseline {
	b := Baseline{}
	for _, e := range entries {
		b[baselineKey(e.Path, e.Rule, e.Line, e.Match)] = true
	}

	return b
}

// BaselineEntry is one recorded accepted finding.
type BaselineEntry struct {
	Path  string `json:"path"`
	Rule  string `json:"rule"`
	Line  int    `json:"line"`
	Match string `json:"match"`
}

// Entries renders a live finding set into baseline entries.
func Entries(files map[string][]Finding) []BaselineEntry {
	var out []BaselineEntry
	for path, fs := range files {
		for _, f := range fs {
			out = append(out, BaselineEntry{Path: path, Rule: f.Rule, Line: f.Line, Match: f.Match})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})

	return out
}

// Suppress drops findings the baseline already holds.
func (b Baseline) Suppress(path string, fs []Finding) []Finding {
	if len(b) == 0 {
		return fs
	}
	var out []Finding
	for _, f := range fs {
		if !b[baselineKey(path, f.Rule, f.Line, f.Match)] {
			out = append(out, f)
		}
	}

	return out
}

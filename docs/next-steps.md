# Remaining work

Checked against the code at 61b2aba. Items from the v0.6.0 audit that are now done are left out.

## Output that misleads

- `history` text omits the pairing rows. They are JSON only.
- `history` does not group changes by reading. Only `change` prints a reading.

- Module depth matches a method by its name, with no type of the receiver. A method named like an unrelated member that another directory reads (`get`, `String`, `value`) counts in `shown_by_name`, so `depth` is a lower bound and `depth_attributed` an upper one. On a 53-method sample 20 were matched only by such a collision. Counting only calls, and treating properties apart, would narrow the range.
- Module depth counts a declaration per file, so a Go name declared once per platform (`util_unix.go`, `util_windows.go`) counts twice.
- Module depth reads a library's public API as hidden when only tests and the outside world use it, since test files are not users.
- The directory table runs past 100 columns on deep paths. The depth table shortens a path in the middle, and two directories can then read alike.

- `history` samples the import graph once per week. A cycle that comes and goes inside one week is never seen, and a cycle that goes away for a week and comes back is dated to its return.
- A cycle's introducing commit is a first-parent commit, so a merge commit stands for its whole branch. The author class is the walk's, and a squash that carries a bot's co-author trailer reads `bot`.
- `median_depth` is taken over the directories with five or more names, often a handful, and jumps when one directory crosses that line.
- Hidden coupling lists few pairs: 7 across twelve public repositories at the shipped floors. It leaves out every pair of TypeScript files in two workspace packages, because a package-name import adds no edge. Resolving workspace package names in the import graph would let those pairs be judged, and would change the cycle counts too.
- Hidden coupling treats a file as production when the path rules do. `test.ts` beside its subject (date-fns) and `__testfixtures__` inputs (query) are not recognised as tests, and pair under looser floors.
- Hidden coupling reads intent from the commit subject. A typing or lint sweep with no Conventional Commit type counts, and two such pairs (requests, chi) sat just under the support floor.
- `history` resolves a graph for every sampled week on every run, which about doubles a warm run (TanStack Query over three years: 4.1 s to 9.2 s). Storing each sampled commit's cycle sets and depths in the cache, keyed by the commit and the digests of the packages that compute them, would leave a warm run only the last tree to read.
- The `history` walk stores blobs under the abbreviated ids `git log` prints, so `snapshot` and `history` do not share cache entries for the same blob. The architecture pass maps the ids for its own lookups. Asking the log for full ids (aragonite) would remove the mapping and the duplicate entries.

## Rules below the bar

`slop-strata label` records verdicts per repository and `slop-strata rules` prints each rule's labeled precision, but the gate is still a judgment call on the corpus. Label more findings across repositories, record precision per release, and move a rule out of the gate only on that evidence. `slop-strata --explain experimental` lists the gated rules.

- `go/silent-error` flags any blank last target on a sole call. Flagging only discarded errors needs callee types, which the rule does not have.
- `x/narrating-comment` is still gated. Drop it, or keep it gated, once labels across repositories show its precision.
- `x/dead-export` skips Go methods, which avoids interface false positives. It does not resolve interface satisfaction.
- `snapshot` reports `x/stale-prose` for every unresolved name, behind `--experimental`. It should flag only names the history shows existed and are now gone. The exemptions for changelogs, ADRs, and migration guides already exist.
- `x/import-cycle` is gated until its hits are labeled. On eight public repositories it reported the same components as an independent resolver, but TypeScript path aliases and workspace package names add no edge, a value import that is only used as a type still counts as a runtime edge, and Python imports of a submodule do not count as importing its parent packages.
- `x/dead-export` and `x/could-be-private` read `Declared` and `Exported`, which miss Go types, constants, and variables, Python decorated functions and module-level assignments, and TypeScript classes and `const` declarations. The module depth pass lists all of those as units. Moving the two rules onto the units would widen what they see, and needs labels first.
- `py/type-any` and `go/type-any` report every hit. They should report a count per file. `go/type-any` already skips generic constraints and variadic `...any` parameters a function spreads into a call, and leaves fields that hold JSON of unknown shape to labels.

## Tests and corpus

- `internal/lang` and `internal/srcdigest` have no tests.
- Goldens at pinned public SHAs are not built. The worked example pins calcipy at 594d31b, but no test reads that pin.
- A per-release precision record needs labels from more than one repository.

## Open from DESIGN.md

- A resumable first run: the blob cache keeps finished blobs, but an interrupted `history` restarts its walk
- Function-level move matching and per-hunk classification
- Stale config: a key a config file defines that no code reads by name is reported, but a key code reads that no config defines is not
- Stale prose: a TODO or "remove when" whose forward pointer is closed
- A history view that drills into the changes behind a trend

## Not planned here

HTML output stays with the consumer. The tool emits text and JSON only.

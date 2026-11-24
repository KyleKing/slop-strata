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
- `x/dead-export` and `x/could-be-private` cannot tell a library's public API from an export nobody wants. They skip what a manifest, an `__all__`, or a whole-module re-export publishes, and still report a name a library exports from a file its entry point imports by name. That is why Go types, constants, and variables (10 of 23 sampled hits real) and TypeScript classes (0 of 4) are recorded and not reported. Following re-exports from each manifest entry to the names it publishes, and treating a Go package outside `internal/` and `main` as importable, would let those kinds in.
- The same two rules count a reference by name in any file the tool parses. A name imported only from a `.vue`, `.svelte`, `.mdx`, `.js`, or `.mjs` file reads as unused (3 of 7 false positives in a sample of 40), and so does one a framework reaches through a namespace import.
- Chained and tuple assignments at module level in Python (`a = b = 1`, `a, b = 1, 2`) declare names the fingerprint does not record.
- `x/last-ref` asks whether any declaration of the name is left, not whether the one the change stopped using is. Over the last 200 commits of six repositories it reported 11 findings, 7 of them a helper the change left behind and 4 a name some unrelated file also declares (a Next.js page component, a test fixture). Before the declaring file's own uses counted and an exported declaration was discounted once, the same commits gave 28, and none of the 12 sampled was unused. Attributing the name through the import graph would settle the 4.
- `x/new-duplicate` is gated. On 3,750 commits of 15 public repositories it gave 5.7 findings per 100 commits, and of 64 read at random 19 were real, 37 plausible, and 8 false: 30% by the strict reading and 88% with the plausible ones. One person labeled them, and only 3 were Python. The false ones were files that are parallel by design and that no path rule names (one locale per file, two flavours of one API, a config per package, an integration app), a guard repeated on purpose after taking a lock, and a line two sites share by chance. Labels from more than one reader, and more Python, come before any move out of the gate.
- `x/new-duplicate` finds few of the repeats that later get merged: 20 of 152 mined consolidation commits at its floor, and 4 of 68 when replaying the commit that wrote the later copy. See "Duplication, after the first stage".
- `py/type-any` and `go/type-any` report every hit. They should report a count per file. `go/type-any` already skips generic constraints and variadic `...any` parameters a function spreads into a call, and leaves fields that hold JSON of unknown shape to labels.

## Dodges the rules lose to

Each of these is a variant in `Gaps` of an adversarial fixture (`internal/rules/adversarial_*_test.go`, `cmd/slop-strata/lastref_test.go`), which asserts the rule reports nothing. Closing one fails its test until the variant is moved to `Dodges` or `Persists`.

Needs types or a call graph:

- `go/silent-error`: call the function bare, or hand its error to a function that ignores it
- `go/type-any`: a type parameter constrained by `any`, which the rule exempts on purpose
- `go/test-reads-source`, `py/test-reads-source`, `ts/test-reads-source`: read the file through a helper or fixture that takes the path, `import` the JSON, or bind a computed path to a name first. Only a name bound once to a string literal is read through: reading through any binding added 6 hits, 4 of them test data found from `__file__`
- `go/timing-hack`, `py/timing-hack`, `ts/timing-hack`: sleep in a helper outside the test files or under another name (`from time import sleep as pause`, `settle(50)`, `timers/promises`), or wait on an event nobody sets. Two TypeScript sleeps are left alone because most hits were a simulated delay inside a handler or a query function and not a test waiting on time: a bare `sleep(n)` (1,916 hits on four repositories, 1,909 of them in one) and `new Promise(r => setTimeout(r, n))` (78 hits, 7 of 20 sampled a test waiting)
- `py/type-any`: `Any` under another name, behind a module-level alias, or in a quoted annotation. `ts/type-any`: an implicit `any`, or `object | Function`
- `py/mock-patch`, `ts/module-mock`: `patch` or `vi` under another name, assigning over the attribute by hand, or `vi.spyOn(...).mockResolvedValue`
- `ts/as-unknown-as`: widen through a variable annotated `unknown`, or cast inside a generic helper
- `ts/record-unknown`: `Map<string, unknown>`, `object`, or an index signature. Matching `[key: string]: unknown` added 33 hits, and about 5 of 20 sampled were a type with nothing but unknown values: the rest were open objects with named fields or the expected side of a type test
- `x/single-impl-iface`: a second method or member, an unrelated type sharing the method name, or no `implements` clause

Needs a judgment the rule does not make:

- `py/silent-except`, `ts/silent-catch`: return a default from the handler, `contextlib.suppress`, `void err`, or a comment in the `catch` block. The comment is the documented-intent case and is left alone on purpose, as ESLint's `no-empty` leaves it: 59 corpus blocks carry one, each saying why
- `py/or-default`: `if not rows: rows = []`
- `py/mock-only-assert`, `ts/mock-only-assert`: copy the call record into a variable first. `assert mock.called` and `mock.call_count` are left alone, since hand-written flags and counters carry those names. `expect(...).rejects` and `.resolves` are not read as assertions, which is 3 of the 4 false hits in 35 sampled
- `py/constant-echo`, `ts/constant-echo`: wrap the constant in a call, use `assertEqual`, copy it into a local, use a looser matcher, or read it off an object. The last is left alone: `expect(process.env.X)` and `expect(schema.enum.A)` were all five corpus hits
- `x/ignore-without-reason`: a reason of one character
- `x/case-table`: two tables of eight, or the records in a list
- `x/comment-density`: move the comments above the function or into the docstring, fold them into one block comment, or pad the body with blank lines. Counting comment lines over non-blank lines closed the last two and added 98 hits, of which a sample of 20 was mostly explanation, commented-out code, `@ts-expect-error`, and `@see` blocks, so the count is as it was
- `x/clone-function`, `x/dup-test-case`: one extra statement, a docstring, a dropped return type, `it.only`, or a move to another file (which `x/clone-function` then reports)
- `x/dup-literal`: name the literal in one file, which takes the count under four
- `x/new-duplicate`: rename more than three things in every stretch of the copy. A copy with its loop rewritten as a comprehension or a chain of calls, or with two statements swapped, is still reported for the stretches that line up, and those are `Persists` variants

Matches a name and nothing more:

- `x/dead-export`, `x/could-be-private`, `x/unused-option`, `x/last-ref`: a reference from a test, an unrelated identifier with the same name, an import left unused, `__all__`, a decorator, or a whole-module re-export. The last three are the exemptions that keep a library's API quiet
- `x/single-entry-registry`, `x/narrating-comment`, `x/spent-test`, `x/unused-option`: a word outside the rule's vocabulary, a registry filled after it is created, or a narration moved into a docstring
- `x/stale-config`, `x/stale-prose`: the key or the name kept alive in an unrelated identifier or string, a negation in front of the name, or a fenced block

`py/silent-except` and `ts/silent-catch` treat a comment differently, as they did before these fixtures. In Python a comment on its own line above `pass` sits outside the handler's block in the grammar and the handler is reported, while `pass  # reason` is not. A comment that only silences a linter (`pass  # noqa`) is now read as no reason. In TypeScript any comment in the block clears the finding.

## Duplication, after the first stage

`x/new-duplicate` lines up whole statements of equal shape, in order, with no gap. What the 153 mined consolidation commits show it missing, largest first:

- An expression repeated inside longer statements (a condition, a struct literal, a call chain, a JSX subtree). In 45 of the 134 mined commits outside tests no statement of one site has the shape of a statement of another. This needs records below the statement, at least for calls and literals over a size.
- A copy with one statement changed in shape, added, or dropped, which splits a run into stretches too short to report. Letting a run bridge one statement that differs, at a price, would join them. A copy with statements reordered needs a set of statements compared, not a sequence.
- Fragments of one or two short statements. In another 49 of the 134 the statements that line up come to under 20 tokens, and no floor separates those from chance on tokens alone. History might: two sites edited in the same commits, or a third copy appearing.
- Sibling methods and repeated type declarations line up only when their members do. Comparing the member lists of two classes or two types as sets would reach the parallel class that a base class should hold.
- A change that adds a third and fourth copy reports each against the first. One finding per group, with how many sites it now has, would say more.
- Tests, examples, and benchmarks are left out whole. A test helper copied between two test files is a real case, and it is 12% of the mined commits.
- A snapshot metric was planned and not built: groups of repeated code ranked by the tokens a merge would remove (tokens times sites less one), with where the sites are. The pass can produce it, but nobody has read a sample of its groups, so it would ship unmeasured. Ranking by what a merge saves, and saying which site is the one to keep, is the split a reader needs to choose where to spend the effort.
- The fingerprint walk is 22% to 29% slower than before the statements were added, because it now visits every token, and the blob cache is 30% to 69% larger (the README has both tables). Hashing a statement's text between its names, in place of a visit per token, would take most of the walk back. Two bytes of the exact hash would do for what it is read for, and the names repeat what `Referenced` already holds.

## Pruning aids measured and not built

- Dead files, as a list of production files nothing imports. A prototype over the import graph listed 802 candidates on eight Python and TypeScript repositories after leaving out tests, manifest entries, declaration files, `*.config.*`, `conftest.py`, `__init__.py`, `__main__.py`, main guards, and Python scripts outside a package. Of 36 sampled, 3 were dead, 4 could not be told, and 29 were in use by something the graph does not see: a framework route or layout (5), an import from a `.vue` or `.svelte` file (5), a path alias (4), a script, benchmark, or example that a package script, a workflow, a `bin` entry, or an `index.html` runs (9), a codemod, fixture, or data module loaded by path (5), and a test double only tests import (1). It needs path aliases, workspace names, the non-TypeScript importers, and package scripts in the graph first. Go was left out: a package is a directory, and files of one package import nothing from each other.
- Wide directories and long files need nothing new: a snapshot's directory rows carry `files` and its file rows carry `sloc`.

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

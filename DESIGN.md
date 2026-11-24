# slop-strata

A Go CLI that scores code quality on the change at hand and over a repository's history, for Python, TypeScript, and Go. It starts from SlopCodeBench's erosion and verbosity ([paper](https://arxiv.org/abs/2603.24755)) and adds what those two miss: how long code survives, what a change was trying to do, and whether a bad pattern spreads after it first appears.

Status: v1 in progress. This file is the repository's `DESIGN.md`.

## Why build it

- [scb-check](https://github.com/gabeorlanski/scb-check) is the paper's scorer. It re-parses the whole tree every run, never reads git, aborts the run on one file it cannot tokenize, spends close to an hour on a 5 MB minified bundle unless told to skip it, and reports findings with rule IDs only in its human-readable output
- No OSS tool caches metrics per blob. wily caches per revision and hercules re-walks history in one pipeline, so a per-change delta costs a full pass in both
- Static snapshot scores say nothing about intent. A change that raises erosion while adding a feature and one that lowers it while paying down a hotspot read the same way in every tool below

## Decisions

- Named `slop-strata`. Pure Go. Rules are Go functions that walk tree-sitter trees, built into the binary. ast-grep YAML packs (scb-check's 137 rules among them) load as an optional external source through `sg`
- Generic, configured per repository: author classes come from logins and trailer patterns in `.slop-strata.toml`, with two built-in classes after the repository's own: `agent` for coding-agent co-author trailers, and `bot` for logins ending in `[bot]`. `change`, `snapshot`, and `history` emit JSON in one envelope that carries a schema version (6, published as `docs/schema.json`) and the definition of each metric. Unknown config keys fail the run. Any HTML or dashboard is the consuming system's job
- No bare command. Subcommands are listed in this order, and `--help` recommends `change` when run with no arguments:
  1. `change`: lines added by `@` (jj) or `HEAD` against its merge base
  2. `snapshot`: the whole tree at a revision
  3. `history`: first-parent merges over a range, as trends
  4. `init`: write `.slop-strata.toml` with bot author classes detected from
     recent history, then record the current baseline
- Every number is a guardrail with no target and no gate. Components stay visible and never blend into one score

## Engine

- Built from [my_go_template](https://github.com/KyleKing/my_go_template). Version control through [aragonite](https://github.com/KyleKing/aragonite) `vcs`, so jj works through the colocated git repository
- Parsing through the official [tree-sitter/go-tree-sitter](https://github.com/tree-sitter/go-tree-sitter) with the Python, TypeScript, and Go grammars, one parser per worker goroutine, explicitly `Close`d, with blobs scored in parallel and cache writes made from one goroutine. Storage in `modernc.org/sqlite`
- A SQLite cache keyed by (blob SHA, path class, digest of the code that produced the payload and the `max_file_bytes` and `minified_avg_line` limits). Path class is the language and whether the path is a test, since test-only rules read it. For prose and config files it is the text kind, the extension, and the package manifest name, since the fingerprint reads them. Rules that read the file name run outside the cache. A blob skipped for its content (oversize, binary, minified, or generated) stores that verdict, so `history` answers a cached blob without reading it. A commit or change score sums only the blobs it touched. The file lives under the user cache directory, and CI restores it from a cache keyed by the tool's version
- Files over a size cap or with a minified shape (very long average line) are skipped with a warning naming each one. Generated files (a generator marker in the first five lines, or `linguist-generated` in the root `.gitattributes`) are skipped as generated. A file that will not parse gets an error row and the run continues
- Cross-file passes (clones, repeated literals, coupling, the import graph) run over cached per-blob fingerprints, never by re-parsing

## Metric families, in shipping order

### 1. Structure (snapshot)

- Erosion exactly as the paper defines it: complexity mass is CC × √SLOC per function, and erosion is the share of mass in functions with CC over 10. Kept exact so figures compare with the paper's 473-repository baseline
- Beside it, the distributions erosion hides: p90 and max of function length, nesting depth, parameter count, and cognitive complexity, and the count of functions over 50 lines
- [rust-code-analysis](https://github.com/mozilla/rust-code-analysis) is the reference for metric definitions

### 2. Time (git only, no parsing)

- Churn within 14 and 30 days of a line's addition, credited to the directory the line was added in, so churn never exceeds the lines that directory added. Lines keep their birth day through moves and renames, so churn and survival follow the code and not the path
- How young lines die, as separate counts that sum to 30-day churn: removed by the author who wrote them or by someone else, deleted outright or replaced in place, removed with their whole file or edited, removed within a day, a week, or 30 days, and removed in a revert. They tell deliberate pruning (try, learn, delete) from rewriting the same thing, and the tool never blends them into one score or ranks people: author identity feeds only the self-or-other comparison and is never printed. Counting costs one pass over the diffs already read. Squash merges hide whatever happens inside a branch
- Flapping, kept apart from pruning. Re-addition counts the young deaths that came back: a later commit added a line with the same trimmed text to the same file within 30 days. It matches by 64-bit hash over the trailing 30 days of deaths, from the removed text already in each diff, so it adds no git call and bounded memory. The file is the match scope because the same text across sibling files is mostly shared boilerplate: in a 270,000-death window, 47% of directory-scope matches repeated their text three or more times, against 19% in file scope. The 30-byte floor comes from the same measure, where the repeated share falls from 72% at 8 to 11 bytes to 21% at 30 to 49. Deaths too near the walk's end to have had 30 days are left out of the denominator, as survival censors young lines. Oscillation counts functions whose body changed in three commits within 14 days. A commit changes a function when a diff hunk touches its lines, read against the function ranges the structure report already holds, because comparing cc, sloc, and the other function metrics misses between a quarter and two fifths of body edits on the repositories measured, and a body hash would change the cache payload for no gain. Both are counts with no score and no person attached
- Line survival by change cohort (Kaplan-Meier, as [git-of-theseus](https://github.com/erikbern/git-of-theseus) does), with Greenwood 95% intervals at each horizon. The unit and cohort definitions are pinned and documented, because two 2026 studies of agent code ([arXiv 2601.16809](https://arxiv.org/abs/2601.16809), [arXiv 2604.00917](https://arxiv.org/abs/2604.00917)) disagree on direction. A horizon with too few lines old enough to reach it reports no rate, rather than an extrapolated one
- Revert and rework rate: reverts, and fix commits touching recently added lines
- Hotspots as churn × complexity, and change coupling by degree of co-change, skipping very large commits ([code-maat](https://github.com/adamtornhill/code-maat) definitions)
- Knowledge concentration per file, reported by directory and never as a ranking of people

### 3. Slop rules (snapshot)

- One pack per language. Each finding carries a rule ID, file, and line, and a baseline file holds existing hits
- First subset:
  - Silent fallback: empty `except` or `catch`, Go `_ = f()`, `or default` chains
  - Speculative abstraction: an interface with one implementation, a single-entry registry, an option no caller passes
  - Type evasion: `as unknown as`, `Any`, `Record<string, unknown>`, module mocking
  - Comments: density per function, and narrating comments (now, moved, was, a ticket that is not a forward pointer)
- Duplication: normalized-subtree clones (scb-check's method), and string or number literals repeated across files
- The default set runs only the rules that cleared the precision bar. `--experimental` adds the rest, and `--explain experimental` lists them
- A finding's id is ten hex characters of a hash over its rule, path, enclosing function (when the rule reads one), matched text, the trimmed text of its line, and an occurrence index that breaks ties among findings sharing the other five, counted from the top of the file. A line move changes none of those, and neither does a new hit with other line text above it. A rename of the function, an edit of the matched text or of the line, or a new hit above it with identical line text changes the id. Cross-file rules such as `x/stale-prose` have no enclosing function, so their id covers rule, path, and matched text. A set-shaped finding such as `x/import-cycle` is keyed by its rule and sorted member files alone, so neither the import line it is anchored on nor the way the imports are written is part of the id
- Inline suppression has one form: `slop-strata:ignore <rule-id> <reason>` in a comment on the finding's line or the line above, in the file's comment syntax. Markdown takes an HTML comment outside code blocks, and JSON takes none. The reason is required: an ignore without one suppresses nothing and is itself reported as `x/ignore-without-reason`. In Go, `//nolint:<linter> // <reason>` also suppresses the rules that overlap that linter (`errcheck` for `go/silent-error`). A comment that only mentions the marker mid-sentence is not a directive, because the marker must open the comment
- The baseline, inline ignores, and false-positive labels are three ways to set a finding aside, and all three are counted in the envelope's `counts.suppressed`. The baseline file keys findings by id and carries a format version. Version 3 adds the member files to a set-shaped entry and is otherwise version 2, which still loads, and a file older than that fails the run with an instruction to rewrite it. An entry with members covers every finding of its rule whose members are a subset of them, so shrinking a cycle is never a new finding and growing one is. A snapshot counts the entries that no finding carries the id of in `counts.stale_baseline` and prints one line asking for the baseline to be rewritten, without changing the exit status

### 4. Dependencies (snapshot)

- The architecture is inferred and never declared: there is no layer list, no allowed-import config, and no rule a repository has to write. The graph is the imports the tool can resolve from one file of the tree to another
- Each blob's fingerprint keeps its imports as written, with the names they bind and a kind from where the import sits. Specifiers, names, type-only markers, and the enclosing guard or function are read off tree-sitter nodes in the one walk the fingerprint already makes, with no second parse and no pattern matching over source text. Resolution runs at cross-file time, so a file that moves changes no cached blob
  - Python: absolute and relative imports to module files and `__init__.py` packages. A module path is matched as a suffix of tree paths, so a `src/` layout and several roots resolve. The root must not itself be a package, the nearest root above the importer wins, and a name two other roots answer to is dropped
  - TypeScript: relative specifiers with extension and `index` resolution, a `.js` specifier mapped to its `.ts` source, and declaration files left out. Bare specifiers, path aliases, and workspace package names add no edge
  - Go: an import path to a directory through the module path of each `go.mod`, the longest module path winning. The edge goes to the files of the package whose exported names the importer uses
- Four edge kinds: `runtime` (module level), `type` (TypeScript `import type`, Python under `TYPE_CHECKING`, anything to or from a stub or declaration file), `deferred` (inside a function body, or a dynamic `import()`), and `main` (under Python's `if __name__ == "__main__"`). The last three are how a cycle is usually worked around, so they stay in the graph and out of cycle detection
- Per directory, `fan_in` and `fan_out`: the distinct other directories that import it and that it imports. Production rows count edges between production files, and test rows the edges that touch a test file
- Import cycles: strongly connected components of two or more non-test files over runtime edges, with the count and the largest size on every snapshot. Go forbids package cycles, so Go adds edges and never a cycle
- `x/import-cycle`, experimental, reports one finding per component on the import line of its cheapest cut: the edge inside the component that imports the fewest names. The note shows the shortest cycle through that edge and the member count, so a 49-file component reads as a two-file cycle and a number. Files that only re-export are left out of the id when two others remain, so routing an import through a barrel does not make a new finding
- `change` reports a cycle the change created or grew, by comparing the base tree's cycles with the end's. `--brief` treats it as any new rule hit

### 5. Module depth (snapshot)

- After Ousterhout's deep modules, by way of [deslop-please](https://github.com/deterministic-machines/deslop-please): a module is shallow when it shows its callers nearly as much as it hides. The module is the directory, as everywhere else in this tool
- Shown is measured by use and never by visibility. A name is shown when a production file of another directory uses it, so `export`, a capital letter, a leading underscore, and `__all__` change nothing, and neither does moving a function between files of one directory
- Any other directory is outside, a parent or a subdirectory included. A row holds a directory's own files, which is also what `fan_in` and `fan_out` count, and a Go package has no nesting at all. The cost is that a private subpackage (`pkg/_internal`) reads shallow, because its parent consumes most of it
- A name is a function, type or class, constant, or variable at file scope, or a method. Each fingerprint lists them as units with their lines, and the engine credits each function's mass to the smallest unit that holds it, so a closure weighs on its function and a method on itself. A Python dunder method and a TypeScript constructor run by naming the class, so their mass stays with the class
- A top-level name is attributed through the resolved import graph to the file that declares it: a named import by its name, a bound module by the names read off its local name (`pkg.Name`, `ns.name`, `mod.name`), a re-export by following it to the declaration. An import the file binds and never uses is no use, so a barrel nobody consumes shows nothing. The fallback is a bare name match against the target's visible names, taken only for a wildcard import that binds no namespace, a Go dot import, and a Go package whose name is not its directory's
- A method cannot be attributed, because nothing here knows a receiver's type. It is matched by name: shown when any file of another directory reads a member of that name off a value that is not an imported module, once anything outside imports the directory. On 53 sampled methods across the corpus 33 of those matches were real uses and 20 shared a name with an unrelated member, while all 47 sampled methods left hidden and all 125 sampled top-level names were right
- So the two kinds are kept apart. `shown_by_name` and `shown_mass_by_name` say how much of `shown` is a name match. `depth` counts those methods as shown and `depth_attributed` counts them as hidden, which brackets the true ratio: attributed uses have no false positive in the sample and hidden methods no false negative. `depth` stays the headline because it is the closer end wherever methods carry the mass: fzf's `src/util` reads 0.16 with them and 0.73 without, and all five of its sampled methods were real uses. On zod's `v4/core`, where none of five sampled matches was real, the two ends are 0.63 and 0.63, since methods are a sliver of its mass
- No name-matched method is ever listed by name
- A pure forwarder (one statement, a call whose arguments are all the function's own parameters) counts in neither side, so wrappers cannot pad a directory into looking deep. A use of a forwarder from outside shows the name it forwards to when the same directory declares it
- `depth` is hidden mass over shown plus hidden mass, falling back to name counts when neither side has a function. It has no target. It is null when nothing outside uses the directory, which is an entry point or a leaf and reads differently from a directory with everything shown. Under five names it is marked provisional
- Test files neither show a name by using it nor have their names counted, so a library's public API that only its tests and the outside world use reads hidden. The number says how much of a directory the rest of this tree depends on, not how much it publishes
- `change` measures both trees and lists the directories whose count of attributed shown names rose, with the names newly used from outside. A name match never opens a row and is only counted (`newly_shown_by_name`), since a new method named like some other type's would otherwise read as newly used. It never feeds `next`, so `--brief` and `--exit-code` ignore it
- No finding. A shallow-module rule would need a threshold, and the method matching is not precise enough to carry one

### 6. Virality and attribution

- Author class per commit, from the configured classes and the built-in `agent` and `bot` classes. A commit that matches none is `person`
- After a rule's first hit in a repository, whether its hit rate rises and in which author class's commits
- Agent changes against person changes merged the same week in the same directories, for every family above. The pairing rows are in JSON only for now

### 7. Architecture over time (history)

- `history` reports how the inferred graph of families 4 and 5 changed over its window, as metrics with no rule id, no baseline, and no gate. Nothing is configured
- The sample is the tree after the last first-parent commit of each week the walk has a commit in, the same weeks its other tables use. A commit authored before the commits it follows counts in their week, so the samples run in walk order. Each tree is resolved from cached fingerprints: a sample keeps the fingerprints of the tree before it and reads only the blobs that differ, and a week that changed no source file and no `go.mod` repeats the week before. The first run parses the files the window never touched, once
- The walk scores each blob under the abbreviated id `git log` prints, and a tree spells ids out, so a sampled tree looks its blobs up under the short id the walk stored them by before it falls back to the full one. Without that, a cold run parsed every changed blob twice
- Per week: the import cycle count, the largest cycle, the files in any cycle, the directories with a depth read off at least five names, and the median of their `depth` and of their `depth_attributed`. Both medians are kept because they bracket the truth, as they do per directory. A median over a handful of directories jumps when one crosses the five-name line, so it is printed beside the count
- `depth_moved` lists up to five directories whose depth moved most between the first and the last week. A row needs both bounds to move the same way by 0.05 or more and reports the smaller move, so a method matched by name on one side alone cannot make one
- Cycle origin: each cycle of the last week is followed back through the samples. A cycle continues an earlier one when the two share at least two files after following renames. Two files that import each other in both samples are one dependency that held, which covers the grown and the shrunk cycle that a baseline entry covers and also a cycle that lost one file and gained another in the same week. The id is the `x/import-cycle` finding's at the end revision, so a rename changes the id and not the origin. A cycle that went away and came back starts again
- The week a cycle first appears in is bisected over its commits, which costs a handful of cached tree reads, and the commit it is first present after is reported with the author class and intent the walk already assigns. It is exact unless the cycle came and went inside that week. A cycle already in the tree before the window's first commit is marked as older than the window, and so is one in a shallow clone whose first tree cannot be read. On 11 cycles across zod, trpc, flask, click, and pydantic, each reported commit was the one that added the closing import when read against `git show`. A clone's oldest commit reads as parentless, so a shallow clone marks the cycles of its first tree as older than the window and never dates them to that commit
- Hidden coupling: pairs of production source files that change together with no chain of imports from either to the other, over edges of every kind. A direct edge is too weak a test, because a barrel puts one file between most pairs of a TypeScript package. Files are the unit: file pairs were precise enough at the shipped floors that directory pairs were not tried
- A commit counts for a pair unless it touched more than 30 files, a bot made it, its stated intent is `docs` or `chore`, or it also changed a file that both files import. The last rule is what makes the coupling hidden: two wrappers of one core that change whenever the core does are already tied together by the graph. Test files, and files the engine does not score, never pair
- A pair needs four such commits, a confidence of 0.5 (co-changes over the changes of the file that changes less), and a support of 1.5. Support weighs each commit by one over the other source files it changed, so a commit of just the two files counts 1 and a sweep over twenty-one counts a twentieth: sibling files that only ever change in package-wide sweeps stay under the floor
- Two cases the graph cannot vouch for are left out: two Go files of one directory are one package and need no import, and two TypeScript files under different `package.json` files may import each other by a workspace package name, which adds no edge
- Measured on twelve public repositories (flask, httpx, click, requests, zod, trpc, query, date-fns, fzf, bubbletea, cobra, chi), over three years of each and the whole history of the eight smaller ones, reading the commits behind each pair. The first cut, a direct-edge test at five co-changes and 0.5 confidence, listed 22 pairs: 15 real, 7 coincidental, and all 7 were documentation or lint sweeps across sibling files. Dropping `docs` and `chore` commits, adding the support weight, and requiring no import path left 30 pairs at looser floors (three co-changes, 0.4 confidence, 0.6 support): 20 real, 7 coincidental, 3 artifacts of test files the path rules do not recognise. At the shipped floors 7 pairs remain and all 7 read as real: parallel implementations, a platform stub, and build scripts that share an output layout. Seven is a small sample, and the cost of that precision is recall: most repositories list nothing

## Reading a result in context

A delta means nothing until the tool knows what the change set out to do, so every result carries its context and a one-line reading.

- Intent comes from the Conventional Commit type, the branch name, and linked issue keywords: `feat`, `fix`, `refactor`, `perf`, `test`, `docs`, `chore`, or unknown. Only `refactor`, `fix`, and `test` carry an expectation the numbers can contradict. `perf`, `docs`, `chore`, `ci`, `build`, and `style` never produce a disagreement
- Gross and net are reported apart: mass grown (the sum of positive per-function deltas), mass removed (the sum of negative ones, deleted functions in full), and the net, which is grown minus removed. Production and test files are summed apart, so tests never inflate a production reading. A refactor that removes three high-complexity functions and adds one smaller one is paying down. A feature that adds the same mass is accruing
- Location matters as much as size: whether the change touched a hotspot, a file with low survival, or code another author class wrote
- Each result reads as a sentence before the numbers: "Refactor in a hotspot: removed 2 functions over CC 10, erosion in `pkg/parse` fell from 0.48 to 0.31." Readings come from rules over these fields, never from a model, so the same input always gives the same sentence
- `change` gives each result one reading. `history` does not group changes by reading yet, so "are we paying down faster than we accrue?" has no direct answer per directory and per author class

## Enriching a result with a model

The tool calls no model. It hands a coding harness what one needs, and the harness writes the prose.

- `--context` adds to the JSON what a reader would open next: the diff hunks behind each delta, the functions that crossed a threshold before and after, the findings with their source lines, the change's commit messages and linked issue text, and the history of the hotspot it touched
- A prompt shipped in the repository (`prompts/explain.md`) takes that JSON and asks for four things: whether the stated intent matches what the numbers show, which one function to look at first and why, whether a rule hit is real or a false positive with the line that decides it, and what a smaller version of the change would look like
- The model's answer is a draft for a person, never stored as a metric. Deterministic readings stay the record, which keeps runs comparable and avoids the self-grading bias that absolute LLM scores show
- Disputed findings a person marks (false positive, or real) go back into a labels file that the rule-authoring loop reads. `slop-strata label <id> tp|fp` writes it, and `slop-strata rules` prints each rule's labeled precision with its count. See "Labels and the precision gate" below for what the file does and does not prove

## Labels and the precision gate

The labels file (`.slop-strata-labels.json`, written by `slop-strata label`) holds a person's verdicts on individual findings of one repository. Each entry stores the finding id, rule, verdict (`tp` or `fp`), path, matched text, an optional note, and the tool version, and never the labeler.

- It is evidence of one thing: the precision of each rule on this repository, from the findings someone read. A finding labeled `fp` is suppressed like a baselined one and counted in `counts.suppressed`, and a finding labeled `tp` stays
- It is not evidence of the corpus-wide precision the gate is set on. The gate is global to the binary, so one rule is either default or experimental for every repository, and a single repository's labels sample code that its authors and tools shaped. A repository whose hits are all docs examples will label a rule poorly, and one full of generated code will label it well, and neither says what the rule does elsewhere
- `slop-strata rules` and `--explain experimental` print each rule's tier, labeled `tp` and `fp`, and precision with `n`, marked `*` under 10 labels. Labels never promote or demote a rule. Moving a rule across the 80% bar is the owner's decision, made with this number and the corpus read side by side

## Presentation

- `change` prints a short block: the reading, then a table of deltas, then findings by rule. It shows only what moved, and `--all` shows the rest
- `change --brief` is the quiet mode for hooks and coding agents. It prints nothing unless the change is worth a look, then the reading and up to three `next:` lines. Worth a look is one fixed rule over existing fields, with no score: a production function over CC 10 grew or was added, a new rule hit outside the baseline appeared, or the stated intent disagrees with the numbers. `--exit-code` maps that to status 1, and errors exit 2, so a build fails only when the caller asks for it. The same items are `result.next` in the JSON, and `--context` adds the source lines of each function in them
- `snapshot` prints a per-directory table, ranked by hotspot (commits touching the directory in the 90 days up to the scored revision, times its mass) and carrying each directory's fan-in and fan-out, then a module depth table of the directories other directories use, shallowest first, with long paths shortened in the middle to fit 100 columns, then the heaviest files and the five heaviest production functions. It takes no `--context`, because a snapshot has no change to give context for
- `history` prints trends per directory as a table with sparklines, then young-death shares and oscillating functions per directory. The per-class pairing rows are in JSON only. After them come the architecture tables: the first week, the last, and each week whose cycle counts changed, then the directories whose depth moved, where each cycle came from, and the hidden coupling pairs, all inside 100 columns. Every week is in the JSON
- `--json` on `change`, `snapshot`, and `history` is the integration surface, and `change --json --pairs FILE` scores many `<base> <rev>` pairs in one process as JSON Lines. The tool renders no HTML
- second-look calls `change` behind the first frame and shows the reading and deltas on the review screen
- Every number is documented where it is printed: `--help` and `--explain <metric>` say what it counts, its threshold, and the behavior it should change, and the JSON carries the same text per field. There is no separate glossary to drift from the code
- Survival rates carry Greenwood 95% intervals. Weekly net mass carries a noise floor, the median absolute deviation of weekly net mass, and a week inside it is marked `~`. A rate under its minimum count reads provisional (`*`): 30 lines for a cohort, 5 test files for a test class, 5 functions for a directory's erosion, and 5 commits for a pairing cell
- Every number names the behavior it should change. A number is dropped if nothing changed after two quarters

## Testing and the premise of a change

Counting tests rewards the wrong ones. On a real project the tests worth deleting were added together with source changes, had real assertions, and looked healthy by every volume measure: tests that read config or docs to assert they agree, AST walks over the source, constant echoes (`expect(MEMORY).toBe(3072)`), tests for migrations that already ran, and stories that differ only in fixture data. Deleting them shows up as large negative test churn, and about a quarter of good fixes touched no test. So the tool never scores test volume, test churn beside source churn, a fix without a test, or coverage.

It reports what predicts a test getting deleted:

- Test survival by author class: the share of test files deleted within 30 and 90 days, split by who added them (agent trailer, bot, or person). This is the most direct signal and needs only git
- Tests that read source, config, or docs: calls such as `read_text`, `open`, `readFileSync`, `ast.parse`, or a regex over file text inside a test body
- Constant echoes: every assertion compares an imported constant, config value, or registry entry, and the test calls nothing with behavior
- Mock-only assertions: every assertion is on a mock call (`toHaveBeenCalled*`, `assert_called*`) and none on a return value or rendered output
- Duplicate cases: tests or stories whose bodies match once literals are removed, grouped per file
- Tests for spent code: the subject is a one-shot (a migration, a data repair) or has had no source change in months
- Timing hacks: sleep patches, clock patches, and retry markers in tests

A change whose stated intent disagrees with its numbers (a refactor that raises erosion, a cleanup that adds mass) gets a reading that says so, as evidence a reviewer can use to question the premise. It never blocks a change, and a reading that a reviewer overrules teaches the rule nothing until a person marks it.
- Its tests run the built binary against a generated repository and diff golden JSON for each subcommand. Unit tests stay for the counting rules where a fixture cannot show the edge, such as nested functions and decorators. Goldens at pinned public SHAs are not built yet
- A metric over history is tested the same way, with a history written as a tree whose keys name the commit each file belongs to, so a variant adds a commit or a file to one. A cycle's origin must hold through a rename of a member, a new member, and a rewritten import, and move when the cycle is broken and made again. Hidden coupling must hold through a bulk reformat, a test that changes with its subject, a lockfile, a generated file, a bot commit, and a docs commit, and move when one file comes to import the other
- A cross-file metric is tested the same way as a cross-file rule: cosmetic variants must leave the number exactly as it was, and real changes must move it. Module depth has both sets for Python, TypeScript, and Go, and a third fixture where an unrelated type's same-named method must leave every attributed number alone
- A cross-file rule is tested against adversarial variants of one fixture tree (`internal/fixturetest`). A dodge is a cosmetic change, such as reordering imports, renaming an alias, or re-exporting through a barrel, and must leave the finding ids exactly as they were. A fix is a real repair, such as extracting the shared piece into a third module, and must leave no finding. `x/import-cycle` has both sets for Python and TypeScript
- Each metric is checked against a small human-labeled sample before it ships: does a high reading match code a reviewer would call hard to change? A metric that fails this does not ship. The labels file records a rule's measured precision for one repository, and the corpus read that sets the gate is still a separate sample

## Evaluation corpus

Public repositories at pinned SHAs, chosen to cover the three languages and both small and large histories:

- [KyleKing/calcipy](https://github.com/KyleKing/calcipy) (Python, small, one author)
- [hukkin/mdformat](https://github.com/hukkin/mdformat) (Python, plugin ecosystem)
- [google/go-github](https://github.com/google/go-github) (Go, large, many contributors)
- [facebook/lexical](https://github.com/facebook/lexical) (TypeScript, large)
- [KyleKing/second-look](https://github.com/KyleKing/second-look) (Go, agent-assisted history)

The worked example pins calcipy at [594d31b](https://github.com/KyleKing/calcipy/commit/594d31bcf9321433336f2ef11135bd29a82cef98) in [docs/examples/calcipy.md](docs/examples/calcipy.md). The other entries have no pinned goldens yet.

Each milestone reports wall time, peak memory, and cache hit rate on a cold and a warm cache for each repository.

## How it compares

Qualitative, per feature. Closed products are judged from public documentation, not from use.

| Feature | This tool | scb-check | wily | lizard | hercules | git-of-theseus | code-maat | SonarQube CE | CodeScene (closed) | GitClear (closed) |
|---|---|---|---|---|---|---|---|---|---|---|
| Erosion as the paper defines it | Exact, all 3 languages | Exact, reference | No | Inputs only (CC, NLOC) | No | No | No | No | No | No |
| Function-shape distributions | p90/max per family | Totals only | Per file over time | Per function, no aggregates | No | No | No | Per file thresholds | Code Health folds them in | No |
| Slop rules with IDs | Python, TS, Go, native + ast-grep | 137 rules, Python only, IDs in human output | No | No | No | No | No | Broad rules, few target agent code | Some code smells | No |
| Clones | Normalized subtrees, cross-file | Same method | No | Token-based option | No | No | No | Token-based | Yes | Copy/paste vs moved lines |
| Change-scoped scoring | Default | No | No | No | No | No | No | New code period | Delta analysis on PRs | Per commit |
| Per-blob cache | Yes | No | Per revision | No | No | No | No | Server database | Hosted | Hosted |
| Churn and rework | 14/30-day churn, reverts, fix-touch | No | No | No | Partial | No | Churn | No | Yes | Yes, core feature |
| Line survival | Kaplan-Meier by cohort | No | No | No | Burndown | Kaplan-Meier | Age only | No | Partial | Partial |
| Hotspots and coupling | Yes | No | No | No | Couples | No | Yes, reference | No | Yes, reference | No |
| Author-class split | Configured classes | No | No | No | Per person | Per person | Per person | No | Per team | Per person and AI tool |
| Pattern spread over time | Yes | No | No | No | No | No | No | No | No | No |
| Change intent and reading | Rules over commit metadata | No | No | No | No | No | No | No | Partial | Partial |
| jj support | Through colocated git | n/a | Git only | n/a | Git only | Git only | Log input | Git | Git | Git |
| Runs offline as one binary | Yes | Python | Python | Python | Go + Python plots | Python | JVM | Server | No | No |

The gaps this tool fills: change-scoped scores with a cache, the reading of intent, pattern spread, and the paper's erosion in TypeScript and Go. The places it is weaker: rule breadth against SonarQube, polish and history against CodeScene, and GitClear's scale of cross-industry baselines.

## After v1

Ordered by value.

### Stale and dead code, comments, and config

The highest-value addition and the hardest. Things go stale where no compiler looks: a comment naming a function that was renamed, a config key nothing reads, a flag no caller sets, a TODO whose ticket closed, a doc listing a module that was deleted. Each needs a reference graph that crosses languages and file kinds.

- A symbol table per blob (definitions and references from tree-sitter), cached like every other metric, joined across the tree at query time — shipped: `Declared`, `Exported`, `Referenced`, `Imports`, `Strings`, `ProseRefs`, `ConfigKeys` per blob
- Dead exports: an exported symbol with no reference in the repository, with entry points (CLI commands, plugin registrations, `__all__`, package `exports`) declared in config so they are not reported — shipped as `x/dead-export`
- Stale options and flags: defined with no caller passing a non-default value (shipped as `x/unused-option`), or read with no writer anywhere in config
- Stale config keys: a key in TOML, YAML, JSON, or Pkl that no code reads by name — shipped as `x/stale-config` over the files `config_paths` names, opt-in so tool and CI config stays quiet. Still open: a code read of a key no config defines
- Stale prose: an identifier in a comment, docstring, or Markdown file that no longer resolves — shipped as `x/stale-prose`. `change` reports only names the change removed, since history shows they existed. A snapshot reports every unresolved name, behind `--experimental`. A name resolves through declared, referenced, and imported names (types included) and through short string contents, which carry config keys and struct-tag keys. A name right after "not", "never", "avoid", "instead of", or "rather than" is a counterexample and is skipped, and so is anything inside a fenced code block, which holds sample code and command output. Still open: a TODO or "remove when" whose forward-pointing ticket is closed (read through `gh` when available)
- Across updates: a symbol whose last reference was removed in a change is reported on that change, which is when the author still remembers it. Shipped as `x/last-ref`
- Dynamic references (reflection, string-built names, dependency injection) make this unsound, so every finding names why the tool believes it is dead and a baseline file absorbs accepted ones

### Moved code, shared with second-look

second-look's `internal/diff` already finds moved runs of lines, reindented lines, and word-level pairs. slop-strata needs the same answer to keep moved code out of churn and survival. Extract it into aragonite as `diff`, with second-look and slop-strata as its two consumers, then improve it there once:

- Function-level matching from tree-sitter: a function that moved files, or changed only its name or indentation, pairs with its old self ([GumTree](https://github.com/GumTreeDiff/gumtree) and [RefDiff](https://arxiv.org/pdf/1704.01544) are the references)
- Classification per hunk: moved, renamed, reindented, or changed, which second-look draws on the review screen and slop-strata uses to exclude moves from churn

Shipped in part: `lineage` matches moved lines with aragonite's `diff.MatchMoves`, and a moved line keeps its birth day. Function-level matching and per-hunk classification are not built yet.

### Polish

What a mature product does that v1 will not, measured against CodeScene and SonarQube:

- Precision first: each rule ships only with a measured false-positive rate on the corpus below a set bar, and a rule that drifts above it is turned off by default. The gate exists, and the labels file now puts a per-repository number beside each rule. Moving a rule across the gate stays the owner's decision, and nothing automatic does it
- Every finding explains itself in one screen: the rule, the line that triggered it, a minimal before and after, and how to suppress it inline with a reason. Shipped in part: a finding carries its rule, line, matched text, enclosing function, and an id, and `slop-strata:ignore <rule-id> <reason>` suppresses it inline. The minimal before and after is not built yet
- Stable output: a JSON schema version (shipped, `docs/schema.json`), finding IDs that survive line moves (shipped, see "Slop rules"), and a changelog entry for every change to a threshold
- Fast first run: a full history of a 3,000-commit repository in minutes on a laptop, with progress and a resumable cache. Parsing is parallel, and `history` prints a commits done/total line to a terminal once a run passes two seconds. Resume is not built yet
- Zero-config start: language detection, vendored and generated file detection, and author classes guessed from known bot logins, with `init` writing what it guessed for review. Shipped
- Trends people trust: a noise floor and provisional markers on the trend tables. Shipped; the charts are text tables for now

### Other

- Harness and prompt comparisons: tag changes by agent, model, or skill version from trailers, and compare their families with the same pairing as author classes
- Languages beyond the first three, in order of use across the repositories this targets: Lua (Neovim plugins and mise tools), then shell scripts. Config formats (TOML, YAML, JSON, Pkl) join through the stale-config work above, not as full languages
- A `history` screen to dig from a trend into the changes and deltas behind it, sharing second-look's diff view through aragonite rather than drawing its own
- Halstead and maintainability index, if a labeled sample shows they add anything over the structure family
- A rule-authoring loop: propose a rule from a cluster of findings a person marked, test it against the corpus, and report its hit rate before it ships

## Open

- Size cap and minified-shape thresholds, set from vendored files in the corpus
- Which scb-check rules to port natively first, chosen by hit counts on the corpus

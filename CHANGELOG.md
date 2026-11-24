## v0.14.0 (2026-11-24)

### Feat

- **snapshot**: infer the import graph, count fan-in, fan-out, and cycles, and report cycles as x/import-cycle

## v0.13.0 (2026-11-14)

### Feat

- **rules**: suppress a finding inline with slop-strata:ignore or nolint and a reason
- **rules**: give findings stable ids, key the baseline by id, and record verdicts in a labels file
- **history**: count functions whose body changed in three commits within 14 days
- **history**: count young deaths that a later commit added back to the same file
- **history**: show commits done out of the total on a terminal after two seconds
- **history**: print young-death shares per directory and hide self on solo-author windows
- **history**: count how young lines die by author, edit shape, file, age, and revert
- **snapshot**: rank by recent touches times mass, list the heaviest functions, and drop --context
- **change**: rank a next list in the JSON and attach each function's source with --context
- **change**: add --brief and --exit-code for hooks and agent loops
- **change**: hide one-directory tables, all-zero columns, and unsupported-file skips from the default text
- **change**: name the functions a change grew, report gross mass per function, split tests from production, and compute directory erosion

### Fix

- **rules**: skip generic constraints and spread variadics in go/type-any
- **rules**: write the baseline and labels files through a temp file and rename
- **rules**: hash the finding's line text into its id so a hit added above keeps the old id
- **history**: check a death's age when matching a re-addition
- **change**: sum mass in sorted path order so the JSON repeats to the last digit
- **history**: key oscillating functions by their parent-qualified names
- **history**: drop a file's tracked lines without deaths when it crosses max_file_bytes
- **change**: pass last-ref findings through the baseline, labels, and ignores
- **snapshot**: rewrite an old-format baseline instead of reading it first
- **rules**: skip fenced code blocks when reading prose for stale names
- **rules**: resolve types, struct-tag keys, and negated names in stale-prose and name the narrating phrase
- **deps**: bump aragonite to v1.17.0 for the long-lived blob reader and size-capped history diffs
- **cache**: key text fingerprints by the extension and manifest name they read
- **history**: order coupling pairs and hotspot sums deterministically
- **change**: name the busiest directory and stop reading docs and perf as test and refactor

### Refactor

- **rules**: make the labels format error private since nothing checks it
- **rules**: gate cross-file and path rules on the catalog's tier table

### Perf

- **history**: leave cached fingerprints undecoded on lookups
- **history**: read the log a window ahead while scoring
- **history**: score blobs in windows of commits with one cache transaction each way
- **history**: answer cached and path-skipped blobs before reading them
- **history**: read every commit's blobs through one cat-file process
- **history**: diff files over max_file_bytes as binary instead of line by line
- **history**: rewrite a file's line tags in one pass per commit

## v0.12.0 (2026-11-11)

### Feat

- **snapshot**: rank tests apart, list files, and score case tables

## v0.11.4 (2026-11-10)

### Fix

- **deps**: bump aragonite to v1.13.1 for correct log record and binary path parsing

## v0.11.3 (2026-11-10)

### Perf

- **change**: count hotspot touches from file names

## v0.11.2 (2026-11-10)

### Fix

- **history**: sort the hotspot table by score and show survival intervals
- **explain**: document the survival interval fields s30_ci and s90_ci
- **lineage**: credit churn to the directory lines were added in
- **change**: sort last-ref findings so output repeats

## v0.11.1 (2026-11-10)

### Fix

- **change**: key the tree memo on the commit a revision resolves to
- **change**: read pure renames from their trees, not the worktree

### Perf

- **change**: batch changed-file blob reads and memoize tree fingerprints

## v0.11.0 (2026-11-10)

### Feat

- **change**: flag prose that still names a name the change removed

### Fix

- **history**: show a dash for a directory with no live lines

### Refactor

- **lineage**: match moved lines with aragonite's MatchMoves

## v0.10.0 (2026-11-10)

### Feat

- **explain**: list metric names and cover every table header with an entry
- **history**: add Greenwood intervals and a median-absolute-deviation noise floor
- **report**: mark rates from thin samples as provisional
- **history**: add churn_14d and an eight-week mass sparkline to the directory table

### Fix

- **history**: print the provisional footnote only beside a marked rate
- **rules**: skip event-loop sleep(0) in py/timing-hack

### Refactor

- **history**: move the week table out of printHistory

## v0.9.0 (2026-11-10)

### Feat

- **report**: publish the JSON schema and golden subcommand output
- **engine**: skip generated files by marker and linguist-generated
- **report**: count skipped and unparsed files in every envelope
- **cli**: per-command help, exit 2 on usage errors, and version JSON
- **change**: score many revision pairs in one process with --pairs

### Fix

- **intent**: break kind ties by commit order so readings repeat
- **cache**: key blobs by path class and the digest of the analysis code
- **rules**: limit dup-test-case to test cases and skip Go methods in dead-export

### Perf

- score blobs on a worker pool with one parser per worker

## v0.8.0 (2026-11-10)

### Feat

- **rules**: run rules below the precision bar only with --experimental

### Fix

- **rules**: report a Go registry declaration once
- **rules**: judge mock-only asserts per it or test body
- **rules**: match only patch entry points for py mock-patch
- **rules**: narrow narrating-comment to change-narration phrases
- **rules**: require six statements or eight lines for clone reports
- **rules**: split could-be-private, honor manifest and interface refs
- **rules**: skip history files, link targets, and domains in stale-prose
- **rules**: flag Python or-defaults only for empty fallbacks on parameters
- **rules**: flag test file reads only for repository source, config, and docs
- **rules**: narrow repeated-literal findings to shared strings across files
- **rules**: flag silent Go errors only for a discarded last result

## v0.7.0 (2026-11-10)

### Feat

- **config**: ship default agent and bot author classes

### Fix

- **reading**: singularize one function in the headline
- **report**: pad table cells by rune width
- **lineage**: report survival only with enough lines at the horizon
- **change**: name missing-node parse errors without a byte share
- **change**: count untracked lines and hotspot touches per commit
- **change**: base the default diff on the upstream of the default branch
- **change**: diff a root commit against the empty tree
- **reading**: lead with net and grown mass and split touched from added
- **change**: mark findings new only on added lines
- **change**: match functions by owner-qualified name
- **lineage**: age lines by committer date in UTC
- **config**: match author class logins against name or email
- **lineage**: null survival past exposure and censor young tests
- **lang**: count comprehension clauses and logical assignments in cc
- **structure**: classify CRLF blank lines as blank
- **structure**: flag any ERROR or MISSING node as a parse error
- **lang**: count parameters from the parameters field
- **structure**: count else in Go and TypeScript toward cognitive
- **lang**: count Go expression switches as nesting
- **structure**: score nested functions on their own

## v0.6.0 (2026-11-10)

### Feat

- **change**: flag a symbol whose last use the change removed

## v0.5.0 (2026-11-10)

### Feat

- **rules**: flag stale prose and unread config keys
- **history**: count the directories a rule's hits spread across
- **rules**: track exports per blob and flag dead exports
- **lineage**: carry line tags across same-commit moves
- **change**: match moved functions exactly so moves don't read as growth
- **lineage**: censor surviving lines at their real exposure in survival estimates

### Fix

- **init**: write top-level keys ahead of the author-class table

## v0.4.0 (2026-11-10)

### Feat

- add init command writing config and baseline

## v0.3.1 (2026-11-10)

### Fix

- **cache**: retry the WAL-and-schema open race across parallel runs

## v0.3.0 (2026-11-10)

### Feat

- **change**: report function-matched mass growth and over-limit share

## v0.2.0 (2026-11-10)

### Feat

- error rows, ast-grep packs, test survival, true mass deltas
- score changes, snapshots, and history on structure, time, and slop rules

### Fix

- **lineage**: pad pre-range file state instead of panicking

### Refactor

- thread context through the analyzer and split command paths
- **internal**: extract helpers, name constants, realign structs
- **rules**: share constants and split complex rules

## v0.1.0 (2026-11-09)

### Feat

- Initialize with my_go_template

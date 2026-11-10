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

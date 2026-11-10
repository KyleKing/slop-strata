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

# Remaining work

Checked against the code at 5c196df. Items from the v0.6.0 audit that are now done are left out.

## Output that misleads

- `history` text omits the pairing rows. They are JSON only.
- `history` does not group changes by reading. Only `change` prints a reading.

## Rules below the bar

No labels file exists, so no rule has a recorded precision, and the gate is a judgment call. Build the labels file, record precision per rule and per release, and move a rule out of the gate only on that evidence. `slop-strata --explain experimental` lists the gated rules.

- `go/silent-error` flags any blank last target on a sole call. Flagging only discarded errors needs callee types, which the rule does not have.
- `x/narrating-comment` is still gated. Drop it, or keep it gated, once the labels file shows its precision.
- `x/dead-export` skips Go methods, which avoids interface false positives. It does not resolve interface satisfaction.
- `snapshot` reports `x/stale-prose` for every unresolved name, behind `--experimental`. It should flag only names the history shows existed and are now gone. The exemptions for changelogs, ADRs, and migration guides already exist.
- `py/type-any` and `go/type-any` report every hit. They should report a count per file.

## Cache and speed

- `Cache.Get` runs one query per blob. Batch a tree's lookups into one read transaction, then measure cold and warm times to see whether it helps.
- The only timings recorded are for calcipy, aragonite, and second-look, all under 600 files. The cold-run target for a large tree is unmeasured.

## Tests and corpus

- `internal/lang` and `internal/srcdigest` have no tests.
- Goldens at pinned public SHAs are not built. The worked example pins calcipy at 594d31b, but no test reads that pin.
- A per-release precision record needs the labels file above.

## Open from DESIGN.md

- Finding IDs that survive line moves
- Progress output and a resumable cache for the first run
- Function-level move matching and per-hunk classification
- Stale config: a key a config file defines that no code reads by name is reported, but a key code reads that no config defines is not
- Stale prose: a TODO or "remove when" whose forward pointer is closed
- A history view that drills into the changes behind a trend

## Not planned here

HTML output stays with the consumer. The tool emits text and JSON only.

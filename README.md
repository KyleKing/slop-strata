# slop-strata

slop-strata scores a code change, a whole tree, and a repository's history for the drift that builds up when code is written fast. It reports complexity mass, churn, how long code survives, hotspots, and a handful of rule hits. Each number stays separate, so you can read them side by side and decide which one matters for the change in front of you.

A single quality score hides what moved. A change can add mass to one function and remove it from three others, and a blended score says only "down a little." slop-strata names the change in one sentence, shows the parts, and leaves the judgment to the reader.

## Installation

### GH CLI Extension

> **Note:** Requires a [GitHub Release](https://github.com/kyleking/slop-strata/releases) with precompiled binaries. See [CONTRIBUTING.md](CONTRIBUTING.md#releases) for creating the first release.

```bash
gh extension install kyleking/slop-strata
```

### Homebrew

goreleaser publishes a cask to [`kyleking/homebrew-tap`](https://github.com/kyleking/homebrew-tap) with each release, so the install needs `--cask`:

```bash
brew install --cask kyleking/tap/slop-strata
```

### Go Install

```bash
go install github.com/kyleking/slop-strata/cmd/slop-strata@latest
```

## Quickstart

Run these from inside a git repository. The output below comes from [calcipy](https://github.com/KyleKing/calcipy) at commit `594d31b`, trimmed to the lines that matter. The [worked example](docs/examples/calcipy.md) lists the exact commands and reads every table.

`change` scores the change at hand against its merge base. With no flags it reads your working tree. Here it scores one commit:

```bash
slop-strata change --rev 00b5c3b375856a0387ef4a149fc53f918d9ef259 --base 00b5c3b375856a0387ef4a149fc53f918d9ef259^
```

```text
Refactor in `calcipy`: net +48.6 mass, 68.6 grown; touched 1 function over CC 10; erosion 0.63 → 0.43 — reads as a refactor that adds mass

delta             value
mass added        159.8
mass removed      111.2
mass net          +48.6
mass grown        68.6
grown over CC 10  0.0
erosion           0.63 → 0.43
lines             +178 −35
```

`snapshot` scores the whole tree at a revision, with directories ranked by hotspot:

```bash
slop-strata snapshot
```

```text
erosion 0.18 · mass 1240 · 182 funcs · 1 over 50 lines

dir                         files  erosion  mass  over_50  rule_hits
tests/tasks                 13     0.00     119   0        6
calcipy/tasks               13     0.23     318   1        0
calcipy                     10     0.25     315   0        0
...
calcipy/experiments         5      0.57     121   0        0
```

`history` walks the first-parent merges in a window and reports trends:

```bash
slop-strata history --since 2025-11-09
```

```text
115 first-parent commits · 2 reverts

week        commits  added  removed  mass
2026-01-12  9        +6779  -1134    +338
2026-01-19  18       +2193  -1691    +308
2026-01-26  38       +2859  -4115    -624
...
2026-07-27  12       +773   -111     +57
2026-08-24  7        +321   -37      +6~
2026-08-31  10       +672   -904     +2~
~ within one noise floor (7) of the median week

cohort   added  alive  s_30d  s_90d
2026-01  11497  7021   0.69   0.64
2026-03  686    576    0.93   0.93
2026-07  922    553    0.99   —
...
```

Add `--all` to `change` or `snapshot` to show every number, including the ones that did not move. Add `--json` to `change`, `snapshot`, or `history` for machine-readable output.

## What the numbers mean

Each name below has a full definition. Run `slop-strata --explain NAME` to read it, or `slop-strata --explain` with no name to list them all.

- `mass` is the sum of CC times the square root of SLOC over each function. `--explain mass`
- `mass grown` is how much a change adds to the functions it touches, with new functions counted in full. `--explain mass_grown`
- `erosion` is the share of mass held by functions with cyclomatic complexity over 10. `--explain erosion`
- `over_limit` is the share of mass grown that lands in functions already over CC 10. `--explain over_limit`
- `churn_14d` and `churn_30d` count lines removed or changed within 14 or 30 days of being written. `--explain churn_30d`
- `rework` counts young lines removed by a fix commit. `--explain rework`
- `hotspot` is 30-day churn times a directory's live mass. `--explain hotspot`
- `survival` is the share of a monthly cohort's added lines still alive at 30 and 90 days. `--explain survival`
- `concentration` is the largest single author's share of a directory's live lines. It is reported per directory and never as a ranking of people. `--explain concentration`
- `findings` lists rule hits, each with a rule id, file, and line. `--explain findings`
- `pairing` compares author classes in the same week and directory. `--explain pairing`
- `counts` says how many files the run could not score, and why. `--explain counts`

Two markers appear in the text output. A `*` marks a rate that rests on too few samples, and `~` marks a week whose net mass sits inside the noise floor. `--explain provisional` and `--explain noise_floor` say what sets each one.

## Guardrails

No number here has a target, and nothing fails a build. slop-strata prints no grade and no combined score. The components stay separate, so a fall in mass cannot cancel a rise in churn.

slop-strata raises questions about a change and leaves the answers to you. A change whose stated intent disagrees with its numbers gets a reading that says so. Whether the disagreement matters is for the person reviewing the change.

## Experimental rules

A rule runs by default only when a sampled read of its hits on the corpus met 80% precision. Rules below that bar stay gated. Turn them on for one run with `--experimental`, or for a repository with `experimental = true` in `.slop-strata.toml`.

The gate exists because a noisy rule teaches people to skip its output, and that includes the hits that are right. `--explain experimental` lists the rules still gated.

## JSON integration

`change`, `snapshot`, and `history` all take `--json`, and that output is the integration surface. Every object is an envelope:

```json
{"result": {...}, "docs": {...}, "tool": "slop-strata", "command": "change", "repo": "...", "at": "...", "counts": {"errors": 0, "skipped": 0}, "warnings": [], "schema": 2}
```

- `docs` holds the definition of every metric in the result, the same text `--explain` prints
- `counts` says how many files the run could not score, so a consumer never reads an unscored file as zero
- `--context` adds the hunks, source lines, and commit messages a model would open next. It works with `--json` only
- `schema` is the envelope version. It goes up on any field change. [docs/schema.json](docs/schema.json) is the JSON Schema for version 2

To score many changes in one process, pass `change --pairs` a file of `<base> <rev>` lines, or `-` to read stdin. Pairs need `--json`. The output is JSON Lines, one envelope per pair. A pair that cannot be scored gets an `error` field and a `null` result, and the other pairs still run. This scores the first-parent commits of the current branch:

```bash
git log --first-parent --format='%P %H' -n 50 | awk '{print $1, $NF}' \
  | slop-strata change --pairs - --json > changes.jsonl
```

## Configuration

Every setting is optional. slop-strata reads `.slop-strata.toml` from the repository root, and a missing file means the defaults. An unknown key fails the run, so a typo cannot quietly turn a setting off.

```toml
skip = ["vendor/*", "*.min.js"]
entry_points = ["cmd/*", "main.go"]
config_paths = ["config/*.toml"]
experimental = false

[[author_class]]
name = "release"
logins = ["release-bot"]
```

- `skip` lists path globs excluded from every analysis, such as vendored trees and generated code
- `author_class` groups commits by who wrote them, so every metric can split one group from another. A class matches on `logins` (a name or email, where a leading `*` matches a suffix) or on `trailers` (regular expressions over the commit body). The first match wins
- The built-in classes come after yours. `agent` matches co-author trailers from coding agents, and `bot` matches logins ending in `[bot]`. Your classes come first, so a repository can claim a login for a different class
- `experimental` turns on the gated rules for every run
- `baseline` names the file of accepted findings, which defaults to `.slop-strata-baseline.json`
- `entry_points` lists files whose exports the dead-export rule skips, and `config_paths` lists the config files the stale-config rule reads
- `default_branch`, `max_file_bytes`, and `minified_avg_line` change which branch a change diffs against and which files get skipped as oversized or minified

`slop-strata init` writes a starter `.slop-strata.toml` when none exists, with bot logins found in recent history. It then records the current findings as the baseline, so a rule added later reports only new hits. `snapshot --write-baseline` records the baseline without touching the config.

## Performance

These are one run each on an Apple M4 Pro with 12 cores. A cold run starts with an empty blob cache. I set that up by pointing `HOME` at a fresh directory. A warm run is the next run against that same cache.

| Repository  | Files | Command              | Cold    | Warm    | Peak memory (cold) |
| ----------- | ----- | -------------------- | ------- | ------- | ------------------ |
| calcipy     | 142   | `snapshot`           | 0.07 s  | 0.03 s  | 29 MB              |
| aragonite   | 162   | `snapshot`           | 0.18 s  | 0.04 s  | 41 MB              |
| second-look | 584   | `snapshot`           | 0.44 s  | 0.07 s  | 67 MB              |
| calcipy     | 142   | `history` (365 days) | 1.56 s  | 1.27 s  | 42 MB              |
| second-look | 584   | `history` (365 days) | 15.70 s | 5.74 s  | 95 MB              |

Parsing runs one worker per CPU core (GOMAXPROCS), with one parser per worker. A warm `snapshot` skips parsing for every blob it has already seen. `history` still walks every commit and reads each changed blob, so a warm history run stays much slower than a warm snapshot. These are small repositories, and I have not measured one with tens of thousands of files.

## Read more

- [DESIGN.md](DESIGN.md) covers the decisions behind each metric, the evaluation corpus, and what is still planned
- [docs/examples/calcipy.md](docs/examples/calcipy.md) is a worked example on a pinned commit, with the commands, real output, and a reading of each table
- [docs/next-steps.md](docs/next-steps.md) lists the work still open

## Development

```bash
mise install && hk install --mise
mise run ci
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for full development workflow.

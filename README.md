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
Refactor in `calcipy`: net +34.8 mass, 35.7 grown; touched 1 function over CC 10; erosion 0.63 → 0.43 — reads as a refactor that adds mass

delta                   value
mass grown              35.7
mass removed            0.9
mass net                +34.8
grown over CC 10        0.0
tests mass grown        32.9
tests mass removed      19.1
tests mass net          +13.8
tests grown over CC 10  0.0
erosion                 0.63 → 0.43
lines                   +178 −35

top functions                                           grown
calcipy/invoke_helpers.py:47  find_repo_root  cc new→5  +18.0
calcipy/invoke_helpers.py:65  get_doc_subdir  cc 2→5    +17.7
```

`snapshot` scores the whole tree at a revision, with directories and the ten hottest files ranked by hotspot, then mass, and the five heaviest production functions named. A hotspot here is the commits that touched a directory or file in the 90 days up to that revision, times its mass (`--explain hotspot`). Test directories get their own table, and a map literal of 15 or more structured entries (a hand-written case table) adds `table_mass` and an `x/case-table` hit. When the tool can resolve imports between files of the tree, a line under the totals counts the import edges and the import cycles, and each directory row carries `fan_in` and `fan_out` (the sample below predates those two columns). A second table, `module depth`, lists the directories that other directories use names of, shallowest first: how many names are `shown` and `hidden`, how many of the shown are only matched by name, and `depth`. Long directory paths are shortened in the middle there, so the table stays inside 100 columns. `--json` lists every file under `files`:

```bash
slop-strata snapshot
```

```text
erosion 0.18 · mass 1240 · 182 funcs · 1 over 50 lines

dir                    files  erosion  mass  table_mass  over_50  rule_hits  hotspot
calcipy/tasks          13     0.23     318   0           1        0          1590
calcipy                10     0.25     315   0           0        0          1261
calcipy/experiments    5      0.57     121   0           0        0          241
...

top functions                                                                                 sloc  mass
calcipy/cli.py:58                                     start_program                    cc 13  37    79.1
calcipy/tasks/tags.py:31                              collect_code_tags                cc 11  46    74.6
...
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

cohort   added  alive  s_30d              s_90d
2026-01  11497  7021   0.69 [0.68, 0.70]  0.64 [0.63, 0.65]
2026-03  686    576    0.93 [0.91, 0.95]  0.93 [0.91, 0.95]
2026-07  922    553    0.99 [0.98, 0.99]  —
...
```

Add `--all` to `change` or `snapshot` to show every number, including the ones that did not move. On `change` it also lists test functions, every directory, and unsupported files. Add `--json` to `change`, `snapshot`, or `history` for machine-readable output.

## In a hook or an agent loop

`slop-strata change --brief` prints nothing when the change is not worth a look. Otherwise it prints the reading and up to three `next:` lines. A change is worth a look when a production function over CC 10 grew or was added, a rule hit that is not in the baseline appeared, or the stated intent disagrees with the numbers (the "reads as" clause). The rule is fixed and has no score. `--explain next` lists the order.

`--exit-code` turns that into a status: 1 when `--brief` would speak, 0 when it would not, and 2 for a real error or a bad command line. It is off by default, so a plain run never fails a build.

A git hook that shows the lines and stops the commit:

```bash
# .git/hooks/pre-commit
slop-strata change --brief --exit-code || [ $? -ne 1 ]
```

That line blocks the commit on status 1 and lets an error through. Drop the `|| ...` part to block on errors too.

A coding agent reads the same lines. A hook that runs after the agent edits a file can pipe them back:

```bash
slop-strata change --brief --exit-code >&2 || exit 2
```

Claude Code treats exit 2 from a hook as "feed stderr to the model", so the agent sees the reading and the `next:` lines and keeps working. `--json` carries the same items in `result.next`, and `--json --context` adds the source lines of each function in it.

## What the numbers mean

Each name below has a full definition. Run `slop-strata --explain NAME` to read it, or `slop-strata --explain` with no name to list them all.

- `mass` is the sum of CC times the square root of SLOC over each function. `--explain mass`
- `mass grown` and `mass removed` are the gross mass a change added to and took from its functions, new and deleted functions counted in full, for production and for tests apart. `--explain mass_grown`, `--explain mass_removed`
- `top_functions` names the functions that grew most on `change`, with file, line, and CC before and after, and the heaviest functions of the tree on `snapshot`. `--explain top_functions`
- `next` lists up to three places to look first, with the reason for each. `--explain next`
- `erosion` is the share of mass held by functions with cyclomatic complexity over 10. `--explain erosion`
- `over_limit` is the share of mass grown that lands in functions already over CC 10. `--explain over_limit`
- `churn_14d` and `churn_30d` count lines removed or changed within 14 or 30 days of being written, credited to the directory they were written in. `--explain churn_30d`
- `rework` counts young lines removed by a fix commit. `--explain rework`
- `deaths_30d` splits the lines behind `churn_30d` by how they died: removed by their own author (`self`), deleted without a replacement (`pure`), taken out with their whole file (`whole_file`), removed in a revert, and by age (a day, a week, 30 days). `history` prints the shares per directory with at least 30 such deaths. Squash merges hide what happens inside a branch. `--explain deaths_30d`, `--explain self`, `--explain pure`, `--explain whole_file`, `--explain within_7d`, `--explain in_revert`
- `readd` is the share of young deaths where a later commit added the same line back to the same file within 30 days, for lines of 30 or more bytes. Deaths in a revert, and re-additions by one, are counted apart as `readded_by_revert`. Deaths in the last 30 days of the window are left out of the share, because they have not had time to come back. `--explain readd`
- `oscillating_funcs` counts the functions whose body changed in at least three commits within 14 days, per directory, with test files counted apart. The JSON names the ten busiest. A formatting commit or a test-first loop on one function can raise it. `--explain oscillating_funcs`
- `hotspot` is the number of recent commits that touched a directory or file times its mass on `snapshot`, and 30-day churn times a directory's live mass on `history`. `--explain hotspot`
- `fan_in` and `fan_out` count, per directory on `snapshot`, the other directories that import it and that it imports. The graph is inferred from the imports the tool can resolve inside the tree, and nothing about layers is declared anywhere. `--explain fan_in`, `--explain fan_out`, `--explain import_edges`
- `shown` and `hidden` count, per directory on `snapshot`, the declared names that a file of another directory uses and the ones none does. Use decides it and never a visibility keyword, so adding `export` or renaming to a capital changes nothing. `depth` is the hidden share by mass: near 1 the directory hides most of what it holds, near 0 its callers see nearly all of it, and `-` means nothing outside uses it. `widest_surface` names the file with the most shown names. The JSON adds `shown_mass`, `hidden_mass`, and `forwarders`, the pass-through functions left out of both. `--explain shown`, `--explain hidden`, `--explain shown_mass`, `--explain hidden_mass`, `--explain forwarders`, `--explain depth`, `--explain widest_surface`
- `shown_by_name` (`by_name` in the table) says how many of `shown` are methods matched by name alone, because the tool infers no types. Those are guesses, so `depth` prints as a range when there are any: `depth` counts them as shown and `depth_attributed` counts them as hidden, and the true value lies between. `--explain shown_by_name`, `--explain depth_attributed`
- `surface_widened` lists, on `change`, the directories that other directories now use more names of, with those names and the depth before and after. Only uses a resolved import attributes count, so a method matched by name never opens a row and is never named. It is information only, and `--brief` never speaks of it. `--explain surface_widened`
- `import_cycles` counts the groups of non-test files that import each other at module level, with the size of the largest. Imports only a type checker reads, imports inside a function, and imports under a main guard do not close a cycle. `--experimental` lists each group as an `x/import-cycle` finding with its cheapest cut. `--explain import_cycles`
- `survival` is the share of a monthly cohort's added lines still alive at 30 and 90 days. `--explain survival`
- `concentration` is the largest single author's share of a directory's live lines. It is reported per directory and never as a ranking of people. `--explain concentration`
- `findings` lists rule hits, each with an id, rule id, file, and line. `--explain findings`
- `suppressed` counts the findings a run set aside, in `counts.suppressed`: accepted in the baseline, ignored inline with a reason, or labeled false positive. `--explain suppressed`
- `stale_baseline` counts, on `snapshot`, the baseline entries that no longer match a finding. `--explain stale_baseline`
- `precision` is the share of a rule's labeled findings that were real, per rule, from `slop-strata rules`. `--explain precision`
- `pairing` compares author classes in the same week and directory. `--explain pairing`
- `counts` says how many files the run could not score, and why. `--explain counts`

Two markers appear in the text output. A `*` marks a rate that rests on too few samples, and `~` marks a week whose net mass sits inside the noise floor. `--explain provisional` and `--explain noise_floor` say what sets each one.

## Guardrails

No number here has a target, and nothing fails a build unless you pass `--exit-code`. slop-strata prints no grade and no combined score. The components stay separate, so a fall in mass cannot cancel a rise in churn.

slop-strata raises questions about a change and leaves the answers to you. A change whose stated intent disagrees with its numbers gets a reading that says so. Whether the disagreement matters is for the person reviewing the change.

## Experimental rules

A rule runs by default only when a sampled read of its hits on the corpus met 80% precision. Rules below that bar stay gated. Turn them on for one run with `--experimental`, or for a repository with `experimental = true` in `.slop-strata.toml`.

The gate exists because a noisy rule teaches people to skip its output, and that includes the hits that are right. `--explain experimental` lists the rules still gated, with the precision your labels give each one.

## Setting findings aside

Every finding has an `id`, ten hex characters hashed from its rule, path, enclosing function, and matched text. The id survives line moves, so a finding keeps its id when the code above it changes. An `x/import-cycle` finding is the exception: its id comes from the rule and the sorted files of the cycle, so it holds however the imports are written and changes when a file joins or leaves. `snapshot --json` and `change --json` print it, and `snapshot --all` and `change` print it in text.

To mute one finding inline, write `slop-strata:ignore`, the rule id, and a reason in a comment on the finding's line or the line above it:

```go
// slop-strata:ignore go/timing-hack waits on the fake clock's tick
time.Sleep(tick)
```

Use the file's own comment syntax: `//` or `#` in code, `#` in TOML and YAML, and an HTML comment in Markdown. A Markdown comment inside a fenced code block is example text and does not count. JSON has no comments, and ast-grep findings keep ast-grep's own suppression comment. In Go, `//nolint:errcheck // reason` also mutes `go/silent-error`.

The reason is required. An ignore without one mutes nothing, and slop-strata reports it as `x/ignore-without-reason`. A `//nolint` with no reason mutes nothing either, and golangci-lint's `nolintlint` is the place that polices it.

The baseline (`snapshot --write-baseline`, or `init`) accepts every current finding at once. It keys findings by id, so a baseline file older than version 2 fails the run with a message to rewrite it. Version 3, which this build writes, also stores the member files of each import cycle, and a version 2 file still loads. An accepted cycle covers any cycle made only of its members, so a cycle that shrinks never reads as new, and one that gains a file does. Ignored, labeled, and baselined findings are counted in `counts.suppressed` and in one line on stderr, so a muted finding never vanishes without a trace.

When baseline entries stop matching anything, because the finding was fixed or a cycle shrank, `snapshot` counts them in `counts.stale_baseline` and says so in one line on stderr. Run `slop-strata snapshot --write-baseline` again to drop them, so a fixed finding that comes back reads as new. The count never changes the exit status.

## Labeling findings

When a finding is wrong, say so in a labels file instead of editing code around it. Take the id from `snapshot --json`, then record a verdict:

```bash
slop-strata label 3f9a1c20be fp --note "an example name in a doc"
slop-strata label 7d41e5a9c0 tp
slop-strata label --list
slop-strata rules
```

`label` runs every rule, experimental ones included, against `HEAD` (or `--rev`) and fails when the tree does not produce the id. Labeling an id again replaces the earlier verdict. The file is `.slop-strata-labels.json` at the repository root, and `labels` in `.slop-strata.toml` renames it. Each entry holds the rule, verdict, path, matched text, note, and tool version. It does not hold who labeled it. Entries stay sorted, so a new label adds one block to the diff.

A finding labeled `fp` is muted like a baselined one and counted as `labeled_fp`. A finding labeled `tp` stays. `slop-strata rules` lists every rule with its tier and, per rule, the labeled `tp`, `fp`, and precision with its `n`. A precision from fewer than 10 labels carries a `*`.

The labels are evidence about this repository. A rule's precision here says nothing certain about its precision on other code, and the 80% bar that gates a rule is set on a corpus across repositories. slop-strata never moves a rule between the default and experimental tiers from labels. The owner decides that, now with a number beside the choice.

## JSON integration

`change`, `snapshot`, `history`, and `rules` all take `--json`, and that output is the integration surface. Every object is an envelope:

```json
{"result": {...}, "docs": {...}, "tool": "slop-strata", "command": "change", "repo": "...", "at": "...", "counts": {"errors": 0, "skipped": 0, "stale_baseline": 0, "suppressed": {"baseline": 0, "ignored": 0, "labeled_fp": 0}}, "warnings": [], "schema": 5}
```

- `docs` holds the definition of every metric in the result, the same text `--explain` prints
- `counts` says how many files the run could not score, so a consumer never reads an unscored file as zero, and how many findings it set aside
- `--context` adds the hunks, source lines, and commit messages a model would open next. It works with `change --json` only
- `schema` is the envelope version. It goes up on any field change. [docs/schema.json](docs/schema.json) is the JSON Schema for version 5

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
- `labels` names the file of verdicts on findings, which defaults to `.slop-strata-labels.json`
- `entry_points` lists files whose exports the dead-export rule skips, and `config_paths` lists the config files the stale-config rule reads
- `default_branch`, `max_file_bytes`, and `minified_avg_line` change which branch a change diffs against and which files get skipped as oversized or minified. On `history`, a file over `max_file_bytes` still counts as touched, but its lines stay out of the week totals and churn. Lines the walk tracked before the file grew past the cap leave without counting as deaths, and survival censors them at that day. Lines the file holds when it comes back under the cap have no recorded birth, so they stay out of churn and survival until an edit adds new ones

`slop-strata init` writes a starter `.slop-strata.toml` when none exists, with bot logins found in recent history. It then records the current findings as the baseline, so a rule added later reports only new hits. `snapshot --write-baseline` records the baseline without touching the config.

## Performance

These are one run each on an Apple M4 Pro with 12 cores. A cold run starts with an empty blob cache. I set that up by pointing `HOME` at a fresh directory. A warm run is the next run against that same cache.

| Repository  | Files | Command              | Cold   | Warm   | Peak memory (cold) |
| ----------- | ----- | -------------------- | ------ | ------ | ------------------ |
| calcipy     | 142   | `snapshot`           | 0.13 s | 0.06 s | 30 MB              |
| aragonite   | 169   | `snapshot`           | 0.35 s | 0.11 s | 44 MB              |
| second-look | 590   | `snapshot`           | 0.63 s | 0.18 s | 71 MB              |
| calcipy     | 142   | `history` (365 days) | 0.22 s | 0.08 s | 59 MB              |
| second-look | 590   | `history` (365 days) | 7.91 s | 0.49 s | 275 MB             |

Parsing runs one worker per CPU core (GOMAXPROCS), with one parser per worker. A warm `snapshot` skips parsing for every blob it has already seen. A warm `history` also skips reading any blob the cache already answers, so what remains is `git log` and the line tracking. On a private repository with 1,529 first-parent commits in three weeks, `history` took 64 s cold and 4.5 s warm, and 2,979 commits over six weeks took 142 s cold and 9.7 s warm. On a terminal, a `history` run past two seconds prints a commits done/total line to stderr.

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

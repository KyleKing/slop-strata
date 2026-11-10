# Worked example: calcipy

This walks through one run of slop-strata on [KyleKing/calcipy](https://github.com/KyleKing/calcipy), a Python project with one main author and five first-parent commits that carry a coding agent's co-author trailer.

I picked it over [KyleKing/second-look](https://github.com/KyleKing/second-look) for one reason. calcipy has an agent class to compare with the person class. second-look has more history and the same kinds of tables, but none of its commits carry an agent trailer, so there is no agent class to compare.

Everything here is pinned. Run the same commands at the same revision and you get the same numbers.

## Pinned inputs

- Repository: [KyleKing/calcipy](https://github.com/KyleKing/calcipy)
- Revision: [594d31bcf9321433336f2ef11135bd29a82cef98](https://github.com/KyleKing/calcipy/commit/594d31bcf9321433336f2ef11135bd29a82cef98) on `main`
- Change read in the walk-through: [00b5c3b375856a0387ef4a149fc53f918d9ef259](https://github.com/KyleKing/calcipy/commit/00b5c3b375856a0387ef4a149fc53f918d9ef259), `refactor: support jj repos and run from anywhere`
- History start: 2025-11-09, an absolute date, so the window does not move as time passes

## Commands

```bash
git clone https://github.com/KyleKing/calcipy && cd calcipy
git checkout 594d31bcf9321433336f2ef11135bd29a82cef98

slop-strata history --rev 594d31bcf9321433336f2ef11135bd29a82cef98 --since 2025-11-09
slop-strata snapshot --rev 594d31bcf9321433336f2ef11135bd29a82cef98
slop-strata change --rev 00b5c3b375856a0387ef4a149fc53f918d9ef259 --base 00b5c3b375856a0387ef4a149fc53f918d9ef259^
slop-strata history --rev 594d31bcf9321433336f2ef11135bd29a82cef98 --since 2025-11-09 --json \
  | jq -c '.result.pairing[] | select(.week == "2026-06-29" and .dir == "calcipy")'
slop-strata --explain erosion
```

The `git checkout` matters. slop-strata reads `.slop-strata.toml` from the working tree, so the checkout should match the revision you are scoring.

## History

The output below is `history` at the pinned revision. Rows the walk-through does not use are cut, and `...` marks each gap.

```text
115 first-parent commits · 2 reverts

week        commits  added  removed  mass
2026-01-12  9        +6779  -1134    +338
2026-01-19  18       +2193  -1691    +308
2026-01-26  38       +2859  -4115    -624
2026-03-16  2        +584   -575     +0~
2026-03-23  10       +127   -113     -5
2026-06-29  8        +793   -172     +9~
2026-07-20  1        +2     -1       +0~
2026-07-27  12       +773   -111     +57
2026-08-24  7        +321   -37      +6~
2026-08-31  10       +672   -904     +2~
~ within one noise floor (7) of the median week

cohort   added  alive  s_30d  s_90d
2026-01  11497  7021   0.69   0.64
2026-03  686    576    0.93   0.93
2026-07  922    553    0.99   —
2026-08  1258   1088   —      —
2026-09  320    315    —      —

test files by class  added  dead<30d  dead<90d  s_30d  s_90d
person               8      1         1         0.83   0.83

hotspot                score
calcipy/markup_writer  6852
tests/experiments      5480
tests                  5475
calcipy/experiments    45996
...
calcipy/tasks          11447

dir                                     added  removed  churn_14d  churn_30d  rework  concentration  mass_8w
.                                       4247   3763     1266       1286       704     0.55           ▁▁▁▁▁▁▁▁
...
.github/workflows                       260    63       20         22         0       0.74           ▁▁▁▁▁▁▁▁
calcipy                                 239    335      135        136        12      0.76           ▁▁▁▁▁▁▁▁
...
calcipy/code_tag_collector              81     347      20         20         0       0.94
calcipy/experiments                     122    442      381        381        10      0.65           ▁▁█▁▁▁▁▂
calcipy/markup_writer                   112    87       32         32         1       0.59           ▁▁▁▁▁▁█▁
calcipy/tasks                           258    126      36         36         3       0.75           ▁▁█▁▁▁▁▁
...
tests/tasks                             771    361      248        248        24      0.72           ▁▁█▁▁▁▃▁
...

rule                 first hit   spread by class
py/mock-only-assert  2026-01-19  person:53
```

### The weekly table

Each row is a week of first-parent merges. `added` and `removed` are lines, and `mass` is the net complexity mass that week, where mass is the sum of CC times the square root of SLOC over each function.

The `~` marks a week whose net mass sits within one noise floor of the median week. Here the floor is 7 mass, so the `+9~` week of June 29 is inside the noise and the `-624` week of January 26 is not. That week had 38 commits, and they removed 4,115 lines, so it is the row to read first.

### The hotspot table

A hotspot score is 30-day churn times the directory's live complexity mass. The table lists directories alphabetically, so read the score column to rank them. The score has no unit you can read on its own.

`calcipy/experiments` scores 45,996, which is 381 churned lines times 120.7 mass. Its `churn_14d` and `churn_30d` are both 381, so every churned line died within 14 days of being written. `calcipy` scores 39,647, which is 136 churned lines times 291.5 mass. The two rank close for different reasons. One has more churn, and the other has more live mass.

The score says where recent edits meet complexity. It does not say whether those edits were wrong, and a directory that is still being built will score high.

### Cohorts and survival

A cohort is the lines added in one calendar month. Survival is the Kaplan-Meier share of those lines still alive at 30 and 90 days.

The January cohort added 11,497 lines. Of those, 7,021 are still alive at the pinned revision. Its `s_30d` is 0.69 and its `s_90d` is 0.64, which reads as: about 69% of January's lines were still there after 30 days, and about 64% after 90. The Greenwood 95% intervals are narrow here, [0.68, 0.70] at 30 days and [0.63, 0.65] at 90 days, because the cohort has 11,497 lines behind it. The JSON carries them as `s30_ci` and `s90_ci`. The text table does not print them.

A dash means too few lines in the cohort have been in the tree as long as the horizon. The July cohort has `s_30d` but no `s_90d`, because no line from July had been in the tree for 90 days by September 3. The August and September cohorts have neither, because too few of their lines had reached 30 days at the pinned revision.

Survival counts a line as dead once it changes, even when the same behavior is kept in a rewritten form. Moved lines keep their age, which is why the moves matter, but a rewrite does not.

### Agent and person

Five first-parent commits carry a `Co-Authored-By` trailer that names a coding agent. The default `agent` class matches Claude, Copilot, Cursor, Codex, Devin, and Aider. All five were committed on July 4, in the week of June 29. Two of them are the Zensical docs migration, and three are fixes to CI, release, and copier guidance.

The split is in the JSON `pairing` rows, which compare author classes in the same week and directory. The text history does not print it. Here is the row for the top-level `calcipy` directory that week:

```json
{"class_mass_net":{"agent":5.175601857662905,"person":0},"class_churn":{},"class_commits":{"agent":1,"person":1},"week":"2026-06-29","dir":"calcipy","provisional":true}
```

The cell holds two commits in total, one from each class, so it is marked `provisional`. The agent commits show net mass of +5.2 in that cell and the person commit shows 0. A commit counts once in every directory it touches, so the class totals across cells add up to more than the 115 commits in the window.

What the split shows is one week of agent work, in a repository where the person class has 110 of the 115 commits. It does not show whether agent work is worse. The test-file table has only a person row, because none of the agent commits added test files.

### Rule spread

`py/mock-only-assert` first hit on January 19. All 53 of its hits come from person commits, and `spread_dirs` is 1, so the pattern has not spread past the directory where it started. The `virality` JSON field carries the same data as `hits_by_class` and `spread_dirs`.

## One change: 00b5c3b

[The refactor commit](https://github.com/KyleKing/calcipy/commit/00b5c3b375856a0387ef4a149fc53f918d9ef259) adds jj support and lets the tasks run from any directory. It touches three files. Here is `change` on it:

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

dir            mass net
calcipy        +35.7
calcipy/tasks  -0.9
tests/tasks    +13.8
```

The reading comes from rules over these numbers, not from a model. Three things in it are worth checking against the diff.

The intent is `refactor`, from the Conventional Commit type. A refactor is expected to move mass around, not add it. This one adds 48.6 net mass. Functions that grew account for 68.6 of mass, and removals elsewhere in the change bring the net down to 48.6.

The reading says it "reads as a refactor that adds mass." That is a question for the reviewer, not a verdict. The largest share of the growth is in `calcipy/invoke_helpers.py`, which went from 15.2 to 50.9 mass. The subject says the change adds jj support, which is the likely reason. Whether that code belongs in a helper module is a judgment the numbers cannot make.

Erosion fell from 0.63 to 0.43, which is the same change read from the other side. Erosion here counts only the files the change touched. The one touched function over CC 10, `collect_code_tags`, is CC 11 on both sides, and `grown over CC 10` is 0.0. The new mass landed in functions under CC 10. The total grew while the over-limit mass did not, so the share dropped.

## One `--explain` excerpt

```text
erosion
Share of complexity mass (CC x sqrt(SLOC) per function) held by functions with cyclomatic complexity over 10, per arXiv 2603.24755. No target; watch direction per directory. When it rises on routine work, the complex functions are getting more of them.
```

The last sentence is the one to use. A single erosion number says little. The direction, per directory and over time, is what the paper's measure is for.

## What the numbers do not tell you

- They describe one repository with one main author. The person class has 110 of 115 commits, and the agent class has five commits in one week.
- Churn is not the same as added lines. `calcipy/experiments` shows 381 churn against 122 added. A rename carries each line's birth day into the new path without adding to `added`, so a line that dies in the renamed directory within 30 days of its original birth counts as churn there. The `churn_30d` text says "lines added in the range," which does not describe that case.
- Survival treats a rewritten line as dead. It does not judge whether the rewrite was an improvement.
- Hotspot scores rank directories against each other. They do not say how much work a directory needs.
- Rule hits are syntax matches. `py/mock-only-assert` reports the pattern, and a person still has to decide whether the test is weak.
- The windows are short. One more agent commit changes the agent row, and a cohort's 90-day figure rests only on the lines old enough to reach it.
- Nothing here says whether the code is good. Each sentence states what the rules saw, and a reviewer decides what to do about it.

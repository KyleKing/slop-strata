# Consolidation dataset

What `x/new-duplicate`, `duplicate_groups`, `split_candidates`, `case_sets`, and `jsx_groups` were designed and measured against: commits in public repositories that replaced repeated code with one function, constant, or type, each read by hand, the pairs of look-alike fragments nobody merged, and the hand labels behind the tables in [DESIGN.md](../../DESIGN.md) sections 8 and 9.

Everything here is data and Python. No Go file lives under `eval/`, so the build, the tests, and the linters do not read it.

## Files

| File | Rows | What it is |
| ---- | ---- | ---------- |
| `events.jsonl` | 153 | One verified consolidation per line, from 142 commits in 20 repositories |
| `rejected.jsonl` | 102 | Reviewed candidates that are not events, with the reason |
| `controls.jsonl` | 393 | Matched negatives: pairs of fragments of like size and shape that were still both there two years later |
| `analysis.txt` | | What `scripts/analyse.py` printed over the three files: the size, similarity, and history figures DESIGN.md quotes |
| `repos.tsv` | 28 | Name, URL, and the commit each clone was at: the 22 that were mined, and six React applications the JSX groups were read on, marked `jsx` in a fourth column |
| `labels/verdicts.tsv` | 255 | The hand verdict on every reviewed candidate: group id, `T` true, `R` rejected, or `U` unclear, kind or reason, note |
| `labels/new-duplicate-first.tsv` | 149 | Hand labels on `x/new-duplicate` findings of the first build, `R` real, `P` plausible, `F` false |
| `labels/new-duplicate-second.tsv` | 83 | The same for the second build, which added expressions and joined stretches |
| `labels/duplicate-groups.tsv` | 60 | Hand labels on the top five duplicate groups of twelve repositories |
| `labels/split-candidates.tsv` | 60 | Hand labels on the top five split candidates of the same twelve, as 0.19 listed them |
| `labels/split-candidates-v2.tsv` | 60 | The same reading of the list after scopes, the exit rule, and the bounds on a part and on both sides were added |
| `labels/case-sets.tsv` | 64 | Hand labels on every case-set family of the 22 repositories |
| `labels/jsx-groups.tsv` | 51 | Hand labels on JSX groups of five React applications |
| `scripts/` | | The mining pipeline and the two replays that need nothing but a build of the CLI |

The data files are byte for byte what the scripts wrote, with two exceptions: one trailing tab was taken off line 11 of `labels/verdicts.tsv`, and the two `new-duplicate` label files were converted from the JSON the labelling tool kept (one object per finding, keyed `repo|rev|path|line`) to one row per finding with every field kept.

## Provenance

All 22 repositories are public. They were cloned on 2026-11-24 and mined over every non-merge commit reachable from the commit below. Two of them, date-fns and zustand, gave no event. The twelve repositories the group and split labels come from (fzf, cobra, chi, bubbletea, flask, httpx, pydantic, rich, vite, trpc, zod, query) were read at the same commits.

| Name | URL | Commit |
| ---- | --- | ------ |
| bubbletea | https://github.com/charmbracelet/bubbletea | `b78ae0e9e27d7965578844178f95348e56e8690b` |
| chi | https://github.com/go-chi/chi | `c64ed7d6635f33a8b2feaff7dcfa55677023ac7d` |
| click | https://github.com/pallets/click | `08b803cf32ceb2ae6a772c62d5da42757a897d44` |
| cobra | https://github.com/spf13/cobra | `adbc8813901bba65827259daa8e22ff94ec1f30e` |
| date-fns | https://github.com/date-fns/date-fns | `a9739fb3e5384a810e90d7376446f6d84ecc5029` |
| echo | https://github.com/labstack/echo | `59aacddc4a13904b8a80a76f1ec838c495b5feca` |
| flask | https://github.com/pallets/flask | `49943b5a94413a530761350f04e896f76e8d620e` |
| fzf | https://github.com/junegunn/fzf | `ed173e71e44b2a5fa6264ecf232a153dee6994d0` |
| gin | https://github.com/gin-gonic/gin | `0f09c3a9b4626d9fe9979cf4d0521d6ae32ab646` |
| gorm | https://github.com/go-gorm/gorm | `ead4e04ba7c6a49654649131e587133b1d9f3eca` |
| httpx | https://github.com/encode/httpx | `7cff5f79bc78649bbcd3fd9aa7a74c86e0f63016` |
| pydantic | https://github.com/pydantic/pydantic | `2b555b79a603ab7eb27a23a9b1b9598897cdfef8` |
| query | https://github.com/TanStack/query | `115fdefa198170f696b6d040469b7d25ac751906` |
| requests | https://github.com/psf/requests | `3c7baaa05ab4aa49cc819ce3a819d260de19c157` |
| rich | https://github.com/Textualize/rich | `3a109ccdffe5b2d0e30684a304fbd388e99ce57b` |
| starlette | https://github.com/encode/starlette | `63d868e1b89063fa1059dafe239e41f924c33805` |
| swr | https://github.com/vercel/swr | `8148ef012fd5b317d1e8ec2da4b2ca0673f63c0f` |
| trpc | https://github.com/trpc/trpc | `19e84b8efab7a64b39e825601a43f39270fd119a` |
| vite | https://github.com/vitejs/vite | `e16ca5a13d74dddd005763df9d4db9fa01c74a4d` |
| werkzeug | https://github.com/pallets/werkzeug | `0f43c6ebd426039e4b99ec22535c2c8a2ec2f69f` |
| zod | https://github.com/colinhacks/zod | `d40c329910436e528d3c26cb1b9179e05abebf05` |
| zustand | https://github.com/pmndrs/zustand | `5d66d53be80363b94491868b41325ea058fd16bb` |

The 22 are libraries and frameworks, and none lists a JSX group: what markup they hold is in tests and examples. The JSX groups were read on six public React applications instead, cloned on 2026-11-25 at a depth of 300 commits. Nothing was mined from them, `repos.tsv` marks them `jsx`, and `scripts/setup.sh` clones them to `WORK/jsx-repos`, beside the 22 and out of the way of the scripts that walk `WORK/repos`.

| Name | URL | Commit |
| ---- | --- | ------ |
| documenso | https://github.com/documenso/documenso | `e745c9ec339f1b16d2f8d181b6457323fb33ebe5` |
| dub | https://github.com/dubinc/dub | `6b463b4bdd9ed2fd9aa416f67b5b77d317b1471e` |
| excalidraw | https://github.com/excalidraw/excalidraw | `eaa7fa32a6645ae9da57eda548e838d0e2a024a1` |
| formbricks | https://github.com/formbricks/formbricks | `44bc3270ebd9301de6c008ee947e27c069f801c1` |
| plane | https://github.com/makeplane/plane | `723bb178f97b87e80a23df9eed134eb1388d7465` |
| umami | https://github.com/umami-software/umami | `dbb8d606fbd4e99220fb83bcb4abad3e0f97f42a` |

No author identity is stored. The history fields hold one boolean about authorship, `same_author_as_earlier`, and commit subjects are kept as written.

## Fields

`events.jsonl`:

- `gid, repo, lang` (`py`, `ts`, or `go`), `sha, parent, commit_ts` (unix), `subj, nfiles_in_commit`
- `kind` is `extract` (a shared helper, constant, type alias, or component), `parameterise`, `pullup`, or `other`, which the note explains
- `note` is a one-line description by the reader, `confidence` is `clear` or `borderline`, and `partial_diff` is set when the reader saw a truncated diff
- `detector_kinds` and `stratum` say which structural family surfaced the candidate
- `sites[]` are the repeated sites at `parent`: `path`, `qual` (the qualified name of the function or method), `unit_lines`, `region_lines` (the removed region, in parent line numbers), `ntok`, `nstmt`
- `target` is what replaced them: `name, path, lines` (at the commit), `kind, from_detector`. The name was corrected by hand where the detector resolved it wrongly
- `n_sites, ntok_min, ntok_median, ntok_max, nstmt_min, nstmt_median, same_file, same_dir, cross_package, test_only, any_test`
- Similarity of the best pair of sites before the merge: `sim_pair_max_raw` (a sequence-matcher ratio on raw tokens), `sim_pair_max_shape` (the same with identifiers and literals normalised), `sim_pair_max_jacc5` (Jaccard over token 5-grams), `holes_best_pair` (aligned positions where the shape is equal and the identifier or literal differs), `unaligned_best_pair` (tokens outside the alignment), and `sim_best_pair`, the full record
- `hist` comes from `git blame -w -M` at `parent` over each site's region, six sites at most: `birth_spread_days`, `dup_lifetime_days`, `oldest_site_age_days`, `same_birth_commit`, `later_site_after_earlier`, `same_author_as_earlier`, `lockstep_commits`, `solo_edit_commits`, `diverged`, `cochange_commits`, `prior_commits_per_file`, `prior_commits_12m_per_file`. Blame sees only surviving lines, so the edit counts are lower bounds, and a formatting commit can overwrite a birth date
- `message_flags, censored_for_controls, n_controls, sibling_class_methods`

`rejected.jsonl`: `gid, repo, sha, parent, verdict` (`R` or `U`), `reason, note, detector_kinds, stratum, n_sites, subj`. The reasons include `spurious`, `mirror`, `api_change`, `move`, `rename`, `test_rewrite`, `base_class_swap`, `inverse`, and `deprecation_shim`.

`controls.jsonl` has the fields of an event for one pair of regions (`n_sites` is 2) sampled at the event's `parent` in the same repository: statement-aligned body fragments on the event's side of the test split, within 0.6 to 1.6 times the event's median site size, and within 0.15 of its best pair's shape similarity. Eight are `match_quality: relaxed`, at 0.4 to 2.5 times and 0.30. A pair is kept only when the parent is 24 months or more before the clone's commit, both units still exist 24 months later with 80% of the region intact (`still_present_at_followup`), and neither unit is a site of any structural candidate in those 24 months. It also carries `event_gid, holes, unaligned`, and `label: negative`.

`labels/duplicate-groups.tsv` and `labels/split-candidates.tsv`: repository, language, rank in the list, verdict, note. `labels/split-candidates-v2.tsv` adds the file and lines of the block, and `carried` when the first list named the same block and the verdict was copied from it, or `fresh` when the block was read for this file. `labels/new-duplicate-*.tsv` have a header row. `g`, `s`, `p`, and `tok` in the second are the kind of hit (`run`, `join`, `expr`), its score, its parameters, and its tokens, as an instrumented build printed them, and `key_suffix` is `was` on two rows that label a finding as an earlier build worded it.

`labels/case-sets.tsv`: repository, language, rank in the list, verdict, note. A case-set verdict is `yes` (a maintainer would plausibly tie the places together with a table, a type, or an exhaustive match, or at least wants to know they are parallel), `plausible`, or `no`. `labels/new-duplicate-*.tsv` have a header row. `g`, `s`, `p`, and `tok` in the second are the kind of hit (`run`, `join`, `expr`), its score, its parameters, and its tokens, as an instrumented build printed them, and `key_suffix` is `was` on two rows that label a finding as an earlier build worded it.

`labels/jsx-groups.tsv`: repository, rank in the list, verdict, note. A JSX-group verdict is `yes` (a maintainer would plausibly make the block one component, or render it from one list), `plausible` (a real repeat whose extraction is a judgment call: a loading skeleton, a layout a component library dictates, a pair that differs in most of its values), or `no`.

## How the numbers were produced

The mining pipeline, in `scripts/`:

1. `units.py` reads functions, methods, constants, and classes out of a file with tree-sitter, each with its own tokens, comments and nested units left out
2. `detect.py REPO OUT` walks every non-merge commit, skips one that touches more than 30 files or no source file, and compares each unit's tokens at the parent and at the commit. It writes candidates of three kinds: `extract`, `pullup`, and `merge_delete`
3. `group.py` merges the candidates of one commit that share a site, and `mkqueue.py` shuffles them into a review queue per stratum with a fixed seed
4. A person read the queue, with `view2.py` to print each candidate's sites, and wrote `labels/verdicts.tsv`
5. `build_events.py`, `run_hist.py` with `age.py`, `controls.py`, `run_hist_ctl.py`, and `finalize.py` write the three data files, and `analyse.py` writes `analysis.txt`

`scripts/setup.sh WORK` lays out a work directory outside this repository and clones each repository at its commit. The scripts read and write beside themselves (`WORK/data`, `WORK/repos`), which is why they run from a copy. They ran under Python 3.13 with tree-sitter 0.27.0, tree-sitter-go 0.25.0, tree-sitter-python 0.25.0, tree-sitter-typescript 0.23.2. The per-repository candidate files and the grouped candidates are not kept here: they are 18 MB and `detect.py` and `group.py` write them again.

What DESIGN.md quotes, and where it comes from:

- "153 commits in 20 public repositories" and "393 pairs nobody merged" are the line counts of `events.jsonl` and `controls.jsonl`. The size, similarity, and later-site figures (a median site under 25 tokens in 102, a later site written by a newer commit in 68) are in `analysis.txt`
- The duplicate group table (32 real, 16 plausible, 12 false of 60) is a count of `labels/duplicate-groups.tsv`, and the first split table (34 yes, 22 arguable, 4 no) of `labels/split-candidates.tsv`. The lists that were read came from `slop-strata snapshot --experimental --json`, built from what became commit 012ad6e, on the twelve repositories at the commits above
- The case-set tables (23 yes, 10 plausible, 1 no of 34, and 23, 6, 1 of 30) are a count of `labels/case-sets.tsv`: the first 34 rows are every family `slop-strata snapshot --experimental --json` lists on the twelve repositories, and the last 30 every family on the other ten (click, date-fns, echo, gin, gorm, requests, starlette, swr, werkzeug, zustand), all at the commits above. No repository lists more than 16, so the 20 a snapshot prints are all there are. Two guards of the list were chosen after a first build's families on the twelve and on a private repository had been read: a literal written as an argument of a call is not a table, and neither is a TypeScript object of methods. The ten were first run after both, so their 30 rows are the ones no guard was fitted to. The labels from the private repository are not here, and DESIGN.md gives their counts
- The JSX-group table (37 yes, 14 plausible, 0 no of 51) is a count of `labels/jsx-groups.tsv`: all 11 groups `slop-strata snapshot --experimental --json` lists on umami, and ranks 1 to 5 and 13, 14, 17, 19, and 20 of the 20 it lists on documenso, dub, formbricks, and plane, at the commits above. The five ranks past the fifth were drawn once from 6 to 20 with a fixed seed and used for all four. excalidraw has no row: a first build listed 6 groups there, all icons, the guard for drawings was fitted to them, and the shipped build lists none. A second guard, one group for the blocks the same units share, was fitted after dub's list had been read. formbricks and plane were first run after both, so their 20 rows are the ones no guard was fitted to. The labels from the private repository are not here, and DESIGN.md gives their counts
- "A group holds two of the sites the commit merged in 28 of 153" is `scripts/replay_groups.py WORK BIN run --experimental` and then `eval`. It snapshots each event's `parent` and asks whether one group overlaps two of the event's sites. `BIN` was that build with `duplicateLimit` and `splitLimit` in `cmd/slop-strata/refactor.go` set to 1000000, so the JSON carries every rank and not the first 20
- "19 commits whose subject says extract, split, or factor out" is `scripts/extract_commits.py WORK BIN find` and then `check`, with the same binary. `find` lists 36 candidates by subject and diff shape, and a person kept the 19 whose diff moves a block of one function into a new one. Which 19 was not written to a file
- The second split table (42 yes, 13 arguable, 5 no of 60, and 23, 13, 4 of the 40 marked `fresh`) is a count of `labels/split-candidates-v2.tsv`. The lists came from `slop-strata snapshot --experimental --json` built from the tree that file was added in. The 30 private candidates (10 yes, 10 arguable, 10 no) are from a private 20k-file Python/TypeScript monorepo, its top ten and twenty drawn from the rest with a fixed seed, ten Python and ten TypeScript. Their labels are not here because they name its code
- "Why the 26 failed" in DESIGN.md is a reading of the notes in `labels/split-candidates.tsv`: 10 for exits, 10 for a part of a step, 4 for counts, 1 too small, 1 a dispatch arm. "What each rule does" came from one build with a switch per rule, which is not in the tree: 619 functions listed in the twelve repositories before, 603 with scopes alone, 439 with every rule, and 514, 515, and 479 with the exit rule, the floor on both sides, and the bound on a part taken out in turn
- "A candidate in 11 of 36, before in 14" is `scripts/extract_commits.py WORK BIN check` over all 36 candidates `find` lists, once with a build of the commit before these rules and once with them, both with `splitLimit` lifted. A commit counts when a listed function holds a stretch the commit shrank, at its best rank, and the block overlaps when it shares a line with that stretch. The earlier "10 of 14" was over the 19 commits a person kept, which were not written down, so the two counts are not over the same commits
- The `x/new-duplicate` table (20 and 23 of 152 mined commits, 3 and 5 of 393 pairs, 30% of 96 and 37% of 131 labelled findings real) came from replays with a build that carried a debugging subcommand, `zzdev hits`, and printed each hit's score in its note. Neither is in the tree, so those replays cannot be rerun from this directory. The labels they were scored with are here. The 3,750-commit replay is `slop-strata change --pairs FILE --json --experimental` over the last 250 commits of 15 of the repositories, and a label joins a finding on repository, revision, path, and line, and on the path and line of the site it repeats

## Known limits

- Most verdicts were made from a view of the removed regions, the head of the target, and the identifier the sites gained. The full diff was read for a minority
- One person read every candidate, every finding, and every group
- 36 events are less than 24 months old and have no controls, and 27 older ones found no pair that matched. The controls are mostly structural boilerplate, and a pair nobody merged in the 24 months may still be one somebody should have
- The detector finds a consolidation by the code it removes, so the dataset holds none found by repeated conditions, and its precision differs by stratum: 73% of the reviewed resolved extractions were true, and 11% to 21% of the other three
- The guards `duplicate_groups` added (stand-alone directories, tables of values) were chosen on the twelve repositories the group labels come from, so those labels overstate what the list does elsewhere
- The split rules were chosen on the twelve repositories the first split labels come from, and the second list was read by the person who chose them, who knew which rule had kept each candidate. The 70% there overstates the list. The 40 `fresh` rows and the 30 private candidates are the ones no rule was fitted to
- The case-set labels use a wider `yes` than the group labels use `real`: a set of places that are parallel and that nothing ties together counts when a maintainer would want to know of it, whether or not a merge is the answer. Sixteen of the 30 fresh rows are one repository, date-fns, and fourteen of those are one design, a formatter and a parser that each switch over the tokens of one unit
- The JSX-group labels are one reader's on five applications, each read from the first and the last site of a group. They cover the top of each list and a draw from the rest of its first 20, and nothing past rank 20, where DESIGN.md's private sample says more are judgment calls. Nothing was mined for them, so they say nothing of what the list misses

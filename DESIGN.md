# slop-strata

A Go CLI that scores code quality on the change at hand and over a repository's history, for Python, TypeScript, and Go. It starts from SlopCodeBench's erosion and verbosity ([paper](https://arxiv.org/abs/2603.24755)) and adds what those two miss: how long code survives, what a change was trying to do, and whether a bad pattern spreads after it first appears.

Status: v1 in progress. This file is the repository's `DESIGN.md`.

## Why build it

- [scb-check](https://github.com/gabeorlanski/scb-check) is the paper's scorer. It re-parses the whole tree every run, never reads git, aborts the run on one file it cannot tokenize, spends close to an hour on a 5 MB minified bundle unless told to skip it, and reports findings with rule IDs only in its human-readable output
- No OSS tool caches metrics per blob. wily caches per revision and hercules re-walks history in one pipeline, so a per-change delta costs a full pass in both
- Static snapshot scores say nothing about intent. A change that raises erosion while adding a feature and one that lowers it while paying down a hotspot read the same way in every tool below

## Decisions

- Named `slop-strata`. Pure Go. Rules are tree-sitter queries built into the binary. ast-grep YAML packs (scb-check's 137 rules among them) load as an optional external source through `sg`
- Generic, configured per repository: author classes come from bot logins and trailer patterns in config, and every subcommand emits JSON. Any HTML or dashboard is the consuming system's job
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
- A SQLite cache keyed by (blob SHA, path class, digest of the code that produced the payload). Path class is the language and whether the path is a test, since test-only rules read it. Rules that read the file name run outside the cache. A commit or change score sums only the blobs it touched. The file lives under the user cache directory, and CI restores it from a cache keyed by the tool's version
- Files over a size cap or with a minified shape (very long average line) are skipped with a warning naming each one. Generated files (a generator marker in the first five lines, or `linguist-generated` in the root `.gitattributes`) are skipped as generated A file that will not parse gets an error row and the run continues
- Cross-file passes (clones, repeated literals, coupling) run over cached per-blob fingerprints, never by re-parsing

## Metric families, in shipping order

### 1. Structure (snapshot)

- Erosion exactly as the paper defines it: complexity mass is CC × √SLOC per function, and erosion is the share of mass in functions with CC over 10. Kept exact so figures compare with the paper's 473-repository baseline
- Beside it, the distributions erosion hides: p90 and max of function length, nesting depth, parameter count, and cognitive complexity, and the count of functions over 50 lines
- [rust-code-analysis](https://github.com/mozilla/rust-code-analysis) is the reference for metric definitions

### 2. Time (git only, no parsing)

- Churn within 14 and 30 days of a line's addition
- Line survival by change cohort (Kaplan-Meier, as [git-of-theseus](https://github.com/erikbern/git-of-theseus) does). The unit and cohort definitions are pinned and documented, because two 2026 studies of agent code ([arXiv 2601.16809](https://arxiv.org/abs/2601.16809), [arXiv 2604.00917](https://arxiv.org/abs/2604.00917)) disagree on direction
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

### 4. Virality and attribution

- Author class per change from config: bot, agent co-author trailer, or person
- After a rule's first hit in a repository, whether its hit rate rises and in which author class's commits
- Agent changes against person changes merged the same week in the same directories, for every family above

## Reading a result in context

A delta means nothing until the tool knows what the change set out to do, so every result carries its context and a one-line reading.

- Intent comes from the Conventional Commit type, the branch name, and linked issue keywords: `feat`, `fix`, `refactor`, `test`, `chore`, or unknown
- Gross and net are reported apart: mass added, mass removed, and the net. A refactor that removes three high-complexity functions and adds one smaller one is paying down. A feature that adds the same mass is accruing
- Location matters as much as size: whether the change touched a hotspot, a file with low survival, or code another author class wrote
- Each result reads as a sentence before the numbers: "Refactor in a hotspot: removed 2 functions over CC 10, erosion in `pkg/parse` fell from 0.48 to 0.31." Readings come from rules over these fields, never from a model, so the same input always gives the same sentence
- `history` groups changes into these readings, so the question "are we paying down faster than we accrue?" has a direct answer per directory and per author class

## Enriching a result with a model

The tool calls no model. It hands a coding harness what one needs, and the harness writes the prose.

- `--context` adds to the JSON what a reader would open next: the diff hunks behind each delta, the functions that crossed a threshold before and after, the findings with their source lines, the change's commit messages and linked issue text, and the history of the hotspot it touched
- A prompt shipped in the repository (`prompts/explain.md`) takes that JSON and asks for four things: whether the stated intent matches what the numbers show, which one function to look at first and why, whether a rule hit is real or a false positive with the line that decides it, and what a smaller version of the change would look like
- The model's answer is a draft for a person, never stored as a metric. Deterministic readings stay the record, which keeps runs comparable and avoids the self-grading bias that absolute LLM scores show
- Disputed findings a person marks (false positive, or real) go back into a labels file that the rule-authoring loop reads

## Presentation

- `change` prints a short block: the reading, then a table of deltas, then findings by rule. It shows only what moved, and `--all` shows the rest
- `snapshot` prints a per-directory table, ranked by hotspot score
- `history` prints trends per directory as a table with sparklines
- `--json` on every subcommand is the integration surface, and `change --json --pairs FILE` scores many `<base> <rev>` pairs in one process as JSON Lines. The tool renders no HTML
- second-look calls `change` behind the first frame and shows the reading and deltas on the review screen
- Every number is documented where it is printed: `--help` and `--explain <metric>` say what it counts, its threshold, and the behavior it should change, and the JSON carries the same text per field. There is no separate glossary to drift from the code
- Comparisons are paired with confidence intervals and a noise floor from repeat runs. A figure reads provisional below a minimum count, and team figures need at least 5 people
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
- Its own tests run the built binary against pinned public repositories at fixed SHAs and diff golden JSON. Unit tests stay for the counting rules where a fixture cannot show the edge, such as nested functions and decorators
- Each metric is checked against a small human-labeled sample before it ships: does a high reading match code a reviewer would call hard to change? A metric that fails this does not ship

## Evaluation corpus

Public repositories at pinned SHAs, chosen to cover the three languages and both small and large histories:

- [KyleKing/calcipy](https://github.com/KyleKing/calcipy) (Python, small, one author)
- [hukkin/mdformat](https://github.com/hukkin/mdformat) (Python, plugin ecosystem)
- [google/go-github](https://github.com/google/go-github) (Go, large, many contributors)
- [facebook/lexical](https://github.com/facebook/lexical) (TypeScript, large)
- [KyleKing/second-look](https://github.com/KyleKing/second-look) (Go, agent-assisted history)

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
- Stale prose: an identifier in a comment, docstring, or Markdown file that no longer resolves — shipped as `x/stale-prose`. `change` reports only names the change removed, since history shows they existed. A snapshot reports every unresolved name, behind `--experimental`. Still open: a TODO or "remove when" whose forward-pointing ticket is closed (read through `gh` when available)
- Across updates: a symbol whose last reference was removed in a change is reported on that change, which is when the author still remembers it
- Dynamic references (reflection, string-built names, dependency injection) make this unsound, so every finding names why the tool believes it is dead and a baseline file absorbs accepted ones

### Moved code, shared with second-look

second-look's `internal/diff` already finds moved runs of lines, reindented lines, and word-level pairs. slop-strata needs the same answer to keep moved code out of churn and survival. Extract it into aragonite as `diff`, with second-look and slop-strata as its two consumers, then improve it there once:

- Function-level matching from tree-sitter: a function that moved files, or changed only its name or indentation, pairs with its old self ([GumTree](https://github.com/GumTreeDiff/gumtree) and [RefDiff](https://arxiv.org/pdf/1704.01544) are the references)
- Classification per hunk: moved, renamed, reindented, or changed, which second-look draws on the review screen and slop-strata uses to exclude moves from churn

### Polish

What a mature product does that v1 will not, measured against CodeScene and SonarQube:

- Precision first: each rule ships only with a measured false-positive rate on the corpus below a set bar, and a rule that drifts above it is turned off by default
- Every finding explains itself in one screen: the rule, the line that triggered it, a minimal before and after, and how to suppress it inline with a reason
- Stable output: a JSON schema version, finding IDs that survive line moves (keyed by function and rule, not line number), and a changelog entry for every change to a threshold
- Fast first run: a full history of a 3,000-commit repository in minutes on a laptop, with progress and a resumable cache
- Zero-config start: language detection, vendored and generated file detection, and author classes guessed from known bot logins, with `init` writing what it guessed for review
- Trends people trust: a noise floor and minimum counts shown on every chart, so a jump is never a measurement artifact

### Other

- Harness and prompt comparisons: tag changes by agent, model, or skill version from trailers, and compare their families with the same pairing as author classes
- Languages beyond the first three, in order of use across the repositories this targets: Lua (Neovim plugins and mise tools), then shell scripts. Config formats (TOML, YAML, JSON, Pkl) join through the stale-config work above, not as full languages
- A `history` screen to dig from a trend into the changes and deltas behind it, sharing second-look's diff view through aragonite rather than drawing its own
- Halstead and maintainability index, if a labeled sample shows they add anything over the structure family
- A rule-authoring loop: propose a rule from a cluster of findings a person marked, test it against the corpus, and report its hit rate before it ships

## Open

- Size cap and minified-shape thresholds, set from vendored files in the corpus
- Which scb-check rules to port natively first, chosen by hit counts on the corpus

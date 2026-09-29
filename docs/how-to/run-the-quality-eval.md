# Run the output-quality eval

Measure how well the review pipeline actually finds bugs. `make eval` runs the
**shipped** review pipeline against a small labeled corpus of seeded-bug cases
and scores the findings for precision, recall, and severity accuracy.

> [!IMPORTANT]
> The eval invokes the **real** `claude` CLI and **spends tokens**. It needs an
> authenticated Claude Code install and network access. It is deliberately kept
> out of `make test` and CI — it never runs in unit CI. Only the loader, the
> scorer, the baseline comparison and the repo setup are unit-tested (no model
> calls).
>
> `make` runs the eval inside the toolbox container, whose `claude` cannot see
> your host login: export `CLAUDE_CODE_OAUTH_TOKEN` or `ANTHROPIC_API_KEY`
> first, or run it natively with `TOOLBOX=0`. See
> [Build from source](/how-to/build-from-source#run-the-eval-in-the-toolbox).

```bash
# Score every case in the corpus (human-readable table)
make eval

# Score one case
make eval EVAL_ARGS="-case sql-injection"

# Also run the adversarial (thorough) pass
make eval EVAL_ARGS=-thorough

# Also run the domain specialist fan-out
make eval EVAL_ARGS=-specialists

# Run every case three times and pool the scores
make eval EVAL_ARGS="-runs 3"

# Emit machine-readable JSON
make eval EVAL_ARGS=-json
```

`make eval` is a thin wrapper over the dev-only binary `cmd/planwerk-eval`; run
it directly for the full flag set (`go run ./cmd/planwerk-eval -h`):

- `-thorough` adds the adversarial pass, like `review --thorough`.
- `-specialists` adds the domain specialist fan-out, like
  `review --specialists`. It is off by default.
- `-runs N` (default 1) runs each case N times, each run with a fresh Claude
  client, and pools the tallies of all runs.
- `-baseline <path>` compares the run against a report written by `-json`; see
  [Compare a change against a baseline](#compare-a-change-against-a-baseline).
- `-case <name>` runs one case, `-json` emits the report as JSON, and
  `-corpus <dir>` points at another corpus.

The Claude model, effort, and timeout come from the same `PLANWERK_*`
environment overrides the main CLI honors (`PLANWERK_CLAUDE_MODEL`,
`PLANWERK_CLAUDE_EFFORT`, `PLANWERK_STRUCTURE_MODEL`,
`PLANWERK_STRUCTURE_EFFORT`, `PLANWERK_CLAUDE_TIMEOUT`,
`PLANWERK_CLAUDE_INHERIT_USER_CONFIG`).

## What it measures

For each case the harness builds a throwaway git repo — `base/` committed on
`main`, `head/` overlaid on a feature branch — and runs the review pipeline
against it exactly as `review` would, capturing the JSON report. It then matches
each predicted finding against the case's expected findings and tallies:

- **Precision** = TP / (TP + FP) — of the findings reported, how many were real.
- **Recall** = TP / (TP + FN) — of the seeded bugs, how many were caught.
- **Severity accuracy** = severity matches / TP — of the matched findings, how
  many were labeled with the expected severity.

A predicted finding **matches** an expected one when it is in the same file,
within ±3 lines, and at least one expected keyword appears (case-insensitively)
in the predicted title or problem text.

With `-runs N` every tally is summed over the N runs of a case, so each ratio
is a pooled ratio: three runs of a one-bug case count as three expected
findings.

The harness also credits each expected finding to the passes that saw it. A
merged finding records the passes that reported it in `confirmed_by`
(`review`, `adversarial`, `specialist:<key>`); a finding with no
`confirmed_by` counts as `review`, because provenance is stamped only when a
second pass ran. Every pass named by any prediction that matches the expected
finding is credited once, even when another prediction claimed the match. A
pass's recall is its credited findings over the aggregate TP + FN. One
prediction that matches two nearby expected findings credits its passes for
both, so a pass's recall can exceed the pooled recall.

Each throwaway repo gets a `go.mod` (`module planwerk-eval/corpus`, `go 1.22`)
in its base commit, written by the harness. Technology detection then tags
the repo `go` and the review loads the Go technology patterns a real Go
repository gets. The file is part of the base commit, so it never shows up
among the changed files.

The exit code is non-zero **only** on a harness error (bad corpus, git failure,
pipeline or JSON error) — **never** because the scores are low. A low score is a
signal to read, not a build break. A run in which the adversarial pass or a
specialist fails is a harness error too: the pipeline drops a failed pass and
goes on, so the score would count the lost pass as one that found nothing. A
failed run is retried once, and a second failure ends the eval.

## The corpus layout

The corpus lives under `internal/eval/corpus/`. Each case is a directory:

```
internal/eval/corpus/<case>/
  base/           source tree the review diffs against (committed on main)
  head/           the changed tree overlaid on base (the "PR")
  expected.json   the label: description, clean flag, expected findings
```

Source files are stored with a `.go.txt` suffix rather than `.go`. The suffix
keeps the Go toolchain from compiling the corpus as part of the build (a
directory with no `.go` files is not a package), so `go build ./...` and
`go vet ./...` ignore it. The harness strips the `.txt` when it materializes each
tree into the throwaway repo, so the files land as real `.go`. Any non-`.go.txt`
file (e.g. a `.sql` migration) is copied verbatim.

The corpus holds ten cases, ten seeded findings in all:

| Case | Seeded defect | Domain |
|------|---------------|--------|
| `clean-refactor` | none (the clean case) | false positives |
| `goroutine-leak` | a goroutine that never observes `ctx.Done()` | concurrency |
| `nil-map-append` | a nil map write and a discarded `append` result | correctness |
| `off-by-one` | a tail clamp that drops the last element | correctness |
| `sql-injection` | a query built with `fmt.Sprintf` | security |
| `swallowed-error` | read and parse errors discarded | error handling |
| `irreversible-migration` | a column drop with no down migration | data migration |
| `breaking-response-field` | a renamed JSON field in a handler response | API contract |
| `n-plus-one-query` | one query per order inside a loop | performance |
| `untested-branch` | a new branch without a test | testing |

The last four target the data-migration, api-contract, performance and
testing specialists. The api-contract specialist runs only when a changed path
is a route file, so that case keeps its handler under `handlers/`.

`expected.json` has this shape:

```json
{
  "description": "one line explaining the seeded bug",
  "clean": false,
  "findings": [
    {
      "file": "db.go",
      "line": 11,
      "severity": "BLOCKING",
      "keywords": ["sql injection", "injection"]
    }
  ]
}
```

A **clean** case seeds no bug (`"clean": true`, no findings). It measures false
positives: every finding the pipeline reports on it is an FP, and its recall is
undefined (there is nothing to recall) — the table prints `n/a`.

## Add a case

1. Create `internal/eval/corpus/<name>/base/` with a small, compiling Go tree
   stored as `.go.txt` files. Do not add a `go.mod`: the harness writes one.
2. Create `internal/eval/corpus/<name>/head/` containing only the files that
   change, with the bug seeded (or a behavior-preserving change for a clean
   case).
3. Write `expected.json`. For a non-clean case declare at least one expected
   finding; each finding needs a `file` and at least one `keyword`. Pick
   keywords a reviewer would plausibly use, and anchor `line` at the buggy line
   (the ±3 window absorbs small drift).
4. Verify the corpus loads: `go test ./internal/eval/...` runs the loader and a
   check that the shipped corpus is well-formed — no tokens spent.
5. Score it: `make eval EVAL_ARGS="-case <name>"`.

Keep cases tiny and self-contained: one seeded bug (or a handful of related
ones) per case, minimal surrounding code. Keep the code comments to what the
code does. A comment that names the seeded defect hands it to every pass that
reads the file, and the per-pass credit then no longer shows which pass found
it.

## Read the scores

```
CASE                    CLEAN   TP   FP   FN  PRECISION   RECALL   SEV-ACC
goroutine-leak             no    3    0    0       100%     100%      100%
clean-refactor            yes    0    2    0         0%      n/a       n/a
...
--------------------------------------------------------------------------
AGGREGATE                  no   27    9    3        75%      90%       78%

PRECISION = TP/(TP+FP), RECALL = TP/(TP+FN), SEV-ACC = severity matches/TP.
A clean case seeds no bug: recall is undefined (n/a) and every finding is a false positive.

RUNS: 3 per case (thorough: yes, specialists: yes)

RECALL BY PASS
PASS                          FOUND   RECALL
review                           25      83%
specialist:security               6      20%
...

COST PER RUN
CASE                           CALLS  PROMPT TOKENS  OUTPUT TOKENS   EST USD
goroutine-leak                  14.0         412000          19000     $2.10
clean-refactor                  14.0         412000          19000     $2.10
...
----------------------------------------------------------------------------
AGGREGATE                      140.0        4120000         190000    $21.00
  review                        10.0        1140000          70000     $7.00
  specialist-security           10.0         450000          20000     $2.00
...
```

The aggregate row sums the raw tallies across cases (it does not average the
per-case ratios, which would over-weight small cases). Watch the aggregate over
time; individual cases are noisy because the model is stochastic. `n/a` marks an
undefined ratio (recall on a clean case; any ratio with a zero denominator).

Three sections follow the table:

- `RUNS:` states how many runs each case had and which passes they added.
- `RECALL BY PASS` lists every pass credited with at least one seeded finding:
  how many it found and its recall.
- `COST PER RUN` lists calls, prompt tokens, output tokens and the estimated
  USD per case and for the aggregate, followed by the aggregate's cost per
  pass. Every value is a total divided by the runs. Prompt tokens are input
  plus cache-read plus cache-creation tokens, because caching moves tokens
  between those three counters from run to run.

The two sections spell the pass labels differently. Recall uses the
provenance labels (`specialist:security`), cost uses the usage labels
(`specialist-security`).

In `-json` output each case carries `found_by` and `usage` (totals over all
runs), and the report carries `runs`, `thorough`, `specialists` and
`recall_by_pass`.

## Compare a change against a baseline

A change is judged against the commit it branches from by one rule (decision
106):

- Both sides ran at least 3 times per case, with the same `-thorough` and
  `-specialists` flags, over the same cases, and at least one case seeds a
  bug. Over clean cases alone no check can fail.
- Pooled recall is not below the baseline's. Recall gets no tolerance.
- Pooled precision and pooled severity accuracy are each at most 10 points
  below the baseline's (with a 1e-9 slack for rounding).
- A metric that is undefined on either side has no floor and holds (`n/a`).
- Cost is printed beside the verdict and never decides it.

The verdict is `HELD` when every check holds and `REGRESSED` otherwise. Record
the baseline, then compare the change against it:

```bash
# On the commit the change branches from
go run ./cmd/planwerk-eval -thorough -specialists -runs 3 -json > /tmp/eval-baseline.json

# On the change
go run ./cmd/planwerk-eval -thorough -specialists -runs 3 -baseline /tmp/eval-baseline.json
```

Use `go run`, not `make eval`, to write the baseline: `make` echoes the recipe
line onto stdout, which corrupts the redirected JSON. Under the toolbox,
export the credential, open one shell with `make toolbox-shell`, and run both
commands in it. The container is removed when the shell exits, so a baseline
in its `/tmp` does not survive into a second `make` call.

The harness loads the baseline and checks the runs, flags and cases before the
first case runs, so a mismatch exits 1 without spending tokens. A baseline
from before the harness recorded its runs is rejected with `records no runs`.
After the table, the comparison prints the verdict, one row per check, the
recall of each pass on both sides, and both sides' cost per run:

```
VERDICT: HELD

METRIC                         BASELINE  CANDIDATE      FLOOR  RESULT
recall                            90.0%      93.3%      90.0%  held
precision                         75.0%      71.8%      65.0%  held
severity_accuracy                 77.8%      78.6%      67.8%  held

PASS RECALL                    BASELINE  CANDIDATE       DELTA
review                            83.3%      86.7%    +3.3 pts
specialist:testing                10.0%       6.7%    -3.3 pts

COST PER RUN                       BASELINE      CANDIDATE    CHANGE
prompt tokens                       4120000        3570000    -13.3%
output tokens                        190000         190000     +0.0%
est USD                              $21.00         $18.65    -11.2%
```

With `-json` the same data is in the report's `comparison` field. A
`REGRESSED` verdict does not change the exit code (decision 59): it is for a
person to read before the change lands.

> [!TIP]
> A behavioral prompt change — anything that alters what the reviewer flags or
> how it is graded — should ship with a baseline comparison in the PR
> description, so a precision or recall regression is visible rather than
> discovered in production. Run the recipe in
> [Compare a change against a baseline](#compare-a-change-against-a-baseline)
> and paste the verdict, its checks, the pass deltas and the cost lines.

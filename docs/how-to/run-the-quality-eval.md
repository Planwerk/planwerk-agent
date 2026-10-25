# Run the output-quality eval

Measure how well the review pipeline actually finds bugs. `make eval` runs the
**shipped** review pipeline against a small labeled corpus of seeded-bug cases
and scores the findings for precision, recall, F1, and severity accuracy. It
can also score a sample of AACR-Bench, a benchmark of real pull requests; see
[Run against AACR-Bench](#run-against-aacr-bench).

> [!IMPORTANT]
> The eval invokes the **real** `claude` CLI and **spends tokens**. It needs an
> authenticated Claude Code install and network access. It is deliberately kept
> out of `make test` and CI — it never runs in unit CI. Only the loader, the
> scorer, the baseline comparison, the repo setup, and the verifier bench's
> sampling, mapping and tally are unit-tested (no model calls).
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
- `-scope-patterns` runs the review with `review --scope-patterns`. Off by
  default.
- `-runs N` (default 1) runs each case N times, each run with a fresh Claude
  client, and pools the tallies of all runs.
- `-baseline <path>` compares the run against a report written by `-json`; see
  [Compare a change against a baseline](#compare-a-change-against-a-baseline).
- `-case <name>` runs one case, `-json` emits the report as JSON, and
  `-corpus <dir>` points at another corpus.
- `-aacr <export>` scores a sample of AACR-Bench instead of the corpus, and
  `-aacr-sample N` (default 10) sets how many pull requests it holds; see
  [Run against AACR-Bench](#run-against-aacr-bench).
- `-aacr-cache <dir>` sets where the benchmark repositories are fetched to,
  and `-judge` also asks whether each line-overlapping pair raises the same
  concern; see [Run against AACR-Bench](#run-against-aacr-bench).
- `-verifier-bench` measures the claim verifier on AACR-Bench's labeled
  comments instead of scoring the review, with `-verifier-sample N` (default
  20) comments per label; see
  [Measure the claim verifier](#measure-the-claim-verifier).

The Claude model, effort, and timeout come from the same `PLANWERK_*`
environment overrides the main CLI honors (`PLANWERK_CLAUDE_MODEL`,
`PLANWERK_CLAUDE_EFFORT`, `PLANWERK_STRUCTURE_MODEL`,
`PLANWERK_STRUCTURE_EFFORT`, `PLANWERK_FINDER_MODEL`,
`PLANWERK_FINDER_EFFORT`, `PLANWERK_FINDER_TOOLS`, `PLANWERK_CLAUDE_TIMEOUT`,
`PLANWERK_CLAUDE_INHERIT_USER_CONFIG`, `PLANWERK_DIFF_EXCLUSIONS`), and the
two switches take the values the CLI takes: `1`, `true`, `yes`, `on`, `0`,
`false`, `no`, `off`, in any letter case. An effort that is not one of `low`,
`medium`, `high`, `xhigh`, `max`, a tool list that
[`--finder-tools`](/reference/cli#global-flags) would refuse, or a switch set
to any other value exits 1 before any case runs. The corpus holds no lock,
vendored or generated file, so `PLANWERK_DIFF_EXCLUSIONS` leaves a corpus run
unchanged. An AACR-Bench pull request can change such a file, and the variable
then leaves it out as it does on a real review.

## What it measures

For each case the harness builds a throwaway git repo — `base/` committed on
`main`, `head/` overlaid on a feature branch — and runs the review pipeline
against it exactly as `review` would, capturing the JSON report. It then matches
each predicted finding against the case's expected findings and tallies:

- **Precision** = TP / (TP + FP) — of the findings reported, how many were real.
- **Recall** = TP / (TP + FN) — of the seeded bugs, how many were caught.
- **F1** = 2·TP / (2·TP + FP + FN) — the harmonic mean of precision and
  recall, the single number benchmarks such as AACR-Bench publish. It is
  undefined where recall is, on a clean case, and 0 on a seeded case where
  nothing matched.
- **Severity accuracy** = severity matches / TP — of the matched findings, how
  many were labeled with the expected severity.

A predicted finding **matches** an expected one when it is in the same file,
its line range comes within 3 lines of the expected range, and at least one
expected keyword appears (case-insensitively) in the predicted title or
problem text. A range runs from `line` to `line_end`, and a finding without
`line_end` covers its `line` alone, so for two single lines the window is ±3.
A prediction spanning lines 5 to 20 matches an expected finding anywhere from
line 2 to line 23.

Each expected finding claims at most one prediction, and each prediction is
claimed at most once. The harness pairs them so that as many expected findings
as possible are matched. A wide prediction that matches two seeded bugs, next
to a narrower one that matches only the first, is paired with the second, so
both bugs count. TP, FP and FN therefore do not depend on the order the
findings come in. Where an expected finding has several matches, one with the
expected severity is tried first. The report names this rule in `match`
(`range-overlap`).

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

Each corpus repo gets a `go.mod` (`module planwerk-eval/corpus`, `go 1.22`)
in its base commit, written by the harness. Technology detection then tags
the repo `go` and the review loads the Go technology patterns a real Go
repository gets. The file is part of the base commit, so it never shows up
among the changed files. An AACR-Bench repository is a real one and gets
none.

The exit code is non-zero **only** on a harness error (bad corpus, git failure,
pipeline or JSON error) — **never** because the scores are low. A low score is a
signal to read, not a build break. A run in which the adversarial pass or a
specialist fails is a harness error too: the pipeline drops a failed pass and
goes on, so the score would count the lost pass as one that found nothing. The
harness reads the failure from the `passes` array of the review's JSON output
([Pass Status](/reference/output-format#pass-status)), which also covers a
failed feature-compliance check. A failed run is retried once, and a second
failure ends the eval.

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

A seeded bug that spans several lines may add `line_end`, the last line of
its range; it must not be before `line`.

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
   keywords a reviewer would plausibly use, anchor `line` at the buggy line,
   and add `line_end` when the defect spans several lines (the ±3 window
   absorbs small drift).
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
CASE                    CLEAN   TP   FP   FN  PRECISION   RECALL    F1   SEV-ACC
goroutine-leak             no    3    0    0       100%     100%  100%      100%
clean-refactor            yes    0    2    0         0%      n/a   n/a       n/a
...
--------------------------------------------------------------------------------
AGGREGATE                  no   27    9    3        75%      90%   82%       78%

PRECISION = TP/(TP+FP), RECALL = TP/(TP+FN), SEV-ACC = severity matches/TP.
F1 = 2TP/(2TP+FP+FN), the harmonic mean of PRECISION and RECALL.
A clean case seeds no bug: recall and F1 are undefined (n/a) and every finding is a false positive.

RUNS: 3 per case (thorough: yes, specialists: yes)
TIERS: claude opus/xhigh, finder opus/xhigh, structure sonnet/xhigh

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
undefined ratio (recall and F1 on a clean case; any ratio with a zero
denominator).

Four sections follow the table:

- `RUNS:` states how many runs each case had and which passes they added,
  and ends with `scope-patterns: yes` for a `-scope-patterns` run.
- `TIERS:` names the model and effort the main, finder and structure tiers
  resolved to, so a report says what it measured.
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

In `-json` output each case and the aggregate carry `precision`, `recall`,
`f1` and `severity_accuracy` (null where undefined), `found_by` and `usage`
(totals over all runs), and the report carries `runs`, `thorough`,
`specialists`, `scope_patterns` (present when set), `tiers`, `match` and
`recall_by_pass`. `tiers` holds `claude_model`, `claude_effort`,
`finder_model`, `finder_effort`, `structure_model` and `structure_effort`, and
`finder_tools` when `PLANWERK_FINDER_TOOLS` is set. Each `usage.passes` entry
also carries `num_turns`, `permission_denials`, `tool_uses` and `session_ids`
(see [Claude Usage Totals](/reference/output-format#claude-usage-totals)).

## Run against AACR-Bench

The corpus says whether a change loses a seeded bug in a small Go tree, not
how the review fares on real code in other languages.
[AACR-Bench](https://huggingface.co/datasets/Alibaba-Aone/aacr-bench)
(Apache-2.0, paper [arXiv 2601.19494](https://arxiv.org/abs/2601.19494)) holds
2,145 review comments on 200 pull requests from 50 repositories in 10
languages, 1,505 of them verified by experts. `-aacr` scores a sample of it
instead of the corpus. The repository never vendors the dataset: download it
yourself.

> [!IMPORTANT]
> Every sampled pull request is a full review of a real repository. Expect a
> multiple of a corpus case's cost per run, plus network traffic and gigabytes
> of disk for the shallow clones. Start with `-aacr-sample 2`. The benchmark
> never runs in `make test` or CI.

Fetch the export at a pinned revision and check its hash:

```bash
curl -L -o ~/aacr-bench.json \
  https://huggingface.co/datasets/Alibaba-Aone/aacr-bench/resolve/47be1d6df1e7faf222cf531587772d92f79fe6b2/dataset.json
sha256sum ~/aacr-bench.json
# 0804505f0a474765ce2840c832cfeaa6c4f0250dd6ccb169fe73c6758b245a86
```

Then score a sample:

```bash
# Ten pull requests, matched on lines
go run ./cmd/planwerk-eval -aacr ~/aacr-bench.json -aacr-sample 10

# Also judge every line-overlapping pair
go run ./cmd/planwerk-eval -aacr ~/aacr-bench.json -aacr-sample 10 -judge

# With the adversarial pass and the specialists, as JSON
go run ./cmd/planwerk-eval -aacr ~/aacr-bench.json -thorough -specialists -json

# One pull request, named <repo>-<number>
go run ./cmd/planwerk-eval -aacr ~/aacr-bench.json -case uv-12941
```

`-case` finds any candidate of the export, sampled or not, so `-aacr-sample`
beside it exits 1 before anything runs. So do `-aacr-sample`, `-aacr-cache`
and `-judge` without `-aacr`, which a corpus run would ignore.

> [!NOTE]
> Under the toolbox, the export and the cache must sit on a path the container
> mounts. Put the export in the checkout without committing it and pass
> `-aacr-cache /cache/planwerk-eval-aacr`, the toolbox's cache volume, or run
> natively with `TOOLBOX=0`. The default cache lives in the container's home
> directory and goes away with it.

### How a case is built

- Every pull request with at least one verified (`label` 1) comment on the
  right side becomes a case; 196 of the 200 have one.
- The export names the commits the reverse of GitHub's wording:
  `pr_source_commit` is the base the pull request was opened against, and
  `pr_target_commit` is the revision under review.
- Before the first session starts, the harness fetches both commits into a
  bare repository of their own under the cache (`planwerk-eval/aacr` in the
  user cache directory, or `-aacr-cache`), at increasing depths until their
  merge base appears. A later run reuses the repository without the network.
  A pull request that cannot be fetched stops the eval before a token is
  spent, and the error names every such pull request.
- Each run reviews a fresh repository whose `main` and `origin/main` carry the
  merge base's tree and whose `eval/head` carries the revision's tree. The
  review therefore sees exactly the revision's diff, even when the base has
  moved on since. The pull request has an empty title, technology detection
  runs on the real repository, and no `go.mod` is written. As for a corpus
  case, the review prompt lists the revision's changed files and the
  review's JSON output records which of them the session reported reading
  (decision 138).

### Sampling

`-aacr-sample N` draws N pull requests deterministically. It rotates over the
languages and, for each pull request, over the context most of its verified
comments carry (`Diff Level`, `File Level` or `Repo Level`). Both are taken
in sorted order and counted from 0: in round r, language i takes a pull
request of context i + r, wrapping around the three, and falls through to its
next context when it has none there. Within one language and context the
order is fixed by a hash of the pull request URL. With today's export, a
sample of 10 holds one pull request per language.

### Matching

Only verified right-side comments are expected findings. Verified left-side
comments sit on deleted lines no finding can cite; the report counts them as
`left_excluded` and does not score them. A prediction matches a comment when
it names the same file and its line range shares a line with the comment's
range, with no tolerance and no keyword: the benchmark's Line Correct. Each
comment claims at most one prediction, as in the corpus.

`-judge` adds one structure-tier call per line-overlapping pair. It asks
whether the finding and the comment raise the same concern, and only a yes
matches: the benchmark's Semantically Correct. A reply that gives no verdict
is a failed judgement, not a no, and a run's judging is retried once like a
failed run. The judge runs on the structure tier, so
`PLANWERK_STRUCTURE_MODEL` and `PLANWERK_STRUCTURE_EFFORT` move it together
with the review's structuring. Severity accuracy is `n/a`, because no comment
names a severity.

### Read the benchmark report

The ground truth is incomplete: a finding no annotator wrote counts as a
false positive, so precision is a lower bound. Recall is a lower bound too,
because several verified comments can share lines while one finding claims
only one of them. The table's footer says so, and three lines follow the
`RUNS:` line or close the report:

```
RUNS: 1 per case (thorough: no, specialists: no)
AACR-BENCH: 10 of 196 pull requests from aacr-bench.json (sha256 0804505f0a47), match: line+judge (range overlap and a same-concern judgement)
LEFT SIDE: 2 verified comments on deleted lines not scored
TIERS: claude opus/xhigh, finder opus/xhigh, structure sonnet/xhigh
...
JUDGE: 41 pairs, 17 same concern; 41 calls, 820000 prompt tokens, 9000 output tokens, $1.20 (total)
```

In `-json` output the report carries `match` (`line` or `line+judge`), `aacr`
(`export`, `sha256`, `sample`, `candidates`) and, under `-judge`, `judge`
(`pairs`, `same`, `usage`). Each case adds `url`, `language`, `context` and
`left_excluded`, and the aggregate sums `left_excluded`.

A benchmark run is never the gate of decision 106: `-baseline` refuses an
AACR-Bench report on either side. Compare two benchmark reports by eye, and
only when their `sha256`, `sample` and `match` agree.

## Measure the claim verifier

Real runs record what the claim verifier did in `gates.claim`: how many
findings it was sent and how many it refuted. A real finding carries no
label, so that ratio cannot say whether a refuted finding was wrong.
AACR-Bench can: experts confirmed 1,505 of its comments (`label` 1) and
rejected 640 (`label` 0). `-verifier-bench` sends the verifier a sample of
both and reports how often it refutes each. It scores no review.

```bash
# 20 comments per label, as a table
go run ./cmd/planwerk-eval -verifier-bench -aacr ~/aacr-bench.json

# 50 per label, as JSON
go run ./cmd/planwerk-eval -verifier-bench -aacr ~/aacr-bench.json -verifier-sample 50 -json > /tmp/verifier.json
```

Download the export as [Run against AACR-Bench](#run-against-aacr-bench)
describes.

> [!NOTE]
> Under the toolbox, the export and the cache must sit on a path the container
> mounts, as for `-aacr`: put the export in the checkout without committing it
> and pass `-aacr-cache /cache/planwerk-eval-aacr`.

### What the bench sends

- **The draw.** Only right-side comments with a valid line range in the
  `Code Defect` or `Security Vulnerability` category are drawn. Those are the
  claims the severity ladder rates BLOCKING or CRITICAL, the only ones the
  claim gate sends. Today's export holds 332 of them with label 0 and 757 with
  label 1, on 189 pull requests. Each label's comments are ordered by a
  SHA-256 of their pull request, path, lines and text, and the first N are
  drawn, so a rerun draws the same comments and a larger sample contains the
  smaller.
- **The finding.** Each comment becomes a CRITICAL finding: its first line is
  the title, the comment is the problem, and its lines at `pr_target_commit`
  are the quoted code. `pr_target_commit` is the revision the comment was made
  on, and `pr_source_commit` is the pull request's base. The label never
  reaches the verifier.
- **The checkout.** Each pull request's revision is checked out once, from the
  cache `-aacr` fills (`-aacr-cache`). A revision the cache lacks is fetched
  by SHA at depth 1, so the first run needs git and network.
- **The calls.** A pull request's comments go to the verifier at most five per
  call, on the finder tier: `PLANWERK_FINDER_MODEL` and
  `PLANWERK_FINDER_EFFORT` move it. The bench prints the number of calls
  before the first checkout. The default sample makes 32 calls on today's
  export, each a finder-tier session on a real repository.

### Read the verifier report

```
LABEL               SENT  CONFIRMED  REFUTED  UNVERIFIABLE  NO-VERDICT  REFUTED%  UNVERIFIABLE%
0 expert-rejected     19          9        6             3           1       32%            16%
1 expert-confirmed    20         16        2             2           0       10%            10%
-----------------------------------------------------------------------------------------------
ALL                   39         25        8             5           1       21%            13%

REFUTED% = refuted/sent: the share the gate would demote. UNVERIFIABLE% = unverifiable/sent.
A verifier that tells the labels apart refutes label 0 far more often than label 1.

SAMPLE: 20 per label; 1056 rows not eligible; 1 skipped
  SKIPPED https://github.com/acme/widget/pull/41 src/cache.rs:88-91 (label 0): lines 88-91 past the end of src/cache.rs (84 lines)
TIERS: claude opus/xhigh, finder opus/xhigh, structure sonnet/xhigh (the verifier runs on the finder tier)
COST: 32 calls, 4700000 prompt tokens, 160000 output tokens, $24.10
```

- `REFUTED%` is the share of the comments sent that the gate would demote. A
  verifier that tells the labels apart refutes label 0 far more often than
  label 1; equal rates mean it does not.
- `REFUTED%` on label 1 is the rate at which the gate would demote a correct
  finding.
- `UNVERIFIABLE` counts the comments the verifier said the checkout cannot
  settle. `NO-VERDICT` counts those it returned nothing for, the case in which
  the gate fails open. A verdict outside the three counts as confirmed, as in
  the gate: both classify a verdict with the same function. A comment the
  verifier answers twice keeps the outcome the gate would act on, so a
  refutation stands.
- Experts also reject comments that are true but not worth raising, so even a
  perfect verifier would not refute every label-0 comment.

A revision that cannot be fetched, or lines its file does not have, skip the
comment: the report lists it under `SKIPPED` with the reason, and `SENT` can
fall below the sample. A failed verifier call is retried once, and a second
failure exits 1. Low rates never change the exit code. `-baseline`, `-case`,
`-corpus`, `-runs`, `-thorough`, `-specialists`, `-aacr-sample`, `-judge` and
`-scope-patterns` shape review runs, which the bench does not make: each
exits 1 before anything runs. So does `-verifier-sample` without
`-verifier-bench`, which a review run would ignore.

In `-json` output the report carries `per_label`, `excluded` (the rows not
eligible), `tiers`, `labels[]` and `all`, `skipped[]`, `samples[]` and
`usage`. Each entry of `labels[]` holds `label`, `sent`, `confirmed`,
`refuted`, `unverifiable`, `no_verdict`, `refute_rate` and
`unverifiable_rate`, and `all` the same counts over both labels; a rate is
null when nothing was sent. `skipped[]` holds `pr_url`, `file`, `line_start`,
`line_end`, `label` and `reason`. `samples[]` holds every comment sent, with
`pr_url`, `commit`, `file`, `line_start`, `line_end`, `label`, `category`,
`context`, its `outcome` (`confirmed`, `refuted`, `unverifiable` or
`no-verdict`), and the verifier's `verdict`, `evidence` and `reason`.

## Compare a change against a baseline

A change is judged against the commit it branches from by one rule (decision
106):

- Both sides ran at least 3 times per case, with the same `-thorough` and
  `-specialists` flags, over the same cases, scored by the same match rule,
  and at least one case seeds a bug. Over clean cases alone no check can
  fail. Neither side is an AACR-Bench run.
- Pooled recall is not below the baseline's. Recall gets no tolerance.
- Pooled precision and pooled severity accuracy are each at most 10 points
  below the baseline's (with a 1e-9 slack for rounding).
- A metric that is undefined on either side has no floor and holds (`n/a`).
- Pooled F1 is printed beside the checks as `not judged` and never decides
  the verdict (decision 146).
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

The harness loads the baseline and checks the runs, flags, match rule and
cases before the first case runs, so a mismatch exits 1 without spending
tokens. A baseline from before the harness recorded its runs is rejected with
`records no runs`, and one scored before the match rule was recorded, or by
another rule, with `match rule`.

A change to the match rule leaves every earlier baseline incomparable. To
judge a pipeline change that lands together with one, record the baseline on
the base commit with the scoring commit cherry-picked onto it.

After the table, the comparison prints the verdict, the tiers each side ran
on, one row per check, an `f1` row the verdict never reads, the recall of each
pass on both sides, and both sides' cost per run. A baseline written before
the tiers were recorded prints `(unrecorded)` on its tier line.

```
VERDICT: HELD
BASELINE TIERS                claude opus/xhigh, finder opus/xhigh, structure sonnet/xhigh
CANDIDATE TIERS               claude opus/xhigh, finder opus/xhigh, structure sonnet/xhigh

METRIC                         BASELINE  CANDIDATE      FLOOR  RESULT
recall                            90.0%      93.3%      90.0%  held
precision                         75.0%      71.8%      65.0%  held
severity_accuracy                 77.8%      78.6%      67.8%  held
f1                                81.8%      81.2%        n/a  not judged

PASS RECALL                    BASELINE  CANDIDATE       DELTA
review                            83.3%      86.7%    +3.3 pts
specialist:testing                10.0%       6.7%    -3.3 pts

COST PER RUN                       BASELINE      CANDIDATE    CHANGE
prompt tokens                       4120000        3570000    -13.3%
output tokens                        190000         190000     +0.0%
est USD                              $21.00         $18.65    -11.2%
```

With `-json` the same data is in the report's `comparison` field, the tiers
under `baseline_tiers` and `candidate_tiers` and both sides' F1 under `f1`
(`baseline`, `candidate`). A `REGRESSED` verdict does not change the exit code
(decision 59): it is for a person to read before the change lands.

> [!TIP]
> A behavioral prompt change — anything that alters what the reviewer flags or
> how it is graded — should ship with a baseline comparison in the PR
> description, so a precision or recall regression is visible rather than
> discovered in production. Run the recipe in
> [Compare a change against a baseline](#compare-a-change-against-a-baseline)
> and paste the verdict, its checks, the f1 row, the pass deltas and the cost
> lines.

## Try a candidate finder tier

The read-only finder passes share one tier, set by `PLANWERK_FINDER_MODEL`
and `PLANWERK_FINDER_EFFORT`. The corpus runs three of them: the adversarial
pass, the domain specialists and claim verification. The coverage map, the
feature-compliance check, the simplify finder and the implementation verifier
run on the same tier, but no case measures them. `-verifier-bench` measures
claim verification alone, so a candidate finder tier can be checked against
its refute rates as well; see
[Measure the claim verifier](#measure-the-claim-verifier).

To measure whether a cheaper tier keeps the recall, record a baseline with
neither variable set, then compare each candidate against it with one or both
set. Run every side from one build of the same commit, so the tier is the only
variable between the sides.

Try a lower effort before a cheaper model, and stop at the first `REGRESSED`
verdict:

```bash
# Baseline: the shipped default
go run ./cmd/planwerk-eval -thorough -specialists -runs 3 -json > /tmp/eval-baseline.json

# Candidate 1: a lower finder effort
PLANWERK_FINDER_EFFORT=high \
  go run ./cmd/planwerk-eval -thorough -specialists -runs 3 -baseline /tmp/eval-baseline.json

# Candidate 2, a cheaper model, only after candidate 1 printed VERDICT: HELD
PLANWERK_FINDER_MODEL=sonnet \
  go run ./cmd/planwerk-eval -thorough -specialists -runs 3 -baseline /tmp/eval-baseline.json

# Candidate 3, both, only after candidate 2 printed VERDICT: HELD
PLANWERK_FINDER_MODEL=sonnet PLANWERK_FINDER_EFFORT=high \
  go run ./cmd/planwerk-eval -thorough -specialists -runs 3 -baseline /tmp/eval-baseline.json
```

Run the sides natively with `TOOLBOX=0` or all in one `make toolbox-shell`,
which forwards every `PLANWERK_*` variable into the container (see
[Build from source](/how-to/build-from-source#run-the-eval-in-the-toolbox)).
Each side makes 3 runs of every case, so a side takes hours and spends real
tokens.

The comparison accepts a baseline and a candidate that ran on different tiers
by design: a differing finder tier is the experiment. Read the
`BASELINE TIERS` and `CANDIDATE TIERS` lines to check each side. The claude and
structure tiers must match, and the candidate's finder tier must be the one its
command set.

## Try the scoped pattern catalog

`-scope-patterns` runs the review with `review --scope-patterns`, which carries
only the bodies of the patterns that apply to every file or that the changed
files match, and lists the rest as an index
([How a Session Receives the Catalog](/reference/review-patterns#how-a-session-receives-the-catalog)).
The flag stays off until a comparison holds. Record a baseline without it, then
compare a run with it against that baseline:

```bash
# Baseline: without the flag
go run ./cmd/planwerk-eval -thorough -specialists -runs 3 -json > /tmp/eval-baseline.json

# Candidate: the scoped catalog
go run ./cmd/planwerk-eval -thorough -specialists -runs 3 -scope-patterns -baseline /tmp/eval-baseline.json
```

The comparison accepts sides that differ in `-scope-patterns` by design, as it
accepts differing finder tiers, and the candidate's `RUNS:` line shows
`scope-patterns: yes`. The corpus is Go-only, so it exercises the split
little. Nine of its ten cases change a `.go` file, which keeps every body but
Markdown Quality's, so for them the comparison measures mostly the
`<patterns for="…">` grouping. Only `irreversible-migration`, whose head
changes a SQL file alone, moves the Go patterns to the index.

## Try a finder tool set

`PLANWERK_FINDER_TOOLS` names the built-in tools the seven passes that emit
findings load, the review and the audit included; the coverage map and claim
verification keep the default set. Unset, the passes load Claude Code's
default set. What a finder can call decides what it can read, so a smaller set
can cost recall, and the same rule decides as for the finder tier (decision
106).

Record a baseline with the variable unset, then compare a candidate set
against it, for example one that drops `Agent`, `TodoWrite` and `Skill`:

```bash
# Baseline: Claude Code's default tool set
go run ./cmd/planwerk-eval -thorough -specialists -runs 3 -json > /tmp/eval-baseline.json

# Candidate: the tools a finder reads the change with
PLANWERK_FINDER_TOOLS=Bash,Glob,Grep,Read,WebFetch,WebSearch \
  go run ./cmd/planwerk-eval -thorough -specialists -runs 3 \
  -baseline /tmp/eval-baseline.json -json > /tmp/eval-candidate.json
```

Keep `Bash` in any candidate: without it a pass cannot run git or
`brain search`. The candidate's `-json` report carries the comparison: its
`comparison` field holds the verdict, and `candidate_tiers.finder_tools`
names the tools the candidate ran with, while `baseline_tiers` names none. In
each side's report, `tool_uses` per pass shows which tools the finders
actually called, so you can see what a candidate took away before you read
its verdict.

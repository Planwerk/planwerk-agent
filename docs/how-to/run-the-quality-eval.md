# Run the output-quality eval

Measure how well the review pipeline actually finds bugs. `make eval` runs the
**shipped** review pipeline against a small labeled corpus of seeded-bug cases
and scores the findings for precision, recall, and severity accuracy.

> [!IMPORTANT]
> The eval invokes the **real** `claude` CLI and **spends tokens**. It needs an
> authenticated Claude Code install and network access. It is deliberately kept
> out of `make test` and CI — it never runs in unit CI. Only the loader and the
> scorer are unit-tested (no model calls).

```bash
# Score every case in the corpus (human-readable table)
make eval

# Score one case
make eval EVAL_ARGS="-case sql-injection"

# Also run the adversarial (thorough) pass
make eval EVAL_ARGS=-thorough

# Emit machine-readable JSON
make eval EVAL_ARGS=-json
```

`make eval` is a thin wrapper over the dev-only binary `cmd/planwerk-eval`; run
it directly for the full flag set (`go run ./cmd/planwerk-eval -h`). The Claude
model, effort, and timeout come from the same `PLANWERK_*` environment overrides
the main CLI honors (`PLANWERK_CLAUDE_MODEL`, `PLANWERK_CLAUDE_EFFORT`,
`PLANWERK_STRUCTURE_MODEL`, `PLANWERK_STRUCTURE_EFFORT`, `PLANWERK_CLAUDE_TIMEOUT`,
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

The exit code is non-zero **only** on a harness error (bad corpus, git failure,
pipeline or JSON error) — **never** because the scores are low. A low score is a
signal to read, not a build break.

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
file (e.g. `expected.json`) is copied verbatim.

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
   stored as `.go.txt` files.
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
ones) per case, minimal surrounding code.

## Read the scores

```
CASE                    CLEAN   TP   FP   FN  PRECISION   RECALL   SEV-ACC
goroutine-leak            no     1    0    0       100%     100%      100%
clean-refactor           yes     0    1    0         0%      n/a       n/a
...
--------------------------------------------------------------------------
AGGREGATE                 no     5    2    1        71%      83%       80%
```

The aggregate row sums the raw tallies across cases (it does not average the
per-case ratios, which would over-weight small cases). Watch the aggregate over
time; individual cases are noisy because the model is stochastic. `n/a` marks an
undefined ratio (recall on a clean case; any ratio with a zero denominator).

> [!TIP]
> A behavioral prompt change — anything that alters what the reviewer flags or
> how it is graded — should ship with **before/after** eval numbers in the PR
> description, so a precision or recall regression is visible rather than
> discovered in production. Run `make eval` on the base branch and again on the
> change, and paste both aggregate rows.

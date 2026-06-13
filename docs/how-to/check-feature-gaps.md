# Check completed features for gaps

Use `gap-analysis` to compare every Planwerk feature file under
`.planwerk/completed/` in the target repo against the actual codebase and report
incomplete implementations. Useful when you want to verify that "completed"
features really are complete: missing acceptance criteria, scenarios that are
not honored by code, planned tests that were never written, or tasks marked done
whose description is not visible anywhere.

```bash
# Audit every completed feature in the repo
planwerk-review gap-analysis owner/repo

# Single feature by ID
planwerk-review gap-analysis --feature CC-0042 owner/repo

# Single feature by file (path or basename, must be under .planwerk/completed/)
planwerk-review gap-analysis --file CC-0042-thing.json owner/repo

# JSON output for automation
planwerk-review gap-analysis --format json owner/repo

# Walk the gaps interactively and create GitHub issues for the ones you select
planwerk-review gap-analysis --create-issues owner/repo
```

See the [CLI reference](/reference/cli#gap-analysis) for every flag.

## How it works

1. **Repo Input**: The tool receives a GitHub repository reference (URL or `owner/repo`).
2. **Cache Check**: The default-branch HEAD SHA is fetched first so a hit can short-circuit the clone. The cache key folds in `--feature` and `--file` so a single-feature run never overwrites the full-repo result.
3. **Clone**: On a miss, the repo is cloned locally with a partial filter.
4. **Spec Load**: Every `.json` under `.planwerk/completed/` is parsed via the existing Planwerk feature loader. `--feature CC-NNNN` filters by `feature_id`; `--file <path>` narrows to a single completed file (paths outside `.planwerk/completed/` are rejected — gap analysis runs only against features the team has declared done).
5. **Pattern Load**: The same pattern catalog used by `audit` / `review` / `propose` is loaded for context, but it is NOT the focus — the spec is.
6. **Claude Gap Analysis**: Claude compares each spec block (stories, requirements + scenarios, planned test specifications, completed tasks) against the actual codebase and reports four gap types: `missing_criterion`, `missing_scenario`, `missing_test`, and `missing_task`. Severity is mapped from the requirement priority (critical → CRITICAL, high/medium → WARNING, low → INFO; default WARNING). `BLOCKING` is never used because the work is already merged.
7. **Structuring**: A second Claude call converts the report into strict JSON grouped by `feature_id`, with one bucket per analyzed feature. Features the model omitted are surfaced with an empty `gaps` array so users see what was checked even when nothing is wrong.
8. **Output**: Gaps are rendered as a Markdown table plus per-feature detail sections (default), or as JSON. With `--create-issues`, the same interactive flow used by `audit` and `propose` walks each gap, dedupes against existing GitHub issues by title, and posts the model's `suggested_issue` (title + body) verbatim once the user confirms.

`--feature` and `--file` may be combined as a sanity check; if the file's
`feature_id` does not match `--feature`, the run aborts before invoking Claude.

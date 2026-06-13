# Elaborate an issue

Take a high-level GitHub issue (typically the output of `propose` or `audit`)
and expand it into a deeply detailed engineering plan grounded in the actual
repository state — the kind of issue body a senior engineer can pick up and
execute without further clarification (mirrors the structure shown in
[plexsphere/plexsphere#10](https://github.com/plexsphere/plexsphere/issues/10):
Description with concrete "already exists / this story adds" boundaries,
Motivation, Affected Areas, Acceptance Criteria, Non-Goals, References).

```bash
# Render the elaborated body to stdout
planwerk-review elaborate https://github.com/owner/repo/issues/123

# Short form
planwerk-review elaborate owner/repo#123

# JSON for automation
planwerk-review elaborate --format json owner/repo#123

# Replace the issue body with the elaborated body
planwerk-review elaborate --update-issue owner/repo#123

# Or post the elaboration as a new comment instead
planwerk-review elaborate --post-comment owner/repo#123
```

`--update-issue` and `--post-comment` are mutually exclusive — pick the one that
matches your team's workflow (overwrite the source issue vs. preserve history
and append a follow-up comment). See the
[CLI reference](/reference/cli#elaborate) for every flag.

## How it works

1. **Issue Input**: The tool receives a GitHub issue reference (URL or `owner/repo#number`).
2. **Fetch Issue**: Title, body, URL, and state are fetched via `gh issue view`.
3. **Cache Check**: The default-branch HEAD SHA is resolved via `gh api graphql`. The cache key combines repo + HEAD + issue number + a fingerprint of the issue body, so the cache invalidates automatically when either the repo or the issue is edited.
4. **Clone**: On a cache miss, the repository is cloned locally.
5. **Pattern Load**: The same pattern catalog used by `review` / `audit` / `propose` is loaded, filtered by detected technologies.
6. **Claude Elaboration**: Claude is instructed to walk the repo first, identify what already exists vs. what the issue adds, and emit a detailed plan in six sections (Description with concrete "already exists / this story adds" boundaries, Motivation, Affected Areas, Acceptance Criteria, Non-Goals, References).
7. **Structuring**: A second Claude call converts the elaboration into a strict JSON schema so the final body renders consistently.
8. **Output**: The elaborated body is rendered as Markdown (default) or JSON. With `--update-issue`, the issue body is overwritten; with `--post-comment`, the elaboration is posted as a new comment.

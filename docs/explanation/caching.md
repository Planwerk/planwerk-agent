# Caching model

planwerk-agent caches the result of every expensive Claude analysis on disk so
that re-running a command against an unchanged input is free. The cache is
shared by `review`, `propose`, `audit`, `elaborate`, and `gap-analysis`, and is
keyed by repository plus a content fingerprint plus the flags that affect the
result.

## What the cache key is built from

Each command derives its key from the state that would change the analysis, so
the cache invalidates automatically when that state changes:

- **Review** — the PR HEAD SHA. This avoids repeated reviews of an unchanged PR
  state.
- **Propose** — the default-branch HEAD SHA, resolved via `gh api graphql` (so
  private repos work). Proposals refresh when the repo changes.
- **Audit** — the default-branch HEAD SHA together with the set of flags that
  affect the audit.
- **Gap analysis** — the default-branch HEAD SHA, folded together with
  `--feature` and `--file` so a single-feature run never overwrites the
  full-repo result. The SHA is fetched first so a cache hit can short-circuit
  the clone entirely.
- **Elaborate** — repository plus HEAD plus issue number plus a fingerprint of
  the issue body, so the cache invalidates when either the repo or the issue is
  edited.

Entries are written under the user cache directory. Both `propose` and `audit`
fetch the default-branch HEAD SHA via `git ls-remote` before cloning, so a hit
avoids the clone.

## What every key is scoped to

On top of the per-command state above, every key carries the configuration that
shapes the analysis itself:

- **The Claude tiers** — the resolved `--claude-model`, `--claude-effort`,
  `--structure-model`, `--structure-effort`, `--finder-model` and
  `--finder-effort`. A review by Sonnet and a review by Fable are different
  analyses of the same diff, so they are different entries. Re-running with a
  stronger model gives you that model's findings, not a hit on the weaker run.
- **The pattern sources** — the `--patterns` sources in order, whether
  `--no-repo-patterns` / `--no-local-patterns` suppressed a catalog, and
  `--max-patterns`. The loaded catalog decides what the analysis looks for, so
  adding a rule directory invalidates the entry rather than serving a result
  from a run that never saw those rules.

The tool version is deliberately *not* part of the key: a new release would
otherwise discard every entry, and `--no-cache` is the switch for re-running
against changed prompts.

## Controlling the cache

- `--no-cache` forces a fresh run, ignoring any cached entry.
- `--clear-cache` (with optional `--clear-cache-scope`) wipes cached entries.
- `--cache-max-age` rejects entries older than a given duration (where
  supported).

See the [CLI reference](/reference/cli) for the exact flags on each command.

## Inspecting the cache

The [`cache` subcommand](/reference/cli#cache) gives visibility into what is
stored:

- `cache stats` shows total entries, on-disk size, the age distribution, and a
  per-command breakdown — useful before running `--clear-cache` to decide
  whether you actually need a full wipe.
- `cache inspect <key>` dumps the cached command, `writtenAt`, age, size, and
  the full JSON payload for a single entry, so you can confirm what would be
  reused on the next run without rerunning the analysis.

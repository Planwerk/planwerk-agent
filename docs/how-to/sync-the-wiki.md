# Sync the wiki

A target repository's [GitHub Wiki](/reference/review-patterns#github-wiki) holds
review patterns and project memory that drift as the code changes — paths move,
symbols disappear, and one entry supersedes another. `sync` reconciles that
knowledge against the current code: it flags entries that are **stale** (they
reference code that no longer exists) or **redundant** (duplicated or superseded
by another entry), and — on request — prunes them so the wiki stays worth
reading.

## Report stale and redundant entries (dry run)

The default mode is read-only. It clones the repo and its wiki, runs the analysis
pass, and reports what it found without changing anything:

```bash
planwerk-review sync owner/repo
```

The report lists each flagged entry under a **Stale** or **Redundant** section
with the reason — the concrete missing code reference for a stale entry, or the
superseding entry for a redundant one. Use `--format json` for a machine-readable
report:

```bash
planwerk-review sync owner/repo --format json
```

Nothing is ever deleted in this mode, so it is safe to run on any repo whose wiki
you can read.

## Prune the flagged entries (write phase)

`--prune` (or its alias `--apply`) runs a separate write phase **after** the
report: it deletes the flagged entries on the wiki and pushes. The deletion is
never part of the read-only analysis.

```bash
planwerk-review sync owner/repo --prune
```

The write phase lists exactly what it will delete and asks you to confirm before
touching the wiki. It then clones the wiki fresh, removes only the flagged entries
that still exist — reporting any that already vanished, and noting if the wiki
moved since the analysis — commits, and pushes to the wiki's default branch.

To prune without the prompt (for example in CI), pass `--yes`:

```bash
planwerk-review sync owner/repo --prune --yes
```

Without `--yes`, a non-interactive run (no TTY) refuses to prune rather than
deleting unprompted.

`sync` removes whole entries; it does not edit an entry's contents. Re-author a
page through the wiki UI when you want to revise rather than remove it.

## Scope: whole-entry deletion

`sync` deletes entire wiki pages — a stale `review_patterns/<name>.md` or a
redundant `memory/<name>.md`. It does not rewrite parts of a page. The read-only
analysis and the write phase are deliberately separate: the analysis never
authors content, and the write phase only deletes, so a misleading classification
can never silently rewrite your knowledge.

## Authentication and the GitHub Action

A private wiki is cloned and pushed with your GitHub token, taken from
`gh auth token` and injected through git's environment so it never lands in the
clone's config, git's output, or the process command line. A public wiki clones
anonymously but still needs a token to push.

When you run `sync --prune` from a GitHub Action:

- The wiki must be **initialized** — create at least one page through the
  repository's Wiki tab on github.com before the `.wiki.git` clone exists. A wiki
  that was never initialized is treated as "no wiki" and `sync` reports there is
  nothing to reconcile.
- The job's token needs `contents: write` permission to push to the wiki.
- Make `gh auth token` resolve a token with wiki write access (for example by
  exporting `GH_TOKEN`), the same token the [GitHub Action](/how-to/use-the-github-action)
  uses for its other write operations.

## See also

- [Use the GitHub Wiki](/how-to/use-the-github-wiki) — enable and populate the
  wiki knowledge source `sync` reconciles.
- [Audit a codebase](/how-to/audit-a-codebase) — apply the patterns to the code;
  `sync` keeps the wiki those patterns may live on trustworthy.

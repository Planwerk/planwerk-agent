# Rebase a PR

Rebase a pull request's branch onto its base branch, let Claude resolve any
conflicts semantically instead of blindly picking one side, then analyze the
rebased commits against the upstream changes that landed on the base since the
PR forked.

```bash
# Rebase the PR onto main (default) and report the analysis
planwerk-review rebase owner/repo#123

# Rebase onto a different base branch
planwerk-review rebase --onto develop owner/repo#123

# Preview the plan and the first conflicting commit, change nothing
planwerk-review rebase --dry-run owner/repo#123

# Rebase the current branch's PR in this checkout and publish the result
planwerk-review rebase --local --push
```

See the [CLI reference](/reference/cli#rebase) for the full flag table.

## How it works

1. **Resolve the PR.** Without `--local` the PR head is cloned into a temp dir;
   with `--local` the command operates on the current checkout (see
   [Use local mode](/how-to/use-local-mode)). The dirty-tree gate and `--force`
   apply in local mode.
2. **Fetch the base and pin the fork point.** The base branch (`--onto`,
   default `main`) is fetched fresh, and the PR's original merge-base is
   recorded before any rewrite.
3. **Replay the commits.** Each PR commit is replayed onto the base with
   `git rebase` — individual commits are preserved (no squash), so the analysis
   keeps per-commit granularity.
4. **Resolve conflicts semantically.** On each conflicting commit, Claude is
   given both sides, the replayed commit's intent, and the project's review
   patterns, and produces a resolution that keeps both the commit's intent and
   the upstream change correct — not a blind `ours`/`theirs` pick. The loop
   continues until the rebase completes or `--max-iterations` is hit, in which
   case the rebase is aborted cleanly and the conflicting commit is named.
5. **Analyze the rebased commits.** Each rebased commit is checked against the
   upstream commits that entered the base since the fork point. Claude reports
   per-commit adjustments even where git produced no textual conflict — a
   renamed symbol, a changed signature, a removed helper, a new lint/format
   rule, or a semantic behavior change. By default this is **report-only**
   (consistent with the review-tool identity); the report is also posted back
   on the PR as a comment (`--no-analysis-comment` to skip, `--no-analysis` to
   skip the analysis entirely).
6. **Apply adjustments (opt-in).** With `--apply-adjustments`, the reported
   adjustments are applied as fixup commits folded into the commits they belong
   to, instead of only being reported.
7. **Publish (opt-in).** A rebase rewrites commit SHAs, so publishing requires a
   force-push. This happens **only** with `--push`, which uses
   `git push --force-with-lease` — history is never force-pushed implicitly.

## Render the prompt instead of running

To drive the rebase manually in a Claude Code session you already have open
inside a checkout of the PR, render a self-contained prompt and paste it in:

```bash
planwerk-review rebase --print-bare-prompt owner/repo#123
```

`--print-prompt` instead renders the post-rebase analysis prompt (computed from
the upstream range without performing the rebase), useful for inspection or
piping into other tooling.

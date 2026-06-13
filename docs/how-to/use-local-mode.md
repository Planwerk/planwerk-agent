# Use local mode

By default every repo-facing subcommand (`review`, `fix`, `rebase`,
`implement`, `propose`, `audit`, `gap-analysis`, `review-prepared`,
`elaborate`) performs a fresh `gh repo clone` into a temp directory and deletes
it on exit. The
`--local` flag makes the command operate on the **current working directory**
instead — no clone, and the checkout is left in place when the command exits.

This unlocks three workflows the temp-dir clone blocks:

- **CI**: `actions/checkout` already populates the runner workspace, so a
  second clone of the same repo doubles the cold-start time and network cost.
  `--local` reuses the existing checkout. See
  [Local mode in CI](/how-to/use-the-github-action#local-mode-in-ci).
- **Local-first iteration**: review or fix unpushed commits and experimental
  branches without pushing them first.
- **Post-run inspection**: after `fix` or `implement` finishes, the working
  tree it operated on is still there to `cd` into and inspect — it is never
  `rm`-ed.

## Semantics

- **Reference inference.** The PR/repo reference may be omitted for the
  repo-facing commands: `review`/`fix`/`rebase` infer the PR from the current
  branch (via `gh pr view`); `propose`/`audit`/`gap-analysis`/`review-prepared`
  infer owner/repo from the `origin` remote. `elaborate` and `implement` still
  require their issue reference (you must name the issue) — only the repository
  checkout is taken locally. When a reference **is** given explicitly, its
  owner/repo must match the cwd's `origin`, otherwise the run aborts.
- **Branch left on.** For `review`/`fix`/`rebase` the working tree is switched
  to the PR head via `gh pr checkout` (no restore afterwards). The runner logs
  `working tree left on PR branch` so you know where you landed. `rebase` then
  rewrites that branch in place; pass `--push` to publish it with
  `--force-with-lease`.
- **Dirty-tree gate.** If the working tree has uncommitted changes, `--local`
  asks for confirmation before doing anything. With `--force` it proceeds and
  logs a warning instead. In a non-interactive context (stdin is not a TTY,
  e.g. CI) a dirty tree aborts with an actionable error suggesting `--force` —
  the tool never silently stashes or discards your changes.
- **Never deletes your tree.** The cleanup step that removes a temp-dir clone
  is a no-op in local mode, so there is no code path that can `rm -rf` your
  working directory.
- **`fix` loop.** Each fix iteration fast-forwards the existing checkout with
  `git pull --ff-only` instead of re-cloning, which is materially cheaper and
  produces the same state for the next Claude session.
- **`fix` commit strategy.** In `--local` mode the fix is folded into the
  branch's existing commits instead of being appended as a fresh "Fix failing
  CI checks" commit: each change is staged against the commit that introduced
  the code it repairs (`git commit --fixup=<sha>`), all fixups are squashed in
  with `git rebase --autosquash origin/<base>`, and the rewritten branch is
  published with `git push --force-with-lease`. A new standalone commit is
  created **only** when a change belongs to no existing commit on the branch.
  The default temp-dir mode is unchanged — it appends a single follow-up commit
  and never force-pushes. Force-pushing is therefore scoped to `--local`, only
  ever uses `--force-with-lease`, and never touches commits that already exist
  on the base branch.

```bash
# Review the PR for the current branch, using this checkout
planwerk-review --local

# Audit the repo whose origin is this checkout (no clone)
planwerk-review audit --local

# Fix the current branch's PR, proceeding even if the tree is dirty
planwerk-review fix --local --force
```

`--local` does not change cache behavior, `gh` authentication, or the
`--patterns` flag. It also does not (yet) accept an arbitrary directory other
than the current one.

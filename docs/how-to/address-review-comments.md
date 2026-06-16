# Address review comments

Turn the inline comments a reviewer left on a pull request into applied changes.
`address` reads the PR's human review threads, lets you pick which unresolved
ones to act on, and drives Claude to incorporate each as a follow-up commit on
the PR head branch — optionally replying to and resolving each thread.

```bash
# Pick interactively which unresolved threads to address
planwerk-review address owner/repo#123

# Address every unresolved thread without prompting
planwerk-review address --all owner/repo#123

# Address only specific threads (repeat --thread)
planwerk-review address --thread PRRT_kwDOAbc123 --thread PRRT_kwDOAbc456 owner/repo#123

# Also mark the addressed threads resolved (outward-facing, off by default)
planwerk-review address --resolve owner/repo#123

# Preview the selected threads and the plan, change nothing
planwerk-review address --dry-run owner/repo#123

# Address the current branch's PR in this checkout
planwerk-review address --local --force
```

See the [CLI reference](/reference/cli#address) for the full flag table.

## How it works

1. **Resolve the PR.** Without `--local` the PR head is cloned into a temp dir;
   with `--local` the command operates on the current checkout (see
   [Use local mode](/how-to/use-local-mode)). The dirty-tree gate and `--force`
   apply in local mode.
2. **Fetch the review threads.** The PR's review threads are pulled via the
   GitHub GraphQL API, each carrying its resolved status, file and line, author,
   the full comment chain, and the diff hunk it is anchored to. Threads GitHub
   already marks **resolved** are skipped by default (`--include-resolved` to
   include them), as are the tool's own inline review comments — `address` never
   tries to address planwerk-review's own findings.
3. **Select which to address.** The unresolved threads are presented as an
   interactive selection list (file:line, author, and a one-line excerpt per
   row). Non-interactive paths: `--all` takes every unresolved thread,
   `--thread <id>` (repeatable) targets specific threads, and a TTY-less
   environment defaults to `--all` with a logged note — there are no silent
   partial runs.
4. **Incorporate the feedback.** Claude is dispatched per thread (or once for an
   aggregate commit) with the comment chain, the file and its surrounding
   context, the diff hunk, and the project's review patterns, and asked for the
   **minimal-invasive** change that addresses the feedback. By default each
   thread becomes one follow-up commit (`--one-commit-per-thread`) so the
   comment-to-commit mapping stays legible; `--one-commit-per-thread=false`
   aggregates the changes into a single commit. The loop runs until every
   selected thread is handled or `--max-iterations` is hit.
5. **Push, reply, and resolve.** The follow-up commits are pushed to the PR head
   branch. After each thread's change is pushed, a reply summarizing what
   changed is posted by default (`--no-reply` to skip) and — only with
   `--resolve` — the thread is marked resolved. Replying and resolving are
   **best-effort**: a GitHub failure is logged but never aborts the run, the same
   way `fix` posts its fix comment.
6. **Report.** An aggregate report of what each thread changed is posted back
   onto the PR (`--no-address-comment` to skip), so the record lives on the PR
   itself.

## Render the prompt instead of running

To drive the work manually in a Claude Code session you already have open inside
a checkout of the PR, render a self-contained prompt and paste it in:

```bash
planwerk-review address --print-bare-prompt owner/repo#123
```

The bare prompt instructs that session to fetch the unresolved threads itself,
address them as follow-up commits, push, and reply/resolve. `--print-prompt`
instead renders the orchestrator-driven prompt for the selected threads (with
the comment chains and diff hunks embedded), useful for inspection or piping
into other tooling.

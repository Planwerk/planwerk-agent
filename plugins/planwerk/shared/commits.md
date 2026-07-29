# Commits

Rules for every commit a planwerk skill creates, and for every push that
publishes one.

## Trailers

Every commit you create must end with exactly these two trailers, in this order:

```
Assisted-by: Claude
Signed-off-by: <committer name> <committer email>
```

- Pass `-s` to `git commit` so git appends the `Signed-off-by` line from the
  committer identity. It must be the very last line of the message.
- Name yourself in an `Assisted-by` trailer. Append your exact model id when your
  runtime provides it (`Assisted-by: Claude:claude-opus-5`); otherwise emit
  `Assisted-by: Claude` alone — never guess the id. Pass it as the final `-m`
  paragraph, not via `--trailer`: git places `--trailer` values *after* the
  sign-off, which breaks the order.
- Never add a `Co-authored-by` trailer — not for Claude, not for planwerk-agent,
  not for anyone.
- Never pass `--no-verify` or `--no-gpg-sign`. A pre-commit hook that rejects
  your commit has found something; it is not an obstacle to route around.

## References in a commit message

A commit message resolves its references against the repository the commit lands
in. Anything from another repository is written out in full — `owner/repo#123`
for an issue or pull request, `owner/repo@<sha>` for a commit, a URL for
everything else. The same holds for a closing keyword: `Closes owner/repo#123`
links and closes across repositories, while `Closes #123` closes whatever issue
carries that number here.

## Folding a change into the commit that caused it

A branch carries more than one commit, and a repair for code that an earlier
commit introduced belongs **in** that commit — not in a new commit stacked on
top. `<base>` below is the branch the pull request targets.

1. List the branch's own commits, oldest first:

   ```bash
   git log --oneline --reverse origin/<base>..HEAD
   ```

2. For each distinct change, find the commit that introduced the code you are
   changing: `git blame <file>`, `git log -p -- <file>`, or `git log -S<symbol>`.
3. Stage only that change and record it as a fixup of its target commit. Repeat
   per change that maps to a different commit:

   ```bash
   git add -- <files for this change>
   git commit --fixup=<target-sha>
   ```

4. Fold every fixup in non-interactively, so no editor opens:

   ```bash
   GIT_SEQUENCE_EDITOR=true git rebase -i --autosquash "$(git merge-base origin/<base> HEAD)"
   ```

   Rebase against the **merge-base**, never against `origin/<base>` itself.
   Rebasing onto the branch tip silently advances your work onto a base that
   moved since you branched, mixing an unrequested rebase into a repair.

A new standalone commit is the rare exception, for a change that genuinely
belongs to no existing commit on this branch — a new file unrelated to any of
them. Only then:

```bash
git commit -s -m "<concise summary>" -m "<one line of context>" -m "Assisted-by: Claude"
```

## Publishing a fold

```bash
git push --force-with-lease origin HEAD:<head-branch>
```

The autosquash rewrote the branch's commit SHAs, so a plain push is rejected.

- Use `--force-with-lease`, never plain `--force`. It publishes the fold while
  refusing to clobber commits you have not seen.
- Only the branch's own commits (`origin/<base>..HEAD`) may be rewritten. Never
  rebase, reorder, drop, or rewrite a commit that already exists on the base
  branch.
- Push only to the branch the work belongs to. Never to the base branch.

## Repairing what the rewritten SHAs left behind

The fold gave every commit it touched a new SHA, and the old ones are now
unreachable. Text that quoted them still reads as if they exist. The pull
request body is the one that matters: it is the document a reviewer opens, and a
description that walks the change set in commit order often cites those commits
by SHA.

After the push, repair the body — and only the body's stale references.

1. Read it:

   ```bash
   gh pr view <number> --repo <owner/repo> --json body -q .body
   ```

2. For every hex token of seven characters or more in it — bare, inside an
   `owner/repo@<sha>` reference, or at the end of a `.../commit/<sha>` URL — ask
   git whether it still reaches the branch:

   ```bash
   git merge-base --is-ancestor <sha> HEAD
   ```

   Exit 0 means the commit survived; leave the reference alone. Exit 1 means the
   fold replaced it. An error ("Not a valid object name") means the token is no
   commit of this repository at all — a checksum, a hash quoted from a log, a
   commit somewhere else — and it is not yours to touch.

3. Map each replaced SHA to its successor by subject. The fold changed the SHA,
   not the commit:

   ```bash
   git log -1 --format=%s <old-sha>
   git log --format='%H %s' "$(git merge-base origin/<base> HEAD)"..HEAD
   ```

4. Rewrite those tokens, and no other word of the body, then put it back:

   ```bash
   gh pr edit <number> --repo <owner/repo> --body-file <path>
   ```

Keep the abbreviation each reference used: where the body wrote seven
characters, write seven characters of the new SHA. The description is the
author's, and you are correcting a pointer, not editing their text.

When a replaced SHA has no successor — its commit was dropped, or two were
squashed into one — do not guess at a replacement. Leave the reference where it
is and say so in your report.

A follow-up commit on top rewrites nothing, so none of this applies to it.

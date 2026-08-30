# Commits

Rules for every commit a planwerk skill creates, and for every push that
publishes one. Folding a change into the commit that caused it — and publishing
that fold — is in `commits-fold.md`, for the skills that repair existing work.

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

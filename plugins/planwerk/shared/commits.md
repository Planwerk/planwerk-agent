# Commits

Rules for every commit a planwerk skill creates, and for every push that
publishes one. Folding a change into the commit that caused it — and publishing
that fold — is in `commits-fold.md`, for the skills that repair existing work.

## Trailers

Every commit you create must end with exactly these two trailers, in this order
(a `git commit --fixup` commit is the one exception: the autosquash discards
its message):

```
Assisted-by: Claude
Signed-off-by: <committer name> <committer email>
```

- Pass `-s` to `git commit` so git appends the `Signed-off-by` line from the
  committer identity. It must be the very last line of the message.
- Name yourself in an `Assisted-by` trailer. Append your exact model id when your
  runtime provides it (`Assisted-by: Claude:claude-opus-5-5`); otherwise emit
  `Assisted-by: Claude` alone — never guess the id. Drop any bracketed suffix
  such as `[1m]`: it names a context window, not the model. Pass the trailer as
  the final `-m` paragraph, not via `--trailer`: git places `--trailer` values
  *after* the sign-off, which breaks the order.
- Never add a `Co-authored-by` trailer — not for Claude, not for planwerk-agent,
  not for anyone. The harness proposes one by default; this project records
  assistance in `Assisted-by` instead.
- Never pass `--no-verify` or `--no-gpg-sign`. A pre-commit hook that rejects
  your commit has found something; it is not an obstacle to route around.

## What never makes a check green

A check that goes green because the check was silenced hides the cause and
costs the next person a bisect. None of these is a fix, in a repair or in a
fresh change: `t.Skip`, `pytest.skip`, `xit`, or `xdescribe` added to bypass a
test; `//nolint`, `# noqa`, `# type: ignore`, `@ts-ignore`, or
`@SuppressWarnings` added to silence a finding; widening a type to `any`,
`interface{}`, `unknown`, or `Any` to silence a type-checker; deleting a test
case or relaxing an assertion; pinning a dependency backwards to dodge a
security finding; adding a retry, a sleep, or a longer timeout to a test, or
re-running the job, to turn a result green; `--no-verify` on a commit.

## A red you forced counts once a diff shows the edit

A test that fails before a fix proves the fix only when it failed for the
reason you think. When you force that red by editing code or a fixture
(reverting the fix, planting the bug, breaking an input), `diff` the edited
file against a pristine copy, such as `git diff -- <file>` or a copy saved
before the edit, and find your change in it before you trust the red. An edit
can report success and change nothing: a `sed` address that never matched, a
write into the wrong copy of a file, a build output that gets regenerated. A
`grep` for the new text proves presence, not change, since the text may have
been there already.

## References in a commit message

A commit message resolves its references against the repository the commit lands
in. Anything from another repository is written out in full — `owner/repo#123`
for an issue or pull request, `owner/repo@<sha>` for a commit, a URL for
everything else. The same holds for a closing keyword: `Closes owner/repo#123`
links and closes across repositories, while `Closes #123` closes whatever issue
carries that number here.

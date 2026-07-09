# Fix failing checks

Take a pull request whose CI checks are red, repair the cause each check reports,
and fold the repair into the commit that introduced it.

```
/planwerk:fix owner/repo#123
```

Run it from inside a checkout of the pull request's head branch, with a clean
working tree. With no argument the skill targets the pull request for the branch
you are on.

## Skill or command?

`fix` exists both ways, and they do the same work under different supervision.

| | `/planwerk:fix` | [`planwerk-agent fix`](/reference/cli#fix) |
|---|---|---|
| Runs in | Your session, in your checkout | A throw-away clone, or `--local` |
| Iterates | Once, then reports | Polls and repairs until green or `--max-iterations` |
| At a fork | Asks you | Decides alone |
| Pushes | Only after you say yes | Every iteration |

Reach for the command when nobody is watching: a CI job, a long run you want to
walk away from. Reach for the skill when the repair needs a judgment call, or
when you want to see the diff before it reaches the branch.

## The check is a symptom

Every failing check can be made green two ways: repair the cause, or silence the
report. The second is always available, usually shorter, and always wrong. So the
skill treats an entire class of edits as forbidden rather than as a last resort —
`t.Skip`, `pytest.skip`, `xit`, `//nolint`, `# noqa`, `# type: ignore`,
`@ts-ignore`, widening a type to `any` or `interface{}`, deleting a test case,
relaxing an assertion, pinning a dependency backwards to dodge a security
finding, `--no-verify`. None of these is ever offered to you as an option.

The same reasoning rules out the placebo commit. A flake, an expired secret, a
runner that ran out of memory: no commit repairs any of them, and pushing one
buries the real signal. The skill reports and stops.

## What it asks you

Most of what a failing check reports has one honest repair, and the skill applies
it without asking. A missing import, a formatter's diff, a type annotation the
checker demands — none of these is a decision, and being asked about them teaches
you to stop reading the questions.

Four forks are real, and each reaches you with a recommendation and a concrete
downside on every option:

1. **Is the code wrong, or is the test?** The fork the unattended loop cannot
   see, because both answers make the check green and only one is correct. You
   get the assertion, the code under it, and what the PR body says the code is
   for.
2. **May the fix reach outside the failure surface?** Named file, and why nothing
   inside the PR's own diff resolves it. Declined, the run reports `BLOCKED`.
3. **This looks like a flake.** Re-run the job, or stop and report. A code change
   is not offered.
4. **A dependency the log directly implicates.** Bump, pin, or work around, with
   the transitive cost of each. A dependency the log does *not* implicate is
   never touched.

## What it does before it asks anything

- **Stands on the right tree.** `HEAD` must be the PR's head SHA and the working
  tree must be clean. A repair computed against a different tree is a repair for
  a different pull request, and uncommitted changes get swept into the fold.
- **Reads every log to the bottom.** CI clusters its errors at the end. A
  cancelled check is read too — it hides a timeout, not a passing test.
- **Reproduces before diagnosing.** It runs the exact command CI ran, not the
  closest equivalent. A failure that will not reproduce locally is itself a
  finding, and the skill says which kind: environment, ordering, version skew,
  race, or flake.
- **Reads your review patterns.** When the repository carries
  `.planwerk/review_patterns/`, the fix must not introduce code those patterns
  flag. One finding traded for another is not a fix.

Because it re-runs whatever command CI ran, the skill declares unrestricted
`Bash` in its `allowed-tools`. There is no allowlist that contains "the command
that failed".

## How the repair lands

You choose, and the first is recommended:

- **Fold each change into the commit that introduced it** —
  `git commit --fixup` + `git rebase --autosquash`, published with
  `git push --force-with-lease`. The branch keeps a history of the work rather
  than a history of its repairs. This is what the `fix` command does by default.
- **One follow-up commit on top**, pushed without rewriting history. Right when
  the PR's commits are already under review and a rewritten SHA would strand a
  reviewer's comment.
- **Leave it in the working tree.** Nothing is committed, nothing is pushed.

The rebase is bounded by `git merge-base`, never by `origin/<base>` itself, so a
base branch that moved since you branched is not silently rebased onto. Only the
branch's own commits are ever rewritten, and the push is always leased. Commits
carry `Assisted-by` above `Signed-off-by`, and never `Co-authored-by`. That
doctrine lives in `plugins/planwerk/shared/commits.md`, and a Go test
(`TestSharedCommitsDocMatchesFoldDiscipline`) fails when it and the `fix` command's
prompt disagree.

## The report

The skill emits the same `## Fix Report` shape the command posts — per check a
category, a root cause, the files touched, the exact verification command and its
result, and the regression test it added or why none applies — closing with:

```
STATUS: <DONE | DONE_WITH_CONCERNS | BLOCKED | NEEDS_CONTEXT>
```

`DONE_WITH_CONCERNS` means it pushed with a reservation you must see: an
out-of-scope reach, or a fix it could not exercise locally. `BLOCKED` is a
successful run — a placebo commit costs the next person a bisect.

It then offers to post the report as a PR comment, exactly as the command does,
so a pull request carries one report format whichever repaired it.

## Next steps

- [Rebase a PR](/how-to/rebase-a-pr) when the checks are red because the base
  moved, not because the code is wrong.
- [Address review comments](/how-to/address-review-comments) once the checks are
  green and a human has read the diff.
- [`fix` command reference](/reference/cli#fix) for the unattended loop.

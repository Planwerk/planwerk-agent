# Rebase a PR interactively

Take a pull request whose base branch moved, replay its commits onto the new
base in the checkout you are sitting in, resolve every conflict so that both
sides survive, and correct what the rewrite left stale in the pull request body.

```
/planwerk:rebase owner/repo#123
/planwerk:rebase --onto develop owner/repo#123
```

Run it from inside a checkout of the pull request's head branch, with a clean
working tree. With no argument the skill targets the pull request for the
branch you are on. The base is the branch the pull request targets unless
`--onto` names another.

## Skill or command?

`rebase` exists both ways, and they do the same work under different
supervision.

| | `/planwerk:rebase` | [`planwerk-agent rebase`](/reference/cli#rebase) |
|---|---|---|
| Runs in | Your session, in your checkout | A throw-away clone, or `--local` |
| At a conflict the sides disagree on | Asks you | Aborts the rebase |
| After a clean replay | Runs the repository's gate, folds in what the rebase broke | Posts a per-commit analysis of the upstream range as a PR comment |
| Review threads | Resolves the ones the upstream range settled, with evidence, behind your yes | Leaves them alone |
| Pushes | Only after you say yes | Only with `--push` |
| The PR body | Corrects the stale SHAs and the sentences the rebase changed the truth of | Leaves it alone |

Reach for the command when nobody is watching: a CI job, a long run you want to
walk away from. Reach for the skill when a conflict needs a judgment call, or
when you want to see the rewritten commits before they reach the branch.

## A rebase changes where the commits stand, never what they mean

Every conflict can be made to go away two ways: reconcile the two sides, or pick
one. The second is always available, usually shorter, and always drops a change
somebody made on purpose. So the skill treats the side-pick as forbidden rather
than as a last resort: no `-X ours`, no `-X theirs`, no deleting one half of a
hunk because the other half compiles. Before it edits a conflicted file it reads
the commit being replayed, its message, and what the upstream range did to that
file and why. The resolved file serves both.

The same reasoning rules out the squash. Each commit is replayed as itself, in
the same order, so a reviewer who read the branch commit by commit can read it
the same way after.

## What it asks you

Most conflicts have one honest resolution, and the skill writes it without
asking: a renamed symbol, a changed signature, a helper that moved, a line both
sides added for different reasons. Two conflicts are yours, and each reaches you
with a recommendation and a concrete downside on every option:

1. **The two sides want different behavior.** The replayed commit changes what a
   function does and upstream changed it the other way, or upstream removed the
   thing the commit extends. You get both diffs, both commit messages, and what
   the pull request says the branch is for. The options are the resolution the
   skill would write and the abort. A side-pick is never offered.
2. **A replayed commit no longer makes sense on the new base.** Upstream already
   did the work differently, or deleted its subject. Drop the commit, or abort.

An abort puts the branch back exactly as it was, and the run reports
`BLOCKED`. That is a successful run: a side-pick that compiles costs the next
person the change it dropped.

## What it does before it asks anything

- **Stands on the right tree.** `HEAD` must be the pull request's head SHA and
  the working tree must be clean. A rebase computed against a different tree
  rewrites a different pull request, and an uncommitted change would be swept
  into a replayed commit.
- **Records what the rewrite erases.** The fork point, every commit's old SHA
  and subject, the upstream range, and the pull request body are written down
  before the first commit is replayed. The tip it started from is said out loud,
  so you can return to it if you decline the push.
- **Reads the upstream range first.** The paths both sides changed are where
  the conflicts will be, and reading what upstream did there is what makes a
  resolution semantic rather than textual.
- **Reads your review patterns.** When the repository carries
  `.planwerk/review_patterns/`, a resolution must not introduce code those
  patterns flag.

Because it runs `git rebase` and whatever gate the repository defines, the
skill declares unrestricted `Bash` in its `allowed-tools`.

## The conflicts git did not see

A clean replay is not a correct one. Upstream may have renamed a symbol the
branch calls, removed a helper it uses, or added a lint rule its code fails,
none of which produces a textual conflict. After the replay the skill runs the
repository's own build, tests, and lint, and checks the symbols the branch
depends on against the upstream range.

What the rebase broke is repaired as a fixup of the commit whose change no
longer holds, folded in per `plugins/planwerk/shared/commits-fold.md`. What
was already red before the rebase is not: the skill names it in the report and
hands it to [`/planwerk:fix`](/how-to/fix-failing-checks).

## The threads the base settled

A review thread asks for a change. When that change was moved out into its own
issue and landed on the base before this branch did, the rebase just brought it
in, and the thread is answered without anyone on the branch having typed a line.
It would stay open until someone noticed.

After the replay the skill reads every unresolved thread and looks for the one
thing that settles it, in the upstream range and nowhere else: the thread names
an issue, and a pull request that closed it merged into the range; or the range
changed the lines the thread sits on the way the thread asked. A path match
alone settles nothing, and neither does GitHub's `outdated` mark, which says the
diff moved, not that the request was met.

Each settled thread reaches you as a proposal with its evidence: resolve it with
a reply naming the pull request or commit that settled it, in `owner/repo#N`
form, or leave it open. The resolve is recommended when a merged pull request
closed the issue the thread named, and the leave-open when the skill matched a
diff by reading alone. A thread only the branch's own code can answer is not
the rebase's and stays open, named in the report with the reason.

## How it lands

Before anything leaves the checkout you see every commit, old SHA to new SHA by
subject, every conflict with one sentence per file on what each side wanted
and what the file now does, what the gate ran and said, and the pull request
body as a diff, and each thread it would resolve with the reply it would
carry. One yes covers the push, the body edit, and the threads.

The push is `git push --force-with-lease` to the pull request's own head
branch, never plain `--force` and never to the base. Only the branch's own
commits are rewritten. Declined, the local branch stays rebased, `origin` is
untouched, and `git reset --hard <before>` restores the tip the skill named.

## The rewrite takes the PR description with it

A rebase gives every commit a new SHA, so every reference to one of them in the
pull request body now points at nothing. After the push the skill repairs them
the way [`/planwerk:fix`](/how-to/fix-failing-checks#the-fold-takes-the-pr-description-with-it)
repairs a fold's: each hex token is tested with
`git merge-base --is-ancestor`, the ones that no longer reach the branch are
mapped to their successor by subject, and those tokens are rewritten at the
abbreviation the body used. A dropped commit has no successor; its reference
stays and the report says so.

The rebase can also change the truth of a sentence. A body that walks the
change set in commit order describes each commit, and where a resolution
changed what a commit does, the skill corrects that sentence to what the commit
does now, in your words, and nothing beyond it. The body's statement of what the
pull request is for does not change, because the rebase did not change it.
Every edited line traces to a replaced SHA or a change the rebase made.

When `--onto` named a branch other than the pull request's base, the retarget
(`gh pr edit --base`) is shown and written in the same step.

## The report

The skill emits a `## Rebase Report`: the base and the upstream range, the
commits replayed and dropped, each conflict and its resolution, the gate it ran
and the adjustments it folded in, what reached the pull request, and the
threads it resolved and left open, closing with:

```
STATUS: <DONE | DONE_WITH_CONCERNS | BLOCKED | NEEDS_CONTEXT>
```

`DONE_WITH_CONCERNS` means the rebase is complete with a reservation you must
see: a gate that could not run here, a dropped commit, a pre-existing failure
handed to `fix`, or a push you declined. It then offers to post the report as a
PR comment.

## Next steps

- [Fix failing checks](/how-to/fix-failing-checks) when the checks on the pushed
  tip come back red for a reason the rebase did not introduce.
- [Address review comments](/how-to/address-review-comments) once the checks
  are green and a human has read the diff.
- [`rebase` command reference](/reference/cli#rebase) for the unattended run
  and its per-commit analysis.

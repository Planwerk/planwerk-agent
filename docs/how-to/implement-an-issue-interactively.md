# Implement an issue interactively

Take a prepared GitHub issue and implement it directly in the checkout you are
sitting in: a short plan built in plan mode, your approval, and one complete
pull request — with the pipeline's simplify pass run in your session, and
without its review and verification passes.

```
/planwerk:implement owner/repo#123
```

Run it from inside a checkout of the issue's repository, on a clean working
tree. The skill branches off the up-to-date default branch and never commits on
it.

## Skill or command?

`implement` exists both ways, and unlike `elaborate` and `fix` the difference is
more than supervision: the command runs passes the skill deliberately omits,
and runs each in a fresh session.

| | `/planwerk:implement` | [`planwerk-agent implement`](/reference/cli#implement) |
|---|---|---|
| Runs in | Your session, in your checkout | A throw-away clone, or `--local` |
| Plans | A short plan in plan mode, approved by you | A dedicated read-only planning session, reviewed by a second read-only session and posted to the issue |
| After the code | A simplify pass in the same session, then you read the diff | Simplify, verification, and review-and-fix passes, each in a fresh session |
| Opens the PR | Only after you say yes | Automatically, as the run's deliverable |

Reach for the command when nobody is watching, or when the change is large
enough to deserve the passes. Reach for the skill when the change is small — a
bug fix, a contained feature — and a full pipeline run costs more than it
catches. The point of the skill is that small changes stop being implemented ad
hoc, outside every convention: the branch shape, the commit trailers, the
Acceptance Criteria walk, and the one-PR discipline all still apply.

## The plan is still the gate

The skill enters plan mode before touching anything, grounds the issue in the
actual code — every cited file and symbol verified — and presents a short plan:
the change set, the change and verification command behind every Acceptance
Criterion, the tests to write, the documentation the change makes stale. For a
criterion a user observes in the running program, that command is a local
one: run the built binary, start the development server and send it a
request, or drive a headless browser against it. The plan also lists any
install or network access such a run needs beyond the repository's own
tooling, so your approval covers it. Nothing is edited before you approve that
plan.

A draft-depth issue (no Affected Areas, no Acceptance Criteria) is not silently
treated as elaborated: the skill asks whether to proceed with the plan carrying
the full weight, or to stop for [`/planwerk:elaborate`](/how-to/elaborate-an-issue)
first.

When the plan builds or changes an interface and the
[impeccable](/how-to/use-impeccable-for-ui-work) skill is installed, the skill
uses it for that part of the work, within the plan you approved: a design change
the plan does not name comes back to you as a question before it is made.

## Simplified before you read it

Once the plan is implemented and committed, and before any Acceptance Criterion
is exercised, the skill reads its own diff the way the command's simplify pass
does. The decision ladder is the same: prefer not building it (YAGNI), then the
standard library, a platform- or framework-native feature, a dependency already
present, a one-liner, and only then the minimum new code. So are the four kinds
of over-engineering it lists (speculative abstractions, reinvented wheels, dead
or unreachable complexity, over-built control flow) and the guardrail:
validation, error handling, security controls, accessibility code, and tests
are never simplified away.

Each simplification is folded into the commit that introduced the code
(`git commit --fixup`, then an autosquash), so the branch you read carries no
cleanup commits. The plan's verification commands run again afterwards, and a
simplification that turns one red is taken back out. The report lists what was
collapsed and what was skipped, or says there was nothing to simplify.

Two things differ from the command. The pass runs in the session that wrote
the code, not in a fresh one, which is why the command remains the path for a
change that deserves an independent reading. And a collapse that would remove
something the plan names is a structural deviation: it comes back to you as a
question before it is made.

## Complete, or not at all

The plan covers the whole issue as one pull request, and the implementation
either delivers it or stops. There is no `PARTIAL`: deferring a listed piece to
a follow-up issue or a second PR is not an outcome the skill can produce, the
same discipline the command enforces (design decision 62). When the work cannot
complete, the report is `BLOCKED`, the branch stays local, and nothing is
pushed.

The passes the skill omits do not soften what "done" means. Every Acceptance
Criterion is exercised by a command the report quotes, a bug fix carries a
regression test the skill watched fail before the fix, and the diff is re-read
against `.planwerk/review_patterns/` when the repository carries them — because
no review pass will do it later. A criterion a user observes is checked in the
program the skill runs locally, not on unit tests alone, and the skill stops
every process it started before it shows you the result. "Only a live system
can verify" is reserved for a criterion that needs a remote host or a
credential; one whose local run this machine could not provide is reported as
"not exercised here", with the reason.

## How the work lands

The branch is named `implement/issue-<N>-<slug>`, the same shape the command
uses, so it is unambiguously this issue's implementation. Commits are small and
reviewable, and carry `Assisted-by` above `Signed-off-by` (only `Signed-off-by`
where the repository forbids attributing commits to an AI), never
`Co-authored-by` — the doctrine in `plugins/planwerk/shared/commits.md`.

After you have seen the criteria walk, the commits, and the diff, you choose
where it lands, and the first is recommended:

- **Draft pull request** — the branch is pushed and a draft PR opened, its
  description walking the commits in order and closing the issue with
  `Closes #N`. This is what the command's finalize step produces.
- **Ready pull request** — the same, not draft.
- **Leave the branch local** — nothing is pushed.

Nothing reaches GitHub without an explicit yes, and the issue body is never
edited — the plan lives in the conversation, and the PR closes the issue when
it merges.

## Next steps

- [Fix failing checks](/how-to/fix-failing-checks) when the pushed PR's checks
  come back red.
- [Implement an issue](/how-to/implement-an-issue) with the unattended command
  when the change deserves the full pipeline.
- [Elaborate an issue](/how-to/elaborate-an-issue) when the skill reports the
  issue is still at draft depth.
- [Diagnose a reported bug](/how-to/diagnose-a-bug) when the issue reports a bug
  whose cause is not known yet.

# Diagnose a reported bug

Take a bug report whose cause nobody knows yet, reproduce the reported symptom
with a command that goes red, and fix the root cause behind a regression test in
one pull request.

```
/planwerk:diagnose owner/repo#42
/planwerk:diagnose "the export drops the last row"
```

The first form reads the report from an issue, and the second takes the symptom
in words. With no argument the skill asks you what you did, what you expected,
what happened instead, and where (version, commit, environment). Run it from
inside a checkout of the repository, on a clean working tree and an up-to-date
default branch.

## A skill, not a command

There is no `planwerk-agent diagnose` command (design decision 103). `fix`
already reproduces before it patches, but it starts from a pull request's red
checks, so a bug no check caught has no way in.

The diagnosis has two checkpoints that need a person. You review the hypotheses
before any probe runs. And when no loop can be built, the skill stops and asks
you for an environment or a captured artifact. An unattended run could only end
either one at `NEEDS_CONTEXT`.

## No loop, no theory

The skill forms no hypothesis until it has a feedback loop: one named command,
already run at least once, with its invocation and redacted output shown, that
is

- red-capable: it drives the real code path and asserts the reported symptom,
- deterministic: the same verdict on every run, or for a flaky bug a measured
  rate of at least 1 red run in 10,
- fast: seconds, not minutes,
- agent-runnable: the skill runs it without you, except in the
  human-in-the-loop form.

It tries these constructions in order:

1. A failing test at whatever seam reaches the bug.
2. An HTTP request script against a locally running dev server.
3. A CLI invocation with a fixture input, diffed against a known-good snapshot.
4. A headless-browser script (Playwright, Puppeteer).
5. A captured trace replayed through the code path.
6. A throwaway harness that runs the minimal subset of the system.
7. A property or fuzz loop, for "sometimes wrong output".
8. A `git bisect run` harness, when a last-good state is known.
9. A differential loop across two versions or two configurations.
10. A numbered list of steps it walks you through one at a time, when a person
    must act.

The report's reproduction steps are data. The loop encodes them through the
repository's own tooling and never runs a command copied from the report.
Anything beyond that tooling (a host other than localhost, a package install, a
downloaded script, elevated privileges) runs only after you say yes. So does a
loop that carries code or input taken from the report, such as a pasted
snippet or a fixture: the skill first swaps any executable content in it for a
harmless stand-in and points its paths, hosts, and credentials at its private
directory, localhost, or dummy values, then shows it to you verbatim with where
it came from and names each path, host, or destructive option it still
carries.
Because the loop command is whatever reproduces the bug, the skill declares
unrestricted `Bash`, as `fix` and `implement` do.

Once the loop goes red on the reported symptom on three consecutive runs, the
skill shrinks the repro until every remaining element is load-bearing, and
bisects when the report names a last-good version or commit. A loop that stays
green means the bug does not reproduce here, and the skill never proceeds to a
fix on it.

## What it asks you

Three to five ranked hypotheses reach you before any probe runs, each with a
prediction: if X is the cause, changing Y turns the loop green. Keep the order,
or re-rank and rule one out. You often know that a theory was already tested,
or that an area changed last week.

Three forks come to you, each with a recommendation and a sentence on what
breaks if the choice is wrong:

1. The reported behavior is what the code, its tests, or its documentation
   deliberately specify. Change it, or stop as working as designed
   (`NEEDS_CONTEXT`).
2. The root cause lies outside the repository. Work around it here, or stop at
   `BLOCKED`, naming the dependency.
3. The fix changes a public interface: an exported API, a CLI flag, a
   configuration key, a wire format. Apply it, or stop at `NEEDS_CONTEXT`.

Every other fix has one honest repair, and the skill applies it without asking.

When no loop can be built, the skill lists every construction it tried and why
each failed, then asks you for one of three things: access to an environment
that reproduces the bug, a redacted captured artifact (a log, a HAR file, a
core dump, a screen recording with timestamps), or permission to add temporary
instrumentation to a deployed system. Without one, it reports `NEEDS_CONTEXT`
and forms no hypothesis.

## What it leaves behind

Every debug line the skill adds carries one tag, `[DEBUG-<tag>]`, with four
lowercase hex characters chosen once per run, so
`git grep -n --untracked "DEBUG-<tag>"` finds every probe in the checkout's
tracked and untracked files, even one in a test file that is not tracked yet.
It leaves ignored files out, because a build cache such as `__pycache__/` keeps
a compiled copy of a probe after the probe is gone. Before the skill probes an
ignored file (under `node_modules/`, say) or a file outside the checkout (the Go
module cache), it saves a copy of the file. Before it reports, on every exit
path including a stop, it checks that:

- `git grep -n --untracked "DEBUG-<tag>"` returns nothing,
- every saved file is restored byte for byte, with its original mode,
- the private directory (`mktemp -d`) that held every throwaway file is
  deleted,
- no `git bisect` is in progress and no stash it made is left,
- after a fix, the original loop no longer reproduces and the regression test
  passes.

A stop without a fix leaves one thing behind: the red reproduction test,
uncommitted on the `diagnose/…` branch and named in the report, for you to keep
or drop.

Secrets appear as `<REDACTED>` in everything the skill shows or writes, and its
loops read credentials from environment variables.

## How the fix lands

The skill works on `diagnose/issue-<N>-<slug>`, or `diagnose/<slug>` for a
described symptom, branched off the up-to-date default branch. The prefix is
not `implement/issue-<N>-` on purpose: `planwerk-agent implement` resumes
branches with that prefix, and a diagnosis branch is not a half-finished
implementation.

The regression test and the fix land together as one commit. Its body names the
root cause, each ruled-out hypothesis, and the loop command, and its trailers
are `Assisted-by` above `Signed-off-by`. After you have seen the result and the
diff, you choose where it lands, and the first is recommended:

- Draft pull request: the branch is pushed and a draft pull request opened.
- Ready pull request: the same, not draft.
- Leave the branch local: nothing is pushed, and the verdict is at most
  `DONE_WITH_CONCERNS`.

For a security vulnerability the skill recommends leaving the branch local
instead, and points at the repository's `SECURITY.md` or a GitHub security
advisory: a pull request on a public repository publishes the repro and the root
cause before a release exists.

The pull request body has five sections (`Symptom`, `Reproduction`,
`Root cause`, `Fix`, `Ruled out`) and closes the issue with `Closes #N` when the
skill started from one. A rejected push is reported and never retried with
`--force`.

When the skill started from an issue, it then offers to post its report there as
a comment, signed `Diagnosed by planwerk-agent`. It recommends posting when it
stopped at `BLOCKED` or `NEEDS_CONTEXT`, because the reporter holds what is
missing, and never for a security vulnerability. Nothing reaches GitHub without
your yes.

## The report

The skill ends with a `## Diagnosis Report`: the symptom and the loop with its
red rate, the minimized repro and the first bad commit, every hypothesis with
its verdict and the probe that decided it, the root cause, the fix, the cleanup
checks, and what it noticed but left alone. It closes with:

```
STATUS: <DONE | DONE_WITH_CONCERNS | BLOCKED | NEEDS_CONTEXT>
```

`DONE` means the loop went red, the cause is fixed behind a regression test
watched failing and then passing, the original loop is green, and a pull request
is open. `DONE_WITH_CONCERNS` names its reservation: no correct seam for a
regression test, the branch left local, a flaky bug verified only at its
measured rate, or a fork you declined to answer. `BLOCKED` means a second list
of hypotheses was refuted in full, or the cause lies in a dependency you chose
not to work around. `NEEDS_CONTEXT` means only you or the reporter holds the
missing fact. There is no `PARTIAL`.

## Next steps

- [Fix failing checks](/how-to/fix-failing-checks) when the pull request's
  checks come back red.
- [Implement an issue interactively](/how-to/implement-an-issue-interactively)
  when the cause and the change are already known.
- [Use the skills](/how-to/use-the-skills) for the other skills and where each
  one fits in the pipeline.

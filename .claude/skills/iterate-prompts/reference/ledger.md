# Prompt audit <YYYY-MM-DD>

head: <short SHA of the commit the audit read; the next check counts commits from this entry's own commit, not from here>
claude_code: <claude --version, as the probe prints it>
models: opus=<id> sonnet=<id> fable=<id>
guidance: <the "## Migrating to …" headings of model-migration.md that cover the ids above, separated by "; ">
eval_baseline: <path or commit of the baseline report this iteration recorded, or "none">
branch: <the branch that carries this iteration's commits>

The four lines after the heading are read by `scripts/check.sh`; keep their
names, order, and one-line form. Everything below is for the next reader.

## Scope

One sentence on why the iteration ran (the `check.sh` reason line) and the
surfaces it covered: builders (count), skills (count), shared blocks, shared
docs. Name the slices that returned `clean`.

## Applied

One line per commit on the branch, newest last: `<sha> <theme>`, then the
findings it cleared as `file:line` pairs. A golden regeneration is its own
line when it stands alone.

## Deferred

Behavioral changes the author did not approve in this iteration, each with
the evidence it still needs (an eval verdict, a probe, a decision) and the
reason given. The next iteration reads this list as its `known-open` list.

## Known-open

Findings carried from earlier ledgers that this iteration did not settle.
Drop an entry only when the finding is fixed or withdrawn, and say which.

## Dropped

Findings the auditors returned that the synthesis rejected, with the reason
(the line no longer exists, the exception is documented in the components
header, the rule is load-bearing on the target model). A dropped finding
that comes back next time is a sign the lens needs a sharper test.

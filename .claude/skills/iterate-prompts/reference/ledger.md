# Prompt audit <YYYY-MM-DD>

head: <short SHA of the commit the audit read; the next check counts commits from this entry's own commit, not from here>
claude_code: <claude --version, as the probe prints it>
models: opus=<id> sonnet=<id> fable=<id> haiku=<id>
guidance: <the "## Migrating to …" headings of model-migration.md that cover the ids above, separated by "; ">
eval_baseline: <path or commit of the baseline report this iteration recorded, or "none">
branch: <the branch that carries this iteration's commits>

The four lines after the heading are read by `scripts/check.sh`; keep their
names, order, and one-line form. Everything below is for the next reader.

## Scope

One sentence on why the iteration ran (the `check.sh` reason line) and the
surfaces it covered: builders (count), skills (count), shared blocks, shared
docs. Name the slices that returned `clean`, and every builder or file that
ended the iteration `not audited`, with the reason: the next iteration reads
that list as surfaces nobody has looked at.

## Applied

One line per commit on the branch, newest last: `<sha> <theme>`, then the
findings it cleared, each by its key and its `file:line`. A golden regeneration is its own
line when it stands alone. The repository merges by rebase, which rewrites
every SHA on the branch: write the SHAs `main` carries after the merge (map
the branch's commits by subject), and cite a change that waits on another
pull request by that pull request and its theme, never by a branch SHA.

## Deferred

Behavioral changes the author did not approve in this iteration, each with
the evidence it still needs (an eval verdict, a probe, a decision) and the
reason given. The next iteration reads this list as its `known-open` list.

## Known-open

Findings carried from earlier ledgers that this iteration did not settle.
Drop an entry only when the finding is fixed or withdrawn, and say which.

An entry of either list stands on its own: the finding's key, its `file:line`,
the sentence it quotes, and what it claims, written out here. Never a bare id,
and never a pointer to an auditor's report or to an earlier ledger's list: no
report is stored, and the next brief copies these two lists verbatim to
auditors who have nothing else to resolve an id against. An entry carried from
a ledger that numbered its findings gets a key now, with the old number beside
it once.

## Dropped

Findings the auditors returned that the synthesis rejected, each by its key, with the reason
(the line no longer exists, the exception is documented in the components
header, the target model needs the rule). A dropped finding
that comes back next time is a sign the lens needs a sharper test.

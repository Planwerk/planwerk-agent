# Practice analysis <YYYY-MM-DD>: <owner>/<repo>

url: <the url= line fetch-source.sh printed>
commit: <the commit= line: the source commit this analysis read>
head: <short SHA of the commit of this repository the analysis read>
claude_code: <the claude_code= line>
license: <the license= line>

The five lines after the heading are read by `scripts/fetch-source.sh`; keep
their names, order, and one-line form. Everything below is for the next
reader.

## Scope

One sentence on why the analysis ran (the `state=` line and its detail).
Then every reading group with its role, files, and bytes, and whether it was
read or the author left it out; every skipped set with its reason; and the
keys carried forward from earlier ledgers, each with the ledger it came from.

## Practices

| key | kind | authority | practice | status | slices | provenance | evidence |
|---|---|---|---|---|---|---|---|

One row per practice, grouped by the reading group of its first provenance
line. Provenance is `path:line` in the source at the commit above; evidence
is `file:line` in this repository (or, for a gap, the search that came back
empty). A status that differs per slice is written per slice, e.g.
`adopted (1, 3) / gap (4)`.

## Proposals

Ranked by value, highest first. For each: the key, what changes and where,
size, class, the authority behind it and the failure it prevents, risk and
cost, the evidence it needs, and its disposition: `built <short SHA>` on
`adopt/<slug>-<date>`, `issue #N`, `deferred` (with what it waits for), or
`declined` (with the author's reason). The next analysis reads the deferred
ones as open.

## Conflicts

Where the source contradicts a recorded decision, the doctrine, a vendor
page, or the installed CLI: both quoted, which is newer, and what the author
decided, if anything.

## Dropped

Practices and verdicts the consolidation or the synthesis rejected, with the
reason (the quote is not at that line, the absence was never searched, the
status had no record behind it). One that comes back on the next run is a
sign an agent's lens needs a sharper test.

## Follow-ups

The vendor pages the source mirrors that `.claude/guidance-analyses/` has no
ledger for, and the repositories the source points at that could be read
next, one line each with why.

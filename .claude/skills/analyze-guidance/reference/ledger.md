# Guidance analysis <YYYY-MM-DD>: <page title>

url: <the url= line fetch.sh printed>
sha256: <the sha256= line: the version of the page this analysis read>
head: <short SHA of the commit the analysis read>
claude_code: <the claude_code= line>
linked: <the linked pages read to settle a recommendation, separated by ", ", or "none">

The five lines after the heading are read by `scripts/fetch.sh`; keep their
names, order, and one-line form. Everything below is for the next reader.

## Scope

One sentence on why the analysis ran (the `state=` line and its detail), the
slices that had mappers, and the recommendations carried forward unchanged
from the previous entry.

## Recommendations

| key | section | recommendation | status | slices | evidence |
|---|---|---|---|---|---|

One row per recommendation the page makes, in page order. The evidence cell
holds `file:line` anchors (or, for a gap, the search that came back empty);
a status that differs per slice is written per slice, e.g.
`adopted (1, 3) / gap (4)`.

## Proposals

Ranked by value, highest first. For each: the key, what changes and where,
size, class, the failure it prevents, risk and cost, the evidence it needs,
and its disposition: `issue #N`, `accepted` (wanted, not yet filed),
`deferred` (with what it waits for), or `declined` (with the author's
reason). The next analysis reads the accepted and deferred ones as open.

## Conflicts

Where the page contradicts a recorded decision or the doctrine: both quoted,
which is newer, and what the author decided, if anything.

## Dropped

Mapper verdicts the synthesis rejected, with the reason (the quoted line is
not there, the absence was never searched, the status had no record behind
it). A verdict that comes back on the next run is a sign the mapper's lens
needs a sharper test.

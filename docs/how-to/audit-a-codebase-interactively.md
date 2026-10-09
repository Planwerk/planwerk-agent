# Audit a codebase interactively

Audit the checkout you are sitting in against the review patterns it commits
and the principles the planwerk catalog teaches, verify every finding against
the line it cites, and file the ones worth a pull request as issues
[`/planwerk:elaborate`](/how-to/elaborate-an-issue) plans from there.

```
/planwerk:audit
```

Run it from inside a checkout of the repository, on an up-to-date default
branch. Optional path arguments narrow the audit
(`/planwerk:audit internal/ cmd/`); with none, the whole repository is in
scope. `--patterns <dir>` adds a pattern directory on your machine. The skill
records the HEAD commit it audited, and every finding is pinned to it.

## Skill or command?

`audit` exists both ways, and they apply the same ladder under different
supervision.

| | `/planwerk:audit` | [`planwerk-agent audit`](/reference/cli#audit) |
|---|---|---|
| Runs in | Your session, in your checkout | A throw-away clone, or `--local` |
| Patterns | The checkout's `.planwerk/review_patterns/`, `--patterns` directories, and the catalog's principles as the skill carries them | The embedded catalog, the wiki, the repository, and every `--patterns` source |
| A finding | Quoted from the file and checked against what would refute it before it reaches you | Reported as the session found it, with its confidence |
| The calls only you can make | Asked, with a recommendation | Reported at `needs-discussion` or `architectural` |
| Product | One issue per work package, filed after you say yes | A report; `--create-issues` walks the finding groups |
| Dead and duplicated code | Handed to [`/planwerk:cleanup`](/how-to/plan-a-codebase-cleanup) | Reported like any other finding |

Reach for the command when nobody is watching, when you want the full catalog
and the wiki's patterns applied, or when a report is the deliverable. Reach for
the skill when you want each finding's evidence in front of you and issues
filed one package at a time.

## A finding is a claim the code must back

The skill inventories the languages and the test conventions the project
uses, runs whichever read-only detectors are already installed (it never
installs one), loads the committed patterns and the
[project memory](/how-to/use-the-skills#project-memory), and then works
through the tree with three lenses: every loaded pattern's "What to check"
list, the catalog's design and technology principles for the stack it found,
and the questions a Staff Engineer asks of any module — what happens at ten
times the load, what the blast radius is, whether on-call will understand the
error path, where the tests are, whether a user could find the feature in the
docs. Beyond the patterns it reports any defect that could cause incorrect
behavior, data loss, a security exposure, or a test failure, judges missing
tests and documentation against the project's own conventions, and reads
every dependency manifest for deprecated, unmaintained, or far-behind
versions.

Every hit is a lead, not a finding. A lead becomes a finding only after the
skill has quoted the lines that trigger it, verbatim, and run the searches
that would refute it: the guard or validation that might handle it elsewhere
(cited, or "not verified"), the test that covers the behavior (named, or
"test coverage unknown"), a memory page or pattern that records the choice as
deliberate, and the command's own suppression list. Each lead lands in exactly
one bucket:

| Bucket | What it means | Where it lands |
|--------|---------------|----------------|
| **Finding** | Quoted, the refutation searches came back empty, confidence verified or likely | A work package, with its evidence |
| **Unverified** | No quotable line, or confidence uncertain | The report only, with what would verify it |
| **Cleanup's** | A dead symbol or a duplicated block | One line in the report pointing at `/planwerk:cleanup` |
| **Author's call** | A dependency jump, a public surface, a fix that changes behavior, a contradicted decision | A question to you |
| **Clean** | The lead was refuted | Dropped silently |

Severity comes from the same ladder the command applies — BLOCKING for an
exploitable vulnerability or data loss, CRITICAL for a defect on a normal
path or a dependency nobody maintains, WARNING for a defect on an edge path
and for the tests and documents the project's conventions call for, INFO for
the optional — and never from how a finding reads.

## The packages are sized to ship

Findings at WARNING and above are grouped by the change that resolves them:
the same pattern across files that one sweep fixes is one package, two
unrelated defects in one file are two. Each package is one issue, and
[one issue is one complete pull request](/how-to/use-the-skills#one-format-every-issue-skill).
INFO findings stay in the report unless you ask for one as an issue.

Before anything is filed, every package is searched against the tracker,
open and closed issues alike. The command's
[existing-issue dedupe](/how-to/analyze-a-repository#existing-issue-dedupe)
matches titles; the skill also reads an issue that describes the same fix.
A package an existing issue already describes is reported as tracked in that
issue, not filed again, so a repeated audit stays idempotent. A closed issue
whose fix landed, for a defect the audit verified anyway, is reported as a
regression and offered for filing. An exploitable vulnerability or an exposed
secret is never filed publicly on the skill's own initiative: it reaches you
with the repository's security route recommended, and a secret's value is
redacted wherever the finding is quoted.

The issues keep the draft depth's path-free Description and Motivation and
add an `## Evidence` section: the audited commit, the severity, the pattern
violated, and each site with its lines quoted and the refutation searches
recorded. That is a deliberate, scoped exception to the draft-depth no-paths
rule, the same one the cleanup survey makes: a finding without its site is an
opinion. [`/planwerk:elaborate`](/how-to/elaborate-an-issue) re-verifies
every site against the checkout it plans in, so an audit that has aged
corrects itself at planning time.

## What reaches you

Priority, cost, a vendor, a public surface, and anything irreversible are
yours to decide, so those leads reach you as questions — grouped by concern,
each with a recommendation, an upside and a downside per option, and what
breaks if the choice is wrong. A lead that contradicts a decision the project
memory records is a question too, naming the page. What you settle moves into
a package; what you decline is named in the report as unresolved and is never
filed as if it were settled.

You see the verdict line, the package table with each package's severity and
tracker status, the full body of every new issue, and the skill's own
verification results before anything is filed. You file all, choose package
by package, or cancel. Nothing reaches GitHub without your explicit yes.

## What it will not do

- **It never changes code.** The working tree is untouched; the issues are
  the only artifact.
- **It never installs tools.** Detectors already on `PATH` run in their
  report-only form; a missing one is named in the report.
- **It never files what it could not quote.** An unverified lead stays in the
  report.
- **It never files INFO findings** unless you ask for one.
- **It never re-files what the tracker holds.**
- **It never hunts dead or duplicated code.** Run
  [`/planwerk:cleanup`](/how-to/plan-a-codebase-cleanup) for that.
- **It never files a Meta Issue.** Each package is its own issue.

## Where it sits in the pipeline

`/planwerk:audit` → `/planwerk:elaborate` on each issue →
`planwerk-agent implement` or `/planwerk:implement` → `/planwerk:fix`.

The skill enters the pipeline where [`/planwerk:draft`](/how-to/draft-an-issue)
does, one issue per package: where `draft` writes an issue from an idea you
bring, `audit` writes one from what the code itself shows.

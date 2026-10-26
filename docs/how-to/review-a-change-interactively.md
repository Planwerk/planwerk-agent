# Review a change interactively

Review the branch you are on, or the changes you have not committed, in your
own checkout before a pull request exists. Every finding is verified against
the line it cites, and every file the change touches is accounted for.

```
/planwerk:review
```

Run it from the root of your checkout, on the branch you want reviewed; the
working tree may be dirty. Started from a subdirectory, the skill stops and
asks you to start the session at the root. It compares the branch from its
merge base to `HEAD` against a base: `--base <ref>` when you give one, else the
base of the branch's open pull request, else the default branch. `--workspace`
reviews the staged, unstaged, and untracked changes against `HEAD` instead,
and the skill switches to that mode by itself when the branch has no commits
of its own and the tree is dirty. `--patterns <dir>` adds a pattern directory
on your machine. The skill records the commit it reviewed, and every finding
cites lines in the reviewed tree: that commit in branch mode, your working
tree with `--workspace`.

## Skill or command?

`review` exists both ways, and they apply the same ladder under different
supervision.

| | `/planwerk:review` | [`planwerk-agent <pr-ref>`](/reference/cli#review-default-command) |
|---|---|---|
| Needs | A checkout | A pull request (`--local` infers it from the branch) |
| Reviews | `<merge-base>..HEAD`, or the uncommitted changes | The pull request's diff |
| Rules | The base's `.planwerk/review_patterns/` and checklist, `--patterns` directories, and the default checklist's classes as the skill carries them | The embedded catalog, the wiki, the repository, and every `--patterns` source |
| A finding | Quoted from the reviewed tree and checked against what would refute it before it reaches you | Checked by the snippet and claim gates |
| The calls only you can make | Asked, with a recommendation | Reported at `needs-discussion` or `architectural` |
| Product | A report in the conversation, with JSON or one pull request comment on request | A Markdown, JSON, or SARIF report; the Markdown one also posts with `--post-review` and `--inline` |

Reach for the skill before you push, or between pushes, when you want the
evidence for each finding in front of you and a verdict on code nobody else has
read yet. Reach for the command once the pull request exists, when nobody is
watching, or when you want the full catalog, the wiki's patterns, and the
specialist passes applied.

## Every changed file is accounted for

The skill opens a ledger from git's list of the change: one entry per changed
file, a rename under its new path, an untracked file as an entry of its own.
Every entry ends `reviewed`, or `skipped` with one of a closed set of reasons:
a binary file, a generated file (with its marker named), a vendored file, a
lockfile whose manifest changed too, a submodule pointer, a planning artifact
under `.planwerk/`, a path no command line carries safely, or a git error.
Size is never one of them: a diff too large to read at once is written to a
temporary file and read in chunks. A deleted file is reviewed too, because a
caller may still use what it removed. The report closes the ledger with
`Coverage: <reviewed> of <total> files reviewed (<percent>%), <skipped> skipped`.

Every git call that prints a diff or a file runs as
`git --no-pager -c core.quotePath=false … --no-ext-diff --no-textconv --no-color`,
and the calls that list the change or search it start
`git --no-pager -c core.quotePath=false`. No pager waits for input; no external
diff or text-conversion driver that the repository's `.gitattributes` names
shows the skill something other than the code it quotes; no color code lands
inside a quote; and a path prints as it is, not escaped. The skill's grants
name those prefixes, so a plain `git diff`, `git show`, or `git grep` stops at
a permission prompt rather than running.

## A finding is a claim the code must back

The review patterns and the checklist, with the `.planwerk/checklist.d/` files
appended as [Customize the review checklist](/how-to/customize-the-review-checklist)
describes, are read at the base, not from your branch, the way the command
reads them, so a change cannot rewrite the rules it is judged by; a change to
them is a finding of its own. The skill also reads
the [project memory](/how-to/use-the-skills#project-memory), the domain
glossary, and the statement of intent: the open pull request's title and body,
or your commit messages.

It works through every entry with the questions a Staff Engineer asks of any
change, the checklist's classes, each loaded pattern's "What to check" list,
the tests and documentation the project's own conventions call for, the
dependencies the change introduces, and the scope against the statement of
intent. Beyond every pattern it reports any defect that causes incorrect
behavior, data loss, a security exposure, or a test failure, including
breakage in unchanged code that the change causes.

Every hit is a lead. It becomes a finding only once the skill has quoted the
lines that trigger it, verbatim from the reviewed tree, and run the searches
that would refute it: the guard that might handle it elsewhere, the test that
covers it, a memory page or pattern that records the choice as deliberate, and
the command's suppression list. Its severity comes from the command's ladder,
never from how it reads. A lead the skill cannot quote is reported as
unverified, with what would verify it. Calls only you can make, such as a
behavior change users depend on or a published interface, reach you as
questions before the report.

## JSON and the pull request

With `--format json`, or when you ask after the report, the skill adds JSON that
validates against `planwerk-agent schema review`
([Output format](/reference/output-format#json-schema)), with the report's ids
and order. It leaves out the records of the passes and gates it does not run.
Its ledger goes into `gates.files`, the changed-file record, when the schema
defines one, counted the way the command counts it: the planning artifacts
stay out, and a path that is both deleted and untracked counts once. Otherwise
the summary ends with the coverage line. It needs
`planwerk-agent` on `PATH` for the schema.

On request the skill posts its report as one comment on the open pull request.
It does so only when the review covers exactly what the pull request shows,
read again right before the post: the pull request is still open, its head is
the reviewed commit and still your `HEAD`, and the base is its base. You see
the full body first, and it is posted on your yes. The comment carries no HTML
comment, so none of the command's `<!-- planwerk-agent` markers, and the
`review` command neither rewrites it nor reads it as its own earlier review; a
later `--post-review` run leaves it standing. A quote that holds an HTML
comment, as one does when your change touches such a marker, is left out of
the comment by its location and stays in the report in the conversation.

## What it will not do

- **It never changes code.** It does not edit, commit, or push; the working
  tree ends as it began.
- **It never posts on its own.** The one comment goes out on your yes.
- **It never reports a finding it could not quote.** Such a lead is
  unverified.
- **It never skips a file silently.** Every entry in the ledger is reviewed
  or skipped with a reason.
- **It never judges visual taste.** Accessibility (contrast and reduced motion
  included), a missing interface state, a layout that breaks at a narrow width
  or on zoom, and a value hard-coded where the project uses design tokens are
  findings like any other; taste in layout, typography, color, and motion is one
  only where the project's documented design decisions rule on it. When the
  change touches UI files, the report says in one line what judges the rest:
  `/impeccable critique` and `/impeccable audit` when the
  [impeccable](/how-to/use-impeccable-for-ui-work) skill is installed, and
  otherwise that the review did not judge how the interface looks.

## Where it sits in the pipeline

Your commits → `/planwerk:review` → push and open the pull request → the
[`review` command](/how-to/review-a-pr) and
[`/planwerk:fix`](/how-to/fix-failing-checks).

When a finding stands at warning or above, fix it, commit, and run
`/planwerk:review` again in a fresh session, so what the command would flag on
the pull request is fixed before anyone else reads it.

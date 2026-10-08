# Bootstrap the project memory

`brain bootstrap` fills an empty project memory from what a repository already
decided. It reads the closed issues, merged pull requests, commits, and decision
documents, proposes memory pages and review patterns, has a second model review
each one, and keeps the result in a directory on your machine until you push it
to the [GitHub Wiki](/how-to/use-the-github-wiki).

This guide is for a maintainer who runs the command once when adopting the
wiki, at a terminal, with `gh` authenticated and Claude Code installed.

Run every command below from one directory: the state lives in
`.planwerk-brain-sync` in the directory you run the command in.

## Count the units first

A dry run clones the repository, lists the units of its history, and stops. It
starts no Claude session and writes nothing:

```bash
planwerk-agent brain bootstrap owner/repo --dry-run
```

The first line gives the number of units. Each unit costs one analysis session,
and one review session when it proposes a page, so that number is the size of
the run.

## Try a few units

Process five units and stop:

```bash
planwerk-agent brain bootstrap owner/repo --max-units 5
```

Read the pages under `.planwerk-brain-sync/pages` and the `Usage this run:`
line before you pay for the rest. Both sessions run on `haiku` at `xhigh`. To
change the models, set the analysis with `--analysis-model` and
`--analysis-effort` and the review with `--review-model` and `--review-effort`;
`--claude-model` and `--claude-effort` do not reach either session:

```bash
planwerk-agent brain bootstrap owner/repo --max-units 5 --analysis-model sonnet --review-model opus
```

## Run the rest

Run the command without `--max-units`. It continues with the first unit that is
not processed:

```bash
planwerk-agent brain bootstrap owner/repo
```

When the run stops (a usage limit, a timeout, Ctrl-C), run the same command
again. It continues with the unit it stopped at and does not repeat the others.

## Read and edit the pages

The pages are plain Markdown files:

```text
.planwerk-brain-sync/pages/memory/<name>.md
.planwerk-brain-sync/pages/review_patterns/<name>.md
```

Edit a page in place to correct it. Delete the file of a page the run proposed
to drop that page. Deleting the file of a page the wiki already holds does not
remove it from the wiki: the command never deletes a wiki page. Do not add a
file of your own: the next run moves a file it does not know to
`.planwerk-brain-sync/orphans` and logs a warning.

The run report lists every page that differs from the wiki, with the issue,
pull request, commit, or document it came from.

## Push the pages

Create the first page of the wiki in the repository's Wiki tab if the wiki has
none yet. A wiki without a page cannot be cloned or pushed to.

Then run the command with `--write-wiki`:

```bash
planwerk-agent brain bootstrap owner/repo --write-wiki
```

The run lists every page it will write and asks for a confirmation. Answer `y`
to push them as one commit. The question needs a terminal: the command has no
`--yes`, and a run in CI or with redirected input refuses to push.

The push runs only when no unit remains. With units left, the run says how many
and pushes nothing.

## Pick up new history later

Run the same command again weeks later. It processes only the issues, pull
requests, and commits that were closed, merged, or committed since, and the
chunks of a decision document that changed:

```bash
planwerk-agent brain bootstrap owner/repo --write-wiki
```

## Read the history from the mirror

Every run lists the history from the GitHub API and fetches each issue and pull
request as its unit is processed. When you keep a
[local mirror](/how-to/mirror-a-repository) of the repository, the run can read
both from disk:

```bash
planwerk-agent brain sync owner/repo
planwerk-agent brain bootstrap owner/repo --source mirror
```

The run reads the mirror as it is and does not sync it, so it sees the history
up to the last `brain sync`. Run `brain sync` before each bootstrap run that
should pick up new history. Without a mirror the run stops and names the
command to run.

## Start over

Delete the state directory. The next run processes every unit again:

```bash
rm -rf .planwerk-brain-sync
```

## Resolve a diverged page

A page is diverged when you or a unit changed it locally and someone changed
the same page on the wiki. The run report lists it, and `--write-wiki` leaves
it out.

To resolve it, compare the wiki page with the local file and decide on one
text. Write that text to the local file and, through the wiki's web editor, to
the wiki page. The next run finds the same text in both and no longer reports
the page.

## See also

- [CLI reference: `brain bootstrap`](/reference/cli#brain-bootstrap) for every
  flag, the unit kinds, the state directory, and the output lines.
- [Mirror a repository](/how-to/mirror-a-repository) for the local mirror that
  `--source mirror` reads.
- [Use the GitHub Wiki](/how-to/use-the-github-wiki) for how sessions read the
  pages.
- [Sync the wiki](/how-to/sync-the-wiki) to prune pages that went stale later.

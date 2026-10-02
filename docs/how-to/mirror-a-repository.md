# Mirror a repository

`brain sync` copies what a repository knows on GitHub to your machine: every
issue and pull request as one Markdown file with its whole conversation, the
commit list of the default branch, and a clone of the wiki. You can then search
the project's decisions with `grep`, offline.

This guide is for a maintainer with `gh` authenticated. The command starts no
Claude session. It is not the [`sync`](/how-to/sync-the-wiki) command, which
checks the wiki's pages against the code.

## Run the first sync

```bash
planwerk-agent brain sync owner/repo
```

The first line of the output is the mirror directory. The first run fetches
every issue and pull request, one request each, so it takes a while on a large
repository:

```text
Mirror of owner/repo at /home/u/.cache/planwerk-agent/brain/owner/repo
items: 262 listed, 262 fetched, 262 in the mirror (116 issues, 146 pull requests)
history: 563 new commits, 563 in the mirror
wiki: owner/repo.wiki at 1a2b3c4
```

When the run stops (a GitHub rate limit, a network error, Ctrl-C), run the same
command again. It keeps the files it already wrote and fetches the rest.

A repository without a wiki prints `wiki: owner/repo.wiki not mirrored`. The
rest of the mirror is complete.

## Keep the mirror current

Run the same command again whenever you want the mirror up to date:

```bash
planwerk-agent brain sync owner/repo
```

It fetches only the issues and pull requests that changed since the last run,
appends the new commits, and clones the wiki again. A run right after another
one prints `items: 1 listed, 0 fetched`.

## Search the files

The files are in the directory the first output line names:

```text
issues/<number>.md
pulls/<number>.md
history.jsonl
wiki/
```

Set a variable to that path and search with the tools you know:

```bash
# The path the first output line printed
MIRROR=~/.cache/planwerk-agent/brain/owner/repo

# Every issue and pull request that mentions a phrase
grep -ril "rate limit" "$MIRROR/issues" "$MIRROR/pulls"

# The matching lines with two lines of context, review threads included
grep -rn -C 2 "wrap the error" "$MIRROR/pulls"

# The merged pull requests
grep -l "^state: merged" "$MIRROR"/pulls/*.md

# The open issues labeled "bug"
grep -l "^    - bug$" "$MIRROR"/issues/*.md | xargs grep -l "^state: open"
```

Each file opens with a frontmatter (number, title, state, author, labels,
timestamps), followed by the body, the comments, and for a pull request the
reviews, the review threads with their diff hunks, and the commit messages. See
the [item file format](/reference/cli#item-file) for every field.

The files hold the text as GitHub returns it, with any secret someone pasted
into a comment. Treat the directory like the repository's private data.

## Read the wiki's history

`wiki/` is a full git clone, so it holds every change to every page:

```bash
git -C "$MIRROR/wiki" log --oneline
git -C "$MIRROR/wiki" log -p -- memory/pin-dependencies.md
```

To mirror a pinned version of the wiki, pass `--wiki-ref`:

```bash
planwerk-agent brain sync owner/repo --wiki-ref v1
```

## Rebuild the mirror

A routine run does not remove what was deleted on GitHub: a deleted comment
stays until its issue or pull request changes again, and a deleted or
transferred issue stays. It can also miss an item that was updated while the
listing was being read. To get a mirror that matches GitHub exactly, rebuild it:

```bash
planwerk-agent brain sync owner/repo --full
```

`--full` deletes this repository's mirror and fetches everything again. It
deletes nothing before GitHub has answered a first request, so a run without a
network or with an expired token leaves the mirror as it was. Deleting the
directory by hand and running the command has the same effect: the mirror is a
copy, and nothing is lost with it.

## Bootstrap the project memory from the mirror

[`brain bootstrap`](/how-to/bootstrap-the-project-memory) reads the history
from the GitHub API on every run. With a mirror it can read the same history
from disk:

```bash
planwerk-agent brain sync owner/repo
planwerk-agent brain bootstrap owner/repo --source mirror --dry-run
```

`brain bootstrap` reads the mirror as it is and does not sync it, so run
`brain sync` first. A mirror that is missing, or in which no sync has finished,
stops the run with the command to run.

## See also

- [CLI reference: `brain sync`](/reference/cli#brain-sync) for the flags, the
  directory layout, the file format, and the output lines.
- [Bootstrap the project memory](/how-to/bootstrap-the-project-memory) for the
  run that distills the history into wiki pages.
- [Caching model](/explanation/caching) for where the mirror lives beside the
  result cache.

# Search the mirror

`brain search` ranks the issues, pull requests, and wiki pages of a mirrored
repository against a keyword query and prints the best matches first. Use it to
find the thread that settled a question when you do not know the words the
thread used.

This guide is for a maintainer who has run
[`brain sync`](/how-to/mirror-a-repository) for the repository. The command
reads the files on your machine. It starts no Claude session and calls no
GitHub API.

## Run a first search

Give the repository, then the words:

```bash
planwerk-agent brain search owner/repo one cursor
```

```text
Search of owner/repo, mirror synced 2026-10-02T09:00:00Z: 2 hits

1. issue #186 [closed] Add a brain sync subcommand
   labels: brain, feature
   comment by alice (MEMBER) on 2026-10-01T08:00:00Z, block 4 of 17
   https://github.com/owner/repo/issues/186#issuecomment-1
   id: issues/186.md:4
   ...one cursor for issues and pull requests, because updated_at rises...

2. wiki memory/one-cursor.md: One cursor
   section, block 2 of 3
   id: wiki/memory/one-cursor.md:2
   Items share one cursor...
```

Each hit is one issue, pull request, or wiki page, with the comment, review, or
section of it that matched best. A hit holds at least one of your words, and a
hit that holds more of them ranks higher, so add the words the discussion would
have used.

The first line names the time of the last sync. To search newer discussions,
run `planwerk-agent brain sync owner/repo` first: `brain search` never syncs.

## Narrow the query

Put a `*` after a word to match every word that begins with it, and double
quotes around words that must stand next to each other. Quote the whole query
for your shell:

```bash
planwerk-agent brain search owner/repo 'curs* "rate limit"'
```

Keep only the hits you want with the filters:

```bash
# Merged pull requests only
planwerk-agent brain search owner/repo wrap the error --type pull --state merged

# Open issues that carry both labels
planwerk-agent brain search owner/repo flaky test --type issue --state open --label bug --label ci

# Wiki pages only, up to 30 hits
planwerk-agent brain search owner/repo release checklist --type wiki --limit 30
```

A query word that starts with `-` is read as a flag. Put `--` before the query
to search for it, and the filters before the `--`:

```bash
planwerk-agent brain search owner/repo --type pull -- --no-cache
```

## Read a block in full

An excerpt is a fragment. Pass the `id:` of a hit to `--show` to print the whole
comment, review, or section:

```bash
planwerk-agent brain search owner/repo --show issues/186.md:4
```

```text
issue #186 [closed] Add a brain sync subcommand
comment by alice (MEMBER) on 2026-10-01T08:00:00Z, block 4 of 17
https://github.com/owner/repo/issues/186#issuecomment-1

text, 2 lines, each after "| ":
| We keep one cursor for issues and pull requests, because updated_at rises
| when a comment is edited and when a review is submitted.
```

Every line of the text follows `| `, so a comment cannot pass for the head of
another block. The block line says how many blocks the item has. To read the neighbors, change
the number after the colon: `issues/186.md:3`, `issues/186.md:5`.

A recognized secret is printed as a marker such as `[REDACTED:github-token]`.
The unredacted text is in the file under the mirror directory.

## Use the result in a script

Add `--json` to get one JSON object on one line:

```bash
# The URLs of the five best hits
planwerk-agent brain search owner/repo one cursor --limit 5 --json | jq -r '.hits[].url'

# The text of one block
planwerk-agent brain search owner/repo --show issues/186.md:4 --json | jq -r '.block.text'
```

A search without a hit prints `"hits":[]` and exits 0.

## Let the sessions search

The sessions that plan and judge can run the same search while they work. Turn
it on for one run with `--brain`:

```bash
planwerk-agent --brain owner/repo#123
planwerk-agent audit --brain owner/repo
planwerk-agent propose --brain owner/repo
planwerk-agent elaborate --brain owner/repo#42
planwerk-agent implement --brain owner/repo#42
planwerk-agent ship --brain owner/repo#40
```

Or turn it on for every run in a checkout, in `.planwerk/config.yaml`:

```yaml
brain:
  enabled: true
```

`--no-brain` turns it off for one run. The search is off by default, because the
mirror holds what everyone who can comment on the repository wrote.

A run does not sync the mirror. Run `brain sync` before the run, or the session
searches the repository as of the last sync. When the repository has no
finished mirror, the run logs a warning that names the command to run and
continues without the search.

See [Sessions that search the mirror](/reference/cli#sessions-that-search-the-mirror)
for which sessions search and what they are approved for.

## Rebuild the index

The search keeps its index in `index.sqlite` in the mirror directory and updates
it before every search. It is rebuilt from the mirrored files whenever it is
missing, so delete it to start over:

```bash
# The path the first line of brain sync printed
MIRROR=~/.cache/planwerk-agent/brain/owner/repo

rm "$MIRROR/index.sqlite"
planwerk-agent brain search owner/repo one cursor
```

`brain sync --full` deletes the index together with the mirror.

See the [`brain search` reference](/reference/cli#brain-search) for every flag,
the block kinds, and both output forms.

# Split a Meta Issue

Turn a **Meta Issue** — an issue that frames a larger body of work as several
self-contained work packages — into linked, draft-depth Sub Issues. `meta` reads
the Meta Issue, decides the breakdown on its own, files each Sub Issue, links it
to the Meta Issue with GitHub's native sub-issue relationship, and back-fills the
Meta Issue body so its work-package lines reference the freshly created Sub
Issues.

Each Sub Issue stops at draft depth, like [`draft`](/how-to/draft-an-issue)
produces — a title plus Description, Motivation, and a rough Scope. It is
deliberately **not** elaborated. Pick a Sub Issue and run
[`elaborate`](/how-to/elaborate-an-issue) / [`implement`](/how-to/implement-an-issue)
on it when you are ready; `meta` itself stops at creating and linking.

```bash
# Preview the planned split without filing or linking anything
planwerk-review meta --dry-run owner/repo#123

# Carve the Meta Issue into Sub Issues, link them, and sync the body
planwerk-review meta owner/repo#123

# Attach a label to each created Sub Issue
planwerk-review meta --label enhancement owner/repo#123

# JSON for automation
planwerk-review meta --format json --dry-run owner/repo#123
```

## Preview first

Run `--dry-run` (or its alias `--no-create`) before filing. It renders the
planned Sub Issues and the Meta body that would be written, without any GitHub
write. When the split looks right, drop the flag to file and link.

## How it works

1. **Issue input**: The command takes a Meta Issue reference (URL or
   `owner/repo#number`).
2. **Fetch issue**: The title, body, URL, and state are fetched via
   `gh issue view`.
3. **Split**: Claude reads the Meta Issue and carves it into the fewest sensible
   Sub Issues. Where the Meta Issue implies an order — a foundation package,
   numbered tiers, lettered workstreams — that structure is preserved.
4. **File**: Each Sub Issue is created in order with the house draft body
   (Category/Scope, Description, Motivation) and a footer pointing back at the
   Meta Issue.
5. **Link**: Each Sub Issue is attached to the Meta Issue via GitHub's native
   sub-issue relationship, so it appears under the Meta Issue's sub-issue list.
6. **Sync the body**: Where the Meta body carries a work-package list, each line
   is back-filled with the new `#number` reference so the prose and the
   sub-issue list agree.

## What it does not do

`meta` mirrors `draft`: it does **not** clone the repository, load review
patterns, cache, or elaborate. It also does not orchestrate the Sub Issues — it
does not drive them through `elaborate`, `implement`, or `fix`, and it does not
close the Meta Issue. Those steps stay manual and per Sub Issue.

A link failure on one Sub Issue does not abort the run: the Sub Issue is still
created and the failure is reported so you can link it by hand. The Meta body is
edited only when every reference resolves, so it is never left with a dangling
placeholder. See the [CLI reference](/reference/cli#meta) for every flag.

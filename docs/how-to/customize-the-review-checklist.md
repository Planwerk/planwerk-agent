# Customize the review checklist

Add your repository's own items to the checklist `review` works through, or
replace the checklist. The default checklist ships with the binary and walks
three passes, described in
[Review methodology](/explanation/review-methodology#three-pass-review-checklist).

## Add items

Commit Markdown files under `.planwerk/checklist.d/` on the base branch. For
example, `.planwerk/checklist.d/10-tenancy.md`:

```markdown
### Pass 1 — CRITICAL (project)

- [ ] Tenant Scoping: every query that reads or writes tenant data filters on
  the tenant ID from the request context, never on an ID the caller supplies
```

The review appends the files to the checklist it would otherwise use:

- **Order.** The files follow the checklist in file-name order, each after a
  blank line. The order compares bytes, so `9-late.md` sorts after
  `10-early.md`: zero-pad numeric prefixes (`09-`, `10-`).
- **Headings.** Open each file with a `###` heading that names its pass. Text
  without one reads as part of the last pass above it, which in the default
  checklist is Pass 3 — INFORMATIONAL.
- **What counts.** Only regular `*.md` files directly in the directory are
  read. Subdirectories, other extensions, symlinks, and empty or
  whitespace-only files are skipped.
- **Size.** The files' combined size is capped at 64 KB. The file that would
  cross the cap is skipped along with every file after it, and the run logs a
  warning that names it.

## Replace the checklist

`.planwerk/checklist.md` replaces the embedded default. A file larger than
64 KB is ignored with a warning, and an empty file falls back to the default.
The `.planwerk/checklist.d/` files still append to it.

Prefer appending unless you mean to drop default items. A replacement is a
copy, so it no longer picks up the changes a release makes to the default
checklist.

## Where the files are read from

Both are read from the pull request's base branch (`origin/<base>`, in
`--local` runs too), never from its head. A pull request that adds or edits
them is reviewed against the base's checklist, and the review reports the
change as a needs-discussion WARNING titled
`Review Configuration Changed: <file>`. The change applies to the pull requests
reviewed after it merges.

The review cache is keyed by the pull request's head commit (see
[Caching model](/explanation/caching#what-the-cache-key-is-built-from)), so
re-reviewing an unchanged head serves the cached result. Pass `--no-cache` to
apply a checklist change merged since:

```bash
planwerk-agent --no-cache owner/repo#123
```

No flag or config key is needed: committing the files is enough.

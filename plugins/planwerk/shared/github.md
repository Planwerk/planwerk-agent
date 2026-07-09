# GitHub operations

Every skill talks to GitHub through the `gh` CLI. The author is already
authenticated; if `gh auth status` fails, say so and stop rather than guessing at
a token.

## Resolving the target repository

Prefer an explicit `owner/repo` or issue reference from the skill's arguments.
Otherwise read it from the checkout:

```bash
gh repo view --json nameWithOwner --jq .nameWithOwner
```

An issue reference is either a URL
(`https://github.com/owner/repo/issues/123`), a short form (`owner/repo#123`),
or a bare `#123` / `123` when a checkout supplies the repo.

## Reading

```bash
# The issue itself
gh issue view <number> --repo <owner/repo> --json number,title,body,state,url

# Near-duplicate check before filing a new issue
gh issue list --repo <owner/repo> --search "<distinctive words from the title>" \
  --state all --limit 10 --json number,title,state,url
```

## Sub-issues and dependencies

`planwerk-agent ship` drives a Meta Issue's Sub Issues in dependency order by
reading these two native relationships straight back from GitHub. Prose in an
issue body is not a substitute — a "Blocked by: b" line is invisible to `ship`.
Both endpoints key the *parent* or *blocked* issue by its **number**, but
identify the *child* or *blocker* by its integer **database id**, which is not
the issue number. Resolve the id first.

```bash
# Resolve an issue's database id from its number
gh api repos/<owner>/<repo>/issues/<number> --jq .id

# Link a child issue under a parent (native sub-issue relationship)
gh api --method POST repos/<owner>/<repo>/issues/<parent-number>/sub_issues \
  -F sub_issue_id=<child-database-id>

# Record that <blocked-number> is blocked by <blocker-number>
gh api --method POST \
  repos/<owner>/<repo>/issues/<blocked-number>/dependencies/blocked_by \
  -F issue_id=<blocker-database-id>
```

`-F` (not `-f`) matters: the endpoints require a JSON number, and `-f` sends a
string.

Read them back with:

```bash
gh api repos/<owner>/<repo>/issues/<number>/sub_issues
gh api repos/<owner>/<repo>/issues/<number>/dependencies/blocked_by
```

Both relationships are best-effort. A GitHub deployment that does not expose
issue dependencies returns an error here. That is not fatal: the issues already
exist, so report which link could not be set and tell the author to add it by
hand. Never delete a created issue because a link failed.

## Writing

Always pass a body through a file, never through `-b "$(cat …)"` — issue bodies
contain backticks and `$` that a shell will eat.

```bash
gh issue create --repo <owner/repo> --title "<title>" --body-file <path> \
  [--label <label>]

gh issue edit <number> --repo <owner/repo> --body-file <path>

gh issue comment <number> --repo <owner/repo> --body-file <path>
```

Write the body to a temporary file first, then pass its path. `gh issue create`
prints the new issue's URL on stdout; parse the trailing number from it rather
than assuming the next number in sequence.

## Labels

Attach only labels the author asked for. This project's convention is that
issues carry no severity or priority labels.

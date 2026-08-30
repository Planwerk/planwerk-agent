# GitHub operations

Every skill talks to GitHub through the `gh` CLI. The author is already
authenticated; if `gh auth status` fails, say so and stop rather than guessing at
a token.

This file carries the commands every skill needs. Two neighbourhoods have their
own: reading an issue's parent and siblings, and wiring the relationships behind
them, are in `github-relations.md`; reading a pull request and its checks is in
`github-checks.md`.

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

# The issue's comments, where an author moves the goalposts after filing
gh issue view <number> --repo <owner/repo> --comments
```

## Referring to another repository

Anything you name that lives outside the repository the text is written to gets
its repository with it — in issue bodies, comments, commit messages, pull request
descriptions, and the report you print at the end:

| What | Form |
|------|------|
| Issue or pull request | `owner/repo#123` |
| Commit | `owner/repo@<sha>` |
| Anything else — a workflow run, a file at a ref, a discussion, a release | its URL |

A bare `#123` resolves against the repository the text ends up in, and a bare
`<sha>` against the repository the reader is browsing. Neither fails: they
silently point at something unrelated, which is why this is a rule and not a
preference. A full URL is always correct and is the only form that survives being
copied into a release note, a chat message, or another repository's issue.

Qualify by **where the text lands**, not by where you read it. A comment you post
on a Sub Issue in another repository refers to that repository's issues bare, and
to the one you came from as `owner/repo#N`. The same sentence written into your
own repository's issue reverses which side carries the prefix.

The rule holds for every reference you copy forward, too: a plan quoting a PR it
found, a report naming the commit that introduced a change, a Non-Goal handing
work to a counterpart issue. Only shorten a reference when you have confirmed
that its repository is the one the text is written to.

## Writing

Always pass a body through a file, never through `-b "$(cat …)"` — issue bodies
contain backticks and `$` that a shell will eat.

```bash
gh issue create --repo <owner/repo> --title "<title>" --body-file <path> \
  [--label <label>]

gh issue edit <number> --repo <owner/repo> --body-file <path>

gh issue comment <number> --repo <owner/repo> --body-file <path>

gh pr edit <number> --repo <owner/repo> --body-file <path>
```

Write the body to a temporary file first, then pass its path. `gh issue create`
prints the new issue's URL on stdout; parse the trailing number from it rather
than assuming the next number in sequence.

## Labels

Attach only labels the author asked for. This project's convention is that
issues carry no severity or priority labels.

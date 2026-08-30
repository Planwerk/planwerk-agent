# The Meta / Sub-Issue neighborhood

How an issue's parent, siblings and children are read, and how the two
GitHub-native relationships behind them are written — across repositories
included. Read it beside `github.md`, which carries the rest of the `gh`
commands.

## Reading the Meta / Sub-Issue neighborhood

`elaborate` and `revisit` ground a Sub Issue in the larger effort it belongs to,
so they need its parent, its siblings, and what those siblings shipped. REST
cannot answer this: `GET /issues/{n}/sub_issues` lists an issue's **children**,
never its parent. Read the whole neighborhood through GraphQL, in one call:

```bash
gh api graphql -F owner=<owner> -F name=<repo> -F number=<number> -f query='
query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    issue(number: $number) {
      number
      parent {
        number title body url state
        subIssues(first: 100) {
          nodes {
            number title body url state
            closedByPullRequestsReferences(first: 10, includeClosedPrs: true) {
              nodes { number title url state isDraft mergedAt }
            }
          }
        }
      }
      subIssues(first: 100) { totalCount nodes { number title url state } }
    }
  }
}'
```

- `parent` is `null` when the issue is not a Sub Issue.
- `parent.subIssues.nodes` are the siblings. The issue itself appears in that
  list — filter it out by number.
- A non-empty top-level `subIssues` means the issue **is** a Meta Issue.
- `closedByPullRequestsReferences` carries each sibling's linked pull requests.
  `includeClosedPrs: true` brings back the ones that already landed; a PR `state`
  of `MERGED` is the only state that shipped code, and `CLOSED` means it was
  abandoned. **A merged PR is what a sibling delivered. Its issue body is only
  what it promised.**

`planwerk-agent`'s own `GetIssueRelations` (`internal/github/relations.go`)
issues this same query, so the skills and the commands see one neighborhood.

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

## Linking across repositories

Both relationships work between repositories, which is what makes a counterpart
issue in another repository trackable (see `cross-repo.md`). The calls are the
ones above, with one rule that decides every argument:

**The URL carries the anchor issue; the body carries the other side, by database
id.** The anchor is the parent for a sub-issue link, and the blocked issue for a
dependency. So a sub-issue link is POSTed to the *parent's* repository, and a
blocked-by dependency to the *blocked* issue's repository.

The database id is global, so it identifies an issue in any repository — but it
must be resolved in the repository that issue actually lives in:

```bash
# Link acme/gadgets#58 under the Meta Issue acme/widgets#607
CHILD_ID=$(gh api repos/acme/gadgets/issues/58 --jq .id)
gh api --method POST repos/acme/widgets/issues/607/sub_issues -F sub_issue_id="$CHILD_ID"

# Record that acme/gadgets#58 is blocked by acme/widgets#607
BLOCKER_ID=$(gh api repos/acme/widgets/issues/607 --jq .id)
gh api --method POST repos/acme/gadgets/issues/58/dependencies/blocked_by -F issue_id="$BLOCKER_ID"
```

Resolving the id against the wrong repository is the mistake to watch for: issue
numbers are unique per repository, so the lookup usually succeeds and links a
different issue. It does not fail loudly.

Reading back, a returned issue belongs to its own repository, not the one you
queried. The REST dependency endpoints report each entry's `repository.full_name`
and the GraphQL query selects `repository { nameWithOwner }` per node. Use those
rather than assuming the queried repository.

Use `gh api` for this, not the `--blocked-by` flag on `gh issue create`: the flag
needs gh 2.94 or newer, while the API calls work on any version.

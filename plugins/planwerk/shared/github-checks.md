# A pull request and its checks

How a pull request, its check runs and the logs behind a failed one are read.
Read it beside `github.md`, which carries the rest of the `gh` commands.

## Reading a pull request and its checks

```bash
# The pull request itself
gh pr view <number> --repo <owner/repo> \
  --json number,title,body,url,state,headRefName,headRefOid,baseRefName

# The pull request for the branch currently checked out
gh pr view --json number,url

# Its check runs. With --json each check also carries a `bucket` field, which
# collapses `state` into pass / fail / pending / skipping / cancel.
gh pr checks <number> --repo <owner/repo>
gh pr checks <number> --repo <owner/repo> --json name,state,bucket,link,workflow

# The failed steps of an Actions-backed check, which is where the cause is
gh run view <run-id> --repo <owner/repo> --log-failed

# What the pull request changed
gh pr diff <number> --repo <owner/repo> --name-only
```

- An Actions check's `link` ends in `/runs/<run-id>/job/<job-id>`. The run id is
  the segment after `/runs/`.
- A third-party check carries no run id, so the CLI cannot fetch its logs. Open
  its `link`. When the cause is not visible there, say so — never invent one.
- CI logs cluster their errors at the end. Read each log to the bottom.

```bash
gh pr comment <number> --repo <owner/repo> --body-file <path>
```

Post a report through a file, for the same reason an issue body goes through one.

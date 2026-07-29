# Counterpart issues in other repositories

Read by `draft`, `meta` and `elaborate`. The other skills never need it.

Work in one repository often implies work in another: a change to a service's
API surface leaves its client, its Terraform provider, or its CLI out of date.
That second piece is a **counterpart**. It cannot ride along in the same pull
request, because a pull request cannot span repositories, so it needs its own
issue in the repository that owns it.

The point is to file the counterpart while the author still has the whole change
in their head, not to discover it months later when a client breaks.

## The repository map

A repository declares its counterparts in `.planwerk/related-repos.md`, beside
the other per-repo conventions (`.planwerk/STYLE_GUIDE.md`,
`.planwerk/review_patterns/`, `.planwerk/out-of-scope/`).

```markdown
# Related repositories

## acme/gadgets

When: the change alters the control-plane API surface, the registration
handshake, or the policy wire format.
Not when: the change is confined to storage, scheduling, or the admin UI.

## acme/terraform-provider-acme

When: the change adds or removes a user-facing resource type, or a field on one.
```

Each `##` heading is one counterpart repository, in `owner/repo` form. `When:`
states the condition that warrants a counterpart issue. `Not when:` is optional
and narrows it.

Read it from the checkout when there is one. Otherwise:

```bash
gh api repos/<owner>/<repo>/contents/.planwerk/related-repos.md \
  --jq .content | base64 -d
```

A repository with no map has no counterparts. That is the normal case, not an
error: say nothing about counterparts and carry on.

The map is per repository, so each side declares its own. A client repository
may name the service it consumes, for work that flows the other way.

## When to propose one

Propose a counterpart only when **both** hold:

- The map names the repository.
- The work meets that entry's `When:` condition, and does not meet its
  `Not when:`.

Never invent a counterpart for a repository the map does not name, and never
propose one "just in case". A generated client — an SDK produced by a generator
from the service's schema — needs no issue, which is why such repositories are
left out of the map rather than listed with a caveat.

When the condition is genuinely unclear, ask the author rather than guessing in
either direction. Silently skipping a counterpart is the more expensive mistake:
nobody finds it later, because nothing records that it was considered.

## What the counterpart issue says

Draft depth, always, in the house format. It describes the work **from the
counterpart repository's side** — what its users or callers need once the other
change lands — not the change that prompted it. An issue that merely says "mirror
acme/widgets#607" is unactionable to whoever picks it up.

Never elaborate a counterpart in the same run that files it. The details it would
need do not exist yet: they are settled by the plan for the originating issue.
The blocked-by edge below records exactly that order.

The counterpart's body carries one sentence naming the issue it came from,
written `owner/repo#N`. That is the general rule in `github.md` under "Referring
to another repository", and it applies to everything the body names from the
other side: the originating issue and its pull request as `owner/repo#N`, a
commit as `owner/repo@<sha>`, anything else by URL. A bare `#N` resolves against
whichever repository the text lives in, so an unqualified cross-repo reference
points at an unrelated issue. You are writing into the counterpart's repository
now, so it is the originating side that needs the prefix.

The reverse direction needs no prose: the blocked-by edge below shows on the
originating issue as a "blocking" relationship. Do not edit an already-filed body
just to add a link GitHub is already drawing.

## Wiring the two together

Both relationships are GitHub-native, and both work across repositories. See
`github.md` for the exact calls and the database-id resolution they need.

- The counterpart is **blocked by** the originating issue. That direction is the
  claim being made: the counterpart cannot be finished until the work it depends
  on lands.
- When the originating issue is a Meta Issue, the counterpart is also linked as
  one of its **sub-issues**, so the Meta Issue's progress reflects the whole
  effort rather than only the part in its own repository.

`planwerk-agent ship` reads these back. It drives one repository per run, so it
reports a Sub Issue in another repository and does not implement it. Say so when
you file one, so nobody waits for a `ship` run to deliver it.

A failed link is not a failed run: the issues already exist. Record which link
could not be set and tell the author what to add by hand.

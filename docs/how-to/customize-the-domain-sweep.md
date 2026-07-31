# Customize the domain sweep

Replace the list of domains `elaborate` and `implement`'s planning session walk
before they settle a change set, so the sweep asks about what actually breaks in
your system.

## What the sweep is

A diff review reads the code that was written. It cannot flag a rollback path
nobody planned, a metric nobody added, or a config key an existing deployment
never learns about — nothing in the diff points at any of them. Those failures
have to be caught while the change is still being designed, which is what the
sweep does.

Both planning-side prompts carry the list. The elaboration folds a touched
domain into an Acceptance Criterion or an Affected Areas entry, and its reviewer
scores whether it did. The planning session reports the result in a
`### Domain Sweep` section of the plan:

```text
### Domain Sweep
- Data & schema — adds the `retry_budget` column; commit 2 carries the migration
  and its down path, commit 3 the backfill for rows written before it.
- Operability & configuration — adds `--retry-budget` (default 3); an existing
  deployment that sets nothing keeps today's behavior.
- Not touched: Compatibility & contracts, Failure & recovery, Observability,
  Security & trust boundaries, Performance & cost
```

Every domain appears exactly once, on its own bullet or in the `Not touched:`
line. A plan that can place a domain in neither is not `PLAN_READY`.

Sweeping never widens a change. A consequence the sweep surfaces is either work
an Acceptance Criterion already requires, or it is over-scope and belongs under
`Risks & Open Questions` — where it pushes the plan toward `NEEDS_CONTEXT`
rather than into building something nobody approved.

## The default list

Seven domains ship with the binary:

| Domain | Fires when the change… |
|--------|------------------------|
| **Data & schema** | stores, migrates, or reinterprets persisted state |
| **Compatibility & contracts** | alters a signature, route, flag, config key, format, or data an older version wrote |
| **Failure & recovery** | adds a step that can fail partway through |
| **Observability** | adds behavior an operator must see succeed or fail in production |
| **Security & trust boundaries** | accepts new input, holds a new secret, reads external content, or widens what a caller may do |
| **Performance & cost** | adds work per request or per run, including paid API and token spend |
| **Operability & configuration** | adds or changes a flag, environment variable, config key, or default |

Testing and documentation are deliberately absent. The plan already carries a
`### Test Plan` and a `### Documentation Plan`, and the elaborated issue already
forces every data-flow criterion to enumerate its empty, nil, and error paths —
listing them here would split one instruction across two places.

## Overriding the list

Commit your own list at `.planwerk/domains.md` in the target repo:

```markdown
- **Tenancy** — the change reads or writes data another tenant can see. Name the
  scoping predicate and the test that proves a cross-tenant read fails.
- **Clock** — the change depends on wall-clock time or a time zone. Name the
  fixed instant the tests pin.
- **Data & schema** — the change stores, migrates, or reinterprets persisted
  state. Name the migration and its rollback path.
```

The file replaces the default list; it does not extend it. Keep the domains you
still want. One bullet per domain, each naming both the signal that says the
domain is touched and what a covering answer has to name — a domain that only
says "think about security" instructs nothing the planner can act on.

No flag is needed, and no cache key changes: the file is committed, so editing it
moves the default-branch HEAD that `elaborate` already keys its cache on.

A repo without the file runs on the default list. An empty, whitespace-only,
oversized (larger than 64 KB), or symlinked file is ignored and the default is
used instead — an override can replace the sweep, never delete it.

## Seeing the sweep in the prompt

`--print-plan-prompt` renders the planning prompt, sweep included, without
running a session:

```bash
planwerk-agent implement owner/repo#42 --print-plan-prompt
```

It renders the **embedded default**, never an override: the mode makes no clone,
so there is no checkout to read `.planwerk/domains.md` from — the same limitation
the wiki-sourced project memory has there. The sweep still renders rather than
disappearing, because it is part of what the plan must contain. To see an
override in a prompt, run `implement` for real and read the posted plan comment.

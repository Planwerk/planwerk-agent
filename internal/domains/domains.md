Walk this list once, in order. Plan the change against every domain it touches, not only against the sections the issue happened to write down.

- **Data & schema** — the change stores, migrates, or reinterprets persisted state. Name the migration and its rollback path, the backfill for rows the old code wrote, and the order against the code deploy.
- **Compatibility & contracts** — the change alters something a caller already depends on: an exported signature, an HTTP route, a CLI flag, a config key, a serialized format, or data an older version wrote. Name what must still read the old form, and until when.
- **Failure & recovery** — the change adds a step that can fail partway through. Name the concrete error, the state it leaves behind, and whether re-running is safe.
- **Observability** — the change adds behavior an operator must be able to see succeed or fail in production. Name the log line, metric, or exit code that shows it.
- **Security & trust boundaries** — the change accepts new input, holds a new secret, reads new external content, or widens what a caller may do. Name the boundary it crosses and what validates it there.
- **Performance & cost** — the change adds work per request or per run: a query, an external call, an allocation that grows with input, or paid API and token spend. Name the added cost and what bounds it.
- **Operability & configuration** — the change adds or changes a flag, environment variable, config key, or default. Name what an existing deployment must set, and what happens when it sets nothing.

# Review Pattern: Twelve-Factor App

**Review-Area**: architecture
**Detection-Hint**: Config baked into images or repos (hardcoded URLs, secrets in `application.yml`), local file writes for shared state, processes that assume single-instance state, missing graceful shutdown, dev/prod environment drift, mixed admin commands and runtime entrypoints, log files written to disk instead of stdout
**Severity**: WARNING
**Category**: design-principle
**Sources**: The Twelve-Factor App (https://12factor.net/), Twelve-Factor — Open-Source Announcement (https://12factor.net/blog/open-source-announcement), CNCF Cloud Native Definition v1.1 (https://github.com/cncf/toc/blob/main/DEFINITION.md)

## What to check

The twelve factors are a checklist for services intended to run as managed workloads (containers, PaaS, serverless). Each factor is a question the codebase must answer the same way across dev, CI, staging, and production.

### Codebase, dependencies, and config (factors I–III)
1. **One codebase, many deploys**: a single repo (or service-scoped repo) tracked in version control produces every environment. Multiple repos for the same service or environment-specific forks are a smell
2. **Explicitly declared dependencies**: every runtime dependency is declared in a manifest (`go.mod`, `pyproject.toml`, `package.json`) and isolated at build time. Implicit dependencies on system packages must be captured in the container image, not assumed
3. **Config in the environment**: secrets, endpoints, feature flags, credentials, and per-environment values are read from environment variables (or a secret manager) at startup — never committed to the repo and never compiled into binaries

### Backing services, build/release/run, processes (factors IV–VI)
4. **Backing services as attached resources**: databases, caches, queues, and external APIs are addressed by URL/credentials in config; swapping a managed Postgres for a different instance must require only a config change
5. **Strict separation of build, release, run**: the image is built once, combined with config to produce a release, and only then run. Patching code in a running container or rebuilding per environment violates this and breaks rollback
6. **Stateless processes**: processes share nothing in memory or local disk that other replicas need. Session state, uploaded files, and caches go in backing services. `/tmp` is best-effort scratch only

### Port binding, concurrency, disposability (factors VII–IX)
7. **Port binding**: the app exposes itself via a port it binds, not by being injected into a webserver. The orchestrator (k8s Service, load balancer) routes to that port
8. **Concurrency via the process model**: scale out by running more processes/replicas, not by threading within one giant process. Each process type (web, worker, scheduler) is a first-class declaration
9. **Disposability**: processes start fast (<10s ideal) and shut down gracefully on `SIGTERM` — drain in-flight work, close connections, refuse new requests. Crashes must be safe (idempotent jobs, transactional writes)

### Parity, logs, admin (factors X–XII)
10. **Dev/prod parity**: the gap between `localhost` and production is small in time (deploy continuously), personnel (developers operate what they ship), and tools (same backing services in dev as in prod via containers/test instances). SQLite-in-dev/Postgres-in-prod is the canonical anti-example
11. **Logs as event streams**: the app writes plain-text events to `stdout`/`stderr`. Aggregation, routing, and persistence are the platform's job — not the app's. No log files, no log rotation in code, no in-process log shipping
12. **Admin processes as one-off processes**: migrations, REPLs, ad-hoc scripts run in the same release artifact and environment as the long-running processes — same dependencies, same config, just a different command

## Why it matters

The twelve factors are the de-facto contract between an application and a modern compute platform (Kubernetes, ECS, Cloud Run, Heroku-likes). Each factor closes a class of operational failure: factor III prevents secret leaks from repo history; factor V is what makes rollbacks fast and unambiguous; factor IX is what allows rolling deployments without dropped requests; factor XI is what makes centralized logging possible without app changes per backend. Drift from the factors produces services that are technically running but operationally fragile — they need bespoke deploy scripts, can't scale horizontally, leak credentials, or lose work on graceful shutdown. The methodology was refreshed under open-source governance specifically to keep these properties applicable to current platforms (containers, declarative APIs, GenAI workloads). When reviewing a new service, walk the twelve and flag the ones the code answers wrong — each one is a future incident.

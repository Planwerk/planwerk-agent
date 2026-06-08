# Review Pattern: Helm Hooks and Lifecycle

**Review-Area**: quality
**Detection-Hint**: Hooks without `helm.sh/hook-weight`, missing `helm.sh/hook-delete-policy`, long-running resources used as pre-install hooks, hooks that create resources not cleaned up, test hooks outside `templates/tests/`, side-effecting hooks without idempotency
**Severity**: WARNING
**Category**: technology
**Applies-When**: helm
**Sources**: Chart Hooks (https://helm.sh/docs/topics/charts_hooks/), Chart Tests (https://helm.sh/docs/topics/chart_tests/), Charts (https://helm.sh/docs/topics/charts/)

## What to check

1. Every hook must declare a `helm.sh/hook-weight` (integer) when its ordering matters — hooks with the same weight run in manifest-sort order, which is not a safe ordering guarantee
2. `helm.sh/hook-delete-policy` should be set to control cleanup: `before-hook-creation` (default, keeps last run), `hook-succeeded`, `hook-failed`, or `before-hook-creation,hook-succeeded` for tests
3. Hooks should not create resources that outlive the release without being tracked — Helm does not manage hook resources as part of the release history by default
4. Pre-install hooks should finish quickly; long-running setup (migrations, provisioning) as pre-install delays every upgrade and blocks rollback
5. Hooks are Jobs or Pods most of the time; make them idempotent so that a retry after a partial failure converges rather than duplicates work
6. Test hooks (`helm.sh/hook: test`) must live under `templates/tests/` per convention and should be invokable via `helm test <release>`
7. `hook-delete-policy: hook-succeeded` is usually correct for tests; otherwise test Pods pile up in the namespace
8. Avoid side-effects that cannot be rolled back from `pre-rollback`/`post-rollback` hooks — rollback is already an incident-time operation

## Why it matters

Hooks are Helm's escape hatch for tasks the normal install/upgrade flow can't express (migrations, admission pre-warming, validation). But they run outside Helm's normal release tracking, so mistakes leak resources, block upgrades, or fail in ways that are hard to debug (orphaned Jobs, stuck rollouts). Explicit weights and delete policies are what make hook ordering predictable and cleanup reliable. Untested hook logic surfaces during production upgrades — typically at the worst moment.

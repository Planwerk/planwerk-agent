# Review Pattern: Helm Dependencies Management

**Review-Area**: architecture
**Detection-Hint**: Subchart dependencies without version pinning, HTTP (non-HTTPS) repository URLs, missing `condition`/`tags` on optional dependencies, `Values.global` used for application-specific state, `Chart.lock` not committed, unused subcharts pulled in
**Severity**: WARNING
**Category**: technology
**Applies-When**: helm
**Sources**: Dependencies Best Practices (https://helm.sh/docs/chart_best_practices/dependencies/), Subcharts and Globals (https://helm.sh/docs/chart_template_guide/subcharts_and_globals/), Charts (https://helm.sh/docs/topics/charts/)

## What to check

1. Every `dependencies` entry in Chart.yaml must have a version pin — either exact (`1.2.3`) or a narrow range (`~1.2.0` for patch-level updates); never use unpinned or wide ranges in production charts
2. Repository URLs must use `https://` — `http://` is a supply-chain risk and many registries no longer serve unencrypted
3. Optional subcharts should gate on `condition` (single boolean, e.g., `redis.enabled`) or `tags` (multiple charts toggled together) so users can disable them with standard value keys
4. `Chart.lock` should be committed to the repository so `helm install` uses exactly the same subchart versions across environments — running `helm dependency update` regenerates it
5. `Values.global.*` should carry only values that genuinely cross chart boundaries (registry, storage class, ingress domain) — not application-specific config that subcharts leak up
6. Parent charts should document required subchart values in their own `values.yaml` (as commented defaults) so users don't have to read subchart docs to configure them
7. Remove subchart dependencies that are no longer used — `charts/` directory bloat and unused values keys are a common maintenance burden
8. OCI registries (`oci://`) should be preferred where available; they support immutable digests and align with cluster image infrastructure

## Why it matters

Helm subcharts are the transitive-dependency system of chart deployments — and they inherit all the risks of any package system: version drift, supply-chain compromise, silent upgrades. Unpinned versions mean `helm dependency update` pulls different code at different times, producing environment drift. `Values.global` leakage couples subcharts to each other and makes extraction painful. Tracking `Chart.lock` and explicit conditions keeps chart composition predictable across environments.

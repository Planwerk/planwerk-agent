# Review Pattern: Helm Chart Testing

**Review-Area**: quality
**Detection-Hint**: Chart repository without `helm lint` in CI, no `chart-testing` (ct) integration, no `helm-unittest` coverage, charts without `templates/tests/` assets, `helm template` not exercised against multiple `--kube-version` targets
**Severity**: WARNING
**Category**: technology
**Applies-When**: helm
**Sources**: helm lint Command (https://helm.sh/docs/helm/helm_lint/), Chart Tests (https://helm.sh/docs/topics/chart_tests/), helm/chart-testing (https://github.com/helm/chart-testing), helm-unittest (https://github.com/helm-unittest/helm-unittest)

## What to check

1. `helm lint --strict --with-subcharts` should run in CI for every chart change — `--strict` promotes warnings to errors and catches convention drift early
2. `helm template` should be exercised with representative `values.yaml` variants (defaults, production overrides, HA config) against target `--kube-version` ranges
3. Charts should have unit tests via `helm-unittest` covering: default values render correctly, required values fail when missing, conditional blocks toggle as expected, helpers produce expected output
4. Chart repositories should integrate `chart-testing` (ct) in PR pipelines to detect which charts changed and run `ct lint` + `ct install` against a kind/minikube cluster
5. `templates/tests/` should contain at least one functional test hook — typically a connection test against the deployed service — invokable via `helm test <release>`
6. `values.schema.json` should be present for charts with non-trivial value shapes — it validates user inputs at install time and documents expected types
7. Snapshot tests (`helm-unittest`'s snapshot matcher) pin rendered output for regression detection; changes to templates become visible diffs during review
8. Chart CI should verify `helm package` produces a valid artifact and that `helm install --dry-run --debug` succeeds on a fresh cluster

## Why it matters

Charts are code that renders YAML; without tests they accumulate regressions silently. A templating bug that produces broken YAML is caught at install time — often in production, because nothing exercised that values combination before. Lint, unit tests, and integration tests each catch different failure modes: lint catches convention drift, unit tests pin rendered output, integration tests catch actual cluster-apply failures. Chart repositories without a testing baseline become fragile fast, and downstream consumers pay for that fragility.

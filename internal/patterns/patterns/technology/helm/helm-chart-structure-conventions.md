# Review Pattern: Helm Chart Structure Conventions

**Review-Area**: architecture
**Detection-Hint**: Chart.yaml missing `apiVersion: v2`, non-SemVer `version`, chart names with uppercase or underscores, missing `description`/`maintainers`/`home`, `kubeVersion` unpinned, non-standard directory layout
**Severity**: INFO
**Category**: technology
**Applies-When**: helm
**Sources**: Chart Conventions (https://helm.sh/docs/chart_best_practices/conventions/), Charts (https://helm.sh/docs/topics/charts/), Helm Chart Best Practices (https://helm.sh/docs/chart_best_practices/)

## What to check

1. `Chart.yaml` must declare `apiVersion: v2` — `v1` charts are legacy and lack dependency/library-chart support
2. Chart names must be lowercase, use dashes (not underscores or camelCase), and match the chart directory name
3. `version` follows SemVer strictly; `appVersion` tracks the shipped application and may use any convention but should be quoted if not SemVer
4. `kubeVersion` should be pinned to a range (e.g., `">= 1.28.0-0 < 1.35.0-0"`) so `helm install` fails early on incompatible clusters
5. Required Chart.yaml metadata: `description`, `maintainers`, `home`, `sources`, `type` (application or library), `icon` where applicable
6. Directory layout must follow convention: `templates/`, `values.yaml`, `values.schema.json` (optional), `crds/` (for CRDs installed before templates), `charts/` (for subchart dependencies), `templates/tests/` (for test hooks)
7. `.helmignore` should exclude `.git`, editor files, CI configs, and any dev-only artifacts from packaged charts
8. `NOTES.txt` under `templates/` should guide users post-install (how to access the application, where to find credentials, next steps)

## Why it matters

Chart conventions are what allow `helm install`, `helm lint`, Artifact Hub indexing, and downstream tooling (chart-testing, Renovate, Dependabot) to work without chart-specific configuration. Drift from conventions — arbitrary naming, missing metadata, non-SemVer versions — breaks discovery, upgrades, and automated maintenance. A chart that doesn't lint cleanly against conventions is a chart that won't compose well in a larger deployment pipeline.

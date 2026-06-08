# Review Pattern: Helm Labels and Annotations Standards

**Review-Area**: architecture
**Detection-Hint**: Rendered resources missing `app.kubernetes.io/*` recommended labels, `helm.sh/chart` label absent, Selectors including mutable labels (break on upgrade), hook semantics encoded in labels instead of annotations, custom labels where standard ones exist
**Severity**: INFO
**Category**: technology
**Applies-When**: helm
**Sources**: Helm Labels and Annotations (https://helm.sh/docs/chart_best_practices/labels/), Recommended Labels (https://kubernetes.io/docs/concepts/overview/working-with-objects/common-labels/), Labels/Annotations/Taints Reference (https://kubernetes.io/docs/reference/labels-annotations-taints/)

## What to check

1. Every rendered resource should carry the Kubernetes-recommended labels: `app.kubernetes.io/name`, `app.kubernetes.io/instance`, `app.kubernetes.io/version`, `app.kubernetes.io/component`, `app.kubernetes.io/part-of`, `app.kubernetes.io/managed-by`
2. `helm.sh/chart: {{ include "mychart.chart" . }}` identifies the chart + version that produced the object — critical for debugging which chart version is live
3. Selectors (`spec.selector.matchLabels` on Deployments/StatefulSets, `spec.selector` on Services) must use only immutable labels — including `app.kubernetes.io/version` in a selector breaks upgrades because the new pods have a different label and can't be selected
4. The canonical pattern: one helper for all labels (`mychart.labels`) and a separate helper for selector labels (`mychart.selectorLabels`) containing only the immutable subset
5. Hook semantics (`helm.sh/hook`, `helm.sh/hook-weight`, `helm.sh/hook-delete-policy`) belong in annotations, not labels — they are metadata for Helm, not identifiers for Kubernetes selection
6. Custom labels should be namespaced with the chart's domain (`mycompany.com/feature: enabled`), not bare names that risk collision across charts
7. Annotations are the right home for documentation pointers: `a8r.io/owner`, `a8r.io/runbook`, `a8r.io/repository` — they are queryable but don't affect selection

## Why it matters

Labels drive Kubernetes selection logic; annotations carry metadata. Confusing the two causes the common "Deployment selector is immutable" upgrade failure, where including `version` in a selector makes every chart upgrade reject until the Deployment is deleted and recreated. Recommended labels are what enable cross-chart tooling — dashboards, log aggregators, cost-allocation tools — to identify workloads without chart-specific knowledge. Consistent labeling is inexpensive to implement and compounds in value as cluster tooling grows.

# Review Pattern: Helm Values Validation

**Review-Area**: quality
**Detection-Hint**: Templates without default values, missing required value checks, hardcoded values that should be configurable
**Severity**: WARNING
**Category**: technology
**Applies-When**: helm
**Sources**: Values Best Practices (https://helm.sh/docs/chart_best_practices/values/), Values Files (Template Guide) (https://helm.sh/docs/chart_template_guide/values_files/), Charts — Schema Files (https://helm.sh/docs/topics/charts/#schema-files), Helm Best Practices (https://helm.sh/docs/chart_best_practices/), Helm Template Functions (https://helm.sh/docs/chart_template_guide/function_list/), helm lint Command (https://helm.sh/docs/helm/helm_lint/)

## What to check

1. Use `required` function for mandatory values: `{{ required "image.repository is required" .Values.image.repository }}`
2. Provide sensible defaults with `default`: `{{ .Values.replicaCount | default 1 }}`
3. Use `values.schema.json` for JSON Schema validation of values
4. Don't hardcode values that vary between environments — expose them in `values.yaml`
5. Document all values in `values.yaml` with comments explaining purpose and valid options
6. Use `{{ include }}` with named templates for reusable label and selector blocks

## Why it matters

Helm charts without validation fail silently with empty or wrong values, producing broken Kubernetes manifests that only fail at apply time. Schema validation and required checks shift errors left.

# Review Pattern: Helm Template Best Practices

**Review-Area**: quality
**Detection-Hint**: Templates without `_helpers.tpl` prefixing (generic names like `fullname` without chart prefix), missing whitespace control (`{{-`/`-}}`), `template` used where piping requires `include`, NOTES.txt absent, untidy rendered output, template logic embedded directly in manifests instead of helpers
**Severity**: WARNING
**Category**: technology
**Applies-When**: helm
**Sources**: Templates Best Practices (https://helm.sh/docs/chart_best_practices/templates/), Named Templates (https://helm.sh/docs/chart_template_guide/named_templates/), Chart Template Guide (https://helm.sh/docs/chart_template_guide/), Template Function List (https://helm.sh/docs/chart_template_guide/function_list/)

## What to check

1. Named templates in `_helpers.tpl` must be chart-prefixed: `{{ define "mychart.fullname" }}` not `{{ define "fullname" }}` — the template namespace is global and collisions break subchart composition
2. Use `include` (not `template`) when the result feeds a pipeline — `template` is an action, not a function, and cannot be piped to `nindent`, `quote`, etc.
3. Whitespace control is required at block boundaries: `{{- if .Values.foo }}` trims preceding whitespace, `{{- end -}}` trims both — missing trimming leaves empty lines that fail YAML parsers for some manifests
4. Indentation helpers (`nindent`, `indent`) belong at the top of included output, not scattered through templates; `include "mychart.labels" . | nindent 4` is the canonical shape
5. Business logic should live in helpers, not inside templates — a template file should read as a YAML shape with values and includes spliced in
6. Avoid deeply-nested `if/else` and `range` inside templates; extract branches into named templates so each has one responsibility
7. Render-time errors should use `required "message" .Values.foo` or `fail "message"` — silent empty values cause confusing downstream errors
8. `NOTES.txt` should be templated to show user-facing URLs, credentials pointers, and post-install instructions specific to the release

## Why it matters

Helm templates grow quickly from simple to tangled. Missing chart-prefixing causes collisions that only surface when the chart is used as a subchart. Missing whitespace control produces rendered YAML that parses in test but fails in production manifests with stricter parsers. Logic in templates instead of helpers makes diffing chart versions painful and concentrates complexity where it's hardest to test. Following template conventions makes charts composable and keeps reviewers focused on the manifest shape rather than the templating.

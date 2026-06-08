# Review Pattern: Helm Image Patterns

**Review-Area**: quality
**Detection-Hint**: `image.tag` defaulting to `latest` or empty, missing `image.pullPolicy` defaults, monolithic image strings instead of split registry/repository/tag, no digest-pinning option, missing `imagePullSecrets` surfacing, subchart image configuration not overridable via `global`
**Severity**: WARNING
**Category**: technology
**Applies-When**: helm
**Sources**: Helm Pods and PodTemplates Best Practices (https://helm.sh/docs/chart_best_practices/pods/), Images (https://kubernetes.io/docs/concepts/containers/images/), Bitnami Production-Ready Charts (https://techdocs.broadcom.com/us/en/vmware-tanzu/bitnami-secure-images/bitnami-secure-images/services/bsi-doc/apps-tutorials-production-ready-charts-index.html)

## What to check

1. Image configuration should be split into `image.registry`, `image.repository`, and `image.tag` (or `image.digest`) so users can override any component for mirrors or airgapped environments
2. Default `image.tag` should be a SemVer or immutable tag tracking `appVersion`, never `latest` or empty — `{{ .Values.image.tag | default .Chart.AppVersion }}` is the canonical pattern
3. `image.pullPolicy` should default to `IfNotPresent` with SemVer tags; `Always` is appropriate only for floating tags in development
4. Charts should expose an `image.digest` option so users can pin by digest for compliance/reproducibility without switching the whole image field shape
5. `imagePullSecrets` should be surfaced as a top-level chart value (e.g., `imagePullSecrets: []`) so users can attach registry credentials without editing templates
6. `global.imageRegistry` and `global.imagePullSecrets` should be honored for subchart composition — airgapped deployments override these once and have it apply throughout
7. InitContainers and sidecars must follow the same image pattern as the main container — don't hardcode helper images (e.g., `busybox:latest`)
8. Document the default images and tags in the chart's README alongside the update policy (how users learn about new versions)

## Why it matters

Chart image configuration is what determines whether a chart works in airgapped, mirrored, or strictly-controlled environments. A hardcoded `docker.io/image:latest` is unusable in enterprise contexts — users have to fork the chart or patch templates. The split registry/repository/tag/digest shape is the de-facto standard (Bitnami popularized it) because it composes with global overrides and supports both tag and digest pinning. Missing `imagePullSecrets` plumbing makes private registries impossible to use without template edits. These are small conventions with large operational impact for downstream chart consumers.

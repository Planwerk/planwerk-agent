# Review Pattern: Kubernetes Image Management

**Review-Area**: quality
**Detection-Hint**: Container images with `:latest` tag or no tag, missing `imagePullPolicy`, mutable tags used in production manifests, images from unknown or public registries without policy, missing `imagePullSecrets` for private registries, absence of digest pinning (`@sha256:...`)
**Severity**: WARNING
**Category**: technology
**Applies-When**: kubernetes, helm
**Sources**: Images (https://kubernetes.io/docs/concepts/containers/images/), Security Best Practices for Deployment (https://kubernetes.io/blog/2016/08/security-best-practices-kubernetes-deployment/), 7 Common Kubernetes Pitfalls (https://kubernetes.io/blog/2025/10/20/seven-kubernetes-pitfalls-and-how-to-avoid/), Helm Pods and PodTemplates Best Practices (https://helm.sh/docs/chart_best_practices/pods/), Nutanix K8s Anti-Patterns (https://www.nutanix.dev/2026/04/01/avoid-these-kubernetes-anti-patterns-a-guide-for-virtualization-professionals/)

## What to check

1. No container should reference `image: name:latest` or `image: name` without a tag — rollbacks become ambiguous and pod restarts can silently pull a different version
2. `imagePullPolicy` should be `IfNotPresent` with immutable tags (SemVer or digests) and `Always` only with mutable tags in development — explicit is better than the default-by-tag behavior
3. Production manifests should pin images by digest (`name@sha256:...`) or use immutable tags backed by repository-side immutability guarantees
4. Charts and manifests should split `image.registry`, `image.repository`, and `image.tag` (or `image.digest`) so mirrors and airgapped environments can override without rewriting templates
5. Pods using private registries must reference `imagePullSecrets` (or use a ServiceAccount that does) — missing secrets cause pull loops that masquerade as other problems
6. Image scanning and registry allow-lists should be enforced by an admission controller (Gatekeeper, Kyverno, ValidatingAdmissionPolicy) — reviewing every manifest for allowed registries doesn't scale
7. InitContainers and sidecars follow the same rules; don't exempt utility images from tag discipline

## Why it matters

Mutable image tags destroy deployment reproducibility: the same manifest can deploy different code depending on when pods restart. `:latest` is a migration nightmare during incidents because rolling back the Deployment generation doesn't roll back the image. Pinning by digest (or enforcing registry allow-lists) closes both the reproducibility gap and the supply-chain gap — it becomes impossible to silently swap the image binary without changing the manifest.

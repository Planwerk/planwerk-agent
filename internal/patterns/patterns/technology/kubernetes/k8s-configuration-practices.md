# Review Pattern: Kubernetes Configuration Good Practices

**Review-Area**: quality
**Detection-Hint**: YAML manifests with deprecated API versions, JSON instead of YAML, boolean values like yes/no/on/off, naked Pods without controllers, missing annotations, overly verbose specs with default values
**Severity**: WARNING
**Category**: technology
**Applies-When**: kubernetes, helm
**Sources**: Configuration Best Practices (https://kubernetes.io/docs/concepts/configuration/overview/), Configuration Good Practices (https://kubernetes.io/blog/2025/11/25/configuration-good-practices/), Recommended Labels (https://kubernetes.io/docs/concepts/overview/working-with-objects/common-labels/), 7 Common Kubernetes Pitfalls (https://kubernetes.io/blog/2025/10/20/seven-kubernetes-pitfalls-and-how-to-avoid/), K8s Anti-Patterns Field Guide (https://blog.devops.dev/kubernetes-anti-patterns-a-field-guide-to-cluster-sabotage-a0c8e8969824), Nutanix K8s Anti-Patterns (https://www.nutanix.dev/2026/04/01/avoid-these-kubernetes-anti-patterns-a-guide-for-virtualization-professionals/)

## What to check

### API Versions and Format
1. Resources must use the latest stable API version — check with `kubectl api-resources` if unsure
2. Manifests should be written in YAML, not JSON
3. Boolean values must use `true` / `false` only — never `yes`, `no`, `on`, `off` (YAML version-dependent behavior)
4. Strings that look like booleans should be quoted (e.g. `"yes"`)

### Image Tags
5. Never use `image: latest` — always pin to a specific version tag or image digest (e.g. `image: myapp:v1.2.3` or `image: myapp@sha256:...`)
6. Images in production should come from private registries with vulnerability scanning

### Manifest Hygiene
7. Avoid setting fields to their Kubernetes defaults — keep manifests minimal and easier to review
8. Group related objects (Deployment, Service, ConfigMap) in a single manifest file where sensible
9. Configuration manifests must be stored in version control — never applied ad-hoc from a local machine
10. Avoid direct `kubectl apply` against production — use GitOps workflows (Flux, ArgoCD) with Git as source of truth

### Annotations and Labels
8. Use `kubernetes.io/description` annotations to explain why a resource exists or what it does
9. Labels should follow recommended format: `app.kubernetes.io/name`, `app.kubernetes.io/version`, `app.kubernetes.io/component`, etc.

### Namespaces
11. Do not deploy all resources into the `default` namespace — use namespaces for isolation, RBAC, quotas, and organization

### Workload Controllers
12. Never create "naked" Pods without a controller — Pods do not reschedule themselves on node failure
13. Use Deployments for long-running services that should always be available
14. Use Jobs or CronJobs for tasks that should run to completion
15. Use StatefulSets (not Deployments) for stateful workloads like databases — Deployments lose pod identity and persistent storage on reschedule

### Networking
16. Create Services before the backend workloads that use them (Kubernetes injects Service env vars at Pod startup)
17. Prefer DNS-based service discovery over environment variables
18. Avoid `hostPort` and `hostNetwork: true` unless absolutely necessary — they tie Pods to specific nodes and reduce scheduling flexibility

## Why it matters

Poor manifest hygiene leads to hard-to-review configurations, silent breakage from deprecated APIs, and operational surprises. Naked Pods cause outages when nodes fail. Missing annotations make it impossible for teams to understand deployed resources without reading source code.

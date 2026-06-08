# Review Pattern: Kubernetes Workload Patterns

**Review-Area**: architecture
**Detection-Hint**: Multi-container pod specs, sidecar containers, init containers with restartPolicy Always, missing startupProbe on main container when sidecars are present, PriorityClass usage for critical workloads
**Severity**: WARNING
**Category**: technology
**Applies-When**: kubernetes, helm
**Sources**: Pods (https://kubernetes.io/docs/concepts/workloads/pods/), Deployments (https://kubernetes.io/docs/concepts/workloads/controllers/deployment/), KEP-753 Sidecar Containers (https://github.com/kubernetes/enhancements/blob/master/keps/sig-node/753-sidecar-containers/README.md), Start Sidecar First (https://kubernetes.io/blog/2025/06/03/start-sidecar-first/), Multi-Container Pods Overview (https://kubernetes.io/blog/2025/04/22/multi-container-pods-overview/), Protect Pods with PriorityClass (https://kubernetes.io/blog/2023/01/12/protect-mission-critical-pods-priorityclass/), Container Design Patterns (https://kubernetes.io/blog/2016/06/container-design-patterns/), Principles of Container App Design (https://kubernetes.io/blog/2018/03/principles-of-container-app-design/)

## What to check

### Sidecar Containers
1. Native sidecar containers (init containers with `restartPolicy: Always`) start nearly in parallel with the main app — if the main app depends on the sidecar, add a `startupProbe` on the main container that checks sidecar readiness
2. Readiness probes on sidecar containers alone do NOT prevent the main app from starting
3. Set adequate `failureThreshold` on the startup probe to allow time for sidecar initialization
4. Sidecar containers should follow one of the established patterns: sidecar (extend), ambassador (proxy), adapter (transform)

### Multi-Container Design
5. Each container in a pod should have a single, clear responsibility
6. Containers in the same pod share network namespace and volumes — use this for tightly coupled components only
7. Loosely coupled components should be in separate pods, not multi-container pods

### Eviction Protection
8. Mission-critical workloads should use `PriorityClass` to protect against eviction during resource pressure
9. Avoid setting `preemptionPolicy: Never` on high-priority classes unless explicitly intended — it prevents the scheduler from evicting lower-priority pods
10. Be cautious with very high priority values — they can starve other workloads

### Deployment Strategy
11. Use `RollingUpdate` strategy (the default) for zero-downtime deployments
12. Set `maxUnavailable` and `maxSurge` appropriate to the workload — don't rely on defaults blindly for critical services
13. Use `PodDisruptionBudget` to protect availability during voluntary disruptions (node drains, cluster upgrades)

## Why it matters

Incorrect multi-container ordering causes race conditions at startup. Missing PriorityClass means critical workloads get evicted alongside best-effort pods during resource pressure. Without PodDisruptionBudgets, cluster maintenance can take down entire services.

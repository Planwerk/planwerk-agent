# Review Pattern: Kubernetes High Availability

**Review-Area**: quality
**Detection-Hint**: Deployments with replicas=1 in production, missing PodDisruptionBudget, missing pod anti-affinity, missing PriorityClass for critical workloads
**Severity**: WARNING
**Category**: technology
**Applies-When**: kubernetes, helm
**Sources**: Deployments (https://kubernetes.io/docs/concepts/workloads/controllers/deployment/), Pod Disruptions (https://kubernetes.io/docs/concepts/workloads/pods/disruptions/), Protect Pods with PriorityClass (https://kubernetes.io/blog/2023/01/12/protect-mission-critical-pods-priorityclass/), Production Best Practices (https://learnkube.com/production-best-practices), K8s Anti-Patterns Field Guide (https://blog.devops.dev/kubernetes-anti-patterns-a-field-guide-to-cluster-sabotage-a0c8e8969824)

## What to check

1. Production Deployments should have `replicas >= 2` — a single replica means any pod disruption causes downtime
2. Use `podAntiAffinity` to spread replicas across different nodes — all replicas on one node defeats the purpose
3. Define `PodDisruptionBudget` for production workloads to protect availability during voluntary disruptions (node drains, cluster upgrades)
4. Mission-critical workloads should use `PriorityClass` to protect against eviction during resource pressure
5. `topologySpreadConstraints` should be used to distribute pods across failure domains (zones, racks)
6. HorizontalPodAutoscaler should be considered for workloads with variable load
7. Check that `minAvailable` or `maxUnavailable` in PodDisruptionBudget is set appropriately — `minAvailable: 100%` blocks all voluntary disruptions

## Why it matters

A single replica in production is a single point of failure. Without anti-affinity, multiple replicas on the same node provide no redundancy. Without PodDisruptionBudgets, cluster maintenance can take down entire services simultaneously.

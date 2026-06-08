# Review Pattern: Kubernetes Resource Limits

**Review-Area**: quality
**Detection-Hint**: Pod specs or container specs without resources.requests or resources.limits, missing CPU/memory constraints
**Severity**: CRITICAL
**Category**: technology
**Applies-When**: kubernetes, helm
**Sources**: Resource Management for Containers (https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/), Resource Quotas (https://kubernetes.io/docs/concepts/policy/resource-quotas/), The Case for Resource Limits (https://kubernetes.io/blog/2023/11/16/the-case-for-kubernetes-resource-limits/), 7 Common Kubernetes Pitfalls (https://kubernetes.io/blog/2025/10/20/seven-kubernetes-pitfalls-and-how-to-avoid/), Production Best Practices (https://learnkube.com/production-best-practices), K8s Anti-Patterns Field Guide (https://blog.devops.dev/kubernetes-anti-patterns-a-field-guide-to-cluster-sabotage-a0c8e8969824)

## What to check

1. Every container must have `resources.requests` for both CPU and memory
2. Every container must have `resources.limits` for memory (CPU limits are debatable but memory limits are mandatory)
3. Requests should not exceed limits
4. Limits should be reasonable — not `999Gi` or `1000` CPU
5. Init containers also need resource specifications
6. For maximum predictability, consider `requests = limits` (Guaranteed QoS class) for critical workloads
7. Fixed-fraction headroom (`limits = requests × 1.x`) is a good default for balancing efficiency and predictability
8. Start with modest requests (e.g. `100m` CPU, `128Mi` memory) and refine based on monitoring data

## Why it matters

Without resource limits, a single pod can starve the entire node. Without requests, the scheduler cannot make informed placement decisions, leading to overcommitted nodes and OOM kills. Pods without limits become unpredictable — their actual available resources depend on co-located pods, leading to performance variance and cascading failures during traffic surges.

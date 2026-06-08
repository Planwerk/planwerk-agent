# Review Pattern: Kubernetes Operator Lean Design and Resources

**Review-Area**: quality
**Detection-Hint**: Controllers watching high-cardinality resources (Secrets, ConfigMaps) across all namespaces without filters, missing resource requests/limits on operator pods, hardcoded resource values
**Severity**: WARNING
**Category**: technology
**Applies-When**: kubernetes
**Sources**: Operator SDK Designing Lean Operators (https://sdk.operatorframework.io/docs/best-practices/designing-lean-operators/), Operator SDK Managing Resources (https://sdk.operatorframework.io/docs/best-practices/managing-resources/), Kubebuilder Good Practices (https://book.kubebuilder.io/reference/good-practices), controller-runtime (https://github.com/kubernetes-sigs/controller-runtime)

## What to check

### Lean Cache Design
1. Never watch high-cardinality resources (Secrets, ConfigMaps, Pods) across all namespaces without filtering — this causes massive memory consumption on large clusters
2. Filter watched resources by label selectors or field selectors in the manager cache configuration
3. Be aware: filtered caches make filtered-out objects invisible to the client — requests for non-matching objects return nothing
4. Use `DefaultNamespaces` in cache options to limit watched objects to relevant namespaces in multi-tenant clusters
5. Add field indexes to the cache for frequently filtered list operations — transforms O(n) scans into O(1) lookups
6. Avoid API calls inside reconciliation loops — pre-compute data at reconciliation start or use cache-backed listers

### Resource Requests and Limits
4. Operator pods MUST declare resource requests for both CPU and memory — without requests, ResourceQuota may reject pods and the scheduler cannot optimize placement
5. Consider setting memory limits to prevent OOM scenarios and resource monopolization
6. Don't hardcode resource values — allow administrators to customize requests/limits for their environment
7. Don't set requests equal to limits — this overallocates and blocks resources from other workloads
8. Document how consumers can customize/rightsize resource values

### Operator Modesty
9. The operator itself should be modest in its requirements — it's a control plane component, not a data plane workload
10. Run as non-root unless absolutely necessary
11. Use a dedicated ServiceAccount, not the `default` one
12. Don't self-register CRDs — this requires dangerous global privileges for minimal convenience

## Why it matters

An operator watching all Secrets cluster-wide on a 500-node cluster can consume gigabytes of memory just for its informer cache. Operators without resource requests get evicted first under pressure — exactly when the cluster needs them most to manage recovery.

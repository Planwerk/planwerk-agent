# Review Pattern: Kubernetes Operator Reconciliation

**Review-Area**: quality
**Detection-Hint**: Reconcile functions with side effects on repeated calls, missing status updates, non-idempotent resource creation, missing error handling in reconcile loops
**Severity**: CRITICAL
**Category**: technology
**Applies-When**: kubernetes
**Sources**: Kubebuilder Book (https://book.kubebuilder.io/), Kubebuilder Good Practices (https://book.kubebuilder.io/reference/good-practices), controller-runtime (https://github.com/kubernetes-sigs/controller-runtime), CNCF Operator White Paper (https://tag-app-delivery.cncf.io/whitepapers/operator/), Operator SDK Common Recommendations (https://sdk.operatorframework.io/docs/best-practices/common-recommendation/), Operator SDK Best Practices (https://sdk.operatorframework.io/docs/best-practices/best-practices/)

## What to check

### Idempotency
1. The reconciliation loop MUST be idempotent — calling it multiple times with the same input must produce the same result
2. Use create-or-update (server-side apply or `CreateOrUpdate`) instead of bare Create calls that fail on conflicts
3. Avoid side effects that accumulate on each reconciliation (e.g. creating a new Job every time without checking if one exists)

### Status Management
4. Meaningful status information must be written to CRs at all times — CRs are the primary user interface
5. Use standard Status Conditions following Kubernetes conventions: `type`, `status` (True/False/Unknown), `reason`, `message`, `lastTransitionTime`
6. Status updates should use the status subresource, not the main resource endpoint

### Error Handling and Requeueing
7. Distinguish between returning errors (triggers exponential backoff) and `Requeue: true` (re-enqueues without error accounting) — use errors for genuine failures, requeue for expected polling
8. Always re-read the object from the cache at reconciliation start — never trust stale data from the event; you only get namespace and name
9. Handle API server conflicts (HTTP 409) with `retry.RetryOnConflict()` — conflicts are normal at scale, not exceptional
10. Prefer patch operations (strategic merge or server-side apply) over full object updates to reduce conflict frequency
11. Never store controller state in process memory — restarts and failovers lose it; the Kubernetes API is the only source of truth

### Resource Lifecycle
12. Use finalizers when cleanup logic is needed before resource deletion
13. Implement resource pruning for completed Jobs/Pods — object accumulation consumes etcd storage and slows the API
14. Consider MaxCount or MaxAge strategies for pruning completed resources
15. Use preDelete hooks to extract logs or metrics before pruning

### Generation Tracking
16. Controllers must stamp `status.observedGeneration` after successful reconciliation to signal they have processed the latest spec
17. Use `GenerationChangedPredicate` to filter out status-only updates — prevents reconciliation loops where status writes trigger new reconciliations

### Upgrade and Rollback
18. Upgrades must include version-specific logic (e.g. database schema migrations) — not just image tag bumps
19. Monitor upgrade progress and automatically rollback on failure (e.g. unsuccessful pod starts post-upgrade)
20. Operators must recognize and assume management of pre-existing resources seamlessly

### Backup and Recovery
21. If the operator manages stateful applications, backup and restore capabilities should be part of the CRD API
22. Backups must ensure application consistency (not just volume snapshots) and report timing, location, and status
23. Restore operations must maintain application integrity and verify restored state

### Auto-Remediation
24. Operators should detect and resolve complex failure states beyond standard Kubernetes health checks
25. Implement auto-remediation logic that goes beyond restart — e.g. re-initializing cluster members, rebuilding replicas

### Versioning and Evolution
26. Use semantic versioning for the operator itself
27. Follow Kubernetes API versioning guidelines for CRDs (v1alpha1 → v1beta1 → v1)
28. Use CRD conversion webhooks to handle older API versions when evolving APIs
29. Operators must support updating operands that were set up by an older version of the operator

## Why it matters

A non-idempotent reconciler contradicts the fundamental controller-runtime design. Resources get stuck in broken states requiring manual intervention. Without proper status, users have no visibility into what the operator is doing. Without pruning, etcd fills up and the entire cluster degrades.

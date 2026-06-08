# Review Pattern: Kubernetes Finalizer Patterns

**Review-Area**: quality
**Detection-Hint**: CRDs with finalizers added by controller but no removal path, finalizer cleanup that calls unbounded external APIs, finalizers that block deletion forever on error, missing `deletionTimestamp` check in reconcile, finalizers added before the owning controller is healthy
**Severity**: WARNING
**Category**: technology
**Applies-When**: kubernetes
**Sources**: Using Finalizers to Control Deletion (https://kubernetes.io/blog/2021/05/14/using-finalizers-to-control-deletion/), Kubebuilder Book (https://book.kubebuilder.io/), Kubernetes Patterns 2nd Edition (https://www.oreilly.com/library/view/kubernetes-patterns-2nd/9781098131678/), controller-runtime (https://github.com/kubernetes-sigs/controller-runtime)

## What to check

1. Every finalizer that a controller adds must have a corresponding removal branch — without it, resources become undeletable ("stuck in Terminating") and require manual finalizer removal
2. Cleanup work executed on `deletionTimestamp != nil` must be idempotent — the reconciler may be called many times with the same deletion state
3. External-system cleanup (cloud resources, remote APIs) must have timeouts and bounded retries; otherwise a transient external failure blocks deletion indefinitely
4. On unrecoverable cleanup error, the controller should record the failure on the resource status and eventually allow operator override (force-remove) — silently retrying forever hides the problem
5. Finalizers should be added only after the controller is committed to managing the resource; adding a finalizer in the first reconcile before validation is complete can strand resources the controller then refuses to own
6. Finalizer names should be qualified with the controller's domain (e.g., `myapp.example.com/cleanup`) to avoid collisions between controllers that both manage the same resource
7. When a controller is uninstalled, any resources it finalized become undeletable — document this and provide a cleanup path (CRD conversion, removal job) in the uninstall flow

## Why it matters

Finalizers are the right tool for cleanup coordination but easy to misuse: a single bug turns into "resources that can't be deleted" which requires manual intervention with elevated privileges. Worse, it often surfaces during incident response when operators need to clean up state quickly. Idempotency and bounded-time cleanup are not optional — the reconciler runs repeatedly, and external systems can be slow or unavailable. Kubernetes Patterns book dedicates significant coverage to this because it is one of the most common operator-authoring mistakes.

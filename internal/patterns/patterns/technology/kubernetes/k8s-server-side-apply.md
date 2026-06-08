# Review Pattern: Kubernetes Server-Side Apply

**Review-Area**: architecture
**Detection-Hint**: Controllers using `client.Update()` that may clobber fields owned by other controllers, missing `fieldManager` on patches, SSA patches with `force: true` without justification, multiple controllers fighting over the same object's fields, CI/GitOps and controllers both writing the same resource
**Severity**: INFO
**Category**: technology
**Applies-When**: kubernetes
**Sources**: Advanced Server Side Apply (https://kubernetes.io/blog/2022/10/20/advanced-server-side-apply/), Server-Side Apply (https://kubernetes.io/docs/reference/using-api/server-side-apply/), Kubebuilder Book (https://book.kubebuilder.io/), controller-runtime (https://github.com/kubernetes-sigs/controller-runtime)

## What to check

1. Controllers that share objects with other controllers or with CI/GitOps should use Server-Side Apply (SSA) so field ownership is tracked explicitly — not `Update()` which takes ownership of the whole object
2. Every SSA patch must set a stable, descriptive `fieldManager` — collisions between managers are how conflicts are detected, so the name must identify the controller reliably
3. `force: true` on SSA patches should be the exception and documented per call site — it acquires field ownership unconditionally, defeating the protection SSA provides
4. Controllers that only update status should use the status subresource and own only status fields — spec fields should remain owned by the applier (user, GitOps, or parent controller)
5. When a controller observes a `FieldManagerConflict` error, the correct response is usually to surface it (event, status condition) rather than retry with force — the conflict indicates two sources of truth
6. When converting a controller from `Update` to SSA, plan for a migration step: existing objects won't have SSA field ownership until they are patched once
7. GitOps controllers and operators that both write the same object are a common conflict source — design field ownership split up front

## Why it matters

Server-Side Apply moved out of beta years ago but is still under-used. The typical "read-modify-update" pattern means whoever writes last wins, and controllers silently overwrite each other's work. SSA makes ownership explicit at the field level, so two controllers can safely manage different fields of the same object — and conflicts surface as errors rather than silent overwrites. For operators and GitOps tools that share state, SSA is the correct primitive; `Update()` is appropriate only when the controller is the sole writer of the object.

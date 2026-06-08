# Review Pattern: Kubernetes Admission Policies

**Review-Area**: security
**Detection-Hint**: Security-critical policies enforced only in CI/Helm lint, validating webhooks doing purely declarative checks that CEL could cover, missing `ValidatingAdmissionPolicy` for PSS enforcement, CRDs without transition rules for immutable fields
**Severity**: INFO
**Category**: technology
**Applies-When**: kubernetes
**Sources**: Validating Admission Policy (https://kubernetes.io/docs/reference/access-authn-authz/validating-admission-policy/), KEP-3488 CEL Admission Control (https://github.com/kubernetes/enhancements/blob/master/keps/sig-api-machinery/3488-cel-admission-control/README.md), Validating Admission Policy GA (https://kubernetes.io/blog/2024/04/24/validating-admission-policy-ga/), Enforce CRD Immutability with CEL (https://kubernetes.io/blog/2022/09/29/enforce-immutability-using-cel/), Pod Security Standards (https://kubernetes.io/docs/concepts/security/pod-security-standards/)

## What to check

1. Policies that must hold at runtime (no privileged pods, no `hostNetwork`, required labels, image-registry allow-lists) should be enforced by cluster admission — not only by lint steps that can be bypassed by direct `kubectl apply`
2. Webhook admission controllers doing only declarative validation should migrate to `ValidatingAdmissionPolicy` (GA in v1.30): lower latency, no webhook failure modes, no separate deployment
3. `ValidatingAdmissionPolicy` + `ValidatingAdmissionPolicyBinding` replaces many use cases of Gatekeeper/Kyverno for simple rules — check whether existing policies can be expressed in CEL
4. CRDs with immutable-after-create fields should use CEL transition rules (`x-kubernetes-validations`) rather than relying on webhooks to enforce immutability
5. PSS (Pod Security Standards) baseline or restricted should be enforced cluster-wide via built-in `PodSecurity` admission at namespace level, not only documented
6. Policy bindings should specify `validationActions` (Deny, Warn, Audit) intentionally — Audit-only bindings that were meant as enforcement are a silent failure mode
7. Failure policies (`failurePolicy: Fail` vs `Ignore`) on policy bindings must match the risk profile: security-critical → Fail; best-effort annotations → Ignore

## Why it matters

Validation in CI or at Helm template time only protects the happy path — anyone with `kubectl` access can bypass it. Admission-time enforcement is the authoritative gate. Since v1.30, `ValidatingAdmissionPolicy` makes this enforcement lightweight: CEL expressions run in-process without a webhook, eliminating a class of availability and latency problems that historical admission webhooks introduced. Migrating simple policies off webhooks reduces operational surface area and increases availability of the admission path.

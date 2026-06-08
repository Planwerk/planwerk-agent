# Review Pattern: Kubernetes RBAC Practices

**Review-Area**: security
**Detection-Hint**: ClusterRoleBindings granting cluster-admin, wildcard verbs or resources in Role/ClusterRole rules, subjects bound without justification, ServiceAccount tokens auto-mounted in pods that don't call the API, verbs like `escalate`/`bind`/`impersonate` granted outside controller contexts
**Severity**: CRITICAL
**Category**: technology
**Applies-When**: kubernetes, helm
**Sources**: RBAC Good Practices (https://kubernetes.io/docs/concepts/security/rbac-good-practices/), Using RBAC Authorization (https://kubernetes.io/docs/reference/access-authn-authz/rbac/), 11 Ways Not to Get Hacked (https://kubernetes.io/blog/2018/07/18/11-ways-not-to-get-hacked/), CIS Kubernetes Benchmark (https://www.cisecurity.org/benchmark/kubernetes), NSA/CISA Hardening Guide (https://media.defense.gov/2022/Aug/29/2003066362/-1/-1/0/CTR_KUBERNETES_HARDENING_GUIDANCE_1.2_20220829.PDF), OWASP Kubernetes Top 10 (https://owasp.org/www-project-kubernetes-top-ten/)

## What to check

1. No ClusterRoleBinding should grant `cluster-admin` to a ServiceAccount, group, or user outside a narrow, documented bootstrap/admin use case — prefer least-privilege ClusterRoles
2. Avoid wildcards in `verbs`, `resources`, and `apiGroups` — `["*"]` grants unknown permissions as new APIs are added
3. Verbs `escalate`, `bind`, and `impersonate` enable privilege escalation and should only appear in tightly-scoped RBAC for controllers that genuinely need them
4. `create` on `pods/exec`, `pods/attach`, `pods/portforward`, or `secrets` in broad namespaces is a privilege-escalation vector — restrict by namespace and resourceName where possible
5. Pods that do not call the Kubernetes API should set `automountServiceAccountToken: false` (at pod or SA level) — the default auto-mount exposes a bearer token
6. ServiceAccounts should be dedicated per workload; sharing `default` or one SA across many workloads makes permission changes blast-radius too large
7. Aggregated ClusterRoles (`aggregationRule`) can grow permissions silently as labels are added — review label selectors used for aggregation
8. RoleBindings in a namespace that reference a ClusterRole grant those cluster-scoped permissions only within that namespace; check that this intentional scoping isn't bypassed by a parallel ClusterRoleBinding

## Why it matters

RBAC misconfigurations are the most common Kubernetes privilege-escalation vector. A compromised pod with an auto-mounted SA token can use whatever RBAC that SA has been granted — overly broad rules turn a single-container RCE into cluster takeover. The Kubernetes RBAC model is additive (no deny rules), so least-privilege must be enforced at authoring time. Wildcards are especially dangerous because they expand automatically when new API resources are added to the cluster.

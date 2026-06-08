# Review Pattern: Kubernetes Operator Security

**Review-Area**: security
**Detection-Hint**: Operators requesting cluster-admin or broad RBAC, missing threat model documentation, undocumented scope, lax default configurations, missing supply chain verification, operators running privileged
**Severity**: CRITICAL
**Category**: technology
**Applies-When**: kubernetes
**Sources**: RBAC Good Practices (https://kubernetes.io/docs/concepts/security/rbac-good-practices/), CIS Kubernetes Benchmark (https://www.cisecurity.org/benchmark/kubernetes), OWASP Kubernetes Top 10 (https://owasp.org/www-project-kubernetes-top-ten/), CNCF Operator White Paper (https://tag-app-delivery.cncf.io/whitepapers/operator/), 11 Ways Not to Get Hacked (https://kubernetes.io/blog/2018/07/18/11-ways-not-to-get-hacked/), Nutanix K8s Anti-Patterns (https://www.nutanix.dev/2026/04/01/avoid-these-kubernetes-anti-patterns-a-guide-for-virtualization-professionals/)

## What to check

### RBAC and Privilege Scope
1. Operators must follow least-privilege RBAC — grant only permissions necessary for the operator's function
2. "Land grab" privileges (requesting broad admin access without justification) indicate developer inexperience or deeper security weaknesses
3. Prefer namespace-scoped Roles over ClusterRoles when the operator does not need cross-namespace access
4. Use policy engines (OPA/Gatekeeper, Kyverno) to jail operator scope and enforce boundaries

### Deployment Isolation
5. Deploy operators in dedicated namespaces, separate from the applications they manage
6. Operators must not assume their deployment namespace — it should be configurable
7. Use a dedicated ServiceAccount per operator, not the `default` ServiceAccount
8. Configure SELinux, AppArmor, or seccomp policies on operator pods

### Default Configuration
9. Operators must be secure-by-default — requiring extensive manual security hardening is an anti-pattern ("lax default configuration")
10. Default RBAC scopes should be documented and minimal
11. Communication ports, API calls, and network requirements must be documented

### Supply Chain and Provenance
12. Review operator installation scripts before execution — don't blindly `kubectl apply -f` from the internet
13. Verify container image sources and check repository maintenance status
14. Check for signed images and provenance metadata (SLSA, Sigstore/cosign)
15. Assess third-party dependencies the operator pulls in

### Documentation Requirements
16. Operators should document their threat model and communication diagrams
17. RBAC scopes, required permissions, and scope (cluster-wide vs. namespace) must be explicitly documented
18. Security reporting and incident response processes should be defined
19. CVE disclosure history should be available

### Risk Assessment (for consuming third-party operators)
20. What resources does this operator create — especially Roles, RoleBindings, ClusterRoles?
21. Which third-party sources and images are required?
22. Are containers running privileged or with host sharing (hostNetwork, hostPID, hostIPC)?
23. Is the operator actively maintained with recent releases and security patches?

## Why it matters

Operators run with elevated privileges in the cluster and often have write access to critical resources. A compromised or poorly configured operator is a direct path to cluster-wide compromise. The CNCF Operator White Paper explicitly warns that "land grab" privileges and undocumented scope are leading indicators of operator security weaknesses.

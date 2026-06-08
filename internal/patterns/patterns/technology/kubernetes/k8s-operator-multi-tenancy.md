# Review Pattern: Kubernetes Operator Multi-Tenancy

**Review-Area**: security
**Detection-Hint**: Operators creating NetworkPolicies with broad allow rules, missing IngressClass configuration, operators assuming single-tenant deployment
**Severity**: WARNING
**Category**: technology
**Applies-When**: kubernetes
**Sources**: Multi-Tenancy (https://kubernetes.io/docs/concepts/security/multi-tenancy/), RBAC Good Practices (https://kubernetes.io/docs/concepts/security/rbac-good-practices/), CNCF Operator White Paper (https://tag-app-delivery.cncf.io/whitepapers/operator/), Operator SDK Multi-Tenancy Best Practices (https://sdk.operatorframework.io/docs/best-practices/multi-tenancy/)

## What to check

### NetworkPolicy
1. Operators managing NetworkPolicies must follow least-privilege: deny all traffic by default, explicitly allow only necessary connections
2. Never create "allow traffic from everywhere in the cluster" policies — this breaks tenant isolation
3. NetworkPolicies must be fine-grained: enable only internal component communication required for the managed application
4. Provide a configuration option to disable operator-managed NetworkPolicy creation entirely, so users can maintain their own policies

### Ingress and Traffic
5. Allow users to customize ingress resources through CRDs, including IngressClass specification
6. Don't assume a single IngressController — multi-tenant clusters often have multiple controllers for traffic segregation
7. Don't rely on deprecated annotation-based ingress configuration

### Namespace Awareness
8. Support namespace-scoped installation — operators should not require cluster-wide permissions unless absolutely necessary
9. Respect namespace boundaries: don't read or modify resources in namespaces the operator is not authorized for
10. Configurable namespace watching (see Operator Design pattern)

## Why it matters

Multi-tenant clusters are the norm in production. An operator that creates cluster-wide NetworkPolicy exceptions or assumes single-tenant deployment becomes a security blocker that prevents adoption in enterprise environments.

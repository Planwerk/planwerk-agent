# Review Pattern: Kubernetes TLS and Certificate Management

**Review-Area**: security
**Detection-Hint**: Certificates with localhost or 127.0.0.1 SANs, self-signed CAs without rotation, unqualified short hostname SANs, missing cert-manager integration, hardcoded certificates in manifests
**Severity**: CRITICAL
**Category**: technology
**Applies-When**: kubernetes, helm
**Sources**: Manage TLS in a Cluster (https://kubernetes.io/docs/tasks/tls/managing-tls-in-a-cluster/), cert-manager Docs (https://cert-manager.io/docs/), 11 Ways Not to Get Hacked (https://kubernetes.io/blog/2018/07/18/11-ways-not-to-get-hacked/), Certificate Anti-Patterns in Multi-Cluster K8s (https://axelspire.com/blog/localhost-is-not-an-identity-certificate-anti-patterns-in-multi-cluster-kubernetes/), Nutanix K8s Anti-Patterns (https://www.nutanix.dev/2026/04/01/avoid-these-kubernetes-anti-patterns-a-guide-for-virtualization-professionals/)

## What to check

### Subject Alternative Names (SANs)
1. Certificates must NOT use `localhost`, `127.0.0.1`, or `::1` as SANs — any pod with access to that Secret can impersonate the service
2. SANs must be fully qualified internal DNS names (e.g. `myservice.mynamespace.svc.cluster.local`) — not unqualified short hostnames
3. Certificates should be scoped to a specific service and namespace, not broadly reusable

### Certificate Authority
4. Do not use per-cluster throwaway self-signed CAs without revocation infrastructure (CRL/OCSP)
5. Prefer a centralized PKI hierarchy: offline root CA with intermediate CAs per cluster or environment
6. Use cert-manager with an external Issuer (Vault PKI, AWS Private CA, Let's Encrypt) instead of manual certificate generation
7. Self-signed certificates in production are an anti-pattern — expired certs cause cluster-wide failures

### Issuance and Lifecycle
8. Deploy certificate issuance policies (e.g. approver-policy) to reject loopback SANs and enforce naming conventions
9. Certificates must have automated renewal before expiry — manual rotation is error-prone
10. Encryption keys should be rotated regularly; cert Secrets should not be long-lived without rotation
11. TLS should be enabled between all cluster components — not just ingress

## Why it matters

A certificate with localhost SANs provides no real identity — it enables lateral movement, impersonation, and audit blindness. Per-cluster self-signed CAs without revocation create unmanageable trust sprawl. Expired certificates cause total cluster outages when API server to kubelet communication fails.

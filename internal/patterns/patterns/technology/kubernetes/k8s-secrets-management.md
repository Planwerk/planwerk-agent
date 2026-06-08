# Review Pattern: Kubernetes Secrets Management

**Review-Area**: security
**Detection-Hint**: Credentials in ConfigMaps, base64-encoded secrets in Git, hardcoded passwords in manifests, missing external secrets integration
**Severity**: CRITICAL
**Category**: technology
**Applies-When**: kubernetes, helm
**Sources**: Secrets (https://kubernetes.io/docs/concepts/configuration/secret/), External Secrets Operator (https://external-secrets.io/latest/), 11 Ways Not to Get Hacked (https://kubernetes.io/blog/2018/07/18/11-ways-not-to-get-hacked/), K8s Anti-Patterns Field Guide (https://blog.devops.dev/kubernetes-anti-patterns-a-field-guide-to-cluster-sabotage-a0c8e8969824), GitOps Anti-Patterns (https://oneuptime.com/blog/post/2026-02-26-gitops-anti-patterns/view)

## What to check

1. Never store credentials, tokens, or passwords in ConfigMaps — ConfigMaps are plain text in etcd and readable by anyone with access
2. Use Kubernetes `Secret` resources at minimum for sensitive data
3. Never commit unencrypted Secret manifests to Git — base64 is encoding, not encryption, and secrets persist in Git history permanently
4. For GitOps workflows, use Sealed Secrets or External Secrets Operator to safely manage secrets in Git
5. Prefer external secret stores (HashiCorp Vault, AWS Secrets Manager, Azure Key Vault) over native Kubernetes Secrets for production
6. Enable encryption at rest for etcd to protect Secrets stored in the cluster
7. Use RBAC to restrict who can read Secret resources — avoid broad `get secrets` permissions
8. Rotate encryption keys for etcd regularly

## Why it matters

ConfigMaps and base64-encoded Secrets provide no real protection. A single exposed admin token or leaked Git repository can expose every credential in the cluster. Secrets in Git history persist even after deletion and cannot be reliably purged.

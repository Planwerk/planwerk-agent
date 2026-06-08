# Review Pattern: Kubernetes Pod Security

**Review-Area**: security
**Detection-Hint**: Containers running as root, missing securityContext, privileged containers, missing readOnlyRootFilesystem
**Severity**: CRITICAL
**Category**: technology
**Applies-When**: kubernetes, helm
**Sources**: Pod Security Standards (https://kubernetes.io/docs/concepts/security/pod-security-standards/), Security Checklist (https://kubernetes.io/docs/concepts/security/security-checklist/), CIS Kubernetes Benchmark (https://www.cisecurity.org/benchmark/kubernetes), OWASP Kubernetes Top 10 (https://owasp.org/www-project-kubernetes-top-ten/), NSA Kubernetes Hardening Guide (https://media.defense.gov/2022/Aug/29/2003066362/-1/-1/0/CTR_KUBERNETES_HARDENING_GUIDANCE_1.2_20220829.PDF), NSA/CISA Hardening Guidance Analysis (https://kubernetes.io/blog/2021/10/05/nsa-cisa-kubernetes-hardening-guidance/), 11 Ways Not to Get Hacked (https://kubernetes.io/blog/2018/07/18/11-ways-not-to-get-hacked/), Non-root Containers and Devices (https://kubernetes.io/blog/2021/11/09/non-root-containers-and-devices/), Security Best Practices for Deployment (https://kubernetes.io/blog/2016/08/security-best-practices-kubernetes-deployment/)

## What to check

1. `runAsNonRoot: true` should be set in securityContext
2. `readOnlyRootFilesystem: true` unless the application requires writable filesystem
3. `privileged: false` (or not set) — never run privileged containers
4. `allowPrivilegeEscalation: false` should be explicit
5. Drop all capabilities and add only what is needed: `capabilities: { drop: ["ALL"], add: [...] }`
6. `hostNetwork`, `hostPID`, `hostIPC` should not be true unless absolutely required
7. Container images should be from private registries with vulnerability scanning — avoid unvetted public images in production
8. Prefer `seccompProfile: { type: RuntimeDefault }` or a custom Seccomp profile to restrict system calls
9. Consider AppArmor or SELinux profiles for additional application-level sandboxing
10. Set explicit `runAsUser` with a non-zero UID rather than relying solely on `runAsNonRoot`

## Why it matters

A container running as root with a writable filesystem is one exploit away from node compromise. Defense in depth requires minimizing the attack surface even for trusted workloads.

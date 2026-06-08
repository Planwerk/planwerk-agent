# Review Pattern: Kubernetes Network Policies

**Review-Area**: security
**Detection-Hint**: Namespaces without NetworkPolicy resources, pods communicating without explicit allow rules, missing egress policies, missing default-deny
**Severity**: WARNING
**Category**: technology
**Applies-When**: kubernetes, helm
**Sources**: Network Policies (https://kubernetes.io/docs/concepts/services-networking/network-policies/), 11 Ways Not to Get Hacked (https://kubernetes.io/blog/2018/07/18/11-ways-not-to-get-hacked/), NSA Kubernetes Hardening Guide (https://media.defense.gov/2022/Aug/29/2003066362/-1/-1/0/CTR_KUBERNETES_HARDENING_GUIDANCE_1.2_20220829.PDF), Production Best Practices (https://learnkube.com/production-best-practices), Nutanix K8s Anti-Patterns (https://www.nutanix.dev/2026/04/01/avoid-these-kubernetes-anti-patterns-a-guide-for-virtualization-professionals/)

## What to check

1. Every namespace with workloads should have a default-deny ingress NetworkPolicy
2. Egress policies should be defined to control outbound traffic — not just ingress
3. Allow rules should use label selectors, not broad CIDR ranges, for pod-to-pod communication
4. Namespace isolation: cross-namespace traffic should be explicitly allowed, not implicitly open
5. DNS egress (port 53 to kube-dns) must be allowed when using default-deny egress policies
6. NetworkPolicies require a CNI plugin that supports them (Calico, Cilium, Weave) — verify the target cluster supports enforcement
7. Policies should be as specific as possible — avoid `podSelector: {}` in allow rules unless intentional
8. Sensitive workloads (databases, secrets stores) should have the most restrictive policies

## Why it matters

Without NetworkPolicies, every pod in the cluster can communicate with every other pod by default. A compromised container can reach databases, control-plane components, and other namespaces. Network segmentation is a core defense-in-depth measure recommended by the NSA Kubernetes Hardening Guide.

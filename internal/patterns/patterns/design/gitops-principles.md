# Review Pattern: GitOps Principles

**Review-Area**: architecture
**Detection-Hint**: Direct `kubectl apply` from CI without a Git source of truth, manual cluster edits not reconciled back to a repo, drift between live state and desired state with no detection, push-based deployment from CI into the cluster, secrets or environment values that exist only in the cluster, no automatic re-reconciliation when the desired state changes
**Severity**: WARNING
**Category**: design-principle
**Sources**: OpenGitOps Principles (https://opengitops.dev/), CNCF Cloud Native Definition v1.1 (https://github.com/cncf/toc/blob/main/DEFINITION.md)

## What to check

The OpenGitOps working group defines four principles. A system is GitOps only if all four hold; partial adoption is a deployment pipeline, not GitOps.

### 1. Declarative
1. The desired state of the system is expressed declaratively — Kubernetes manifests, Helm charts, Crossplane compositions, Terraform-as-resources. Imperative scripts (`if pod missing then create`) do not qualify
2. Every operational change is a state change in the desired-state document, not a one-off command. "Restart the deployment" must become "bump the annotation that triggers a restart" in Git

### 2. Versioned and immutable
3. Desired state lives in Git (or another versioned, immutable store) — every state is committed, every change has an author and a diff
4. Rollback is a Git revert, not a manual procedure — and it must actually work because nothing else mutates cluster state
5. Tags or commit SHAs identify releases; "latest of the main branch" is acceptable in dev, not in production

### 3. Pulled automatically
6. Software agents (Argo CD, Flux, Rancher Fleet) running inside (or alongside) the target cluster pull the desired state — CI does not push `kubectl apply` from outside
7. Pull-based deployment removes the need for CI to hold cluster credentials and removes a class of supply-chain risk where compromised CI = compromised cluster
8. Pull cadence is defined and observable — operators must know how stale the cluster's view of Git can be

### 4. Continuously reconciled
9. The agent continuously compares observed state to desired state and corrects drift — manual edits in the cluster get reverted unless explicitly captured in Git
10. Drift detection is alertable: if the agent cannot reconcile (broken manifests, denied permissions, conflict with live state), oncall must hear about it
11. Multi-environment promotions (dev → stage → prod) are themselves Git operations — promotion = a commit/PR that updates an image tag or chart version in the prod overlay
12. Secrets that the cluster needs are either committed in encrypted form (Sealed Secrets, SOPS) or referenced from an external manager (External Secrets Operator). "Apply this manually with kubectl create secret" is the failure mode GitOps exists to eliminate

## Why it matters

GitOps is not a tool category — it is an operational invariant: the live system equals the latest reconciled commit, full stop. When that holds, every familiar incident class collapses. Configuration drift becomes impossible (or alertable). Audit becomes "look at the Git log". Disaster recovery becomes "point a new cluster at the same repo". Rollback is a one-line revert. When that invariant doesn't hold — when CI pushes manifests, or operators kubectl-edit hot, or secrets are created out-of-band — every advantage erodes: the cluster becomes uniquely valuable, drift accumulates silently, and recovery requires reconstruction from runbooks. The CNCF Cloud Native definition v1.1 lists declarative APIs and immutable infrastructure as foundation properties; OpenGitOps is the operating model that makes those properties achievable in practice. Reviewing a deployment design against the four principles catches the partial implementations — the ones that look like GitOps from the diagram but still rely on a human to ssh in when things break.

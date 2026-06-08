# Review Pattern: Kubernetes Supply Chain Security

**Review-Area**: security
**Detection-Hint**: Unsigned container images, no SBOM artifacts, base images not scanned for CVEs, build pipelines without provenance attestations (SLSA), admission controllers not verifying signatures, Helm charts pulled without provenance verification
**Severity**: CRITICAL
**Category**: technology
**Applies-When**: kubernetes, helm
**Sources**: CNCF Cloud Native Security Whitepaper v2 (https://github.com/cncf/tag-security/tree/main/community/resources/security-whitepaper/v2), OWASP Kubernetes Top 10 (https://owasp.org/www-project-kubernetes-top-ten/), NSA/CISA Kubernetes Hardening Guide (https://media.defense.gov/2022/Aug/29/2003066362/-1/-1/0/CTR_KUBERNETES_HARDENING_GUIDANCE_1.2_20220829.PDF), Helm Provenance and Integrity (https://helm.sh/docs/topics/provenance/), 11 Ways Not to Get Hacked (https://kubernetes.io/blog/2018/07/18/11-ways-not-to-get-hacked/)

## What to check

1. Container images consumed in production should be signed (cosign/sigstore, Notation) and signatures verified at admission — an unverified image is an untrusted image
2. SBOMs (SPDX or CycloneDX) should be generated at build time and attached to images; absence means no inventory for vulnerability response
3. Build provenance attestations (SLSA Level ≥2) should accompany images so the pipeline that produced them is verifiable
4. Base images should be scanned for known CVEs on every build and rebuilt on base-image updates; stale base images silently accumulate vulnerabilities
5. Helm charts distributed externally should be signed (`helm package --sign`) and consumers should `helm verify` — see Helm Provenance docs
6. Dependencies in charts and operator manifests (subcharts, remote CRDs) should be pulled from pinned, verified sources — never from mutable refs or unverified HTTPS URLs
7. Admission controllers (Kyverno, Gatekeeper, ValidatingAdmissionPolicy with CEL) should enforce image-origin and signature policies at deploy time, not only in CI
8. Registry allow-lists should be enforced in cluster policy so a compromised manifest cannot pull from an attacker-controlled registry

## Why it matters

Supply-chain attacks (typosquatting, dependency confusion, compromised CI) have moved from theoretical to routine. The cluster is the deployment target for every artifact that passes through the pipeline — if that pipeline can be subverted, every running workload is compromised. Verification at admission time is the last-line defense: even if CI is compromised, a signature check against a trusted key can prevent malicious images from running. The OWASP K8s Top 10 lists supply-chain vulnerabilities explicitly (K08), and NSA/CISA hardening guidance now treats image provenance as a baseline expectation.

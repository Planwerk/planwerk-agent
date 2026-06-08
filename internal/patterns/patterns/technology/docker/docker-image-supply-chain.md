# Review Pattern: Docker Image Supply Chain

**Review-Area**: security
**Detection-Hint**: `FROM` without digest, base images from unknown registries, no SBOM/provenance generation in build pipeline, missing image signing (cosign/notation), unverified `pull` of third-party images, build pipelines that don't record their inputs, OCI artifact metadata absent
**Severity**: CRITICAL
**Category**: technology
**Applies-When**: docker
**Sources**: Trusted Content (Docker Hub) (https://docs.docker.com/docker-hub/image-library/trusted-content/), Docker Build — Best Practices (https://docs.docker.com/build/building/best-practices/), OCI Image Format Specification (https://github.com/opencontainers/image-spec), OCI Distribution Specification (https://github.com/opencontainers/distribution-spec), OCI Image Layout Specification (https://github.com/opencontainers/image-spec/blob/main/image-layout.md), SLSA Specification v1.2 (https://slsa.dev/spec/v1.2/about), CISA Secure by Design (https://www.cisa.gov/securebydesign)

## What to check

### Base image trust
1. `FROM` references should resolve to one of: a Docker Official Image, a Verified Publisher image, an internally-curated mirror, or a project-controlled minimal image (distroless, Chainguard, Wolfi). Random Docker Hub accounts are not a supply chain
2. Every `FROM` should carry both a SemVer-aligned tag and a digest pin (`image:1.2.3@sha256:...`) so the build is bit-for-bit reproducible against a specific layer set
3. Base-image refresh must be a deliberate, automated job (Dependabot, Renovate, internal rebuild bot) — letting digest pins go stale silently means accumulating CVEs that no scanner reports against the running image
4. Multi-stage builders (`golang:1.23`, `node:22`) used only at build time still need pinning — a malicious builder image can backdoor binaries even if the runtime stage is clean

### Provenance and attestations
5. Builds should produce an SBOM (SPDX or CycloneDX) and attach it to the image as an OCI referrer or co-located artifact — without an SBOM, vulnerability response is guesswork
6. Builds should produce a SLSA provenance attestation describing the builder identity, source revision, and inputs (`docker buildx ... --attest type=provenance,mode=max`); aim for SLSA Build Track L2 minimum, L3 for high-risk artifacts
7. Images should be signed (cosign keyless via OIDC, or organization key) at publish time; consumers must verify signatures before pulling — an unsigned image is an untrusted image

### Registry and distribution
8. Image references in deployment manifests should use digests, not floating tags, so a registry compromise cannot retroactively swap the binary behind a stable tag
9. Registry choice and namespace policy should be enforced (allow-list, internal mirror) — the build can pin a digest, but the registry it pulls from is still part of the trust path
10. OCI image metadata should follow the spec: `org.opencontainers.image.source`, `.revision`, `.created`, `.version`, `.licenses` — registries, scanners, and audit tooling rely on these annotations
11. When publishing public images, publish to a registry that supports the OCI Distribution Specification with referrers and content discovery so SBOMs/attestations are first-class

### Build pipeline integrity
12. The CI job that builds the image should run with minimum permissions, in an isolated runner, and emit a verifiable provenance — anything else means the supply chain is gated by CI's own security posture
13. Build inputs (Go modules, npm packages, OS packages) must come from pinned, hash-verified sources; lockfiles should be committed and CI must fail on lockfile drift
14. Reusable Dockerfile fragments / build templates / shared base images should be versioned and pinned the same way third-party content is — internal trust is still trust that can be subverted

## Why it matters

A container image is a binary distribution channel. Once it runs in production, every layer is in the trust path of the workload — including the parts the developer didn't choose explicitly. Floating tags, unsigned images, and missing provenance turn every build into a leap of faith: the next pull might be the same code or might be something a typosquatter or a compromised CI account substituted. The OCI specs (image, distribution, layout) and SLSA exist because supply-chain attacks have moved from theoretical to routine — XZ Utils, malicious Docker Hub images, and dependency-confusion incidents all hit production through the same gap: nobody verified what they were running. Pinning digests, signing images, and emitting SBOM/provenance close the gap so a registry compromise or builder-image swap fails loudly instead of silently.

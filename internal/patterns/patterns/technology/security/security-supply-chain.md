# Review Pattern: Software Supply-Chain Integrity

**Review-Area**: security
**Detection-Hint**: Dependencies pulled without lockfiles, no SBOM produced at build, release artifacts not signed (GPG/cosign/sigstore), no provenance attestation, package versions floating (`^1.x`, `latest`), CI workflows that run third-party actions without SHA pinning, new direct dependencies introduced without provenance review, no SECURITY.md or vulnerability disclosure policy
**Severity**: CRITICAL
**Category**: technology
**Sources**: SLSA — Supply-chain Levels for Software Artifacts (https://slsa.dev/), SLSA Specification v1.2 (https://slsa.dev/spec/v1.2/about), CISA Secure by Design (https://www.cisa.gov/securebydesign), OpenSSF Scorecard (https://scorecard.dev/), OpenSSF Best Practices Badge (https://www.bestpractices.dev/), OpenSSF Secure Software Development Fundamentals (https://openssf.org/training/courses/), CWE — Common Weakness Enumeration (https://cwe.mitre.org/), NIST SSDF v1.1 (SP 800-218) (https://csrc.nist.gov/pubs/sp/800/218/final)

## What to check

This pattern is the cross-cutting supply-chain baseline; container-image-specific concerns live in `Docker Image Supply Chain` and the Kubernetes-specific concerns live in `Kubernetes Supply Chain Security`.

### Source integrity
1. The repository protects the trunk branch (required reviews, no force-push, no direct push by bots without approval) and ideally requires signed commits — git commits are not authenticated by default
2. Tags marking a release are themselves signed and verified by CI before downstream consumers (release pipelines, mirrors) trust them. Annotated, signed tags survive history rewrites in a way unsigned lightweight tags do not

### Dependencies
3. All dependencies are declared with lockfiles committed to the repo (`go.sum`, `package-lock.json`, `poetry.lock`, `Cargo.lock`, `requirements.txt` with hashes). Floating ranges in lockfiles undo their purpose
4. Lockfiles include integrity hashes where the format supports it (`integrity` in npm, `--require-hashes` for pip). A lockfile without hashes only pins the version, not the bytes
5. Dependency upgrades go through review like any code change — automated bots (Dependabot, Renovate) propose, humans (or audited automation) approve. Auto-merge is acceptable only for vetted maintainers and minor/patch ranges with passing tests
6. Transitive dependencies are scanned by SCA tools (govulncheck, `npm audit`, `pip-audit`, OSV) on every build; CI fails on new critical CVEs in the path
7. New direct dependencies are evaluated for: maintenance signal (recent commits, multiple maintainers), security history, license compatibility, OpenSSF Scorecard score, alternatives — and the rationale is documented

### Build and CI
8. Builds are reproducible enough that the same commit + same toolchain produces the same hash — pinned tool versions, no embedded timestamps, no random IDs in artifacts
9. CI workflows pin third-party actions/plugins by commit SHA (`uses: actions/checkout@<sha>`) — `@v3` or `@main` re-resolves silently and the published tag/branch can be moved by the upstream
10. CI secrets are scoped to the minimum job that needs them; secrets are not exposed to PRs from forks; `pull_request_target` is treated as a privileged trigger and reviewed accordingly
11. Builds emit SLSA provenance attestations — at minimum SLSA Build Track L2 (hosted, isolated builder, signed provenance), aspirationally L3 (hardened builder, isolated source). The attestation is published with the artifact

### Release artifacts
12. Release artifacts (binaries, container images, packages, charts) are signed at publish time (cosign keyless OIDC, sigstore, GPG, language-native equivalents) and signatures are verifiable by anyone consuming them
13. An SBOM (SPDX or CycloneDX) is generated at build, attached to the release, and version-controlled. Without an SBOM, vulnerability response after disclosure is guesswork
14. The release process is documented and either fully automated or documented step-by-step — ad-hoc releases by individual contributors with their personal keys do not survive personnel changes

### Vulnerability response
15. The repository has a `SECURITY.md` (and ideally a `security.txt` for runtime services) documenting the disclosure channel, the SLA for triage, and the supported versions
16. CVEs and security advisories are tracked by ID; fixes reference the CVE/CWE so downstream consumers can map advisories to fixed versions. GitHub Security Advisories or equivalent is the canonical record
17. End-of-life policy is published — which versions get security patches and for how long. Customers running unsupported versions know they are unsupported

### Continuous improvement
18. OpenSSF Scorecard runs against the repo on a cadence; failing checks become tracked work items, not silent metadata
19. For OSS projects, consider the OpenSSF Best Practices Badge (Passing/Silver/Gold) as a self-assessment checklist that surfaces gaps in process, testing, and disclosure

## Why it matters

The supply chain is every step between source code and a running artifact: source, dependencies, build, package, distribute. Every step is a trust boundary, and every trust boundary has been the source of high-impact incidents — SolarWinds (build pipeline), event-stream (npm dependency), CodeCov (CI integration), XZ Utils (long-game maintainer takeover), confused deputies inside CI workflows. SLSA exists to make these boundaries explicit and verifiable: at L1 you know what was built, at L2 you know who built it, at L3 you know that builder is hardened. The CISA Secure by Design pillars and NIST SSDF practices converge on the same controls — pinned dependencies, signed artifacts, SBOMs, provenance attestations, vulnerability response — because the alternative is paying the same incident cost the industry has already paid many times. Reviewing the supply chain at PR time catches the gaps while they are still configuration changes; once an artifact has shipped without provenance, the gap is permanent for that release.

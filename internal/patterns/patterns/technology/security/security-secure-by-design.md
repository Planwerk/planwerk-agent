# Review Pattern: Secure by Design and Default

**Review-Area**: security
**Detection-Hint**: Insecure defaults that require opt-in to harden (TLS optional, auth optional, default credentials, permissive CORS), security knobs documented in "advanced configuration" rather than enabled out of the box, no threat model for new features, security findings from CI ignored or suppressed, no documented secure SDLC, dependencies introduced without provenance review, hardening shipped only as documentation
**Severity**: WARNING
**Category**: technology
**Sources**: CISA Secure by Design (https://www.cisa.gov/securebydesign), NIST SSDF v1.1 (SP 800-218) (https://csrc.nist.gov/pubs/sp/800/218/final), NIST SSDF Project Home (https://csrc.nist.gov/projects/ssdf), NIST SP 800-218 Rev. 1 (Draft, Dec 2025) (https://csrc.nist.gov/pubs/sp/800/218/r1/ipd), NIST SP 800-218A — SSDF for GenAI (https://csrc.nist.gov/pubs/sp/800/218/a/final), OWASP ASVS (https://owasp.org/www-project-application-security-verification-standard/), OWASP SAMM (https://owaspsamm.org/)

## What to check

### Secure by default (CISA principle)
1. Security-relevant features ship enabled, not opt-in. TLS, authentication, audit logging, MFA hooks, signed updates, sensible CORS — the default configuration is the secure configuration
2. Hardening is a feature of the product, not a 200-page hardening guide users must read. If a deployment can be insecure by leaving the defaults alone, the defaults are wrong
3. Default credentials, sample accounts, demo data, and example configurations are absent in production builds — or, if present, expire on first use and force rotation

### Secure by design (CISA principle)
4. Security requirements (authn/z, input validation, secret handling, logging) are part of the design from the start, not bolted on. New features touching trust boundaries (auth, payment, PII, multi-tenancy) carry a threat model — STRIDE / attack trees / data-flow review
5. Memory-safe languages are the default for new components; reaching for C/C++ requires justification (interop, performance, embedded constraints) and additional safeguards (sanitizers, fuzzing, tight reviews)
6. The principle of least privilege is enforced at every layer: process users, file modes, RBAC roles, IAM policies, network egress. New components enumerate the permissions they need, not "admin to be safe"

### NIST SSDF v1.1 (SP 800-218) — practice mapping
7. **Prepare the Organization (PO)**: security requirements are defined per project; secure development training exists; toolchains are vetted. Does the team know what "secure" means for this product?
8. **Protect the Software (PS)**: source integrity (signed commits, protected branches), dependency provenance (lockfiles, signed releases, SBOMs), build reproducibility. Can someone tamper with the artifact between source and prod undetected?
9. **Produce Well-Secured Software (PW)**: threat modeling, secure coding standards, code review, automated testing (SAST, DAST, SCA, fuzzing), reuse of vetted components. Is security verification embedded in CI, not an afterthought?
10. **Respond to Vulnerabilities (RV)**: vulnerability disclosure policy (security.txt, SECURITY.md), CVE/CWE tracking, patch SLAs, post-incident review, customer notification process. When a vuln is reported, does the team have a defined response path?

### Verification (OWASP ASVS / SAMM)
11. Map the codebase or service to an ASVS level (L1 baseline, L2 standard, L3 high-value) and identify the controls that level requires; gaps become findings or accepted risks with sign-off
12. SAMM provides the maturity dimensions (governance, design, implementation, verification, operations) — use it to assess where the program is and where the next improvement should land

### GenAI-specific (NIST 800-218A)
13. AI/ML components face additional risk surfaces: training-data provenance and integrity, model supply chain, prompt-injection defenses, evaluation for unsafe outputs, monitoring of foundation-model dependencies. NIST SP 800-218A extends SSDF for these
14. Treat foundation models as third-party dependencies — pin versions, track provenance, evaluate against the use case, plan for replacement when a vendor sunsets a model

### Cross-cutting
15. CI security findings are not silently suppressed — every suppression carries a justification, owner, and expiration. Backlogs of "low-severity" findings rot into incidents
16. Security architecture decisions are documented (ADRs, design docs) so future contributors inherit the rationale, not just the outcome. "Why is this hardened" is as important as "how is it hardened"

## Why it matters

CISA's Secure by Design / Secure by Default initiative and NIST's Secure Software Development Framework are the two reference standards U.S. federal procurement, EU regulation (Cyber Resilience Act), and most enterprise security teams use to evaluate vendor products and internal services. They are deliberately complementary: NIST SSDF defines the practices an organization must perform; CISA Secure by Design defines the properties the resulting software must exhibit. Both exist because the alternative — security as a documentation deliverable, security knobs as opt-in features, vulnerabilities as a customer problem to configure around — has produced the steady stream of high-impact incidents that drove the policy response. Reviewing changes against these frameworks catches the design-level decisions (defaults, privilege boundaries, dependency provenance, threat surfaces) that determine the security posture of the product more than any individual line of code.

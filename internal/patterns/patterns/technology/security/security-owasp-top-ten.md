# Review Pattern: OWASP Top 10 Web Application Risks

**Review-Area**: security
**Detection-Hint**: Authorization checks missing or inconsistent across handlers, hardcoded secrets, weak/legacy crypto (MD5, SHA-1, DES, ECB), reliance on client-side validation only, raw SQL string concatenation, deserializing untrusted input, vulnerable/outdated dependencies, no integrity verification on artifacts, no logging on auth events, swallowed exceptions, server-side requests built from user input
**Severity**: CRITICAL
**Category**: technology
**Sources**: OWASP Top 10:2025 (https://owasp.org/Top10/2025/), OWASP Top 10 — Cheat Sheet Index (https://cheatsheetseries.owasp.org/IndexTopTen.html), OWASP Cheat Sheet Series (https://cheatsheetseries.owasp.org/), OWASP API Security Top 10 (2023) (https://owasp.org/API-Security/editions/2023/en/0x00-header/), CWE Top 25 Most Dangerous Software Weaknesses (https://cwe.mitre.org/top25/)

## What to check

The OWASP Top 10:2025 is the canonical web-application risk list. Walk every category against the diff; each line below is a category title with the concrete checks that flag it.

### A01 Broken Access Control
1. Every endpoint enforces authorization server-side based on the authenticated subject — never on a client-supplied role/ID. Object-level checks (does this user own this resource ID) are present on every read, write, and delete
2. Default-deny: routes added without an explicit auth requirement should fail closed (404/401), not open. Test horizontal access: can user A access user B's data by guessing IDs?
3. Admin/internal endpoints are not reachable from the public network surface; if they are, they require step-up auth

### A02 Cryptographic Failures
4. Use modern algorithms only: AES-GCM/ChaCha20-Poly1305 for symmetric, RSA-OAEP-2048+ or ECDSA P-256+ / Ed25519 for asymmetric, SHA-256+ for hashes, Argon2id/scrypt/bcrypt for passwords. MD5, SHA-1, DES, 3DES, RC4, ECB mode are findings on sight
5. Secrets are not hardcoded, committed, or logged. They come from a secret manager or environment, are rotated, and have a documented blast radius
6. TLS 1.2+ everywhere; HSTS on web endpoints; certificate validation is never disabled in production code

### A03 Injection
7. SQL: parameterized queries / prepared statements only. String concatenation or formatting with user input is a finding. ORMs are safe only if used with bind variables, not their string-fragment escape hatches
8. OS commands: pass arguments as a list, never as a shell string. If shell features are required, allow-list the input
9. Template injection (Jinja, Go text/template), LDAP, XPath, NoSQL: same rule — parameterize, don't interpolate
10. Cross-site scripting (XSS) is in this category in the 2025 edition: encode output by context (HTML body, attribute, JS, URL); use frameworks that auto-escape; deploy a Content-Security-Policy

### A04 Insecure Design
11. Design-level threats are addressed before implementation: threat model exists for new features touching auth, money, or PII. Misuse cases are tested, not just happy paths
12. Limits exist for things that can be abused — rate limits, body-size limits, recursion depth, allocation budgets

### A05 Security Misconfiguration
13. Default credentials, default accounts, sample apps, and test endpoints are removed from production builds
14. Stack traces, debug pages, and verbose error messages are not exposed to clients (see API error format pattern)
15. Security headers are present on web responses: `Content-Security-Policy`, `Strict-Transport-Security`, `X-Content-Type-Options`, `Referrer-Policy`, `Permissions-Policy`. CORS allow-list is explicit, not `*`

### A06 Vulnerable and Outdated Components
16. Dependencies are pinned, scanned (SCA), and updated on a documented cadence. Lockfiles committed. CI fails on critical CVEs in the dependency tree
17. End-of-life components (unsupported framework versions, deprecated libraries) are flagged for replacement

### A07 Identification and Authentication Failures
18. See API authentication pattern. Specifically: no plaintext password storage, no credential stuffing without rate limit/lockout, MFA available for sensitive accounts, session fixation prevented (regenerate session ID on login), no `alg: none` JWTs

### A08 Software and Data Integrity Failures
19. Deserializing untrusted data into language objects (Java `ObjectInputStream`, Python `pickle`, PHP `unserialize`) is forbidden
20. Update channels and CI artifacts are signed and verified — see security-supply-chain pattern. Auto-update mechanisms must validate signatures before applying

### A09 Security Logging and Monitoring Failures
21. Authentication events, authorization failures, and high-impact actions emit security-relevant log events with subject, action, resource, outcome, and time
22. Logs do not contain secrets, full session tokens, or PII beyond what is necessary for audit
23. Anomaly detection / alerting hooks off these events — silent failures are findings

### A10 Mishandling of Exceptional Conditions (new in 2025)
24. Errors are caught only where the code can do something useful with them; otherwise propagate. Empty `catch` blocks, swallowed exceptions, and `defer recover()` that does nothing are findings
25. Exceptional paths (timeouts, partial failures, retries, cancellations) have explicit handling — including the case where retry doubles a side effect
26. Resource cleanup runs on every path — `defer`, `finally`, `using`, context managers — so panics and early returns do not leak file descriptors, transactions, or locks

### Cross-cutting
27. The OWASP Cheat Sheet Series has a per-topic deep dive for every category — when a finding is uncertain, the relevant cheat sheet is the canonical reference
28. For HTTP APIs (REST/GraphQL), apply the OWASP API Security Top 10:2023 in addition — BOLA, Broken Authentication, Broken Object Property Level Authorization, Unrestricted Resource Consumption, Broken Function Level Authorization, Unrestricted Access to Sensitive Business Flows, SSRF, Security Misconfiguration, Improper Inventory Management, Unsafe Consumption of APIs

## Why it matters

The OWASP Top 10 is the most widely-adopted shorthand for web-application security risk. Every category in the 2025 edition is supported by a CVE database, a CWE family, and a long history of breach disclosures — these are not theoretical concerns, they are the failure modes that produce the actual incidents the industry sees year after year. A01 (Broken Access Control) has been #1 across multiple editions for a reason: every team writes its own authorization, and most of them get it wrong somewhere. The 2025 edition's new A10 (Mishandling of Exceptional Conditions) reflects the increasing share of incidents traced to error-path bugs — `catch (Throwable e) { /* ignore */ }` is the line that turns a recoverable failure into a silent data-corruption event. Reviewing changes against the Top 10 is not exhaustive security review, but it covers the categories that produce the highest-impact incidents per unit of review effort.

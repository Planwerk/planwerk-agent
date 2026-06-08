# Review Pattern: API Authentication and Authorization

**Review-Area**: security
**Detection-Hint**: Hand-rolled token formats, JWTs without algorithm pinning, missing `aud`/`iss`/`exp`/`nbf` validation, OAuth 2.0 flows confused with OAuth 1.0/OpenID Connect, bearer tokens accepted from query strings, long-lived access tokens, scopes encoded as opaque strings without server-side validation, no token revocation path, basic auth in production, secrets compared without constant-time
**Severity**: CRITICAL
**Category**: technology
**Sources**: RFC 6749 — OAuth 2.0 Authorization Framework (https://www.rfc-editor.org/rfc/rfc6749.html), RFC 9068 — JWT Profile for OAuth 2.0 Access Tokens (https://www.rfc-editor.org/rfc/rfc9068.html), OWASP API Security Top 10 (2023) (https://owasp.org/API-Security/editions/2023/en/0x00-header/), OWASP Cheat Sheet Series (https://cheatsheetseries.owasp.org/), OpenAPI Specification 3.2.0 (https://spec.openapis.org/oas/v3.2.0.html)

## What to check

### Authentication scheme
1. Use OAuth 2.0 (RFC 6749) for delegated access and OpenID Connect for end-user authentication; do not invent custom token issuance flows. Authorization Code with PKCE is the default for public clients; Client Credentials for machine-to-machine; Device Authorization Grant (RFC 8628) for input-constrained devices
2. The Resource Owner Password Credentials grant is deprecated — flag any service still using it and provide a migration path to Authorization Code + PKCE
3. Implicit grant is deprecated for browser apps — use Authorization Code + PKCE
4. HTTP Basic Auth is acceptable only over TLS, only for low-value endpoints, and only with credentials stored as salted hashes (not plaintext config). Bearer tokens (`Authorization: Bearer ...`) are the default for everything else

### Token handling
5. Bearer tokens are accepted ONLY in the `Authorization` header (RFC 6750) — never from query strings (they leak into logs, browser history, and Referer headers) and never from URL paths
6. JWT access tokens follow RFC 9068: `typ: "at+jwt"`, required claims (`iss`, `exp`, `aud`, `sub`, `client_id`, `iat`, `jti`), `alg` pinned to a non-`none` algorithm with the key resolved from the issuer's JWKS
7. Validate every JWT claim explicitly: `iss` matches the trusted authorization server, `aud` matches this resource, `exp`/`nbf` against current time with bounded clock skew, `alg` matches the expected key type. A missing claim or `alg: none` is rejection, not parsing
8. Access tokens are short-lived (minutes); refresh tokens are long-lived but rotated on use and bound to a client. Sessions/tokens have a defined revocation path (introspection endpoint per RFC 7662, or a denylist with TTL ≥ token lifetime)
9. Token storage on the client follows platform best practice — secure cookie with `HttpOnly; Secure; SameSite` for browser apps; OS keychain for native; never `localStorage` for high-value tokens

### Authorization
10. Authorization is enforced server-side on every request — never trust a client-provided role or ID. The token's subject (`sub`) is the only authoritative caller identity
11. Scopes are documented, finite, server-validated, and minimally privileged — `read:orders` and `write:orders` separately, not a single `admin` scope. Token introspection or claim inspection enforces the scope on each endpoint
12. Object-level authorization (BOLA / OWASP API1) is enforced for every resource access: the caller's `sub` must own or be granted access to the requested object ID. Tests must cover horizontal access attempts (caller A reading caller B's resource)
13. Sensitive operations (account deletion, scope grant, payment) require step-up authentication — re-prompt, MFA, or a fresh-auth claim on the token

### Defense in depth
14. Compare bearer tokens, signatures, and HMACs with constant-time comparison (`hmac.Equal` in Go, `crypto.timingSafeEqual` in Node) — `==` leaks length and prefix on rejection
15. All auth-bearing endpoints are over TLS 1.2+ with HSTS; downgrade to HTTP redirects to HTTPS, never serves the API
16. Rate-limit authentication endpoints (login, token refresh, OTP) per identifier and per IP — credential stuffing and password spray rely on uncapped retries. Lock or alert on suspicious patterns
17. Log authentication outcomes (success and failure) with user/client ID, IP, and user agent — but never log tokens, passwords, or secrets

### Specification
18. The OpenAPI document declares a `securitySchemes` block (`oauth2`, `openIdConnect`, or `bearer` JWT) with the supported flows, scopes, and the issuer URL — clients depend on this to drive token acquisition
19. Document the public keys / JWKS URL, token endpoint, and supported scopes in machine-readable form (`/.well-known/openid-configuration` or `/.well-known/oauth-authorization-server`)

## Why it matters

Authentication and authorization are the two controls that distinguish "this resource is mine" from "this resource is the internet's". Every category of failure here ranks at the top of the OWASP API Top 10 for a reason: BOLA (Broken Object Level Authorization), Broken Authentication, Broken Function Level Authorization. The standards (OAuth 2.0, RFC 9068, OpenID Connect) exist precisely because every team that rolls its own token format, scope language, or refresh mechanism converges on the same bugs — `alg: none`, missing audience checks, scope confusion, tokens in query strings, predictable IDs that authorize themselves. Reviewing auth code against the RFCs and OWASP cheat sheets catches these failures while they are local fixes, before they become breach disclosures.

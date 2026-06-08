# Review Pattern: HTTP API Error Format (Problem Details)

**Review-Area**: quality
**Detection-Hint**: Per-endpoint or per-service error envelopes (`{"error": "..."}` vs. `{"message": "..."}` vs. `{"errors": [...]}`), HTTP status codes that disagree with the body's success flag, internal exception traces leaking to clients, error responses without a stable machine-readable code, no `Content-Type: application/problem+json`, no documented error catalog
**Severity**: WARNING
**Category**: technology
**Sources**: RFC 9457 — Problem Details for HTTP APIs (https://www.rfc-editor.org/rfc/rfc9457.html), RFC 9110 — HTTP Semantics (https://www.rfc-editor.org/rfc/rfc9110.html), Zalando RESTful API Guidelines (https://opensource.zalando.com/restful-api-guidelines/), Microsoft REST API Guidelines (https://github.com/microsoft/api-guidelines)

## What to check

### Format
1. Error responses use `application/problem+json` (RFC 9457, which obsoletes RFC 7807) with the documented members: `type` (a URI identifying the problem class), `title` (short human-readable summary), `status` (the HTTP status code as integer, mirroring the response), `detail` (human-readable specifics for this occurrence), `instance` (a URI for this specific failure)
2. The `type` URI should be stable, dereferenceable, and documented — it is the machine-readable error code consumers branch on. `https://errors.example.com/validation` is fine; `urn:problem:validation` is fine; an unstable internal class name is not
3. Extension members are allowed and encouraged for typed details (`invalid-fields: [...]`, `retry-after-seconds: 30`) — document them in the OpenAPI schema for the error type

### Consistency
4. The HTTP status code MUST equal the `status` member; the response status MUST be an error code (4xx/5xx) — never 200 with `success: false`
5. One error envelope shape is used across the entire API. Mixed shapes (`{"error": "..."}` here, `{"errors": [...]}` there, plain text 500s elsewhere) force every consumer to write per-endpoint error parsing
6. Validation errors should aggregate per-field problems in a single response (RFC 9457 extension: `invalid-params: [{"name": "...", "reason": "..."}]`) so clients can render all messages at once

### Content
7. `detail` is for humans and may include parameter values, but MUST NOT leak secrets, stack traces, internal hostnames, or query plans. Security-sensitive errors (auth failures, ratelimits) collapse to a generic message — detail goes to logs, not the response
8. `instance` is a URI (often `/requests/{requestId}`) — clients pass it to support tickets so the server can correlate to the log entry. Logs MUST include the same identifier
9. Unhandled exceptions (uncaught panics, 500s from frameworks) MUST be wrapped into the standard problem format before reaching the client — a stack trace in production is a leak

### Catalog and documentation
10. The set of `type` URIs is a finite catalog documented in the API reference; new error types are added through the same review process as new endpoints. Throwing freshly-invented types per branch produces an unreviewable contract
11. Error types are stable — they may be deprecated and replaced (with `Sunset` headers per RFC 9110) but never silently change semantics
12. The OpenAPI document declares the problem schema once and references it from every operation's error responses (`$ref: '#/components/responses/Problem'`)

### Observability
13. Every error response has a server-side log entry at the appropriate level (`warn` for client errors, `error` for server errors) tagged with the `instance`/request ID and the `type` — alerting hooks off the type, not the status code
14. Repeating client errors of the same type are a sign of a contract drift between client and server; surface a per-type rate metric

## Why it matters

Error responses are the part of an API consumers interact with most under stress — exactly the moment when a clean, predictable contract matters most. When error envelopes vary by endpoint, when status codes disagree with bodies, when a 500 shows a Java stack trace, every team that integrates with the API writes their own brittle parsing and their own internal documentation. RFC 9457 (formerly 7807) is one of the few widely-implemented standards that solves this once: a single content type, a stable JSON shape, machine-readable codes, and documented extension points. Adopting it costs nothing in a greenfield API and pays back every time a consumer wants to branch on a specific failure or open a support ticket with a usable identifier. Reviewing error handling against this baseline also catches the security-sensitive leaks (stack traces, internal IDs, validation messages that confirm account existence) that quietly accumulate as code grows.

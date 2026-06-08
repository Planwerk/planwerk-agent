# Review Pattern: HTTP Semantics and Caching

**Review-Area**: quality
**Detection-Hint**: Custom status codes outside RFC 9110, missing `Content-Type`/`Accept` negotiation, no `Cache-Control` on cacheable responses, conditional requests (`If-Match`, `If-None-Match`, `ETag`, `Last-Modified`) absent, idempotency violations on `PUT`/`DELETE`, `Vary` missing where responses change with `Accept`/`Accept-Encoding`/auth, hand-rolled HTTP parsers
**Severity**: WARNING
**Category**: technology
**Sources**: RFC 9110 — HTTP Semantics (https://www.rfc-editor.org/rfc/rfc9110.html), RFC 9111 — HTTP Caching (https://www.rfc-editor.org/rfc/rfc9111.html), RFC 9112 — HTTP/1.1 (https://www.rfc-editor.org/rfc/rfc9112.html), RFC 9113 — HTTP/2 (https://www.rfc-editor.org/rfc/rfc9113.html), RFC 9114 — HTTP/3 (https://www.rfc-editor.org/rfc/rfc9114.html), HTTPWG Specifications Index (https://httpwg.org/specs/)

## What to check

### Method semantics
1. `GET`/`HEAD` MUST be safe — no side effects, idempotent, cacheable. Endpoints that mutate state on `GET` (visit counters, redirect-with-effect) violate the contract and break crawlers, prefetchers, and proxies
2. `PUT` MUST be idempotent: PUT-ing the same resource twice is identical to once. Servers that allocate new IDs on `PUT` are using the wrong method
3. `DELETE` MUST be idempotent and return `204` (or `200`/`202`) consistently — the second `DELETE` of an already-deleted resource is success or `404`, never `500`
4. `POST` is the catch-all for non-idempotent operations; if the same `POST` must be safe to retry, support an `Idempotency-Key` header (Stripe-style) and document the deduplication window

### Status codes
5. Use the registered status codes from RFC 9110 — never invent custom 6xx/7xx codes. The line between `400` (malformed) and `422` (well-formed but semantically wrong) is a project decision; pick one and apply it
6. `201 Created` carries a `Location` header pointing at the new resource; `202 Accepted` carries a way to poll the async operation; `303 See Other` is for "redirect after POST" patterns
7. `401 Unauthorized` means "authenticate"; `403 Forbidden` means "authenticated but not allowed". Returning `404` instead of `403` to hide existence is a deliberate trade-off and must be documented
8. `429 Too Many Requests` carries `Retry-After`; `503 Service Unavailable` similarly tells the client when to come back

### Headers and content negotiation
9. `Content-Type` MUST be set on every response with a body, including errors. Charset is `utf-8` for text/JSON
10. `Accept` from the client picks the representation; servers respond with `406 Not Acceptable` if no match exists (or `415` for unsupported request bodies)
11. `Vary` MUST list every request header whose value affects the response — typically `Accept`, `Accept-Encoding`, `Authorization`, and any cookie used for content. Missing `Vary` produces cross-user cache poisoning
12. Avoid leaking implementation details via headers (`Server: nginx/1.x`, `X-Powered-By`) — they only help attackers fingerprint

### Caching (RFC 9111)
13. Mark cacheable responses with explicit `Cache-Control` (`public, max-age=300`, `private, no-store`, `no-cache, must-revalidate`) — the default heuristics differ across caches
14. Provide validators on cacheable resources: `ETag` (strong or weak) and/or `Last-Modified`; honor `If-None-Match` / `If-Modified-Since` and return `304 Not Modified` to save bandwidth
15. Mutating endpoints respond with `no-store` for sensitive data and `no-cache` (revalidate) for state that changes often
16. CDN-tier caching uses `s-maxage` and `Cache-Control: public`; consider `stale-while-revalidate` and `stale-if-error` (RFC 5861) for resilience

### Conditional and concurrency control
17. `PUT`/`DELETE` on shared resources should accept `If-Match` (with the prior `ETag`) so concurrent writers don't clobber each other; respond with `412 Precondition Failed` when the validator doesn't match
18. Long-running operations expose a polling endpoint (`GET /operations/{id}`) instead of holding the connection — clients should never need to keep a TCP connection open for minutes

### Wire format
19. Don't hand-roll HTTP parsing or framing — use a maintained library that follows RFC 9112/9113/9114. Header injection, smuggling, and chunked-encoding bugs in custom parsers are recurring CVE territory
20. HTTP/2 and HTTP/3 are wire formats over the same RFC 9110 semantics — application code should not depend on the version. Push, server-sent events, and streaming responses each have specific semantics; verify the framework supports them correctly

## Why it matters

HTTP semantics is the contract every cache, proxy, CDN, framework, and client library agrees on. When that contract is violated — `GET` with side effects, `200` carrying an error, missing `Vary` — the failures are nondeterministic and hard to reproduce: a CDN serves the wrong response to user B because user A's request set a cookie nobody listed in `Vary`, a retry doubles a charge because the endpoint pretended to be `POST` while documented as `PUT`. RFC 9110 is the canonical reference (it consolidates and supersedes RFC 7230–7235); RFC 9111 governs caching specifically. Reviewing handlers against the RFCs catches the issues that won't show up in unit tests but will surface as cross-customer cache pollution, double-spend bugs, or retries that fail because the server treated them as new requests.

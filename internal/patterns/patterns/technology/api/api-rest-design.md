# Review Pattern: REST API Design

**Review-Area**: architecture
**Detection-Hint**: HTTP handlers exposing JSON, OpenAPI/Swagger specs, controllers/routers under `api/` `routes/` `handlers/` `controllers/`, RPC-style endpoints (`/getUser`, `/doThing`), inconsistent path/parameter casing, missing `openapi.yaml`, breaking changes shipped without versioning, mixed concerns in one endpoint, response shapes that drift between endpoints
**Severity**: WARNING
**Category**: technology
**Sources**: OpenAPI Specification 3.2.0 (https://spec.openapis.org/oas/v3.2.0.html), OpenAPI Specification 3.1.0 (https://spec.openapis.org/oas/v3.1.0.html), OpenAPI Initiative (https://www.openapis.org/), Zalando RESTful API Guidelines (https://opensource.zalando.com/restful-api-guidelines/), Microsoft REST API Guidelines (https://github.com/microsoft/api-guidelines), Google API Design Guide (https://cloud.google.com/apis/design), Google AIP — API Improvement Proposals (https://google.aip.dev/)

## What to check

### Resource modeling
1. URLs identify resources, not actions. `/users/42/orders/7` is good; `/getUserOrder?u=42&o=7` and `/processOrderRefund` are RPC dressed in HTTP. Imperative endpoints are appropriate as named sub-resources only when an action does not map to a state mutation (`POST /orders/7:cancel` per AIP-136)
2. Resource names are plural nouns in lowercase (`/orders`, `/api-keys`); collections are plural, items are addressed by ID (`/orders/{orderId}`). Pick one convention (kebab-case vs. snake_case for path segments) and apply it consistently across the API
3. Sub-resources express containment (`/users/{id}/orders`); cross-cutting relations use top-level resources with filters (`/orders?userId={id}`) — not nested aliases that duplicate routing

### HTTP method semantics
4. `GET` is safe and idempotent (no side effects, never used for state change), `PUT` and `DELETE` are idempotent, `POST` is for create/non-idempotent actions, `PATCH` is for partial updates with a defined media type (JSON Merge Patch RFC 7396 or JSON Patch RFC 6902). RFC 9110 is normative
5. `HEAD` and `OPTIONS` should behave correctly — never reuse them for business logic
6. The same operation should not be reachable via multiple methods or routes — pick one and document it

### Status codes
7. 2xx for success with the correct flavor: `200 OK` for retrieval, `201 Created` with a `Location` header for creates, `202 Accepted` for async, `204 No Content` for empty bodies
8. 4xx for client faults: `400` validation, `401` no/invalid auth, `403` authenticated but forbidden, `404` not found, `409` conflict, `422` semantic validation when distinguishing from `400`, `429` rate-limit
9. 5xx only for server-side faults; never `500` for client validation errors
10. Status code conveys the outcome — never `200` with `{"success": false}`. RFC 9110 status codes are the contract

### Request/response shape
11. Request and response bodies are JSON (RFC 8259) with a documented content type (`application/json`). Versioning of the response shape is via the API version (URL or header), not by sniffing client headers
12. Field names are consistent (camelCase or snake_case — pick one project-wide); enums are documented with their valid values; timestamps are ISO 8601 / RFC 3339 in UTC; IDs are strings unless numeric arithmetic is meaningful
13. Collection responses use a wrapped envelope with pagination metadata (`{"items": [...], "nextPageToken": "..."}`) — clients must not be expected to count or guess. Cursor pagination beats offset pagination for stability
14. Filtering, sorting, and field-selection use documented query parameters (`?filter=`, `?orderBy=`, `?fields=`) — ad-hoc combinations of query params per endpoint produce an unbrowsable API

### Specification and evolution
15. The API surface is described by an OpenAPI 3.1 or 3.2 document checked into the repo, generated as part of CI, and used to drive client SDKs/contract tests. A "spec" that lives only in Confluence will drift
16. Breaking changes (renamed fields, removed endpoints, type narrowing) bump the API version and ship with a deprecation period; the response sets `Deprecation` and `Sunset` headers (per IETF deprecation drafts) on legacy paths
17. Optional fields are added without a version bump; removing or repurposing fields is breaking and must be versioned. Consumers depend on the absence as well as the presence of fields
18. Reference an established style guide (Zalando, Microsoft, Google AIPs) instead of inventing conventions per service — picking a baseline and citing exceptions beats per-service drift

## Why it matters

A REST API is a contract with consumers who cannot easily change in lockstep with the server. Every inconsistency — mixed casing, status codes that lie about the outcome, undocumented endpoints, breaking changes without versioning — translates directly into client bugs, broken SDKs, and incident-grade outages when assumptions about the contract turn out to be wrong. The OpenAPI spec, the IETF HTTP RFCs, and the major vendor style guides exist because the same design mistakes recur across organizations and the costs of fixing them at v1.0 are an order of magnitude lower than at v3.7. Reviewing API design against this baseline catches the issues that are nearly free to fix in a PR and prohibitively expensive once external integrations exist.

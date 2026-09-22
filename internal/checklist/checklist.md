## Review Checklist (work through systematically)

### Pass 1 — CRITICAL (always check these)

- [ ] SQL & Data Safety: raw queries, missing parameterization, unsafe migrations, string interpolation in SQL (even if values are cast to int/float — use parameterized queries)
- [ ] Race Conditions: shared mutable state, missing locks, concurrent map access, TOCTOU races (check-then-set patterns that should be atomic WHERE + UPDATE), find-or-create without unique DB index
- [ ] Error Handling: swallowed errors, missing nil checks, panic-worthy paths, error handling that silently drops context
- [ ] Security: hardcoded secrets, injection vectors, auth/authz gaps, unsafe HTML rendering (html_safe, dangerouslySetInnerHTML, v-html) on user-controlled data
- [ ] Input Validation: unvalidated user input at system boundaries, missing format validation on structured data
- [ ] LLM Output Trust Boundary: LLM-generated values (emails, URLs, names) written to DB or passed to mailers without format validation; structured tool output accepted without type/shape checks before database writes
- [ ] Crypto & Entropy: weak RNG for security-sensitive values (use crypto/rand, not math/rand), truncation of data instead of hashing, non-constant-time comparisons on secrets or tokens, deprecated algorithms

### Pass 2 — SEMANTIC (requires tracing beyond the diff)

- [ ] Enum & Value Completeness: when the diff introduces a new enum value, status string, tier name, or type constant — trace it through EVERY consumer. Read each file that switches on, filters by, or displays that value. Check allowlists/filter arrays containing sibling values. Check case/if-elsif chains for fallthrough to wrong default.
- [ ] Conditional Side Effects: code paths that branch on a condition but forget to apply a side effect on one branch (e.g. item promoted but URL only attached conditionally). Log messages that claim an action happened but the action was conditionally skipped.
- [ ] Type Coercion at Boundaries: values crossing language/serialization boundaries where type could change (numeric vs string), hash/digest inputs that don't normalize types, timezone-naive timestamps at API boundaries, JSON parse of user input without schema validation
- [ ] Test Coverage for New Code: every added or significantly modified function, method, or module needs the test types the project already uses (unit, integration, E2E). Apply the Test Completeness section below the checklist, which carries the conventions to search for and the finding titles.
- [ ] Documentation Completeness: every new public API, CLI flag, configuration option, or user-facing behavior change needs its documentation. Apply the Documentation Completeness section below the checklist.
- [ ] Dependency Freshness & Maintenance: every newly introduced dependency is current, maintained, and not deprecated. Apply the Dependency Freshness section below the checklist, which says how to look each fact up.

### Pass 3 — INFORMATIONAL

- [ ] Magic Numbers: unexplained numeric literals used in multiple files, config that should be externalized as named constants
- [ ] Dead Code: unused functions, unreachable branches, commented-out code, variables assigned but never read
- [ ] Test Quality: untested error paths, missing edge cases, negative-path tests that assert type/status but not side effects
- [ ] Performance & Bundle Impact: N+1 queries (missing eager loading), unbounded allocations, missing pagination, known-heavy dependencies (moment.js, lodash full), large static assets committed (>500KB), synchronous script tags without async/defer
- [ ] API Contract: breaking changes to public interfaces without versioning, missing backward compatibility
- [ ] View/Frontend: unescaped user content in templates, missing loading/error states, accessibility regressions, inline style blocks in partials (re-parsed every render)
- [ ] Time Window Safety: date-key lookups that assume "today" covers 24h, mismatched time windows between related features (one uses hourly buckets, another uses daily keys)

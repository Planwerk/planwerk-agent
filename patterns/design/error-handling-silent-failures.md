# Review Pattern: Silent Failures and Hidden Errors

**Review-Area**: reliability
**Detection-Hint**: Catch-all blocks (`catch (Exception)`, `except:`, `.catch(() => {})`, empty `catch {}`), bare `||`/`??` fallbacks that swallow errors, optional chaining (`?.`) on values that should always be present, ignored return-error tuples, retry-on-error without bounding or logging
**Severity**: WARNING
**Category**: design-principle
**Sources**: OWASP Top 10:2025 — A10 Mishandling of Exceptional Conditions (https://owasp.org/Top10/2025/), Google SRE Book — Handling Overload (https://sre.google/sre-book/handling-overload/), Joel Spolsky — Making Wrong Code Look Wrong (https://www.joelonsoftware.com/2005/05/11/making-wrong-code-look-wrong/), Release It! (Michael Nygard) — Stability Patterns

## What to check

1. Every catch-all / broad `except` / `.catch` MUST narrow to specific error types or re-raise. If the handler keeps a wide net, the diff MUST justify why that net is correct.
2. For every fallback (`||`, `??`, default values, retry-on-error, returning empty collections), verify the fallback path is documented AND covered by a test. An undocumented, untested fallback is a silent mask.
3. Every swallowed error MUST be logged with sufficient context to debug from logs alone: operation name, key inputs, error type, and stack/cause where applicable. `console.error(e)` / `log.Print(err)` without context is insufficient.
4. Optional chaining (`?.`) on a value the domain guarantees to be present is a silent null-pointer mask, not defensive programming. Either tighten the type or fail loudly.
5. **Hidden Errors enumeration** — for each broad handler, the reviewer MUST list the concrete error classes that could be silently absorbed (network timeouts, schema mismatches, permission errors, OOM, deserialization errors, partial writes, etc.). Flag the finding with a "Hidden Errors:" subsection naming each.
6. Ignored return values from fallible operations (Go: `_ = …`, JS: `void promise`, Python: bare `try: … except: pass`) — flag unless the discard is justified inline.

## Why it matters

Silent failures are the single most expensive class of production incidents: the system keeps running with subtly wrong state until a downstream component (or user) notices. By the time the alert fires, the original cause is buried under hours of derived data and cache layers. Naming the hidden error types up front turns an invisible failure mode into a deliberate decision — either the reviewer accepts the trade-off explicitly, or the handler gets narrowed.

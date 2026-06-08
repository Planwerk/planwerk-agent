# Review Pattern: Go Testing Patterns

**Review-Area**: quality
**Detection-Hint**: Tests without subtests (`t.Run`), duplicated setup code, helpers without `t.Helper()`, cleanup with `defer` instead of `t.Cleanup()`, independent subtests not running with `t.Parallel()`, repeated test assertions that should be table-driven
**Severity**: INFO
**Category**: technology
**Applies-When**: go
**Sources**: Go Wiki — TableDrivenTests (https://go.dev/wiki/TableDrivenTests), Using Subtests and Sub-benchmarks (https://go.dev/blog/subtests), Uber Go Style Guide (https://github.com/uber-go/guide/blob/master/style.md), Dave Cheney — Practical Go (https://dave.cheney.net/practical-go/presentations/gophercon-singapore-2019.html)

## What to check

1. Tests exercising multiple inputs should be table-driven: a `[]struct{name, input, want}` slice iterated with `t.Run(tc.name, ...)` — not copy-pasted test functions with minor variations
2. Each subtest must get a descriptive `name` field; avoid generic names like "case 1" that don't aid debugging when one subtest fails
3. Independent subtests should call `t.Parallel()` at the top of the subtest body — but only when there is no shared mutable state between iterations
4. Test helper functions that call `t.Fatal`/`t.Error` must start with `t.Helper()` so failures report the caller's line number, not the helper's
5. Resource cleanup should use `t.Cleanup(fn)` rather than `defer fn()` — it runs after subtests complete and composes correctly across helpers
6. Loop variable capture: when using `t.Parallel()` in a table-driven loop, the loop variable must be shadowed (`tc := tc`) before Go 1.22, or the test will observe only the last iteration's value
7. Prefer `testing.T.Context()` (Go 1.24+) or explicit context passing over `context.Background()` inside tests so cancellation propagates

## Why it matters

Table-driven tests with subtests are the idiomatic Go testing structure and unlock `-run TestFoo/specific_case` for targeted debugging, per-subtest pass/fail reporting, and safe parallelism. Missing `t.Helper()` sends developers to the wrong source line during failures. Missing `t.Parallel()` leaves CI time on the table for large test suites. These conventions compound: a well-structured test suite is easier to extend, and mistakes in the pattern propagate across the codebase.

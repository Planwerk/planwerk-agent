# Review Pattern: Go Context Propagation

**Review-Area**: quality
**Detection-Hint**: Functions accepting context.Context not as first parameter, `context.Background()` used inside request handlers, context not passed through call chains
**Severity**: WARNING
**Category**: technology
**Applies-When**: go
**Sources**: Go Blog: Contexts and structs (https://go.dev/blog/context-and-structs), Go Code Review Comments (https://go.dev/wiki/CodeReviewComments#contexts), Google Go Style Guide — Decisions (https://google.github.io/styleguide/go/decisions#contexts)

## What to check

1. `context.Context` should be the first parameter of a function, named `ctx`
2. Don't store `context.Context` in structs — pass it explicitly through function calls
3. `context.Background()` should only appear in `main()`, top-level initialization, or tests — never inside request handlers
4. Cancellation must propagate through the entire call chain to be effective
5. `context.TODO()` signals incomplete work — flag it in new code

## Why it matters

Broken context propagation means cancellation signals don't reach downstream calls. This leads to wasted work after client disconnects and resources held beyond their useful lifetime.

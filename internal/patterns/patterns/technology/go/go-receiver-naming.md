# Review Pattern: Go Receiver and Naming Conventions

**Review-Area**: quality
**Detection-Hint**: Method receivers named `this` or `self`, inconsistent receiver names across methods of the same type, stuttering names like `user.UserName`
**Severity**: INFO
**Category**: technology
**Applies-When**: go
**Sources**: Go Code Review Comments (https://go.dev/wiki/CodeReviewComments#receiver-names), Effective Go (https://go.dev/doc/effective_go#names), Google Go Style Guide — Decisions (https://google.github.io/styleguide/go/decisions#receiver-names)

## What to check

1. Receiver names should be short (1-2 letter abbreviation of the type), not `this` or `self`
2. Receiver name must be consistent across all methods of the same type
3. Avoid stuttering: `user.Name` not `user.UserName`, `http.Client` not `http.HTTPClient`
4. Exported names should not repeat the package name: `log.Info()` not `log.LogInfo()`
5. Acronyms should be all-caps: `HTTPClient`, `ID`, `URL` — not `HttpClient`, `Id`, `Url`

## Why it matters

Consistent naming is core to Go's readability philosophy. The standard library sets the convention — deviations create friction for every reader.

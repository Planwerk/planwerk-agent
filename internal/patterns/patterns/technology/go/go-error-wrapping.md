# Review Pattern: Go Error Wrapping

**Review-Area**: quality
**Detection-Hint**: Bare `return err` without fmt.Errorf wrapping, error messages starting with uppercase or ending with punctuation
**Severity**: WARNING
**Category**: technology
**Applies-When**: go
**Sources**: Effective Go (https://go.dev/doc/effective_go#errors), Go Code Review Comments (https://go.dev/wiki/CodeReviewComments#error-strings), Working with Errors in Go 1.13 (https://go.dev/blog/go1.13-errors), errors package (https://pkg.go.dev/errors), Google Go Style Guide — Best Practices (https://google.github.io/styleguide/go/best-practices#error-handling)

## What to check

1. Every error returned from a function should provide context via `fmt.Errorf("doing X: %w", err)`
2. Error messages must start lowercase and not end with punctuation (Go convention)
3. Use `%w` (not `%v`) to preserve the error chain for `errors.Is`/`errors.As`
4. Avoid `errors.New()` when wrapping an existing error — use `fmt.Errorf` with `%w`

## Not a finding

- On a line the change adds, an error string that ends with punctuation when `.golangci.yml` enables revive's `error-strings` or staticcheck's `ST1005`, and one that starts with a capital letter when it enables `ST1005`, provided a CI workflow runs golangci-lint on pull requests: that job reports it on this change. Revive's `error-strings` reports the capital only when the revive settings lower `confidence` to 0.6 or below; golangci-lint's default is 0.8. Read both files first; without them, or with the matching rule off, it is a finding.

## Why it matters

Without context wrapping, error messages in logs become "file not found" instead of
"loading user config: opening ~/.config/app.yaml: file not found", making production
debugging significantly harder.

# Review Pattern: Go Initialization Patterns

**Review-Area**: quality
**Detection-Hint**: Complex logic in package-level var declarations, init functions with side effects that are hard to test, multiple init functions with ordering dependencies, iota misuse in constants
**Severity**: INFO
**Category**: technology
**Applies-When**: go
**Sources**: Effective Go (https://go.dev/doc/effective_go#init), Effective Go (https://go.dev/doc/effective_go#constants), Google Go Style Guide — Best Practices (https://google.github.io/styleguide/go/best-practices), The Go Memory Model (https://go.dev/ref/mem)

## What to check

1. `init()` functions should validate program state or perform setup that cannot be expressed as variable declarations — not for complex business logic
2. `init()` runs after all package-level variables are initialized and all imported packages are fully initialized — do not rely on cross-package init ordering
3. Multiple `init()` functions in the same file execute in definition order, but relying on this is fragile — prefer a single `init()` per file
4. `import _ "pkg"` (blank import) is only for packages that register side effects in their `init()` (e.g., database drivers, image decoders) — add a comment explaining why
5. Use `iota` for enumerated constants — start with a blank identifier (`_ = iota`) to skip the zero value when zero is not a valid enum member
6. Constants must be compile-time evaluable — only numbers, characters, strings, booleans, and expressions of these are valid

## Why it matters

Opaque init functions make programs hard to test and reason about — their execution order depends on import graphs. Blank imports without comments are mysterious. Proper use of iota and constants prevents magic numbers scattered through the codebase.

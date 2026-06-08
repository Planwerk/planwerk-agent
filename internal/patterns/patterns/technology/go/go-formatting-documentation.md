# Review Pattern: Go Formatting and Documentation

**Review-Area**: quality
**Detection-Hint**: Code not formatted with gofmt, opening braces on next line, missing doc comments on exported symbols, doc comments not starting with the symbol name
**Severity**: INFO
**Category**: technology
**Applies-When**: go
**Sources**: Effective Go (https://go.dev/doc/effective_go#formatting), Effective Go (https://go.dev/doc/effective_go#commentary), Google Go Style Guide — Decisions (https://google.github.io/styleguide/go/decisions#doc-comments), Google Go Style Guide — Guide (https://google.github.io/styleguide/go/guide)

## What to check

1. All code must be formatted with `gofmt` (or `go fmt`) — formatting discussions are resolved by the tool, not by convention debates
2. Opening braces must be on the same line as the control structure (`if`, `for`, `switch`, `func`) — a brace on the next line triggers automatic semicolon insertion and causes compilation errors
3. Every exported function, type, constant, and variable must have a doc comment — the comment must begin with the name of the element it describes (e.g., `// Reader reads from...`)
4. Use `//` line comments as the default — `/* */` block comments are reserved for package-level comments and disabling large code sections
5. Tabs for indentation (gofmt default) — spaces only within lines when alignment is needed
6. No hard line length limit — but long lines should be wrapped with an extra tab for continuation indent

## Why it matters

`gofmt` eliminates all formatting debates and ensures consistent code across the Go ecosystem. Doc comments become the official package documentation via `go doc` and pkg.go.dev. Missing or malformed doc comments degrade API discoverability.

For cross-cutting documentation review (Diátaxis mode coherence, code-and-doc sync, comment WHY-vs-WHAT, deprecation, terminology, links), see the design pattern `Documentation Structure (Diátaxis)`.

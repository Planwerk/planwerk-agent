# Review Pattern: Go Package Naming and Design

**Review-Area**: architecture
**Detection-Hint**: Package names with underscores or mixedCaps, exported names repeating the package name, constructors not named New, `import .` usage, getter methods prefixed with Get
**Severity**: INFO
**Category**: technology
**Applies-When**: go
**Sources**: Effective Go (https://go.dev/doc/effective_go#names), Effective Go (https://go.dev/doc/effective_go#package-names), Google Go Style Guide — Decisions (https://google.github.io/styleguide/go/decisions#package-names), Dave Cheney — Practical Go (https://dave.cheney.net/practical-go/presentations/qcon-china.html), Go Code Review Comments (https://go.dev/wiki/CodeReviewComments#package-names)

## What to check

1. Package names must be lowercase, single-word — no underscores, no mixedCaps (e.g., `bufio`, not `buf_io` or `bufIo`)
2. Exported names must not repeat the package name — `ring.New()` not `ring.NewRing()`, `bufio.Reader` not `bufio.BufReader`; the package name already qualifies the identifier
3. Getter methods must not use a `Get` prefix — a field `owner` gets a getter named `Owner()`, not `GetOwner()`; setters use `Set` prefix: `SetOwner()`
4. One-method interfaces use the method name plus `-er` suffix — `Reader`, `Writer`, `Formatter`, `Stringer`
5. `import .` should not be used outside test files — it obscures which package a name belongs to
6. Constructors returning the package's primary type should be named `New` (or `NewX` for multiple types in a package)
7. Long names do not automatically improve readability — a helpful doc comment is often more valuable than an extra-long identifier

## Why it matters

Go's naming conventions are enforced by community consensus and the standard library. Names like `user.UserName` (stuttering), `GetValue()` (Java-style getter), or `my_package` (non-idiomatic) signal unfamiliarity with Go conventions and create friction for every reader.

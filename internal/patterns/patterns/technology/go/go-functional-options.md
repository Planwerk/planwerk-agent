# Review Pattern: Go Functional Options

**Review-Area**: architecture
**Detection-Hint**: Constructors with many parameters (especially optional ones), large config structs passed to constructors with mostly zero-valued fields, boolean flag parameters, constructors that grow new parameters over time
**Severity**: INFO
**Category**: technology
**Applies-When**: go
**Sources**: Google Go Style Guide — Best Practices (https://google.github.io/styleguide/go/best-practices#options-pattern), Uber Go Style Guide (https://github.com/uber-go/guide/blob/master/style.md), 100 Go Mistakes (https://www.manning.com/books/100-go-mistakes-and-how-to-avoid-them)

## What to check

1. Constructors with four or more parameters, especially where several are optional or commonly zero, should consider functional options: `New(required, opts ...Option)` with `WithTimeout(d)`, `WithLogger(l)`, etc.
2. Boolean flag parameters at call sites (`New(addr, true, false, true)`) obscure intent — replace with named options (`WithTLS()`, `WithoutCompression()`)
3. Options should be types, not functions directly, so they can be introspected, composed, or documented: `type Option func(*config)` with `WithX(x T) Option { return func(c *config) { c.x = x } }`
4. The internal `config` struct should be private; only the `Option` type and `WithX` functions should be exported
5. Required parameters remain positional — don't force callers to discover that an option is mandatory via runtime errors
6. Don't apply options to types that are simple and unlikely to grow configuration; a `New(x, y)` with two well-named parameters is clearer than `New(opts...)` with two mandatory options
7. Avoid "config-struct-as-only-parameter" patterns when options would compose better — struct literals with many nil/zero fields are a smell

## Why it matters

Functional options let APIs evolve without breaking callers: new options can be added without changing existing call sites, and zero-configuration calls remain concise. They also make call sites self-documenting — `NewClient(addr, WithTimeout(5*time.Second), WithTLS(tlsConfig))` reads as intent, while `NewClient(addr, 5*time.Second, tlsConfig, nil, false)` requires reading the signature. Over-use is real though: for simple, stable types, a small positional signature is lighter-weight.

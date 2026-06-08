# Review Pattern: SOLID - Interface Segregation Principle

**Review-Area**: architecture
**Detection-Hint**: Large interfaces with many methods, implementations that leave methods as no-ops or panics, clients depending on interfaces they use only partially
**Severity**: INFO
**Category**: design-principle
**Sources**: Agile Software Development: Principles, Patterns, and Practices (Robert C. Martin)

## What to check

1. Interfaces should be small and focused — clients should not depend on methods they don't use
2. If multiple implementations leave some methods as no-ops, the interface is too large
3. Prefer multiple small interfaces over one large one (role interfaces)
4. In Go: interfaces should be defined by the consumer, not the producer
5. Check if function parameters accept a broad interface when they only call 1-2 methods

## Why it matters

Fat interfaces create unnecessary coupling. A client that only needs `Read()` should not depend on an interface that also declares `Write()`, `Seek()`, and `Close()`. Changes to unused methods force recompilation and retesting.

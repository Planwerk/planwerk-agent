# Review Pattern: SOLID - Dependency Inversion Principle

**Review-Area**: architecture
**Detection-Hint**: High-level modules importing low-level modules directly, concrete types in function signatures where interfaces would suffice, constructor creating its own dependencies
**Severity**: INFO
**Category**: design-principle
**Sources**: Agile Software Development: Principles, Patterns, and Practices (Robert C. Martin), Clean Architecture (Robert C. Martin)

## What to check

1. High-level business logic should depend on abstractions, not concrete implementations
2. Dependencies should be injected, not created internally (constructor injection preferred)
3. Function parameters should accept interfaces when the concrete type is not essential
4. The dependency direction should point inward: infrastructure → application → domain (not reversed)
5. Note: Not every function needs DI — direct construction is fine for value objects and utilities

## Why it matters

When business logic directly depends on infrastructure (database, HTTP client, filesystem), it cannot be tested in isolation and changes to infrastructure ripple through the entire codebase.

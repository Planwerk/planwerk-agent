# Review Pattern: SOLID - Liskov Substitution Principle

**Review-Area**: architecture
**Detection-Hint**: Subclasses that override methods to throw exceptions or do nothing, type checks (`instanceof`/type assertions) in code consuming a base type
**Severity**: WARNING
**Category**: design-principle
**Sources**: Agile Software Development: Principles, Patterns, and Practices (Robert C. Martin), A Behavioral Notion of Subtyping (Barbara Liskov, Jeannette Wing)

## What to check

1. Subtypes must be usable wherever the base type is expected without breaking behavior
2. Overridden methods should not weaken postconditions or strengthen preconditions
3. If a method override throws `NotImplementedError` or `UnsupportedOperationException`, LSP is violated
4. Type assertions/checks on an interface parameter signal that the abstraction is leaking
5. Empty method overrides that silently skip behavior violate caller expectations

## Why it matters

LSP violations break polymorphism — the core mechanism for extensibility. Code that type-checks its arguments cannot be safely extended, leading to fragile and tightly coupled systems.

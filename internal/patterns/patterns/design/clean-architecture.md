# Review Pattern: Clean Architecture

**Review-Area**: architecture
**Detection-Hint**: Domain logic importing framework or infrastructure packages, business rules mixed with HTTP handlers or database queries, circular dependencies between packages
**Severity**: INFO
**Category**: design-principle
**Sources**: Clean Architecture (Robert C. Martin), Hexagonal Architecture (Alistair Cockburn)

## What to check

1. Business/domain logic should have no imports from infrastructure, frameworks, or external services
2. Dependency direction must point inward: handlers → use cases → domain entities
3. Framework-specific types (HTTP request/response, ORM models) should not leak into business logic
4. Use cases should be orchestrators, not contain framework code
5. Circular package dependencies indicate unclear boundaries

## Why it matters

When business logic is entangled with infrastructure, changing the database, HTTP framework, or external service requires rewriting business rules. Clean boundaries enable independent testing and technology migration.

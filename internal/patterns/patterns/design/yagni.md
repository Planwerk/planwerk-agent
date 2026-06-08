# Review Pattern: YAGNI - You Aren't Gonna Need It

**Review-Area**: architecture
**Detection-Hint**: Abstractions with a single implementation, configuration options with only one value, generic solutions for a single concrete case, feature flags always enabled
**Severity**: INFO
**Category**: design-principle
**Sources**: Extreme Programming Explained (Kent Beck), Clean Code (Robert C. Martin)

## What to check

1. Interfaces or abstract classes with a single implementation and no test mocks — is the abstraction premature?
2. Configuration parameters that only have one value in practice
3. Factory patterns where a simple constructor would suffice
4. Generic solutions for problems with only one concrete case today
5. Commented-out code kept "in case we need it later"
6. Unused function parameters kept for "future extensibility"

## Why it matters

Every abstraction has a maintenance cost. Code should solve today's requirements. Speculative features add complexity, slow development, and often turn out to be wrong when the real requirement arrives.

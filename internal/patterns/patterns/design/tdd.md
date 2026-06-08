# Review Pattern: TDD - Test-Driven Development

**Review-Area**: testing
**Detection-Hint**: New code without corresponding tests, tests written after implementation that only cover happy path, test names that don't describe behavior
**Severity**: WARNING
**Category**: design-principle
**Sources**: Test Driven Development: By Example (Kent Beck), Growing Object-Oriented Software Guided by Tests (Freeman/Pryce)

## What to check

1. Every new behavior should have a corresponding test — check for untested code paths
2. Tests should describe the expected behavior in their names, not the implementation: `TestUserCannotLoginWithExpiredPassword` not `TestLogin3`
3. Tests must cover edge cases and error paths, not just the happy path
4. Test assertions should be specific — avoid asserting only that "no error occurred"
5. Tests should be independent and not rely on execution order or shared mutable state
6. Each test should follow Arrange-Act-Assert (or Given-When-Then) structure

## Why it matters

Tests written after the fact tend to verify the implementation rather than the behavior. TDD ensures every behavior is specified before it exists, catching design issues early and creating a reliable safety net for refactoring.

# Review Pattern: SOLID - Single Responsibility Principle

**Review-Area**: architecture
**Detection-Hint**: Functions or classes doing multiple unrelated things, god objects, functions longer than ~50 lines with multiple concerns mixed together
**Severity**: INFO
**Category**: design-principle
**Sources**: Clean Code (Robert C. Martin), Agile Software Development: Principles, Patterns, and Practices (Robert C. Martin)

## What to check

1. Each function/class should have one reason to change — one responsibility
2. Functions combining I/O with business logic should be split (command/query separation)
3. Look for classes that grow a new method for every new feature — they likely need decomposition
4. A function that requires multiple comments to explain different sections is doing too much
5. If the PR description lists unrelated changes in the same function, SRP is likely violated

## Why it matters

When a function has multiple responsibilities, changes for one concern risk breaking the other. SRP keeps changes isolated and makes the codebase easier to understand, test, and extend.

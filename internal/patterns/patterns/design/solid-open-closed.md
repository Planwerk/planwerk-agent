# Review Pattern: SOLID - Open/Closed Principle

**Review-Area**: architecture
**Detection-Hint**: Long switch/if-else chains that grow with each new type, functions modified (not extended) for every new feature, hardcoded type checks
**Severity**: INFO
**Category**: design-principle
**Sources**: Clean Code (Robert C. Martin), Design Patterns (Gamma et al.)

## What to check

1. Adding a new variant should not require modifying existing code — check for growing switch statements
2. Strategy pattern or polymorphism should replace type-based branching when > 3 cases exist
3. Plugin or registry patterns should be considered when new types are added frequently
4. Note: OCP does not mean "never modify code" — it means design for extension points where change is expected

## Why it matters

Code that must be modified for every new feature becomes a merge conflict magnet and regression risk. Extension points allow new behavior without touching working code.

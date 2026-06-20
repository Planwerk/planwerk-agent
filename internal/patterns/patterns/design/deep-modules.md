# Review Pattern: Deep Modules

**Review-Area**: architecture
**Detection-Hint**: Thin pass-through wrappers, interfaces nearly as large as the implementation behind them, classes that only forward calls, single-use abstractions, one-implementation adapters introduced before a second caller exists
**Severity**: INFO
**Category**: design-principle
**Sources**: A Philosophy of Software Design (Ousterhout)

## What to check

1. **Deep vs. shallow modules**: a module is deep when it hides substantial functionality behind a small interface, and shallow when its interface is nearly as complex as the implementation behind it. Prefer fewer, deeper modules over many shallow ones; flag thin wrappers and pass-through layers that add an interface without hiding any complexity.
2. **The deletion test**: ask what complexity would disappear if the module (and its interface) were deleted and its callers inlined. If little complexity goes away, the module was shallow and the abstraction was not earning its keep.
3. **The interface is the test surface**: tests should exercise a module through its interface, not reach into its internals. If a behavior can only be tested by asserting on private state or call order, the interface is too narrow or the module too shallow — fix the design, do not test around it.
4. **The one-vs-two-adapter seam rule**: a single implementation behind an interface is a hypothetical seam, not a real one. One adapter means inline it; a second adapter is what proves the seam is real. Do not introduce the abstraction until the second implementation actually exists.
5. **Dependency categories**: classify every dependency as one of — in-process (a plain function or struct call, no seam), local-substitutable (swappable in the same process, e.g. an interface with a real implementation and a fake), ports & adapters (a seam to an external system: database, network, filesystem, clock), or mock (a test double standing in for one of the above). Mock only at the ports & adapters seams; do not mock in-process collaborators.

## Why it matters

Module depth is the lever for information hiding: a deep module lets callers ignore the complexity it manages, so changes stay local and the cost of understanding the system drops. Shallow modules and premature single-use abstractions do the opposite — they spread knowledge across pass-through layers, multiply the interfaces a reader must learn, and raise the cost of every change without hiding anything. Naming the deletion test, the seam rule, and the dependency categories turns "is this abstraction worth it?" into a question with a concrete answer.

# Review Pattern: Python Docstrings

**Review-Area**: documentation
**Detection-Hint**: Public modules, classes, or functions without a docstring, single-line docstrings missing a period, multi-line docstrings without a summary line + blank line, triple-quote inconsistencies, missing parameter / return / raises sections on documented public API
**Severity**: INFO
**Category**: technology
**Applies-When**: python
**Sources**: PEP 257 — Docstring Conventions (https://peps.python.org/pep-0257/), PEP 287 — reStructuredText Docstring Format (https://peps.python.org/pep-0287/), Python Style Guide (https://peps.python.org/pep-0008/), Google Python Style Guide — Docstrings (https://google.github.io/styleguide/pyguide.html#38-comments-and-docstrings), NumPy Docstring Standard (https://numpydoc.readthedocs.io/en/latest/format.html)

## What to check

1. Every public module, class, function, and method has a docstring. Private (`_name`) helpers may omit them when intent is obvious from the signature.
2. Docstrings use triple double-quotes (`"""`) — never triple single-quotes — and the closing quotes sit on their own line for multi-line docstrings (PEP 257).
3. The first line is a single-sentence summary in the imperative mood ending with a period: `"""Return the active session."""`, not `"""Returns the active session"""` and not a paraphrase of the implementation.
4. Multi-line docstrings have a one-line summary, a blank line, and then the body. The body documents parameters, return value, and raised exceptions in one consistent style across the project (Google, NumPy, or Sphinx / reST — pick one per project and stick with it).
5. Docstrings describe contract, not implementation: pre-conditions, post-conditions, side effects, raised exceptions. Avoid restating what the code already says.
6. Public APIs document every parameter, the return value (or `None`), and any exception the function may raise.
7. Type information lives in annotations (PEP 484), not duplicated inside the docstring — see the `Python Type Hints` pattern.
8. Examples in docstrings are runnable doctests (`>>> ...`) when the function is pure enough; otherwise they are clearly marked as illustrative.

## Why it matters

PEP 257 is the canonical docstring convention and the basis of every doc-generation tool in the Python ecosystem (Sphinx, mkdocs, pdoc). Skipping or paraphrasing docstrings on public API breaks generated reference docs, IDE tooltips, and onboarding flows — and the inconsistency between Google / NumPy / reST styles in the same project quietly degrades autodoc output.

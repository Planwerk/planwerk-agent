# Review Pattern: Documentation Structure (Diátaxis)

**Review-Area**: documentation
**Detection-Hint**: Markdown / reStructuredText / AsciiDoc files, files under `docs/`, README sections, doc-comment changes (godoc, docstrings, JSDoc, rustdoc), or any page that mixes tutorial steps, how-to instructions, reference tables, and conceptual prose
**Severity**: WARNING
**Category**: design-principle
**Sources**: Diátaxis Framework (https://diataxis.fr), Google Developer Documentation Style Guide (https://developers.google.com/style), Microsoft Writing Style Guide (https://learn.microsoft.com/style-guide/welcome/), Write the Docs — Documentation Guide (https://www.writethedocs.org/guide/), Go Doc Comments (https://go.dev/doc/comment), PEP 257 — Docstring Conventions (https://peps.python.org/pep-0257/), ISO/IEC/IEEE 26515:2018, Docs for Developers — Apress 2021

## What to check

### 1. Diátaxis mode coherence

Every documentation page sits in exactly one of four modes; each page must stay in its mode and not silently drift into another:

| Mode | Purpose | Voice | Smell when violated |
|------|---------|-------|---------------------|
| **Tutorial** | Lead a beginner through one guaranteed-success first run | "We will…", second person, strictly linear | Branching paths, optional flags, references to internals, "in production you would…" detours |
| **How-To Guide** | Solve one specific real-world problem for a reader who already knows the basics | Goal-oriented imperatives | Background theory, novice hand-holding, exhaustive option lists |
| **Reference** | Describe the API / CLI / config surface accurately and exhaustively | Neutral, parallel form, structured | Tutorials inline, opinionated recommendations, narrative explanations |
| **Explanation** | Provide context, design rationale, trade-offs | Discursive, narrative | Step-by-step instructions, full API listings, tooling commands |

Identify the intended mode (page title, location under `docs/tutorials/` `docs/how-to/` `docs/reference/` `docs/explanation/`, or by content) and flag any section that drifts. Be specific in the finding: name the mode the page claims and the mode the offending paragraph actually delivers, then point at where to split or relocate.

### 2. Code-and-doc synchronisation

1. Public symbols, CLI flags, config keys, environment variables, and example outputs in docs MUST match the current source. New or removed flags, defaults, or behaviour MUST be reflected in the same PR.
2. Fenced code blocks MUST be copy-pasteable against the current API; prefer doctests / executable docs where the language supports them.
3. Removed or renamed APIs MUST carry an explicit deprecation block with a migration path; the CHANGELOG / release notes MUST mention the change.
4. A user-visible behaviour change in the diff MUST also land in `CHANGELOG.md` or release notes.

### 3. Code-comment hygiene

1. Doc-comments on every exported symbol follow the language convention: Go (`// Name does …`, see `Go Formatting and Documentation`), Python (PEP 257 docstring with one-line summary, blank line, body — see `Python Docstrings`), TypeScript / Rust equivalents.
2. Comments capture WHY (intent, invariant, workaround, hidden constraint), not WHAT — a comment that paraphrases the code is noise and MUST be removed.
3. Cross-references in comments (`see X`, `// TODO(@user): …`, ticket numbers) MUST point to something that still exists.
4. Comment-only diffs are in scope — a stale or paraphrasing comment is a finding even when no code line moved.

### 4. Voice and terminology

1. Active voice, present tense, second person ("You configure X", not "X can be configured") — Google and Microsoft style guides.
2. One canonical name per concept throughout the docs (no drift between `client` / `consumer` / `caller` for the same actor).
3. Jargon defined on first use; acronyms expanded once per page.

### 5. Structure and discoverability

1. Single H1 per page, no level skips in the heading hierarchy, scannable section titles; pages over ~500 words carry a TOC.
2. Internal anchors resolve, external links are not paywalled or stage-only (`localhost`, internal staging hosts).
3. Diagrams use Mermaid / PlantUML or comparable text formats — screenshots only for true UI captures.
4. Each page makes its target reader explicit (beginner / operator / integrator) so audience match is testable.

### 6. Attribution

Any code or text copied from elsewhere MUST carry origin and license.

## Why it matters

A Tutorial that drifts into Reference loses beginners; a Reference that drifts into Explanation loses operators looking up a value. Diátaxis is not a stylistic preference — it is the most widely adopted framework that maps reader needs to documentation forms. Without these structural checks, doc reviews collapse to typo-spotting while drift between code and docs accumulates silently — and stale comments that paraphrase moved code mislead every future reader.

# Review Pattern: Markdown Quality

**Review-Area**: documentation
**Detection-Hint**: `.md` / `.markdown` files; non-CommonMark constructs that render only on GitHub (or only outside it), inconsistent heading levels, multiple H1s per page, fenced code blocks without a language tag, hard-tab indentation in lists, ordered-list numbering that drifts, broken or relative links that resolve only locally, raw HTML mixed with Markdown without need, no markdownlint or Vale configuration in the repo
**Severity**: INFO
**Category**: technology
**Sources**: CommonMark Spec — current (https://spec.commonmark.org/current/), CommonMark Spec 0.31.2 (https://spec.commonmark.org/0.31.2/), CommonMark Project Home (https://commonmark.org/), GitHub Flavored Markdown Spec (https://github.github.com/gfm/), markdownlint Rules (https://github.com/DavidAnson/markdownlint/blob/main/doc/Rules.md), Vale (https://vale.sh/docs/), Diátaxis Framework (https://diataxis.fr)

## What to check

### Spec conformance
1. Markdown files conform to CommonMark 0.31.2 — the only widely-implemented unambiguous Markdown spec. Constructs that depend on a specific renderer (GitLab Flavored Markdown vs. GitHub Flavored Markdown vs. CommonMark base) should be avoided unless the project explicitly targets one renderer
2. Where the project targets GitHub (READMEs, docs rendered on github.com), GitHub Flavored Markdown extensions (tables, task lists, strikethrough, autolinks, fenced code with language, footnotes) are fair game — but the file should still parse meaningfully under base CommonMark
3. Raw HTML in Markdown is a fallback, not a habit. Reach for it only when CommonMark/GFM cannot express the construct (custom alignment, embedded media, accessibility attributes); inline `<div>` soup defeats Markdown's portability

### Structure
4. Exactly one H1 per page, set as the document title; subsequent headings step from H2 down without skipping levels. Skipped levels (H2 → H4) break navigation, screen readers, and TOC generators
5. Heading text is sentence case (or whatever the project style guide picks) and unique within the page; ATX headings (`# Title`) consistently, not setext (`Title\n=====`) — pick one style
6. Long pages (>500 words) carry a TOC near the top; section anchors resolve to slugs the renderer produces (test the renderer the project actually uses)

### Lists, code, links
7. Ordered lists keep their numbering consistent — either all `1.` (renderer auto-numbers) or sequential `1. 2. 3.`, not a mix that drifts during edits
8. Unordered lists use a single bullet style (`-` or `*`, not both); nested lists indent by 2 or 4 spaces consistently with the parser
9. Fenced code blocks always declare a language (` ```bash `, ` ```yaml `, ` ```go `) — language tags drive syntax highlighting, copy buttons, and doctest tooling
10. Inline code uses single backticks; never reach for `<code>` in HTML when backticks suffice
11. Links use the descriptive form (`[OpenAPI Specification](https://...)`) — never bare URLs in prose. Reference-style links (`[OpenAPI][openapi]`) are appropriate when a URL appears repeatedly
12. Internal links are relative (`./../patterns/SOURCES.md`), not absolute to a specific host — relative links survive repo moves and forks. External links are HTTPS where available

### Linting and prose
13. The repo configures `markdownlint` with a checked-in config (`.markdownlint.yaml` / `.markdownlint-cli2.yaml`) and runs it in CI; rules are documented and exceptions justified inline (`<!-- markdownlint-disable-next-line MD013 -->`)
14. For docs directories, prose linting via `Vale` (with a project styles directory or a vendored set like Microsoft/Google) catches voice, terminology, and clarity issues that markdownlint cannot — passive voice, banned terms, undefined acronyms
15. Both linters run in CI on the same scope and fail the build on errors; warnings track as findings, not silent noise
16. Linting rules apply to embedded code blocks too — code in docs that does not compile is documentation rot

### Cross-cutting (see also Documentation Structure)
17. Markdown structure is necessary but not sufficient — content quality (Diátaxis mode coherence, code-and-doc synchronization, voice) is governed by `Documentation Structure (Diátaxis)`. Apply both
18. New `.md` files must declare their target renderer (GitHub README, MkDocs site, Hugo, Docusaurus) so contributors know which extensions are valid

## Why it matters

Markdown is the lingua franca of developer documentation, but "Markdown" is not one thing — base CommonMark, GitHub Flavored Markdown, MkDocs (with extensions), Hugo (with shortcodes), and a dozen other renderers all interpret some constructs differently. Files that render correctly on github.com may break on a docs site, and vice versa. CommonMark 0.31.2 is the unambiguous baseline; GFM is the strict superset that github.com adds on top. Every other dialect is somewhere on a spectrum. Without a documented target and an enforced linter, doc reviews collapse to subjective opinion and broken renders ship to users — code blocks without language tags look fine on GitHub but lose syntax highlighting on the docs site, multi-H1 pages break navigation generators, broken relative links work locally and 404 on production. markdownlint encodes the structural rules, Vale encodes the prose rules, and the CommonMark / GFM specs are the contract both linters check against. Reviewing Markdown changes against this baseline is cheap, mechanizable, and catches the issues that would otherwise become accumulated documentation debt.

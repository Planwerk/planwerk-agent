package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-review/internal/glossary"
)

// buildGlossaryPrompt constructs the prompt that asks Claude to explore the
// checkout and emit a starter CONTEXT.md in the upstream CONTEXT-FORMAT schema:
// a single-context glossary of the repository's own domain-specific vocabulary.
// The output feeds the `glossary` command's stdout, so the prompt ends with a
// hard "output the Markdown only" instruction; sanitizeGlossary is the
// belt-and-braces that strips any preamble the model emits anyway.
//
// The CONTEXT-FORMAT rules are carried verbatim — be opinionated (pick one term,
// list the rest under "_Avoid_"), keep definitions tight, and include ONLY
// context-specific terms (no general programming concepts) — because those rules
// are what keep the artifact a domain glossary rather than a generic dictionary.
// proseStyleBlock and outputLanguageBlock are reused so the writing discipline
// and the English-output pin match every other generated artifact.
func buildGlossaryPrompt(ctx glossary.GenerateContext) string {
	var sb strings.Builder

	sb.WriteString(`You are a senior software architect documenting a codebase's domain language. Read this repository and produce a CONTEXT.md: a tight glossary of the project's own domain vocabulary, so anyone — human or AI — working on the repo speaks its terms instead of generic synonyms.

`)

	if ctx.RepoName != "" {
		fmt.Fprintf(&sb, "Repository: %s\n\n", ctx.RepoName)
	}

	sb.WriteString(`## Method

1. Walk the repository: read the README, the top-level package layout, the core types, and the names that recur across the code, tests, and docs. The domain vocabulary is what the project names its own concepts — not the language or framework it is built on.
2. Identify the terms that are specific to THIS project's domain. A term earns a place only if a newcomer would otherwise guess its meaning wrong or reach for a different word for it.
3. For each term, decide the ONE canonical name the project should use, and list the synonyms or near-misses to avoid.

## Inclusion rules (these are what make this a domain glossary, not a dictionary)

- Include ONLY context-specific terms. EXCLUDE general programming concepts (function, interface, cache, retry, struct, handler, middleware) unless this repository gives the word a specific, non-obvious meaning of its own.
- Be opinionated. When the codebase uses several words for one concept, pick ONE as the term and list the others under "_Avoid_". Do not hedge by listing two terms as equals.
- Keep every definition tight: one or two sentences stating what the term IS, not how it is implemented.
- Group related terms under "## "-level subheadings ONLY when natural clusters emerge; a short glossary needs no subheadings.

## Output format (CONTEXT-FORMAT)

Output a single Markdown document in exactly this shape:

# {Context Name}

A one- or two-sentence description of what this context covers.

## Language

**{Term}**: A tight one- or two-sentence definition of what the term IS.
_Avoid_: {comma-separated synonyms or near-misses to not use for this term}

Repeat the "**{Term}**:" / "_Avoid_:" pair for each term. Omit the "_Avoid_:" line for a term that has no competing synonyms. Name the context (the "# " heading) after the repository's domain, not after the repository slug.

`)

	sb.WriteString(proseStyleBlock())
	sb.WriteString(outputLanguageBlock())

	sb.WriteString(`Output the CONTEXT.md Markdown ONLY. Start with the "# " heading line — no preamble, no closing commentary, and no code fence wrapping the document. This is a starter glossary meant to be reviewed and edited by a human before it is committed.`)

	return sb.String()
}

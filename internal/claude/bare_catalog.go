package claude

import (
	"strings"

	"github.com/planwerk/planwerk-review/internal/patterns"
)

// renderBareCatalog assembles the "## Project Review Patterns to Honor"
// section that the fix and implement bare prompts share. The catalog has
// already been classified by the orchestrator: bundled patterns carry a
// public URL the manual Claude session should fetch, repo-local patterns
// carry a relative path the session can read from its own checkout, and
// user-supplied --patterns entries fall through with a free-form note.
//
// The block always renders SOMETHING — when no catalog was prepared (clone
// or load failed, or every flag was disabled) it falls back to a self-
// inspection instruction so the prompt is never silently missing the
// pattern context.
func renderBareCatalog(catalog []patterns.CatalogReference, hasRepoLocalRefs bool) string {
	if len(catalog) == 0 {
		return `## Project Review Patterns to Honor

No prebuilt pattern catalog was attached to this prompt. Before editing any file, look for project-specific review patterns and treat them as binding context:

1. If ` + "`.planwerk/review_patterns/`" + ` exists in this checkout, read every ` + "`*.md`" + ` file under it. Each file is a pattern: a rule, its detection hint, severity, and rationale.
2. If a top-level ` + "`patterns/`" + ` directory exists in this checkout (and looks like a planwerk-review pattern catalog — files starting with ` + "`# Review Pattern:`" + `), read those too.
3. If neither directory is present, skip this section.

Your work MUST stay consistent with whichever patterns you find. Do not introduce changes that would itself be flagged by one of these patterns.

`
	}

	var sb strings.Builder
	sb.WriteString("## Project Review Patterns to Honor\n\n")
	sb.WriteString("planwerk-review identified the patterns below as relevant to the technologies in the target repo (the catalog has already been filtered by the detected technology tags above). Treat them as binding context: your work MUST stay consistent with them, and when the change touches an area covered by a pattern, prefer the resolution the pattern endorses.\n\n")
	sb.WriteString("To load each pattern, fetch its URL — use the WebFetch tool when available, or ")
	sb.WriteString("`curl -fsSL <URL>`")
	sb.WriteString(" otherwise. Read the markdown body in full; each file follows the planwerk-review pattern schema (`# Review Pattern: …` header, `**Review-Area**`, `**Detection-Hint**`, `**Severity**`, `**Category**`, `**Applies-When**`, optional `**Sources**`, then the rule body). Patterns marked with a checkout path live inside the working tree you are already in and need no fetch — open them directly.\n\n")
	if hasRepoLocalRefs {
		sb.WriteString("`.planwerk/review_patterns/` exists in this checkout; the entries below labelled with a checkout path come from there. If you find additional `*.md` files in that directory that the catalog below does not list, read those too and treat them as equally binding.\n\n")
	}
	sb.WriteString("<review-pattern-catalog>\n")
	sb.WriteString(patterns.FormatCatalogReferences(catalog))
	sb.WriteString("</review-pattern-catalog>\n\n")
	return sb.String()
}

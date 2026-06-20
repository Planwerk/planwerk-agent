package claude

import (
	"fmt"

	"github.com/planwerk/planwerk-review/internal/glossary"
)

// GenerateGlossary runs a single Claude session in the checkout dir to produce a
// starter CONTEXT.md in the CONTEXT-FORMAT schema. Unlike Propose/Audit/Review,
// it makes ONE call and returns the Markdown directly — there is no JSON
// structuring step, since the artifact is itself Markdown — mirroring Fix's
// single-call shape. The output is passed through sanitizeGlossary so any
// conversational preamble or wrapping fence is stripped and only the
// CONTEXT-FORMAT document reaches the caller. It returns the Markdown, the
// resolved model id, and an error.
func (c *Client) GenerateGlossary(dir string, ctx glossary.GenerateContext) (string, string, error) {
	out, model, err := c.runClaude(dir, buildGlossaryPrompt(ctx), "glossary")
	if err != nil {
		return "", "", fmt.Errorf("running glossary generation: %w", err)
	}
	return sanitizeGlossary(out), model, nil
}

// glossaryHeading is the anchor sanitizeGlossary trims to: the first top-level
// Markdown heading, which in CONTEXT-FORMAT is the "# {Context Name}" line. A
// "## "-level subheading does not match this prefix, so the document's own
// "## Language" section is never mistaken for the anchor.
const glossaryHeading = "# "

// sanitizeGlossary strips a wrapping markdown fence and any preamble the model
// emits before the first top-level "# " heading, so the returned text is the
// CONTEXT-FORMAT document alone — ready to be saved as a repo's CONTEXT.md. It
// reuses sanitizeReport (decision #27). Output with no heading is returned
// de-fenced but otherwise intact rather than discarded.
func sanitizeGlossary(out string) string {
	return sanitizeReport(out, glossaryHeading)
}

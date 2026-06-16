package draft

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/planwerk/planwerk-review/internal/attribution"
)

// Renderer writes a drafted issue to an output stream as Markdown or JSON.
type Renderer struct {
	w io.Writer
}

// NewRenderer returns a Renderer that writes to w.
func NewRenderer(w io.Writer) *Renderer {
	return &Renderer{w: w}
}

// RenderJSON writes the draft result as indented JSON.
func (r *Renderer) RenderJSON(result Result) error {
	enc := json.NewEncoder(r.w)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

// RenderMarkdown writes a preview of the drafted issue: a heading naming the
// target repo and title, a generated-by line, then the issue body.
func (r *Renderer) RenderMarkdown(repoFullName, version string, result *Result) {
	_, _ = fmt.Fprintf(r.w, "# Draft issue for %s — %s\n\n", repoFullName, result.Title)
	_, _ = fmt.Fprintf(r.w, "> Drafted by planwerk-review %s %s\n\n", version, attribution.Assistant())
	_, _ = fmt.Fprint(r.w, "---\n\n")
	_, _ = fmt.Fprint(r.w, result.Body)
	if !strings.HasSuffix(result.Body, "\n") {
		_, _ = fmt.Fprintln(r.w)
	}
}

// BuildIssueBody renders the canonical issue body for a draft using the house
// issue format: a `Category`/`Scope` header line followed by Description and
// Motivation sections and a generated-by footer. Category is fixed to "feature"
// because draft only captures feature ideas. Scope defaults to "Medium" when
// the model leaves it blank.
//
// The body deliberately stops at Description and Motivation: affected-areas,
// acceptance criteria, and implementation steps are elaboration, which is the
// separate `elaborate` command's job.
func BuildIssueBody(r *Result) string {
	var b strings.Builder

	scope := strings.TrimSpace(r.Scope)
	if scope == "" {
		scope = "Medium"
	}
	fmt.Fprintf(&b, "**Category**: feature | **Scope**: %s\n\n", scope)

	if d := strings.TrimSpace(r.Description); d != "" {
		fmt.Fprintf(&b, "## Description\n\n%s\n\n", d)
	}

	if m := strings.TrimSpace(r.Motivation); m != "" {
		fmt.Fprintf(&b, "## Motivation\n\n%s\n\n", m)
	}

	fmt.Fprintf(&b, "---\n\n_Drafted by %s %s_\n", attribution.Link, attribution.Assistant())
	return b.String()
}

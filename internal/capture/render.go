package capture

import (
	"fmt"
	"io"
	"strings"

	"github.com/planwerk/planwerk-review/internal/attribution"
	"github.com/planwerk/planwerk-review/internal/report"
)

// provenanceMarkerPrefix is the stable, run-independent prefix of the HTML
// comment every captured page carries. Its fixed shape lets sync, extract, and a
// human tell tool-authored knowledge from hand-authored at a glance. The marker
// deliberately carries no timestamp or run id: a volatile component would change
// the page bytes on every re-run, defeating the stable-slug convention (a re-run
// must update the page in place, not churn it) and breaking golden-test
// determinism.
const provenanceMarkerPrefix = "<!-- planwerk-review: captured from "

// Provenance identifies the implement run a captured page came from, so the
// provenance marker can name the source repository and issue.
type Provenance struct {
	Repo  string // "owner/repo"
	Issue int
}

// Marker renders the stable provenance marker comment, e.g.
// "<!-- planwerk-review: captured from owner/repo#42 -->".
func (p Provenance) Marker() string {
	return fmt.Sprintf("%s%s#%d -->", provenanceMarkerPrefix, p.Repo, p.Issue)
}

// RenderPage returns the page as it would be written: the stable provenance
// marker, then the authored body. The marker is fixed for a given Provenance, so
// rendering the same page twice is byte-identical and a re-run that re-proposes
// the same stable slug updates the page in place rather than appending.
func RenderPage(p ProposedPage, prov Provenance) string {
	body := strings.TrimRight(p.Body, "\n")
	return prov.Marker() + "\n\n" + body + "\n"
}

// Renderer writes a CaptureResult as Markdown.
type Renderer struct {
	w io.Writer
}

// NewRenderer creates a Renderer that writes to w.
func NewRenderer(w io.Writer) *Renderer {
	return &Renderer{w: w}
}

// RenderMarkdown writes the proposal result as Markdown: a provenance header, a
// "Proposed review patterns" and a "Proposed memory pages" section listing each
// candidate's path, title, rationale, and confidence with the rendered page in a
// fenced block, and a propose-only footer noting nothing was written and
// pointing at the (deferred) write-back. prov names the source run for the page
// markers and the header repo; version is the build that produced the report.
func (r *Renderer) RenderMarkdown(result CaptureResult, prov Provenance, version string) {
	_, _ = fmt.Fprintf(r.w, "# Captured Knowledge: %s\n\n", prov.Repo)
	_, _ = fmt.Fprintf(r.w, "> Proposed by %s %s\n", attribution.ToolWithVersion(version), attribution.AssistantWith(result.Model))
	report.RenderWikiProvenance(r.w, result.WikiRepo, result.WikiCommit)
	_, _ = fmt.Fprintln(r.w)

	if !result.HasProposals() {
		_, _ = fmt.Fprintln(r.w, "Nothing new to propose — no generalizable review patterns or durable memory pages found.")
		return
	}

	r.renderSection("Proposed review patterns", "review_patterns/", result.Patterns, prov)
	r.renderSection("Proposed memory pages", "memory/", result.Memory, prov)

	_, _ = fmt.Fprintf(r.w, "## Summary\n\n")
	_, _ = fmt.Fprintf(r.w, "**Proposed**: %d (%d patterns, %d memory)\n\n", len(result.Patterns)+len(result.Memory), len(result.Patterns), len(result.Memory))
	_, _ = fmt.Fprintln(r.w, "> [!NOTE]")
	_, _ = fmt.Fprintln(r.w, "> These are propose-only suggestions — nothing was written to the wiki.")
	_, _ = fmt.Fprintln(r.w, "> Review them and add the ones worth keeping by hand.")
}

// renderSection writes one kind's proposed pages, skipping the section entirely
// when it has none so the report carries no empty noise. Each entry lists its
// path/title/rationale/confidence followed by the rendered page (provenance
// marker included) in a fenced Markdown block.
func (r *Renderer) renderSection(label, gloss string, pages []ProposedPage, prov Provenance) {
	if len(pages) == 0 {
		return
	}
	_, _ = fmt.Fprintf(r.w, "## %s (%d)\n\n", label, len(pages))
	_, _ = fmt.Fprintf(r.w, "Candidate %s pages, deduplicated against the wiki and the pattern catalog.\n\n", gloss)
	for _, p := range pages {
		verb := "new"
		if p.IsUpdate {
			verb = "update"
		}
		_, _ = fmt.Fprintf(r.w, "### `%s` (%s)\n\n", p.Path, verb)
		if p.Title != "" {
			_, _ = fmt.Fprintf(r.w, "**%s**\n\n", p.Title)
		}
		if p.Rationale != "" {
			line := p.Rationale
			if p.Confidence != "" {
				line += fmt.Sprintf(" [confidence: %s]", p.Confidence)
			}
			_, _ = fmt.Fprintf(r.w, "%s\n\n", line)
		}
		page := RenderPage(p, prov)
		fence := fenceFor(page)
		_, _ = fmt.Fprintf(r.w, "%smarkdown\n", fence)
		_, _ = fmt.Fprint(r.w, page)
		_, _ = fmt.Fprintf(r.w, "%s\n\n", fence)
	}
}

// fenceFor returns a backtick fence at least one tick longer than the longest
// run of backticks in s, so a body that itself contains ``` code fences cannot
// terminate the wrapper early. Pattern bodies carry example ```go/```bash
// blocks and memory pages are free-form Markdown, so the wrapper length must be
// computed from the content rather than fixed at three.
func fenceFor(s string) string {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			if run++; run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	n := longest + 1
	if n < 3 {
		n = 3
	}
	return strings.Repeat("`", n)
}

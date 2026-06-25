package sync

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/planwerk/planwerk-review/internal/attribution"
	"github.com/planwerk/planwerk-review/internal/report"
)

// Renderer writes a SyncResult as Markdown or JSON.
type Renderer struct {
	w io.Writer
}

// NewRenderer creates a Renderer that writes to w.
func NewRenderer(w io.Writer) *Renderer {
	return &Renderer{w: w}
}

// RenderMarkdown writes the reconciliation result as Markdown: a provenance
// header, a Stale and a Redundant section listing each flagged entry's path and
// reason, and a footer pointing at --prune to remove them. version is the build
// that produced the report.
func (r *Renderer) RenderMarkdown(result SyncResult, repoFullName, version string) {
	_, _ = fmt.Fprintf(r.w, "# Wiki Sync: %s\n\n", repoFullName)
	_, _ = fmt.Fprintf(r.w, "> Reconciled by %s %s\n", attribution.ToolWithVersion(version), attribution.AssistantWith(result.Model))
	report.RenderWikiProvenance(r.w, result.WikiRepo, result.WikiCommit)
	_, _ = fmt.Fprintln(r.w)

	stale, redundant := result.Stale(), result.Redundant()
	if len(stale) == 0 && len(redundant) == 0 {
		_, _ = fmt.Fprintln(r.w, "The wiki is in sync with the code — no stale or redundant entries found.")
		return
	}

	r.renderSection("Stale", "reference code that no longer exists", stale)
	r.renderSection("Redundant", "duplicated or superseded by another entry", redundant)

	_, _ = fmt.Fprintf(r.w, "## Summary\n\n")
	_, _ = fmt.Fprintf(r.w, "**Flagged**: %d (%d stale, %d redundant)\n\n", len(stale)+len(redundant), len(stale), len(redundant))
	_, _ = fmt.Fprintln(r.w, "> [!NOTE]")
	_, _ = fmt.Fprintln(r.w, "> This is a dry run — nothing on the wiki changed. Re-run with `--prune`")
	_, _ = fmt.Fprintln(r.w, "> (or `--apply`) to delete these entries on the wiki after confirmation.")
}

// RenderJSON writes the result as formatted JSON.
func (r *Renderer) RenderJSON(result SyncResult) error {
	enc := json.NewEncoder(r.w)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

// renderSection writes one classification's entries, skipping the section
// entirely when it has none so the report carries no empty noise.
func (r *Renderer) renderSection(label, gloss string, entries []FlaggedEntry) {
	if len(entries) == 0 {
		return
	}
	_, _ = fmt.Fprintf(r.w, "## %s (%d)\n\n", label, len(entries))
	_, _ = fmt.Fprintf(r.w, "Entries that %s.\n\n", gloss)
	for _, e := range entries {
		_, _ = fmt.Fprintf(r.w, "- `%s` (%s) — %s", e.Path, e.Kind, e.Reason)
		if e.SupersededBy != "" {
			_, _ = fmt.Fprintf(r.w, " (superseded by `%s`)", e.SupersededBy)
		}
		if e.Confidence != "" {
			_, _ = fmt.Fprintf(r.w, " [confidence: %s]", e.Confidence)
		}
		_, _ = fmt.Fprintln(r.w)
	}
	_, _ = fmt.Fprintln(r.w)
}

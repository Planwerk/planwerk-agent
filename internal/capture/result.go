package capture

import "github.com/planwerk/planwerk-review/internal/sync"

// ProposedPage is one candidate wiki page the read-only proposal pass authored
// but did NOT write. The gated write-back (#139) consumes it; here it is only
// surfaced in the run report and the issue comment.
type ProposedPage struct {
	// Path is the proposed wiki-relative path in slash form, e.g.
	// "review_patterns/<slug>.md" or "memory/<slug>.md". The slug is stable so a
	// re-run that re-proposes the same decision updates the page rather than
	// appending a new one.
	Path string `json:"path"`
	// Kind is "pattern" or "memory".
	Kind string `json:"kind"`
	// Title is the human-readable page title shown in the report.
	Title string `json:"title"`
	// Body is the authored page content. For a pattern it is the
	// "# Review Pattern: ..." format; for memory it is free-form Markdown. It does
	// NOT carry the provenance marker — RenderPage prepends that at render time.
	Body string `json:"body"`
	// Rationale is why this is worth capturing (the recurring/generalizable
	// justification for a pattern, the durable-decision justification for memory).
	Rationale string `json:"rationale"`
	// Confidence is the proposal confidence ("verified", "likely", "uncertain").
	Confidence string `json:"confidence,omitempty"`
	// IsUpdate is true when Path matches an existing wiki entry, so the report can
	// distinguish proposing a new page from updating one. It is computed from the
	// enumerated entries by MarkUpdates, never authored by the model.
	IsUpdate bool `json:"-"`
}

// CaptureResult is the outcome of the read-only proposal pass: the candidate
// review patterns and memory pages, plus the wiki provenance recorded in the
// report header. Nothing here is written to the wiki.
type CaptureResult struct {
	// Patterns are the proposed review patterns; Memory the proposed memory pages.
	// Either may be empty (no review findings worth a pattern, or no durable
	// rationale worth a memory page).
	Patterns []ProposedPage `json:"patterns"`
	Memory   []ProposedPage `json:"memory"`
	// Model is the resolved Claude model id that produced this result, threaded to
	// the attribution footer and excluded from the payload.
	Model string `json:"-"`
	// WikiRepo and WikiCommit record the wiki and the concrete commit the
	// proposals were deduplicated against, surfaced in the report header for
	// reproducibility. Threaded per-run and excluded from the payload.
	WikiRepo   string `json:"-"`
	WikiCommit string `json:"-"`
}

// HasProposals reports whether the pass proposed at least one page.
func (r CaptureResult) HasProposals() bool {
	return len(r.Patterns) > 0 || len(r.Memory) > 0
}

// MarkUpdates sets IsUpdate on every proposed page whose Path matches an
// existing wiki entry, so the report can tell a fresh page from an update to an
// existing one. The match is computed from the authoritatively enumerated
// entries rather than trusted from the model, so a re-run that re-proposes a
// stable slug is reliably surfaced as an update.
func MarkUpdates(result *CaptureResult, entries []sync.Entry) {
	if result == nil {
		return
	}
	existing := make(map[string]bool, len(entries))
	for _, e := range entries {
		existing[e.Path] = true
	}
	for i := range result.Patterns {
		result.Patterns[i].IsUpdate = existing[result.Patterns[i].Path]
	}
	for i := range result.Memory {
		result.Memory[i].IsUpdate = existing[result.Memory[i].Path]
	}
}

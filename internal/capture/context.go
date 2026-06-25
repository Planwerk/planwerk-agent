package capture

import (
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/report"
	"github.com/planwerk/planwerk-review/internal/sync"
)

// CaptureContext carries the inputs the read-only proposal pass grounds itself
// in: the review findings to mine for generalizable patterns, the plan and the
// implementation report to mine for durable design rationale, and the existing
// wiki entries plus the loaded pattern catalog to deduplicate every candidate
// against. The pass runs inside the cloned repo on the implementation's feature
// branch, so Claude can verify a candidate against the actual changed code
// before proposing it. Mirrors sync.SyncContext.
type CaptureContext struct {
	// RepoName is "owner/repo" for context in the prompt.
	RepoName string
	// IssueNumber is the source issue the implementation came from, named in the
	// provenance marker of every proposed page.
	IssueNumber int
	// BaseBranch scopes the change set the proposal pass reasons about
	// (origin/<base>..HEAD); empty falls back to the repository default branch.
	BaseBranch string
	// Findings are the candidate review findings (already filtered to those that
	// matched no existing pattern, see CandidateFindings). Empty when the review
	// pass was skipped or found nothing — capture then proposes memory pages only.
	Findings []report.Finding
	// Plan is the implementation plan the planning session produced; a source of
	// durable design rationale for candidate memory pages.
	Plan string
	// ImplementReport is the implement session's report; a second source of
	// durable design rationale (deviations, decisions, trade-offs).
	ImplementReport string
	// Entries are the wiki's existing review_patterns/ and memory/ pages, the
	// dedup target so a candidate that duplicates one is dropped or proposed as an
	// update to it.
	Entries []sync.Entry
	// Patterns is the loaded review-pattern catalog, the second dedup target so a
	// candidate already covered by a bundled or project pattern is not re-proposed.
	Patterns []patterns.Pattern
}

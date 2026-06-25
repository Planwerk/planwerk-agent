// Package capture is the read-only proposal engine that mines an implement
// run's review findings, plan, and implementation report for project knowledge
// worth recording on the target repo's GitHub Wiki — without writing anything.
//
// Generalizable, recurring review findings become candidate review patterns;
// durable design rationale drawn from the plan and the implementation report
// becomes candidate memory pages. Every candidate is deduplicated against the
// wiki's existing entries (ReadWikiEntries) and the bundled pattern catalog, so
// capture never manufactures the redundancy the sync pass exists to clean.
//
// The default posture is propose-only: the candidates surface in the run report
// and as an issue comment, and nothing is pushed. The Claude proposal pass runs
// through the harness's read-only runner, so it authors candidate page bytes but
// cannot mutate the checkout or the wiki. The gated write-back (#139) and the
// standalone review/audit reuse (#140) build on this shared engine.
package capture

import "github.com/planwerk/planwerk-review/internal/sync"

// Kinds of proposed pages, reusing the sync vocabulary so capture, sync, and the
// wiki conventions agree on the spelling.
const (
	// KindPattern is a proposed wiki review pattern under review_patterns/.
	KindPattern = sync.KindPattern
	// KindMemory is a proposed free-form project-memory page under memory/.
	KindMemory = sync.KindMemory
)

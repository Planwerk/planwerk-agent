// Package sync reconciles a target repository's GitHub Wiki knowledge — its
// review patterns and project-memory pages — against the current state of the
// code. A read-only analysis pass clones the repo and its wiki and flags entries
// that are stale (they reference code that no longer exists) or redundant
// (duplicated or superseded by another entry), and reports them. --dry-run is the
// default and reports only; --prune/--apply removes the flagged entries on the
// wiki in a separate, explicitly confirmed write phase, never inside the
// read-only pass.
package sync

// Entry kinds and flag classifications, kept as constants so the renderer, the
// prompt, and the structuring schema agree on the spelling.
const (
	// KindPattern is a wiki review pattern under review_patterns/.
	KindPattern = "pattern"
	// KindMemory is a free-form project-memory page under memory/.
	KindMemory = "memory"

	// ClassStale flags an entry that references code (a path, package, symbol)
	// that no longer exists in the current checkout.
	ClassStale = "stale"
	// ClassRedundant flags an entry duplicated or superseded by another entry.
	ClassRedundant = "redundant"
)

// FlaggedEntry is one wiki entry the analysis pass flagged for removal, with the
// reason and (for redundant entries) the entry that supersedes it.
type FlaggedEntry struct {
	// Path is the entry's wiki-relative path in slash form
	// (e.g. "review_patterns/no-raw-sql.md"). It is both the report identifier
	// and the pathspec the write phase deletes.
	Path string `json:"path"`
	// Kind is "pattern" or "memory".
	Kind string `json:"kind"`
	// Classification is "stale" or "redundant".
	Classification string `json:"classification"`
	// Reason cites the concrete missing reference (stale) or the duplication
	// (redundant) so a human can verify the call before pruning.
	Reason string `json:"reason"`
	// SupersededBy names the entry's superseding Path for a redundant entry;
	// empty otherwise.
	SupersededBy string `json:"superseded_by,omitempty"`
	// Confidence is the analysis confidence ("verified", "likely", "uncertain").
	Confidence string `json:"confidence,omitempty"`
}

// SyncResult is the outcome of the read-only reconciliation pass: the flagged
// entries plus the wiki provenance recorded in the report header.
type SyncResult struct {
	// Entries are the flagged wiki entries (stale and redundant). An empty slice
	// means the wiki is in sync with the code.
	Entries []FlaggedEntry `json:"entries"`
	// WikiRepo and WikiCommit record the wiki and the concrete commit the
	// analysis was pinned to, surfaced in the report header for reproducibility
	// and used by the write phase to detect a moved wiki. Threaded per-run and
	// excluded from the serialized payload.
	WikiRepo   string `json:"-"`
	WikiCommit string `json:"-"`
	// Model is the resolved Claude model id that produced this result, threaded
	// to the attribution footer and excluded from the payload.
	Model string `json:"-"`
}

// Stale returns the entries flagged as stale.
func (r SyncResult) Stale() []FlaggedEntry { return r.byClass(ClassStale) }

// Redundant returns the entries flagged as redundant.
func (r SyncResult) Redundant() []FlaggedEntry { return r.byClass(ClassRedundant) }

func (r SyncResult) byClass(class string) []FlaggedEntry {
	var out []FlaggedEntry
	for _, e := range r.Entries {
		if e.Classification == class {
			out = append(out, e)
		}
	}
	return out
}

// DeletionPaths returns the de-duplicated wiki-relative paths of every flagged
// entry, in first-seen order — the set the write phase removes. An entry flagged
// twice (e.g. both stale and redundant) yields one path.
func (r SyncResult) DeletionPaths() []string {
	seen := make(map[string]bool, len(r.Entries))
	var paths []string
	for _, e := range r.Entries {
		if e.Path == "" || seen[e.Path] {
			continue
		}
		seen[e.Path] = true
		paths = append(paths, e.Path)
	}
	return paths
}

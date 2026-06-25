package sync

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/planwerk/planwerk-review/internal/report"
	"github.com/planwerk/planwerk-review/internal/workspace"
)

// runWritePhase is the --prune write phase: it confirms the deletion with the
// operator, clones the wiki fresh, deletes the flagged entries that still exist,
// and pushes. It runs only after the read-only analysis and report, never inside
// them, so a destructive change is always gated behind an explicit confirmation.
//
// allowed is the set of wiki-relative paths ReadWikiEntries actually enumerated.
// A flagged path outside it is refused, never deleted: result.DeletionPaths() is
// model output derived from untrusted, world-editable wiki bodies, so without
// this gate a crafted page could steer the analysis to flag — and an unattended
// --prune --yes job to delete — a page that was never enumerated.
func (r *Runner) runWritePhase(w io.Writer, opts Options, result *SyncResult, allowed map[string]bool) error {
	var paths []string
	for _, p := range result.DeletionPaths() {
		if allowed[p] {
			paths = append(paths, p)
		} else {
			_, _ = fmt.Fprintf(w, "Refusing to prune %q — not one of the enumerated wiki entries.\n", p)
		}
	}
	if len(paths) == 0 {
		_, _ = fmt.Fprintln(w, "Nothing to prune — no flagged entry matched an enumerated wiki page.")
		return nil
	}

	_, _ = fmt.Fprintf(w, "\n--prune will delete %d wiki %s from %s:\n", len(paths), entryWord(len(paths)), result.WikiRepo)
	for _, p := range paths {
		_, _ = fmt.Fprintf(w, "  %s\n", p)
	}

	if !opts.Yes {
		if !r.IsTTY() {
			return fmt.Errorf("refusing to prune the wiki without confirmation: stdin is not a TTY; re-run with --yes to confirm non-interactively")
		}
		prompter := workspace.StdinPrompter{In: r.In, Out: w}
		ok, err := prompter.Confirm(fmt.Sprintf("Delete %s on the %s wiki and push? (y/N): ", countedEntries(len(paths)), result.WikiRepo))
		if err != nil {
			return fmt.Errorf("confirming wiki prune: %w", err)
		}
		if !ok {
			_, _ = fmt.Fprintln(w, "Aborted — the wiki was not changed.")
			return nil
		}
	}

	// Clone the wiki fresh for the write, isolated from the TTL-cached read clone
	// so a concurrent run or cache refresh cannot race the deletion.
	dir, headSHA, cleanup, err := r.Writer.Clone(result.WikiRepo, opts.Wiki.Ref)
	if err != nil {
		return fmt.Errorf("cloning wiki for write-back: %w", err)
	}
	defer cleanup()

	if headSHA != "" && result.WikiCommit != "" && headSHA != result.WikiCommit {
		_, _ = fmt.Fprintf(w, "Note: the wiki moved since analysis (%s → %s); pruning only the flagged entries that still exist.\n",
			report.ShortSHA(result.WikiCommit), report.ShortSHA(headSHA))
	}

	// Delete only the entries that still exist in the fresh clone. Between the
	// analysis and now the wiki may have changed (a flagged entry was already
	// removed, or the whole wiki moved), so a stale path is reported and skipped
	// rather than failing the push.
	existing, skipped := partitionExisting(dir, paths)
	for _, s := range skipped {
		_, _ = fmt.Fprintf(w, "Skipped %s — already gone from the wiki.\n", s)
	}
	if len(existing) == 0 {
		_, _ = fmt.Fprintln(w, "Nothing to prune — every flagged entry is already gone from the wiki.")
		return nil
	}

	if err := r.Writer.ApplyDeletions(dir, existing, pruneCommitMsg(existing)); err != nil {
		return fmt.Errorf("pushing wiki deletions: %w", err)
	}

	_, _ = fmt.Fprintf(w, "Pruned %s and pushed to the %s wiki.\n", countedEntries(len(existing)), result.WikiRepo)
	return nil
}

// partitionExisting splits paths into those that still exist under dir and those
// already gone, preserving order.
func partitionExisting(dir string, paths []string) (existing, skipped []string) {
	for _, p := range paths {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err == nil {
			existing = append(existing, p)
		} else {
			skipped = append(skipped, p)
		}
	}
	return existing, skipped
}

// pruneCommitMsg renders the commit subject and body for a prune push.
func pruneCommitMsg(paths []string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Prune %s\n\nRemoved by planwerk-review sync:\n", countedEntries(len(paths)))
	for _, p := range paths {
		fmt.Fprintf(&sb, "- %s\n", p)
	}
	return sb.String()
}

// entryWord returns "entry" or "entries" for n.
func entryWord(n int) string {
	if n == 1 {
		return "entry"
	}
	return "entries"
}

// countedEntries returns "1 entry" / "N entries".
func countedEntries(n int) string {
	return fmt.Sprintf("%d %s", n, entryWord(n))
}

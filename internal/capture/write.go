package capture

import (
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/report"
	"github.com/planwerk/planwerk-review/internal/workspace"
)

// WikiWriter performs the capture write phase: a fresh authenticated clone of
// the wiki and the addition+push of the accepted pages. It is an interface so
// the write phase can be exercised without cloning or pushing a real wiki, and
// so the open review/audit reuse (#140) can route through the same engine. The
// default implementation is backed by the patterns package's write-back
// helpers. Mirrors sync.WikiWriter.
type WikiWriter interface {
	// Clone makes a fresh authenticated clone of repo (an "owner/name") at ref
	// and returns the clone root, its HEAD commit, and a cleanup function.
	Clone(repo, ref string) (dir, headSHA string, cleanup func(), err error)
	// ApplyAdditions writes files into the clone at dir, commits with msg, and
	// pushes.
	ApplyAdditions(dir string, files []patterns.WikiFile, msg string) error
}

// DefaultWikiWriter is the production WikiWriter backed by the patterns package.
// Mirrors sync's defaultWikiWriter; exported so the implement Runner (and the
// open #140 reuse) can default the write seam to it.
type DefaultWikiWriter struct{}

// Clone makes a fresh authenticated clone of the wiki.
func (DefaultWikiWriter) Clone(repo, ref string) (string, string, func(), error) {
	return patterns.CloneWikiAuthenticated(repo, ref)
}

// ApplyAdditions writes, commits, and pushes the accepted pages.
func (DefaultWikiWriter) ApplyAdditions(dir string, files []patterns.WikiFile, msg string) error {
	return patterns.PushWikiAdditions(dir, files, msg)
}

// WritePhase is the gated, opt-in write half of the capture loop: it takes the
// accepted pages from the read-only proposal pass and pushes them to the wiki —
// the additive counterpart to sync's delete-only --prune write phase. The
// surrounding implement pass keeps it off by default (a normal run stays
// propose-only) and engages it only under --capture-wiki.
//
// Like sync.runWritePhase the write is strictly separate from the read-only
// authoring: Claude authored the page bytes in the read-only proposal pass and
// never pushes; this phase performs the mechanical add+commit+push. It confirms
// interactively and refuses a non-TTY run without yes, then clones the wiki
// fresh (isolated from the read clone so a concurrent run or cache refresh
// cannot race the push), renders each accepted page with its provenance marker,
// and pushes them as one commit.
func WritePhase(w io.Writer, in io.Reader, isTTY func() bool, yes bool, writer WikiWriter, result *CaptureResult, prov Provenance, ref string) error {
	pages := result.AllPages()
	if len(pages) == 0 {
		_, _ = fmt.Fprintln(w, "Nothing to write — capture proposed no pages.")
		return nil
	}

	// Validate every model-authored path before prompting or touching the wiki:
	// ProposedPage.Path is decoded verbatim from the model's JSON, so a path that
	// escapes the wiki root — or a duplicate that would silently overwrite a
	// sibling — must abort the whole write, not reach the filesystem alongside the
	// good pages.
	if err := validateWikiPaths(pages); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(w, "\n--capture-wiki will write %s to the %s wiki:\n", countedPages(len(pages)), result.WikiRepo)
	for _, p := range pages {
		verb := "new"
		if p.IsUpdate {
			verb = "update"
		}
		_, _ = fmt.Fprintf(w, "  %s (%s)\n", p.Path, verb)
	}

	if !yes {
		if !isTTY() {
			return fmt.Errorf("refusing to write to the wiki without confirmation: stdin is not a TTY; re-run with --yes to confirm non-interactively")
		}
		prompter := workspace.StdinPrompter{In: in, Out: w}
		ok, err := prompter.Confirm(fmt.Sprintf("Write %s to the %s wiki and push? (y/N): ", countedPages(len(pages)), result.WikiRepo))
		if err != nil {
			return fmt.Errorf("confirming wiki write: %w", err)
		}
		if !ok {
			_, _ = fmt.Fprintln(w, "Aborted — the wiki was not changed.")
			return nil
		}
	}

	// Clone the wiki fresh for the write, isolated from the read clone so a
	// concurrent run or cache refresh cannot race the push.
	dir, headSHA, cleanup, err := writer.Clone(result.WikiRepo, ref)
	if err != nil {
		return fmt.Errorf("cloning wiki for write-back: %w", err)
	}
	defer cleanup()

	// If the wiki moved since the proposal pass, every IsUpdate page was authored
	// as a full replacement against a now-stale snapshot. Writing it would clobber
	// any human edit that landed in between with no merge, so skip updates and
	// write only the additive new pages — those create fresh files and cannot lose
	// an edit. The divergence drives this branch rather than being merely noted.
	wikiMoved := headSHA != "" && result.WikiCommit != "" && headSHA != result.WikiCommit
	if wikiMoved {
		_, _ = fmt.Fprintf(w, "Note: the wiki moved since the proposal pass (%s → %s); writing new pages and skipping updates to avoid overwriting newer edits.\n",
			report.ShortSHA(result.WikiCommit), report.ShortSHA(headSHA))
	}

	written := make([]ProposedPage, 0, len(pages))
	for _, p := range pages {
		if wikiMoved && p.IsUpdate {
			_, _ = fmt.Fprintf(w, "Skipped %s — the wiki diverged since the proposal pass; not overwriting it from a stale snapshot.\n", p.Path)
			continue
		}
		written = append(written, p)
	}
	if len(written) == 0 {
		_, _ = fmt.Fprintln(w, "Nothing to write — every accepted page was an update the diverged wiki would clobber.")
		return nil
	}

	files := make([]patterns.WikiFile, 0, len(written))
	for _, p := range written {
		files = append(files, patterns.WikiFile{Path: p.Path, Content: RenderPage(p, prov)})
	}
	if err := writer.ApplyAdditions(dir, files, additionsCommitMsg(written, prov)); err != nil {
		return fmt.Errorf("pushing wiki additions: %w", err)
	}

	_, _ = fmt.Fprintf(w, "Wrote %s and pushed to the %s wiki.\n", countedPages(len(written)), result.WikiRepo)
	return nil
}

// validateWikiPaths checks every model-authored page path before the write phase
// touches the wiki: each must be a safe, canonical, wiki-relative location (see
// validateWikiPath) and no two pages may target the same path. Both are abort
// conditions, not per-page skips — ProposedPage.Path is decoded verbatim from the
// model's JSON and reaches the filesystem, so a single crafted or colliding path
// must never write the surrounding pages alongside it. The duplicate guard also
// stops two same-path proposals from collapsing into one committed file, where the
// operator was told both were written.
func validateWikiPaths(pages []ProposedPage) error {
	seen := make(map[string]bool, len(pages))
	for _, p := range pages {
		if err := validateWikiPath(p.Path); err != nil {
			return err
		}
		if seen[p.Path] {
			return fmt.Errorf("duplicate wiki page path %q: two proposed pages target the same file", p.Path)
		}
		seen[p.Path] = true
	}
	return nil
}

// validateWikiPath rejects a page path that is empty, absolute, non-canonical,
// escapes the wiki root via "..", or falls outside the review_patterns/ and
// memory/ allowlist. The checks use slash (path) semantics because the path is
// always wiki-relative slash form, independent of the host separator: rejecting
// only absolute paths is insufficient, since filepath.Join Cleans a leading "../"
// and lets it escape the clone root and be written before the later `git add`
// could reject the out-of-tree pathspec.
func validateWikiPath(p string) error {
	if p == "" {
		return fmt.Errorf("empty wiki page path")
	}
	if path.IsAbs(p) {
		return fmt.Errorf("wiki page path %q must be relative, not absolute", p)
	}
	if path.Clean(p) != p {
		return fmt.Errorf("wiki page path %q is not canonical", p)
	}
	if p == ".." || strings.HasPrefix(p, "../") {
		return fmt.Errorf("wiki page path %q escapes the wiki root", p)
	}
	if !strings.HasPrefix(p, "review_patterns/") && !strings.HasPrefix(p, "memory/") {
		return fmt.Errorf("wiki page path %q must be under review_patterns/ or memory/", p)
	}
	return nil
}

// additionsCommitMsg renders the commit subject and body for a capture write,
// naming the source run so the wiki history records where each page came from.
func additionsCommitMsg(pages []ProposedPage, prov Provenance) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Capture %s\n\nWritten by planwerk-review from %s#%d:\n", countedPages(len(pages)), prov.Repo, prov.Issue)
	for _, p := range pages {
		fmt.Fprintf(&sb, "- %s\n", p.Path)
	}
	return sb.String()
}

// countedPages returns "1 page" / "N pages".
func countedPages(n int) string {
	word := "pages"
	if n == 1 {
		word = "page"
	}
	return fmt.Sprintf("%d %s", n, word)
}

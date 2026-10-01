package capture

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
	"github.com/planwerk/planwerk-agent/internal/workspace"
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
// never pushes; this phase renders each accepted page with its provenance
// marker and hands the pages to WritePages, which confirms, clones, and pushes.
func WritePhase(w io.Writer, in io.Reader, isTTY func() bool, yes bool, writer WikiWriter, result *CaptureResult, prov Provenance, ref string) error {
	proposed := result.AllPages()
	if len(proposed) == 0 {
		_, _ = fmt.Fprintln(w, "Nothing to write — capture proposed no pages.")
		return nil
	}

	pages := make([]PageWrite, 0, len(proposed))
	for _, p := range proposed {
		pages = append(pages, PageWrite{Path: p.Path, Content: RenderPage(p, prov), IsUpdate: p.IsUpdate})
	}
	_, err := WritePages(w, in, isTTY, writer, WriteRequest{
		Flag:       "--capture-wiki",
		WikiRepo:   result.WikiRepo,
		WikiCommit: result.WikiCommit,
		Ref:        ref,
		Pages:      pages,
		CommitMsg:  func(written []PageWrite) string { return additionsCommitMsg(written, prov) },
		Yes:        yes,
	})
	if errors.Is(err, ErrNoTerminal) {
		return fmt.Errorf("%w; re-run with --yes to confirm non-interactively", err)
	}
	return err
}

// ErrNoTerminal is the refusal of WritePages to write without a confirmation
// when stdin is not a terminal. The caller adds what its command offers
// instead.
var ErrNoTerminal = errors.New("refusing to write to the wiki without confirmation: stdin is not a TTY")

// PageWrite is one page WritePages pushes: its wiki-relative path in slash
// form, the full file content, and whether the caller read the page from the
// wiki before it wrote this content (an update) or holds it as a new page.
type PageWrite struct {
	Path     string
	Content  string
	IsUpdate bool
}

// WriteRequest is one gated wiki write.
type WriteRequest struct {
	// Flag is the command-line flag that asked for the write, named in the
	// listing line, e.g. "--capture-wiki".
	Flag string
	// WikiRepo is the wiki to write to, as "owner/name". WikiCommit is the wiki
	// commit the pages were authored against; when the fresh clone is at
	// another commit, updates are skipped. Ref pins the clone to a branch.
	WikiRepo   string
	WikiCommit string
	Ref        string
	// Pages are the pages to write, in the order they are listed and committed.
	Pages []PageWrite
	// CommitMsg renders the commit message for the pages that are written.
	CommitMsg func(written []PageWrite) string
	// Yes skips the confirmation prompt.
	Yes bool
}

// WritePages pushes req.Pages to the wiki as one commit and returns the paths
// it wrote. It validates the paths, lists the pages, and confirms
// interactively: without req.Yes a run whose stdin is not a terminal is
// refused with ErrNoTerminal, and a declined prompt writes nothing. It then
// clones the wiki fresh (isolated from the read clone so a concurrent run or
// cache refresh cannot race the push) and pushes. A clone that holds memory or
// review_patterns as a symbolic link is refused. Two kinds of page are
// skipped, each with a line on w: an update when the wiki moved since
// req.WikiCommit, and a new page whose path the clone already holds. No pages,
// a declined prompt, and a run in which every page was skipped return nil and
// no error.
func WritePages(w io.Writer, in io.Reader, isTTY func() bool, writer WikiWriter, req WriteRequest) (written []string, err error) {
	pages := req.Pages
	if len(pages) == 0 {
		return nil, nil
	}

	// Validate every model-authored path before prompting or touching the wiki:
	// a page path is decoded verbatim from the model's JSON, so a path that
	// escapes the wiki root — or a duplicate that would silently overwrite a
	// sibling — must abort the whole write, not reach the filesystem alongside the
	// good pages.
	if err := validateWikiPaths(pages); err != nil {
		return nil, err
	}

	_, _ = fmt.Fprintf(w, "\n%s will write %s to the %s wiki:\n", req.Flag, countedPages(len(pages)), req.WikiRepo)
	for _, p := range pages {
		verb := "new"
		if p.IsUpdate {
			verb = "update"
		}
		_, _ = fmt.Fprintf(w, "  %s (%s)\n", p.Path, verb)
	}

	if !req.Yes {
		if !isTTY() {
			return nil, ErrNoTerminal
		}
		prompter := workspace.StdinPrompter{In: in, Out: w}
		ok, err := prompter.Confirm(fmt.Sprintf("Write %s to the %s wiki and push? (y/N): ", countedPages(len(pages)), req.WikiRepo))
		if err != nil {
			return nil, fmt.Errorf("confirming wiki write: %w", err)
		}
		if !ok {
			_, _ = fmt.Fprintln(w, "Aborted — the wiki was not changed.")
			return nil, nil
		}
	}

	// Clone the wiki fresh for the write, isolated from the read clone so a
	// concurrent run or cache refresh cannot race the push.
	dir, headSHA, cleanup, err := writer.Clone(req.WikiRepo, req.Ref)
	if err != nil {
		return nil, fmt.Errorf("cloning wiki for write-back: %w", err)
	}
	defer cleanup()

	// Anyone who can edit the wiki can commit one of its two page directories
	// as a symbolic link, and a page written through it lands wherever the link
	// points on this machine.
	for _, sub := range []string{"memory", "review_patterns"} {
		if info, err := os.Lstat(filepath.Join(dir, sub)); err == nil && info.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("the %s wiki holds %s as a symbolic link; refusing to write through it", req.WikiRepo, sub)
		}
	}

	// If the wiki moved since the pages were read, every IsUpdate page was authored
	// as a full replacement against a now-stale snapshot. Writing it would clobber
	// any human edit that landed in between with no merge, so skip updates and
	// write only the additive new pages — those create fresh files and cannot lose
	// an edit. The divergence drives this branch rather than being merely noted.
	wikiMoved := headSHA != "" && req.WikiCommit != "" && headSHA != req.WikiCommit
	if wikiMoved {
		_, _ = fmt.Fprintf(w, "Note: the wiki moved since these pages were read (%s → %s); writing new pages and skipping updates to avoid overwriting newer edits.\n",
			report.ShortSHA(req.WikiCommit), report.ShortSHA(headSHA))
	}

	kept := make([]PageWrite, 0, len(pages))
	unread := 0
	for _, p := range pages {
		if wikiMoved && p.IsUpdate {
			_, _ = fmt.Fprintf(w, "Skipped %s — the wiki changed since it was read; not overwriting it from a stale snapshot.\n", p.Path)
			continue
		}
		// A new page is one the caller did not read from the wiki. When the
		// clone holds a file at its path all the same, someone added it since,
		// and writing would replace text nobody on this run has seen.
		if !p.IsUpdate && existsInClone(dir, p.Path) {
			_, _ = fmt.Fprintf(w, "Skipped %s — it exists on the wiki, and this run did not read it.\n", p.Path)
			unread++
			continue
		}
		kept = append(kept, p)
	}
	if len(kept) == 0 {
		if unread > 0 {
			_, _ = fmt.Fprintln(w, "Nothing to write — every page was skipped.")
		} else {
			_, _ = fmt.Fprintln(w, "Nothing to write — every page was an update the diverged wiki would clobber.")
		}
		return nil, nil
	}

	files := make([]patterns.WikiFile, 0, len(kept))
	for _, p := range kept {
		files = append(files, patterns.WikiFile{Path: p.Path, Content: p.Content})
		written = append(written, p.Path)
	}
	if err := writer.ApplyAdditions(dir, files, req.CommitMsg(kept)); err != nil {
		return nil, fmt.Errorf("pushing wiki additions: %w", err)
	}

	_, _ = fmt.Fprintf(w, "Wrote %s and pushed to the %s wiki.\n", countedPages(len(kept)), req.WikiRepo)
	return written, nil
}

// existsInClone reports whether the wiki clone at dir holds an entry at the
// wiki-relative slash path p. Lstat reports a symlink as present without
// following it. A path Lstat cannot read counts as absent: the write that
// follows fails on the same path and surfaces the cause.
func existsInClone(dir, p string) bool {
	_, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(p)))
	return err == nil
}

// validateWikiPaths checks every model-authored page path before the write phase
// touches the wiki: each must be a safe, canonical, wiki-relative location (see
// validateWikiPath) and no two pages may target the same path. Both are abort
// conditions, not per-page skips — a page path is decoded verbatim from the
// model's JSON and reaches the filesystem, so a single crafted or colliding path
// must never write the surrounding pages alongside it. The duplicate guard also
// stops two same-path proposals from collapsing into one committed file, where the
// operator was told both were written.
func validateWikiPaths(pages []PageWrite) error {
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
func additionsCommitMsg(pages []PageWrite, prov Provenance) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Capture %s\n\nWritten by planwerk-agent from %s#%d:\n", countedPages(len(pages)), prov.Repo, prov.Issue)
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

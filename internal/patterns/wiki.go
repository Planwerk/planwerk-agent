package patterns

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Wiki convention defaults. A target repo's GitHub Wiki holds review patterns
// under review_patterns/ (in the same format as .planwerk/review_patterns/) and
// free-form project memory pages under memory/.
const (
	defaultWikiPatternsSubpath = "review_patterns"
	defaultWikiMemorySubpath   = "memory"
)

// maxMemoryBytes caps one project-memory page, and the total of the page bodies
// FormatMemoryBodies renders into a prompt. A wiki is human-editable through
// the web UI, so an over-long (or runaway) page would balloon the memory and
// its API cost; a page past the cap is skipped with a warning. Mirrors
// glossary.maxGlossaryBytes.
const maxMemoryBytes = 64 * 1024

// maxMemoryTotalBytes caps the page bodies LoadMemoryPages keeps for one run.
// The per-page cap bounds a page, not their number: every kept body is held in
// memory and written again by MaterializeMemory, so a wiki with tens of
// thousands of pages would otherwise cost gigabytes of both.
const maxMemoryTotalBytes = 16 * 1024 * 1024

// wikiRevParseTimeout bounds the `git rev-parse HEAD` that resolves the wiki to
// a concrete commit for the report.
const wikiRevParseTimeout = 30 * time.Second

// WikiOptions configures whether and how a target repo's GitHub Wiki is used as
// a knowledge source. The zero value is disabled, so a caller that does not opt
// in resolves no wiki. The CLI leaves Enabled off by default: a wiki is a
// separate permission surface (often world-editable, never gated by branch
// protection or PR review), so its content is fed to the agent only on an
// explicit per-repo opt-in (--wiki).
type WikiOptions struct {
	// Enabled turns wiki resolution on. When false ResolveWiki returns the zero
	// ResolvedWiki without touching the network.
	Enabled bool
	// Repo overrides the wiki source as "owner/repo"; empty derives it from the
	// resolved target repository. The wiki cloned is always <repo>.wiki.git.
	Repo string
	// Ref pins the wiki to a branch, tag, or commit; empty uses the wiki's
	// default branch.
	Ref string
}

// ResolvedWiki is the outcome of resolving a target repo's wiki. The zero value
// (every field empty) means "no wiki" — disabled, uninitialized, offline, or
// failed — and every consumer treats it as such, so a run without a usable wiki
// proceeds unchanged.
type ResolvedWiki struct {
	// Repo is the "owner/repo" the wiki was resolved for, for the report header.
	Repo string
	// CommitSHA is the wiki's resolved HEAD commit, recorded in the report so a
	// review is reproducible against a fixed wiki state. Empty when unresolved.
	CommitSHA string
	// Dir is the local clone root of the resolved wiki, so a consumer that needs
	// per-entry access (e.g. sync, enumerating review_patterns/ and memory/ as
	// individual pages) can walk it. Empty when no wiki was resolved. PatternsDir
	// is a subdirectory of it; MemoryPages is read from its memory/ subdir.
	Dir string
	// PatternsDir is the local directory the wiki's review patterns live in, to
	// feed ResolveOptions.Wiki. Empty when the wiki has no patterns subdir.
	PatternsDir string
	// MemoryPages are the project-memory pages read from the wiki's memory/
	// subdir, sorted by file name. Nil when the wiki has no memory.
	MemoryPages []MemoryPage
}

// MemoryPage is one project-memory page read from a wiki's memory/ directory.
type MemoryPage struct {
	Name    string // file name under memory/, ".md" included
	Title   string // first "# " heading, or Name without ".md"
	Summary string // value of the page's "**Summary**:" line, or ""
	Body    string // page text, trimmed
}

// ResolveWiki materializes the target repo's GitHub Wiki and returns its review
// patterns directory, project-memory pages, and resolved commit. It is
// best-effort, mirroring glossary.LoadBody: when disabled, or when the wiki is
// uninitialized, offline, or otherwise unresolvable, it logs and returns the
// zero ResolvedWiki so the caller runs unchanged rather than failing.
//
// The wiki is cloned (and refreshed by TTL) through the same remote-cache
// machinery as --patterns remote sources, via the wiki: URI shorthand, so it
// reuses caching, locking, and token authentication. It is resolved at run
// start and pinned to a concrete commit so a review does not drift with a moving
// wiki between runs.
func ResolveWiki(owner, name string, wopts WikiOptions, ropts RemoteOptions) ResolvedWiki {
	if !wopts.Enabled {
		return ResolvedWiki{}
	}

	repo := wopts.Repo
	if repo == "" {
		repo = owner + "/" + name
	}

	// Build the wiki: URI for the clone root (no subpath, so the cache holds the
	// whole wiki and the subpaths below are joined locally).
	uri := prefixWiki + repo
	if wopts.Ref != "" {
		uri += "@" + wopts.Ref
	}

	dir, err := ResolveRemote(uri, ropts)
	if err != nil {
		slog.Warn("could not resolve target repo wiki, proceeding without it", "repo", repo, "err", err)
		return ResolvedWiki{}
	}

	patternsDir := filepath.Join(dir, defaultWikiPatternsSubpath)
	// Lstat, not Stat: the directory can be a symlink committed to the wiki, and
	// Stat would follow it, so a consumer of PatternsDir would list its target.
	// LoadMemoryPages guards memory/ the same way.
	info, err := os.Lstat(patternsDir)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		slog.Warn("wiki review patterns directory is a symlink; skipping", "dir", patternsDir)
	}
	if err != nil || !info.IsDir() {
		patternsDir = "" // no review-patterns directory in this wiki
	}

	resolved := ResolvedWiki{
		Repo:        repo,
		CommitSHA:   wikiHeadSHA(dir),
		Dir:         dir,
		PatternsDir: patternsDir,
		MemoryPages: LoadMemoryPages(filepath.Join(dir, defaultWikiMemorySubpath)),
	}
	slog.Info("resolved target repo wiki",
		"repo", repo,
		"commit", resolved.CommitSHA,
		"has_patterns", resolved.PatternsDir != "",
		"memory_pages", len(resolved.MemoryPages))
	return resolved
}

// wikiHeadSHA returns the wiki clone's HEAD commit, or "" when it cannot be
// resolved (e.g. an empty repo or a git failure). A missing SHA only costs the
// reproducibility note in the report, so it is non-fatal.
func wikiHeadSHA(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), wikiRevParseTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		slog.Warn("could not resolve wiki commit", "dir", dir, "err", err)
		return ""
	}
	return strings.TrimSpace(string(out))
}

// LoadMemoryPages reads every *.md page under dir and returns the pages sorted
// by file name, each with the title and summary an index line shows. Every .md
// page under the memory subdir is project memory by convention; the patterns
// the wiki author keeps for review live under the separate review_patterns
// subdir. A page larger than maxMemoryBytes is skipped with a warning, and so
// is a page whose file name contains a control character, because the name is
// printed on an index line. Each of the two warnings counts its pages and
// names none: a skill session reads the log of `brain memory`, and a file name
// is wiki content. The names are logged at debug level, quoted. The kept
// bodies stay within maxMemoryTotalBytes:
// the first page that would exceed it ends the load with a warning, and the
// pages after it are not read. A missing, unreadable, symlinked, or empty
// directory yields nil.
func LoadMemoryPages(dir string) []MemoryPage {
	// The directory can be a symlink committed to the wiki, and os.ReadDir
	// would list its target: the regular *.md files behind the link pass the
	// per-entry guard below. os.Lstat reports the entry's own type, so the link
	// is rejected before anything lists what it points at.
	if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		slog.Warn("project memory directory is a symlink; skipping", "dir", dir)
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // absent or unreadable memory dir: no memory
	}

	names := make([]string, 0, len(entries))
	controlNamed := 0
	for _, e := range entries {
		// Skip directories, symlinks, and non-markdown entries. The symlink
		// guard is load-bearing: a wiki is world-editable, so a *.md symlink
		// pointing at e.g. ~/.aws/credentials or ~/.ssh/id_rsa would otherwise be
		// followed by the read below and its target handed to a session.
		// os.DirEntry reports the entry's own type without following it, so a
		// symlink is rejected here before anything opens its target.
		if e.IsDir() || e.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if strings.ContainsFunc(e.Name(), unicode.IsControl) {
			// Quoted: the console log writes a value as it is, and the raw name
			// would drive the terminal it is printed to.
			slog.Debug("project memory page name contains a control character; skipping", "dir", dir, "page", strconv.Quote(e.Name()))
			controlNamed++
			continue
		}
		names = append(names, e.Name())
	}
	if controlNamed > 0 {
		slog.Warn("skipped project memory pages whose file name contains a control character; rerun with --verbose for the names", "dir", dir, "pages", controlNamed)
	}
	sort.Strings(names)

	var pages []MemoryPage
	total, oversized := 0, 0
	for i, n := range names {
		body, err := readMemoryPage(filepath.Join(dir, n))
		if err != nil {
			// An oversized page is counted for the warning below; an unreadable
			// one is skipped silently. Either way a single bad page must not
			// suppress the legitimate pages after it (hence continue, not break).
			if errors.Is(err, errMemoryPageTooLarge) {
				slog.Debug("project memory page exceeds size cap; skipping", "dir", dir, "page", strconv.Quote(n), "cap", maxMemoryBytes)
				oversized++
			}
			continue
		}
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		if total+len(body) > maxMemoryTotalBytes {
			slog.Warn("project memory exceeds total size cap; skipping the remaining pages", "dir", dir, "remaining", len(names)-i, "cap", maxMemoryTotalBytes)
			break
		}
		total += len(body)
		title, summary := memoryPageFields(n, body)
		pages = append(pages, MemoryPage{Name: n, Title: title, Summary: summary, Body: body})
	}
	if oversized > 0 {
		slog.Warn("skipped project memory pages that exceed the size cap; rerun with --verbose for the names", "dir", dir, "pages", oversized, "cap", maxMemoryBytes)
	}
	return pages
}

// memoryPageFields returns the title and the summary of the memory page named
// name: the text of the first "# " heading and the value of the first
// "**Summary**:" line in body, ignoring lines inside a fenced code block. A
// page without a heading gets name without ".md" as its title, and a page
// without a summary line gets "".
func memoryPageFields(name, body string) (title, summary string) {
	const titlePrefix, summaryPrefix = "# ", "**Summary**:"
	var fenceMarker byte // the character that opened the current code block
	var fenceLen int     // the length of the run that opened it, 0 outside a block
	var haveTitle, haveSummary bool
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		marker, n := fenceRun(trimmed)
		switch {
		case fenceLen > 0:
			// A block closes on a line that holds only its own marker, at least
			// as long as the run that opened it; anything else is code inside it.
			if marker == fenceMarker && n >= fenceLen && n == len(trimmed) {
				fenceLen = 0
			}
		// A backtick fence's info string holds no backtick (CommonMark), so a line
		// such as "```cmd``` runs it" is an inline code span, not a fence.
		case n > 0 && (marker != '`' || !strings.Contains(trimmed[n:], "`")):
			fenceMarker, fenceLen = marker, n
		case !haveTitle && strings.HasPrefix(line, titlePrefix):
			title, haveTitle = extractValue(line, titlePrefix), true
		case !haveSummary && strings.HasPrefix(line, summaryPrefix):
			summary, haveSummary = extractValue(line, summaryPrefix), true
		}
	}
	if title == "" {
		title = strings.TrimSuffix(name, ".md")
	}
	return title, summary
}

// fenceRun returns the fence character that starts trimmed and the length of
// its run, or 0, 0 when the line starts with fewer than three of them.
func fenceRun(trimmed string) (marker byte, n int) {
	if trimmed == "" || (trimmed[0] != '`' && trimmed[0] != '~') {
		return 0, 0
	}
	marker = trimmed[0]
	for n < len(trimmed) && trimmed[n] == marker {
		n++
	}
	if n < 3 {
		return 0, 0
	}
	return marker, n
}

// errMemoryPageTooLarge marks a memory page that exceeds maxMemoryBytes on its
// own, so LoadMemoryPages can warn about it specifically rather than treating it
// like an unreadable file.
var errMemoryPageTooLarge = errors.New("memory page exceeds size cap")

// readMemoryPage reads a single memory page through a bounded read: it opens the
// file and pulls at most maxMemoryBytes+1 bytes via an io.LimitReader, so a
// runaway (multi-gigabyte) page never allocates more than the cap before it is
// rejected. It returns errMemoryPageTooLarge when the page is larger than
// maxMemoryBytes, so the caller skips the whole page rather than truncating it
// mid-content. Reading the cap into a []byte first (os.ReadFile) would allocate
// the entire file before any size check could run.
func readMemoryPage(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxMemoryBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxMemoryBytes {
		return "", errMemoryPageTooLarge
	}
	return string(data), nil
}

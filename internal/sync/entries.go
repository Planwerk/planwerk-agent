package sync

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// mdExt is the markdown extension wiki pages use.
const mdExt = ".md"

// maxEntryBytes caps a single wiki page read into memory. The wiki is
// world-editable, so an over-long (or runaway) page would balloon the prompt and
// its API cost; pages past the cap are skipped with a warning. Mirrors
// patterns.maxMemoryBytes and extract.maxPatternBytes.
const maxEntryBytes = 64 * 1024

// errEntryTooLarge marks a wiki page that exceeds maxEntryBytes on its own, so
// ReadWikiEntries can warn about it specifically rather than treating it like an
// unreadable file.
var errEntryTooLarge = errors.New("wiki entry exceeds size cap")

// wikiSubdir pairs a wiki subdirectory with the Entry kind its pages carry.
type wikiSubdir struct {
	name string
	kind string
}

// wikiSubdirs are the two conventions sync reconciles against the code: review
// patterns and project-memory pages (decision #47). The wiki's "reviews" live as
// memory pages — there is no separate reviews/ convention to enumerate.
var wikiSubdirs = []wikiSubdir{
	{name: "review_patterns", kind: KindPattern},
	{name: "memory", kind: KindMemory},
}

// Entry is one wiki page available for reconciliation: its wiki-relative path
// (slash form), kind (pattern/memory), and raw content. The analysis pass reads
// Raw to judge whether the entry is stale or redundant.
type Entry struct {
	Path string
	Kind string
	Raw  string
}

// ReadWikiEntries enumerates the wiki's review_patterns/ and memory/ pages from
// the clone root and returns one Entry per page, sorted by path for a stable
// order. An absent subdirectory is skipped (a wiki may carry only patterns or
// only memory). Directories, symlinks, non-markdown files, the standard wiki
// navigation pages (Home, _Sidebar, _Footer), and the SOURCES catalog are
// skipped, so a normal wiki holding both navigation and knowledge enumerates only
// the knowledge. It is exported so the capture pass can deduplicate its
// proposals against the same enumerated entries the sync pass reconciles.
func ReadWikiEntries(wikiDir string) ([]Entry, error) {
	var entries []Entry
	for _, sub := range wikiSubdirs {
		dir := filepath.Join(wikiDir, sub.name)
		files, err := os.ReadDir(dir)
		if err != nil {
			continue // absent or unreadable subdir: nothing to enumerate here
		}
		for _, f := range files {
			// Skip directories, symlinks, and non-markdown entries. The symlink
			// guard is load-bearing: the wiki is world-editable, so a *.md symlink
			// pointing at e.g. ~/.ssh/id_ed25519 would otherwise be followed by the
			// read below and its target fed into the prompt (and, under --prune,
			// proposed for deletion). os.DirEntry reports the entry's own type
			// without following it. Mirrors patterns.LoadMemory / extract.readEntries.
			if f.IsDir() || f.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(f.Name(), mdExt) {
				continue
			}
			if isNavigationPage(f.Name()) {
				continue
			}
			path := filepath.Join(dir, f.Name())
			raw, err := readEntryFile(path)
			if err != nil {
				if errors.Is(err, errEntryTooLarge) {
					slog.Warn("skipping oversized wiki entry", "path", path, "cap", maxEntryBytes)
				} else {
					slog.Warn("skipping unreadable wiki entry", "path", path, "err", err)
				}
				continue
			}
			entries = append(entries, Entry{
				Path: filepath.ToSlash(filepath.Join(sub.name, f.Name())),
				Kind: sub.kind,
				Raw:  string(raw),
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

// isNavigationPage reports whether name is one of GitHub's standard wiki
// navigation pages or the SOURCES catalog, which carry no review knowledge.
func isNavigationPage(name string) bool {
	switch strings.ToLower(name) {
	case "home.md", "_sidebar.md", "_footer.md", "sources.md":
		return true
	default:
		return false
	}
}

// readEntryFile reads a single wiki page through a bounded read: it pulls at most
// maxEntryBytes+1 bytes via an io.LimitReader, so a runaway (multi-gigabyte) page
// never allocates more than the cap before it is rejected. It returns
// errEntryTooLarge when the page is larger than maxEntryBytes, so the caller skips
// the whole page rather than truncating it mid-content. Mirrors
// patterns.readMemoryPage / extract.readPatternFile.
func readEntryFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxEntryBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxEntryBytes {
		return nil, errEntryTooLarge
	}
	return data, nil
}

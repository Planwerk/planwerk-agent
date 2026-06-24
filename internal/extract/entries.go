package extract

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/planwerk/planwerk-review/internal/patterns"
)

// mdExt is the markdown file extension wiki review patterns use.
const mdExt = ".md"

// maxPatternBytes caps a single wiki review pattern file read into memory. The
// wiki is world-editable, so an over-long (or runaway) .md file would balloon
// memory before it is ever parsed; files past the cap are skipped with a
// warning. Mirrors patterns.maxMemoryBytes / readMemoryPage in
// internal/patterns/wiki.go.
const maxPatternBytes = 64 * 1024

// errPatternTooLarge marks a wiki pattern file that exceeds maxPatternBytes on
// its own, so readEntries can warn about it specifically rather than treating it
// like an unreadable file.
var errPatternTooLarge = errors.New("wiki pattern exceeds size cap")

// entry is one wiki review pattern available for extraction. Stem is the
// filename without its extension — it is both the selector key (--pattern) and
// the output filename. Name and Severity come from the parsed pattern header
// and drive the interactive selection display. Raw holds the exact file bytes,
// written back verbatim (or, for --to-catalog, with the category normalized).
type entry struct {
	Stem     string
	Name     string
	Severity string
	Raw      []byte
}

// readEntries enumerates the wiki's review_patterns directory and returns one
// entry per file that parses as a pattern. It mirrors patterns.loadDir's
// posture: non-markdown files, the SOURCES.md catalog, and human-navigation
// pages that do not parse as patterns (Home.md, _Sidebar.md) are skipped
// silently, so a wiki can hold both navigation and patterns. The entries are
// sorted by stem for a stable selection order.
func readEntries(dir string) ([]entry, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading wiki patterns directory %s: %w", dir, err)
	}

	var entries []entry
	for _, f := range files {
		// Skip directories, symlinks, and non-markdown entries. The symlink
		// guard is load-bearing: the wiki is world-editable, so a *.md symlink
		// pointing at e.g. ~/.ssh/id_ed25519 would otherwise be followed by the
		// read below and its target committed (and, in PR mode, pushed). The
		// entry type is reported without following the link, so it is rejected
		// before anything opens its target. Mirrors patterns.LoadMemory.
		if f.IsDir() || f.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(f.Name(), mdExt) {
			continue
		}
		if strings.EqualFold(f.Name(), "SOURCES.md") {
			continue
		}
		path := filepath.Join(dir, f.Name())
		raw, err := readPatternFile(path)
		if err != nil {
			if errors.Is(err, errPatternTooLarge) {
				slog.Warn("skipping oversized wiki pattern", "path", path, "cap", maxPatternBytes)
			} else {
				slog.Warn("skipping unreadable wiki pattern", "path", path, "err", err)
			}
			continue
		}
		p, err := patterns.Parse(string(raw))
		if err != nil {
			// Not a pattern (navigation page, README); skip like loadDir does.
			continue
		}
		entries = append(entries, entry{
			Stem:     strings.TrimSuffix(f.Name(), mdExt),
			Name:     p.Name,
			Severity: p.Severity,
			Raw:      raw,
		})
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Stem < entries[j].Stem })
	return entries, nil
}

// readPatternFile reads a single wiki pattern through a bounded read: it opens
// the file and pulls at most maxPatternBytes+1 bytes via an io.LimitReader, so a
// runaway (multi-gigabyte) file never allocates more than the cap before it is
// rejected. It returns errPatternTooLarge when the file is larger than
// maxPatternBytes, so the caller skips the whole file rather than truncating it
// mid-content. Reading the cap into a []byte first (os.ReadFile) would allocate
// the entire file before any size check could run. Mirrors readMemoryPage in
// internal/patterns/wiki.go.
func readPatternFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxPatternBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxPatternBytes {
		return nil, errPatternTooLarge
	}
	return data, nil
}

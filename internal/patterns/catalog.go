package patterns

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Catalog is a loaded pattern set written to disk, one file per pattern, so a
// session can read the bodies it needs from a directory instead of carrying
// every body in its prompt. Dir is the absolute directory holding the files.
// The zero value means no catalog was written, and a session then carries the
// pattern bodies in its prompt instead.
type Catalog struct {
	Dir     string
	Entries []CatalogEntry
}

// CatalogEntry pairs one pattern of a Catalog with the file that holds its
// FormatForPrompt text. File is the basename under Catalog.Dir.
type CatalogEntry struct {
	Pattern Pattern
	File    string
}

// CatalogDirPrefix begins the name of every directory Materialize creates, so
// a check for a leftover catalog matches the same prefix the writer uses.
const CatalogDirPrefix = "planwerk-agent-patterns-"

// catalogSlugRe matches the runs of characters a catalog file name replaces
// with a single dash.
var catalogSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Materialize writes each pattern's FormatForPrompt text to its own file in a
// new temporary directory and returns the resulting Catalog, with Entries in
// input order. The cleanup removes the directory; calling it more than once is
// safe. A nil or empty pats creates nothing and returns the zero Catalog. On
// error nothing is left on disk, and the returned Catalog is zero with a no-op
// cleanup.
func Materialize(pats []Pattern) (Catalog, func(), error) {
	noop := func() {}
	if len(pats) == 0 {
		return Catalog{}, noop, nil
	}
	dir, err := os.MkdirTemp("", CatalogDirPrefix)
	if err != nil {
		return Catalog{}, noop, fmt.Errorf("creating the pattern catalog directory: %w", err)
	}
	// A relative TMPDIR yields a relative dir, and a session running in
	// another working directory could not resolve it.
	abs, err := filepath.Abs(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return Catalog{}, noop, fmt.Errorf("resolving the pattern catalog directory: %w", err)
	}
	dir = abs

	cat := Catalog{Dir: dir, Entries: make([]CatalogEntry, 0, len(pats))}
	used := make(map[string]bool, len(pats))
	for _, p := range pats {
		file := catalogFileName(p.Name, used)
		if err := os.WriteFile(filepath.Join(dir, file), []byte(p.FormatForPrompt()), 0o600); err != nil {
			_ = os.RemoveAll(dir)
			return Catalog{}, noop, fmt.Errorf("writing pattern catalog file %s: %w", file, err)
		}
		cat.Entries = append(cat.Entries, CatalogEntry{Pattern: p, File: file})
	}

	cleanup := func() {
		if err := os.RemoveAll(dir); err != nil {
			slog.Warn("removing the pattern catalog directory failed", "dir", dir, "err", err)
		}
	}
	return cat, cleanup, nil
}

// MaterializeOrWarn is Materialize for callers that run without an on-disk
// catalog rather than fail. It is the entry point the orchestrators call, so
// the warning text lives in one place: a write error is logged and yields the
// zero Catalog with a no-op cleanup, and the sessions then carry the pattern
// bodies in their prompts.
func MaterializeOrWarn(pats []Pattern) (Catalog, func()) {
	cat, cleanup, err := Materialize(pats)
	if err != nil {
		slog.Warn("writing the pattern catalog to disk failed; the sessions carry the pattern bodies instead", "err", err)
		return cat, cleanup
	}
	if len(cat.Entries) > 0 {
		slog.Info("wrote the pattern catalog for the sessions to read", "dir", cat.Dir, "patterns", len(cat.Entries))
	}
	return cat, cleanup
}

// catalogFileName derives a file name for a pattern from its name: lower-case,
// every run of characters outside a-z and 0-9 replaced by one dash, leading and
// trailing dashes trimmed, and "pattern" when nothing remains. A name already
// in used gets the first free "-2", "-3", ... suffix. The result is marked in
// used before it is returned.
func catalogFileName(name string, used map[string]bool) string {
	base := strings.Trim(catalogSlugRe.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if base == "" {
		base = "pattern"
	}
	file := base + ".md"
	for n := 2; used[file]; n++ {
		file = fmt.Sprintf("%s-%d.md", base, n)
	}
	used[file] = true
	return file
}

// FormatCatalogIndex renders one line per catalog entry: the entry's file name
// followed by the same name, area, severity and detection hint
// FormatIndexForPrompt shows. A session reads the index to decide which files
// under cat.Dir to open. The detection hint is optional in a pattern file, and
// no hint would ever send a session to a pattern without one, so its line asks
// for the file to be read before the session starts. maxPatterns bounds the
// list severity first, as
// FormatIndexForPrompt does; <= 0 renders every entry. A catalog without a
// directory or without entries renders the empty string.
func FormatCatalogIndex(cat Catalog, maxPatterns int) string {
	if cat.Dir == "" || len(cat.Entries) == 0 {
		return ""
	}
	pats := make([]Pattern, len(cat.Entries))
	for i, e := range cat.Entries {
		pats[i] = e.Pattern
	}
	var sb strings.Builder
	for _, i := range truncatedIndices(pats, maxPatterns) {
		e := cat.Entries[i]
		sb.WriteString("- ")
		sb.WriteString(e.File)
		sb.WriteString(": ")
		sb.WriteString(indexLine(e.Pattern))
		if strings.TrimSpace(e.Pattern.DetectionHint) == "" {
			sb.WriteString("(no detection hint: read this file in full before you start)")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

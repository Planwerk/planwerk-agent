package patterns

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// MemoryCatalog is a wiki's project memory for one run: the pages, and the
// directory their bodies were written to. A zero Dir means nothing was
// written, and a session then carries the page bodies in its prompt.
type MemoryCatalog struct {
	Dir   string
	Pages []MemoryPage
}

// MemoryDirPrefix begins the name of every directory MaterializeMemory
// creates, so a check for a leftover directory matches the same prefix the
// writer uses.
const MemoryDirPrefix = "planwerk-agent-memory-"

// maxMemoryIndexBytes caps the index FormatMemoryIndex renders into a prompt.
// It holds about 250 lines of typical length; the pages past it are still on
// disk, and the session is told to list the directory for them.
const maxMemoryIndexBytes = 64 * 1024

// maxMemoryIndexFieldBytes caps a title and a summary on an index line, each
// on its own.
const maxMemoryIndexFieldBytes = 300

// MaterializeMemory writes each page's body to <Dir>/<Name> in a new temporary
// directory and returns the resulting MemoryCatalog, with Pages as given. The
// cleanup removes the directory; calling it more than once is safe. Nil or
// empty pages create nothing and return the zero MemoryCatalog. On error
// nothing is left on disk, and the returned catalog holds the pages without a
// Dir, with a no-op cleanup. A page's Name is joined into the path as it is:
// LoadMemoryPages yields base names only.
func MaterializeMemory(pages []MemoryPage) (MemoryCatalog, func(), error) {
	noop := func() {}
	if len(pages) == 0 {
		return MemoryCatalog{}, noop, nil
	}
	dir, err := os.MkdirTemp("", MemoryDirPrefix)
	if err != nil {
		return MemoryCatalog{Pages: pages}, noop, fmt.Errorf("creating the project memory directory: %w", err)
	}
	// A relative TMPDIR yields a relative dir, and a session running in
	// another working directory could not resolve it.
	abs, err := filepath.Abs(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return MemoryCatalog{Pages: pages}, noop, fmt.Errorf("resolving the project memory directory: %w", err)
	}
	dir = abs

	for _, p := range pages {
		if err := os.WriteFile(filepath.Join(dir, p.Name), []byte(p.Body+"\n"), 0o600); err != nil {
			_ = os.RemoveAll(dir)
			return MemoryCatalog{Pages: pages}, noop, fmt.Errorf("writing project memory file %s: %w", p.Name, err)
		}
	}

	cleanup := func() {
		if err := os.RemoveAll(dir); err != nil {
			slog.Warn("removing the project memory directory failed", "dir", dir, "err", err)
		}
	}
	return MemoryCatalog{Dir: dir, Pages: pages}, cleanup, nil
}

// MaterializeMemoryOrWarn is MaterializeMemory for callers that run without an
// on-disk memory rather than fail. It is the entry point the orchestrators
// call, so the log lines live in one place: a write error is logged and yields
// the pages without a Dir and a no-op cleanup, and the sessions then carry the
// page bodies in their prompts. An index that exceeds its size budget is
// logged with the number of pages it does not list; that warning is the notice
// the run gives its author.
func MaterializeMemoryOrWarn(pages []MemoryPage) (MemoryCatalog, func()) {
	cat, cleanup, err := MaterializeMemory(pages)
	if err != nil {
		slog.Warn("writing the project memory to disk failed; the sessions carry the page bodies instead", "err", err)
		return cat, cleanup
	}
	if len(cat.Pages) > 0 {
		slog.Info("wrote the project memory for the sessions to read", "dir", cat.Dir, "pages", len(cat.Pages))
	}
	if _, unlisted := FormatMemoryIndex(cat); unlisted > 0 {
		slog.Warn("the project memory index exceeds its size budget; the sessions are told to list the directory for the remaining pages",
			"listed", len(cat.Pages)-unlisted, "unlisted", unlisted, "cap", maxMemoryIndexBytes)
	}
	return cat, cleanup
}

// FormatMemoryIndex renders one line per page of cat, in Pages order: the
// page's file name and title and, after a " | ", its summary when it has one.
// A session reads the index to decide which files under cat.Dir to open. Title
// and summary are each folded to one line and cut to maxMemoryIndexFieldBytes.
// The index stays within maxMemoryIndexBytes: the first line that would exceed
// it ends the index, and unlisted is the number of pages from that one on, so
// the listed pages are always a prefix of Pages. A catalog without a directory
// or without pages renders "" and 0.
func FormatMemoryIndex(cat MemoryCatalog) (index string, unlisted int) {
	if cat.Dir == "" || len(cat.Pages) == 0 {
		return "", 0
	}
	var sb strings.Builder
	for i, p := range cat.Pages {
		line := "- " + p.Name + ": " + memoryIndexField(p.Title)
		if summary := memoryIndexField(p.Summary); summary != "" {
			line += " | " + summary
		}
		line += "\n"
		if sb.Len()+len(line) > maxMemoryIndexBytes {
			return sb.String(), len(cat.Pages) - i
		}
		sb.WriteString(line)
	}
	return sb.String(), 0
}

// memoryIndexField folds s to one line and cuts it to maxMemoryIndexFieldBytes
// on a rune boundary, with "..." appended when it was cut.
func memoryIndexField(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= maxMemoryIndexFieldBytes {
		return s
	}
	cut := maxMemoryIndexFieldBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

// FormatMemoryBodies renders pages as one project-memory block for a prompt:
// each page as a "### <name>" header derived from its file name, a blank line,
// and its body, with a blank line between pages. The total is capped at
// maxMemoryBytes; a page that no longer fits is skipped with a warning, and the
// pages after it are still tried. Nil or empty pages render "".
func FormatMemoryBodies(pages []MemoryPage) string {
	var sb strings.Builder
	for _, p := range pages {
		entry := "### " + strings.TrimSuffix(p.Name, ".md") + "\n\n" + p.Body + "\n"
		if sb.Len()+len(entry) > maxMemoryBytes {
			slog.Warn("project memory exceeds total size cap; skipping page", "page", p.Name, "cap", maxMemoryBytes)
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(entry)
	}
	return strings.TrimSpace(sb.String())
}

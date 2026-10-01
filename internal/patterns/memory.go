package patterns

import (
	"log/slog"
	"strings"
)

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

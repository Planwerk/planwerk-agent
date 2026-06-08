package patterns

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

// embeddedFS carries a compile-time copy of the canonical pattern catalog so
// a binary produced by `go install` — or any channel that ships only the
// executable — still loads the catalog when no on-disk patterns/ sibling
// exists. It is the lowest-priority source in the loader's chain: any on-disk
// source overrides an embedded pattern of the same name. This mirrors the
// //go:embed precedent in internal/checklist/checklist.go.
//
//go:embed all:patterns
var embeddedFS embed.FS

// loadEmbedded parses every .md file in the embedded catalog into a Pattern.
// It is the embed.FS counterpart of loadDir: it walks embeddedFS, skips
// SOURCES.md (case-insensitive) and any file that fails to parse, and records
// a synthetic FilePath of the form "embedded:<path>" (e.g.
// "embedded:patterns/technology/go/go-error-wrapping.md"). The walk path is
// slash-separated and rooted at the embed root, so the FilePath stays stable
// across operating systems.
func loadEmbedded() ([]Pattern, error) {
	var result []Pattern

	err := fs.WalkDir(embeddedFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		// Skip SOURCES.md (documentation catalog, not a pattern).
		if strings.EqualFold(d.Name(), "SOURCES.md") {
			return nil
		}

		content, err := embeddedFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading embedded pattern %s: %w", path, err)
		}

		p, err := Parse(string(content))
		if err != nil {
			// Skip files that don't parse as patterns, matching loadDir.
			return nil
		}

		p.FilePath = "embedded:" + path
		result = append(result, p)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

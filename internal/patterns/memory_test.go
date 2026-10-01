package patterns

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// captureLogs routes the default logger into the returned buffer for the rest
// of the test. A test that calls it must not run in parallel.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &logBuf
}

func TestFormatMemoryBodies(t *testing.T) {
	t.Run("renders each page under a header from its file name", func(t *testing.T) {
		got := FormatMemoryBodies([]MemoryPage{
			{Name: "01-first.md", Title: "First", Body: "First body."},
			{Name: "02-second.md", Title: "Second", Summary: "ignored here", Body: "Second body."},
		})
		// Byte for byte the block a prompt carried for these two pages when
		// the bodies were its only form.
		const want = "### 01-first\n\nFirst body.\n\n### 02-second\n\nSecond body."
		if got != want {
			t.Errorf("FormatMemoryBodies =\n%q\nwant\n%q", got, want)
		}
	})

	t.Run("skips pages past the total cap with a warning", func(t *testing.T) {
		logBuf := captureLogs(t)
		half := strings.Repeat("x", maxMemoryBytes/2)
		got := FormatMemoryBodies([]MemoryPage{
			{Name: "a.md", Body: half},
			{Name: "b.md", Body: half},
			{Name: "c.md", Body: "small"},
		})
		if len(got) > maxMemoryBytes {
			t.Errorf("memory length = %d, want <= %d", len(got), maxMemoryBytes)
		}
		if strings.Contains(got, "### b") {
			t.Error("a page past the total cap must be skipped")
		}
		if !strings.Contains(got, "### c\n\nsmall") {
			t.Error("a page that still fits after a skipped one must render")
		}
		log := logBuf.String()
		if !strings.Contains(log, "project memory exceeds total size cap; skipping page") || !strings.Contains(log, "page=b.md") {
			t.Errorf("expected the total-cap warning for b.md, got:\n%s", log)
		}
	})

	t.Run("no pages render the empty string", func(t *testing.T) {
		for _, pages := range [][]MemoryPage{nil, {}} {
			if got := FormatMemoryBodies(pages); got != "" {
				t.Errorf("FormatMemoryBodies(%v) = %q, want empty", pages, got)
			}
		}
	})
}

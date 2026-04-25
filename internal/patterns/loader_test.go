package patterns

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePattern(t *testing.T, dir, filename, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const goPattern = `# Review Pattern: Go Errors

**Review-Area**: quality
**Detection-Hint**: bare returns
**Severity**: WARNING
**Category**: technology
**Applies-When**: go

## What to check

Wrap errors.
`

const pythonPattern = `# Review Pattern: Python Types

**Review-Area**: quality
**Detection-Hint**: missing type hints
**Severity**: INFO
**Category**: technology
**Applies-When**: python

## What to check

Add type hints.
`

const yagniPattern = `# Review Pattern: YAGNI

**Review-Area**: architecture
**Detection-Hint**: premature abstractions
**Severity**: INFO
**Category**: design-principle

## What to check

No premature abstractions.
`

const legacyPattern = `# Review Pattern: Legacy Check

**Review-Area**: quality
**Detection-Hint**: check stuff
**Severity**: WARNING

## What to check

Check stuff.
`

func TestLoad_Recursive(t *testing.T) {
	dir := t.TempDir()
	writePattern(t, filepath.Join(dir, "technology", "go"), "go-errors.md", goPattern)
	writePattern(t, filepath.Join(dir, "design"), "yagni.md", yagniPattern)

	pats, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pats) != 2 {
		t.Fatalf("expected 2 patterns, got %d", len(pats))
	}

	names := map[string]bool{}
	for _, p := range pats {
		names[p.Name] = true
	}
	if !names["Go Errors"] {
		t.Error("missing Go Errors pattern")
	}
	if !names["YAGNI"] {
		t.Error("missing YAGNI pattern")
	}
}

func TestLoadFiltered_GoOnly(t *testing.T) {
	dir := t.TempDir()
	writePattern(t, filepath.Join(dir, "technology", "go"), "go-errors.md", goPattern)
	writePattern(t, filepath.Join(dir, "technology", "python"), "python-types.md", pythonPattern)
	writePattern(t, filepath.Join(dir, "design"), "yagni.md", yagniPattern)

	pats, err := LoadFiltered([]string{"go"}, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should include Go Errors + YAGNI (no AppliesWhen = always), but NOT Python Types
	names := map[string]bool{}
	for _, p := range pats {
		names[p.Name] = true
	}
	if !names["Go Errors"] {
		t.Error("missing Go Errors pattern")
	}
	if !names["YAGNI"] {
		t.Error("missing YAGNI (design pattern should always apply)")
	}
	if names["Python Types"] {
		t.Error("Python Types should be filtered out for go project")
	}
}

func TestLoadFiltered_NilTags(t *testing.T) {
	dir := t.TempDir()
	writePattern(t, dir, "go-errors.md", goPattern)
	writePattern(t, dir, "python-types.md", pythonPattern)

	pats, err := LoadFiltered(nil, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pats) != 2 {
		t.Errorf("nil tags should return all patterns, got %d", len(pats))
	}
}

func TestLoadFiltered_LegacyAlwaysApplies(t *testing.T) {
	dir := t.TempDir()
	writePattern(t, dir, "legacy.md", legacyPattern)
	writePattern(t, dir, "go-errors.md", goPattern)

	pats, err := LoadFiltered([]string{"python"}, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	names := map[string]bool{}
	for _, p := range pats {
		names[p.Name] = true
	}
	if !names["Legacy Check"] {
		t.Error("legacy pattern (no AppliesWhen) should always apply")
	}
	if names["Go Errors"] {
		t.Error("Go Errors should be filtered out for python project")
	}
}

func TestLoad_PriorityOverride(t *testing.T) {
	general := t.TempDir()
	repo := t.TempDir()

	// Same pattern name, different severity
	writePattern(t, general, "check.md", `# Review Pattern: Check

**Review-Area**: quality
**Detection-Hint**: check
**Severity**: INFO

## What to check

General version.
`)
	writePattern(t, repo, "check.md", `# Review Pattern: Check

**Review-Area**: quality
**Detection-Hint**: check
**Severity**: CRITICAL

## What to check

Repo-specific version.
`)

	pats, err := Load(general, repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pats) != 1 {
		t.Fatalf("expected 1 pattern after dedup, got %d", len(pats))
	}
	if pats[0].Severity != "CRITICAL" {
		t.Errorf("repo-specific pattern should override: Severity = %q, want CRITICAL", pats[0].Severity)
	}
}

func TestLoad_SkipsSourcesMD(t *testing.T) {
	dir := t.TempDir()
	writePattern(t, dir, "SOURCES.md", "# Best Practice Sources\n\nNot a pattern.\n")
	writePattern(t, dir, "go-errors.md", goPattern)

	pats, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pats) != 1 {
		t.Errorf("expected 1 pattern (SOURCES.md skipped), got %d", len(pats))
	}
}

func TestLoad_NonexistentDir(t *testing.T) {
	pats, err := Load("/nonexistent/path")
	if err != nil {
		t.Fatalf("nonexistent dir should not error: %v", err)
	}
	if len(pats) != 0 {
		t.Errorf("expected 0 patterns, got %d", len(pats))
	}
}

func TestFormatGroupedForPrompt(t *testing.T) {
	pats := []Pattern{
		{Name: "Go Errors", Category: "technology", ReviewArea: "quality", DetectionHint: "check", Severity: "WARNING", Body: "## Check\nWrap."},
		{Name: "YAGNI", Category: "design-principle", ReviewArea: "architecture", DetectionHint: "check", Severity: "INFO", Body: "## Check\nDon't."},
		{Name: "Legacy", Category: "", ReviewArea: "quality", DetectionHint: "check", Severity: "INFO", Body: "## Check\nStuff."},
	}

	out := FormatGroupedForPrompt(pats, DefaultMaxPatternsInPrompt)

	if !strings.Contains(out, "<technology-patterns>") {
		t.Error("should contain technology-patterns tag")
	}
	if !strings.Contains(out, "<design-patterns>") {
		t.Error("should contain design-patterns tag")
	}
	if !strings.Contains(out, "<project-patterns>") {
		t.Error("should contain project-patterns tag")
	}
	if !strings.Contains(out, "Go Errors") {
		t.Error("should contain Go Errors")
	}
	if !strings.Contains(out, "YAGNI") {
		t.Error("should contain YAGNI")
	}
}

func TestFormatGroupedForPrompt_Empty(t *testing.T) {
	out := FormatGroupedForPrompt(nil, DefaultMaxPatternsInPrompt)
	if out != "" {
		t.Errorf("empty patterns should return empty string, got %q", out)
	}
}

func TestTruncatePatterns(t *testing.T) {
	// Create more than the limit: 5 BLOCKING, 10 CRITICAL, rest INFO
	limit := DefaultMaxPatternsInPrompt
	var pats []Pattern
	for i := 0; i < limit+10; i++ {
		sev := "INFO"
		if i < 5 {
			sev = "BLOCKING"
		} else if i < 15 {
			sev = "CRITICAL"
		}
		pats = append(pats, Pattern{
			Name:     "P" + string(rune('A'+i%26)),
			Severity: sev,
		})
	}

	result := truncatePatterns(pats, limit)
	if len(result) != limit {
		t.Errorf("truncated to %d, want %d", len(result), limit)
	}

	// All BLOCKING patterns should be present
	blocking := 0
	for _, p := range result {
		if p.Severity == "BLOCKING" {
			blocking++
		}
	}
	if blocking != 5 {
		t.Errorf("expected all 5 BLOCKING patterns, got %d", blocking)
	}
}

func TestTruncatePatterns_CustomLimit(t *testing.T) {
	pats := make([]Pattern, 20)
	for i := range pats {
		pats[i] = Pattern{Name: "P", Severity: "INFO"}
	}

	result := truncatePatterns(pats, 7)
	if len(result) != 7 {
		t.Errorf("expected 7 patterns with custom limit, got %d", len(result))
	}
}

func TestTruncatePatterns_NoLimit(t *testing.T) {
	pats := make([]Pattern, 200)
	for i := range pats {
		pats[i] = Pattern{Name: "P", Severity: "INFO"}
	}

	for _, limit := range []int{0, -1} {
		result := truncatePatterns(pats, limit)
		if len(result) != len(pats) {
			t.Errorf("limit=%d: expected no truncation (%d), got %d", limit, len(pats), len(result))
		}
	}
}

func TestTruncatePatterns_BelowLimit(t *testing.T) {
	pats := make([]Pattern, 3)
	for i := range pats {
		pats[i] = Pattern{Name: "P", Severity: "INFO"}
	}

	result := truncatePatterns(pats, 10)
	if len(result) != 3 {
		t.Errorf("expected 3 patterns (unchanged), got %d", len(result))
	}
}

func TestLoadFiltered_MixesLocalAndRemote(t *testing.T) {
	// Local source: a real directory on disk.
	local := t.TempDir()
	writePattern(t, local, "go-errors.md", goPattern)

	// Remote source: simulated via the fetchRemote stub. The "clone" just
	// materializes a directory containing one pattern file.
	remoteCache := t.TempDir()
	restore := stubFetch(func(p parsedURI, dest string) error {
		if err := os.MkdirAll(dest, 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dest, "yagni.md"), []byte(yagniPattern), 0o600)
	})
	defer restore()

	opts := LoadOptions{Remote: RemoteOptions{CacheDir: remoteCache}}
	pats, err := LoadFilteredWithOptions(opts, nil, local, "github:org/patterns")
	if err != nil {
		t.Fatalf("LoadFilteredWithOptions: %v", err)
	}
	names := map[string]bool{}
	for _, p := range pats {
		names[p.Name] = true
	}
	if !names["Go Errors"] {
		t.Error("missing Go Errors (from local source)")
	}
	if !names["YAGNI"] {
		t.Error("missing YAGNI (from remote source)")
	}
}

func TestLoadFiltered_RemoteFailureSurfacesError(t *testing.T) {
	restore := stubFetch(func(p parsedURI, dest string) error {
		return os.ErrPermission
	})
	defer restore()

	opts := LoadOptions{Remote: RemoteOptions{CacheDir: t.TempDir()}}
	_, err := LoadFilteredWithOptions(opts, nil, "github:broken/repo")
	if err == nil {
		t.Fatal("expected error when remote fetch fails")
	}
	if !strings.Contains(err.Error(), "github:broken/repo") {
		t.Errorf("error should name the failing source: %v", err)
	}
}

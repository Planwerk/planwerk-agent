package review

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/planwerk/planwerk-review/internal/report"
)

func writeChangedFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestVerifyFindingSnippets(t *testing.T) {
	dir := t.TempDir()
	writeChangedFile(t, dir, "internal/foo/foo.go", "func Foo() error {\n\treturn db.Exec(query)\n}\n")

	result := &report.ReviewResult{
		Findings: []report.Finding{
			// Present (different indentation than the file → still matches).
			{Title: "real", Confidence: report.ConfidenceVerified, CodeSnippet: "return db.Exec(query)"},
			// Fabricated — not in any changed file.
			{Title: "hallucinated", Confidence: report.ConfidenceVerified, CodeSnippet: "user.DeleteAllRecords()"},
			// No snippet at all → unverifiable.
			{Title: "no-snippet", Confidence: report.ConfidenceLikely, CodeSnippet: ""},
			// Already uncertain → left alone, not counted.
			{Title: "already-uncertain", Confidence: report.ConfidenceUncertain, CodeSnippet: "whatever"},
		},
	}

	demoted := verifyFindingSnippets(result, dir, []string{"internal/foo/foo.go"})
	if demoted != 2 {
		t.Errorf("demoted = %d, want 2 (hallucinated + no-snippet)", demoted)
	}
	want := map[string]report.Confidence{
		"real":              report.ConfidenceVerified,
		"hallucinated":      report.ConfidenceUncertain,
		"no-snippet":        report.ConfidenceUncertain,
		"already-uncertain": report.ConfidenceUncertain,
	}
	for _, f := range result.Findings {
		if f.Confidence != want[f.Title] {
			t.Errorf("%s: confidence = %q, want %q", f.Title, f.Confidence, want[f.Title])
		}
	}
}

func TestVerifyFindingSnippets_NoGroundTruthSkips(t *testing.T) {
	dir := t.TempDir() // no changed files written
	result := &report.ReviewResult{
		Findings: []report.Finding{
			{Title: "x", Confidence: report.ConfidenceVerified, CodeSnippet: "anything()"},
		},
	}
	// Empty/unreadable change set → gate is skipped, nothing demoted.
	if n := verifyFindingSnippets(result, dir, []string{"missing.go"}); n != 0 {
		t.Errorf("demoted = %d, want 0 when no content can be loaded", n)
	}
	if result.Findings[0].Confidence != report.ConfidenceVerified {
		t.Error("finding must not be demoted when there is no ground truth")
	}
}

func TestVerifyFindingSnippets_PathEscapeIgnored(t *testing.T) {
	dir := t.TempDir()
	writeChangedFile(t, dir, "ok.go", "safeContent()")
	result := &report.ReviewResult{
		Findings: []report.Finding{{Title: "x", Confidence: report.ConfidenceVerified, CodeSnippet: "safeContent()"}},
	}
	// A path-escaping entry must be skipped without panicking; the in-tree file
	// still provides the haystack so the legitimate finding survives.
	if n := verifyFindingSnippets(result, dir, []string{"../../../etc/passwd", "ok.go"}); n != 0 {
		t.Errorf("demoted = %d, want 0", n)
	}
}

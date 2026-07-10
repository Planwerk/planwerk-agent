package hygiene

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/report"
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

func TestVerifySnippets(t *testing.T) {
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

	demoted := VerifySnippets(result, dir, []string{"internal/foo/foo.go"})
	if demoted != 2 {
		t.Errorf("demoted = %d, want 2 (hallucinated + no-snippet)", demoted)
	}
	want := map[string]report.Confidence{
		"real":              report.ConfidenceVerified,
		"hallucinated":      report.ConfidenceUncertain,
		"no-snippet":        report.ConfidenceUncertain,
		"already-uncertain": report.ConfidenceUncertain,
	}
	// Every examined finding records what the gate decided; the already-uncertain
	// finding is skipped (not examined) and so stays unstamped.
	wantCheck := map[string]string{
		"real":              report.SnippetCheckPassed,
		"hallucinated":      snippetReasonNotFound,
		"no-snippet":        snippetReasonNoQuote,
		"already-uncertain": "",
	}
	for _, f := range result.Findings {
		if f.Confidence != want[f.Title] {
			t.Errorf("%s: confidence = %q, want %q", f.Title, f.Confidence, want[f.Title])
		}
		if f.SnippetCheck != wantCheck[f.Title] {
			t.Errorf("%s: snippet check = %q, want %q", f.Title, f.SnippetCheck, wantCheck[f.Title])
		}
	}
	// The run-level counts must record what the gate examined and demoted, so a
	// clean pass is distinguishable from a skipped gate (Story 1).
	if result.Gates == nil || result.Gates.Snippet == nil {
		t.Fatalf("gate stats not recorded: %+v", result.Gates)
	}
	if got := *result.Gates.Snippet; got != (report.SnippetGateStats{Examined: 3, Demoted: 2}) {
		t.Errorf("snippet stats = %+v, want {Examined:3 Demoted:2}", got)
	}
}

// TestVerifySnippets_DiffMarkers locks the quote-or-demote gate against
// snippets that carry leading +/- diff markers (issue #156, defect 1): a
// finding quoting its snippet verbatim from `git diff` output must pass the
// gate rather than be falsely demoted to uncertain.
func TestVerifySnippets_DiffMarkers(t *testing.T) {
	dir := t.TempDir()
	writeChangedFile(t, dir, "internal/foo/foo.go", "func Foo() error {\n\treturn db.Exec(query)\n}\n")
	writeChangedFile(t, dir, "docs/list.md", "- item one\n- item two\n")
	changed := []string{"internal/foo/foo.go", "docs/list.md"}

	cases := []struct {
		name    string
		snippet string
		want    report.Confidence
	}{
		// Copied straight out of `git diff`: every line carries a leading '+'.
		{"markers on all lines", "+func Foo() error {\n+\treturn db.Exec(query)", report.ConfidenceVerified},
		// Mixed diff context: an unchanged context line plus an added line.
		{"markers on some lines", " func Foo() error {\n+\treturn db.Exec(query)", report.ConfidenceVerified},
		// Pre-existing base case: a plain quote with no markers still matches.
		{"no markers", "return db.Exec(query)", report.ConfidenceVerified},
		// Markers are not a free pass: a fabricated snippet is still demoted.
		{"fabricated with markers", "+user.DeleteAllRecords()", report.ConfidenceUncertain},
		// Genuine leading-dash content (a markdown bullet) quoted verbatim from
		// the file still matches: the single marker is stripped off the needle.
		{"leading-dash markdown bullet", "- item one", report.ConfidenceVerified},
		// An added line whose own content begins with '-' is quoted from the
		// diff with a double marker ('+- item one'). The on-disk file carries the
		// genuine '- item one'; stripping exactly one marker off the needle must
		// leave '- item one' so it still matches. This is the double-marker case
		// the prior single-marker fix missed (issue #156, defect 1).
		{"added line whose content starts with a dash", "+- item one", report.ConfidenceVerified},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := &report.ReviewResult{
				Findings: []report.Finding{
					{Title: tc.name, Confidence: report.ConfidenceVerified, CodeSnippet: tc.snippet},
				},
			}
			VerifySnippets(result, dir, changed)
			if got := result.Findings[0].Confidence; got != tc.want {
				t.Errorf("confidence = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestVerifySnippets_NoGroundTruthSkips(t *testing.T) {
	dir := t.TempDir() // no changed files written
	result := &report.ReviewResult{
		Findings: []report.Finding{
			{Title: "x", Confidence: report.ConfidenceVerified, CodeSnippet: "anything()"},
		},
	}
	// Empty/unreadable change set → gate is skipped, nothing demoted.
	if n := VerifySnippets(result, dir, []string{"missing.go"}); n != 0 {
		t.Errorf("demoted = %d, want 0 when no content can be loaded", n)
	}
	if result.Findings[0].Confidence != report.ConfidenceVerified {
		t.Error("finding must not be demoted when there is no ground truth")
	}
	// A skipped gate records nothing and stamps nothing — a nil Gates.Snippet is
	// how a reader tells "gate skipped" apart from "gate ran and passed all".
	if result.Gates != nil {
		t.Errorf("skipped gate must not record stats, got %+v", result.Gates)
	}
	if result.Findings[0].SnippetCheck != "" {
		t.Errorf("skipped gate must not stamp a finding, got %q", result.Findings[0].SnippetCheck)
	}
}

func TestVerifySnippets_PathEscapeIgnored(t *testing.T) {
	dir := t.TempDir()
	writeChangedFile(t, dir, "ok.go", "safeContent()")
	result := &report.ReviewResult{
		Findings: []report.Finding{{Title: "x", Confidence: report.ConfidenceVerified, CodeSnippet: "safeContent()"}},
	}
	// A path-escaping entry must be skipped without panicking; the in-tree file
	// still provides the haystack so the legitimate finding survives.
	if n := VerifySnippets(result, dir, []string{"../../../etc/passwd", "ok.go"}); n != 0 {
		t.Errorf("demoted = %d, want 0", n)
	}
}

func TestVerifySnippets_NilResult(t *testing.T) {
	// A nil result is a documented no-op; it must not panic.
	if n := VerifySnippets(nil, t.TempDir(), []string{"ok.go"}); n != 0 {
		t.Errorf("demoted = %d, want 0 for a nil result", n)
	}
}

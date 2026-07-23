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

// TestVerifySnippets_MultilineComment locks the comment-insensitive match: a
// finding that quotes a multi-line // comment as continuous prose — without
// reproducing the interior // marker the source carries at each line break —
// must pass rather than be demoted (the false-demotion class that withheld a
// valid "misleading comment" finding). The same comment quoted WITH its markers
// must pass too.
func TestVerifySnippets_MultilineComment(t *testing.T) {
	dir := t.TempDir()
	writeChangedFile(t, dir, "svc/reconciler.go",
		"func sweep() {\n"+
			"\t// timeout is a legal edge from every\n"+
			"\t// non-terminal status, so this never errors\n"+
			"\trun()\n"+
			"}\n")
	result := &report.ReviewResult{
		Findings: []report.Finding{
			// Quoted as flowing prose, no interior // — spans the two comment lines.
			{Title: "prose", Confidence: report.ConfidenceVerified, CodeSnippet: "timeout is a legal edge from every non-terminal status"},
			// The same comment quoted verbatim WITH the // markers.
			{Title: "with-markers", Confidence: report.ConfidenceVerified, CodeSnippet: "// timeout is a legal edge from every\n// non-terminal status"},
			// A comment line that is genuinely NOT in the file is still demoted —
			// the loosening does not turn the gate into a free pass.
			{Title: "fabricated", Confidence: report.ConfidenceVerified, CodeSnippet: "// this comment does not exist anywhere"},
		},
	}
	demoted := VerifySnippets(result, dir, []string{"svc/reconciler.go"})
	if demoted != 1 {
		t.Errorf("demoted = %d, want 1 (only the fabricated comment)", demoted)
	}
	want := map[string]report.Confidence{
		"prose":        report.ConfidenceVerified,
		"with-markers": report.ConfidenceVerified,
		"fabricated":   report.ConfidenceUncertain,
	}
	for _, f := range result.Findings {
		if f.Confidence != want[f.Title] {
			t.Errorf("%s: confidence = %q, want %q", f.Title, f.Confidence, want[f.Title])
		}
	}
}

// TestVerifySnippets_RecoversOutsideDiff locks the checkout-wide fallback: a real
// finding whose quoted code lives in a file the change did NOT modify (the
// cross-file class the changed-files haystack is blind to) must be verified and
// left actionable, not demoted — while a snippet found nowhere in the checkout
// is still demoted.
func TestVerifySnippets_RecoversOutsideDiff(t *testing.T) {
	dir := t.TempDir()
	// The changed file — a new caller added by the diff.
	writeChangedFile(t, dir, "internal/svc/reconciler.go", "func sweep() {\n\tfor range xs {\n\t\texec.Record()\n\t}\n}\n")
	// An UNCHANGED file elsewhere in the checkout holding the quoted evidence.
	writeChangedFile(t, dir, "internal/svc/execution.go", "func (e Execution) Record() {\n\tinvocations := make([]Inv, len(e.invocations))\n\tcopy(invocations, e.invocations)\n}\n")

	result := &report.ReviewResult{
		Findings: []report.Finding{
			// Evidence in the unchanged file → recovered, not demoted.
			{Title: "cross-file", Confidence: report.ConfidenceLikely, CodeSnippet: "invocations := make([]Inv, len(e.invocations))"},
			// Evidence in the changed file → passes on the primary haystack.
			{Title: "in-diff", Confidence: report.ConfidenceLikely, CodeSnippet: "exec.Record()"},
			// Nowhere in the checkout → still demoted as hallucinated.
			{Title: "hallucinated", Confidence: report.ConfidenceLikely, CodeSnippet: "user.DeleteAllRecords()"},
		},
	}

	// Only reconciler.go is in the change set; execution.go is not.
	demoted := VerifySnippets(result, dir, []string{"internal/svc/reconciler.go"})
	if demoted != 1 {
		t.Errorf("demoted = %d, want 1 (only the hallucinated finding)", demoted)
	}
	wantConf := map[string]report.Confidence{
		"cross-file":   report.ConfidenceLikely,    // recovered → confidence untouched
		"in-diff":      report.ConfidenceLikely,    // passed on the diff
		"hallucinated": report.ConfidenceUncertain, // demoted
	}
	wantCheck := map[string]string{
		"cross-file":   snippetPassedOutsideDiff,
		"in-diff":      report.SnippetCheckPassed,
		"hallucinated": snippetReasonNotFound,
	}
	for _, f := range result.Findings {
		if f.Confidence != wantConf[f.Title] {
			t.Errorf("%s: confidence = %q, want %q", f.Title, f.Confidence, wantConf[f.Title])
		}
		if f.SnippetCheck != wantCheck[f.Title] {
			t.Errorf("%s: snippet check = %q, want %q", f.Title, f.SnippetCheck, wantCheck[f.Title])
		}
	}
	// A recovered finding is NOT unverified — it must survive the apply gate.
	for _, f := range result.Findings {
		if f.Title == "cross-file" && f.Unverified() {
			t.Error("cross-file finding recovered from the checkout must not be Unverified")
		}
	}
	if result.Gates == nil || result.Gates.Snippet == nil {
		t.Fatalf("gate stats not recorded: %+v", result.Gates)
	}
	if got := *result.Gates.Snippet; got != (report.SnippetGateStats{Examined: 3, Demoted: 1, RecoveredOutsideDiff: 1}) {
		t.Errorf("snippet stats = %+v, want {Examined:3 Demoted:1 RecoveredOutsideDiff:1}", got)
	}
}

// TestVerifySnippets_PartialLineMatch locks the loosening in #77: a snippet that
// is NOT present as one contiguous block — because the model elided a line,
// reconstructed a signature, or quoted non-adjacent lines together — must still
// pass when one real distinctive line resolves, rather than being buried in the
// Unverified section. A snippet whose every distinctive line is fabricated is
// still demoted, so the looser match is not a free pass.
func TestVerifySnippets_PartialLineMatch(t *testing.T) {
	dir := t.TempDir()
	writeChangedFile(t, dir, "internal/api/verifier.go",
		"func Verify(env Envelope) error {\n"+
			"\tif time.Since(env.Timestamp) > freshnessWindow {\n"+
			"\t\treturn ErrStale\n"+
			"\t}\n"+
			"\treturn verifySignature(env.Signature, env.Payload)\n"+
			"}\n")
	changed := []string{"internal/api/verifier.go"}

	cases := []struct {
		name    string
		snippet string
		want    report.Confidence
	}{
		// Real code with the middle elided by an ellipsis and the signature
		// reconstructed with `...` args: no contiguous block matches, yet a
		// distinctive line (the return) is present, so the finding stays actionable.
		{
			"elided middle line",
			"func Verify(...) error {\n\t// ... omitted ...\n\treturn verifySignature(env.Signature, env.Payload)",
			report.ConfidenceVerified,
		},
		// Two real lines quoted non-adjacently (the reviewer joined them).
		{
			"non-adjacent lines joined",
			"if time.Since(env.Timestamp) > freshnessWindow {\n\treturn verifySignature(env.Signature, env.Payload)",
			report.ConfidenceVerified,
		},
		// Every distinctive line is fabricated → still demoted.
		{
			"fully fabricated block",
			"if db.DropAllTables(ctx) != nil {\n\tuser.PurgeEverything(ctx, opts)\n\treturn wipeDisk()",
			report.ConfidenceUncertain,
		},
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

package hygiene

import (
	"reflect"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// fileStats returns the recorded changed-file record, failing the test if the
// check left none.
func fileStats(t *testing.T, result *report.ReviewResult) report.FileGateStats {
	t.Helper()
	if result.Gates == nil || result.Gates.Files == nil {
		t.Fatalf("changed-file record not written: %+v", result.Gates)
	}
	return *result.Gates.Files
}

func TestCheckFileLedger_AllReviewed(t *testing.T) {
	result := &report.ReviewResult{FilesReviewed: []report.FileReview{
		{File: "a.go", Status: report.FileReviewed},
		{File: "b.go", Status: report.FileReviewed},
	}}
	if n := CheckFileLedger(result, []string{"a.go", "b.go"}); n != 0 {
		t.Errorf("CheckFileLedger = %d, want 0", n)
	}
	if got, want := fileStats(t, result), (report.FileGateStats{Changed: 2, Reviewed: 2}); !reflect.DeepEqual(got, want) {
		t.Errorf("record = %+v, want %+v", got, want)
	}
}

// TestCheckFileLedger_SkippedCarriesReason pins the skipped branch, with the
// reason collapsed onto one line and a status in another letter case read as
// written, since the CLI does not validate the fallback output.
func TestCheckFileLedger_SkippedCarriesReason(t *testing.T) {
	result := &report.ReviewResult{FilesReviewed: []report.FileReview{
		{File: "a.go", Status: "Reviewed"},
		{File: "go.sum", Status: report.FileSkipped, Reason: "generated\n  checksum file"},
		{File: "logo.png", Status: report.FileSkipped},
	}}
	if n := CheckFileLedger(result, []string{"a.go", "go.sum", "logo.png"}); n != 2 {
		t.Errorf("CheckFileLedger = %d, want 2", n)
	}
	want := report.FileGateStats{Changed: 3, Reviewed: 1, Missing: []report.MissingFile{
		{File: "go.sum", Status: report.FileSkipped, Reason: "generated checksum file"},
		{File: "logo.png", Status: report.FileSkipped},
	}}
	if got := fileStats(t, result); !reflect.DeepEqual(got, want) {
		t.Errorf("record = %+v, want %+v", got, want)
	}
}

// TestCheckFileLedger_UnreportedFile pins a listed file the account leaves out,
// and the listed order of the missing entries.
func TestCheckFileLedger_UnreportedFile(t *testing.T) {
	result := &report.ReviewResult{FilesReviewed: []report.FileReview{
		{File: "c.go", Status: report.FileSkipped, Reason: "vendored"},
		{File: "a.go", Status: report.FileReviewed},
	}}
	if n := CheckFileLedger(result, []string{"a.go", "b.go", "c.go"}); n != 2 {
		t.Errorf("CheckFileLedger = %d, want 2", n)
	}
	want := []report.MissingFile{
		{File: "b.go", Status: report.FileUnreported},
		{File: "c.go", Status: report.FileSkipped, Reason: "vendored"},
	}
	if got := fileStats(t, result).Missing; !reflect.DeepEqual(got, want) {
		t.Errorf("Missing = %+v, want %+v", got, want)
	}
}

// TestCheckFileLedger_IgnoresUnlistedEntries pins that an entry for a file the
// prompt never listed counts toward nothing.
func TestCheckFileLedger_IgnoresUnlistedEntries(t *testing.T) {
	result := &report.ReviewResult{FilesReviewed: []report.FileReview{
		{File: "a.go", Status: report.FileReviewed},
		{File: "elsewhere.go", Status: report.FileReviewed},
		{File: "", Status: report.FileReviewed},
	}}
	CheckFileLedger(result, []string{"a.go"})
	if got, want := fileStats(t, result), (report.FileGateStats{Changed: 1, Reviewed: 1}); !reflect.DeepEqual(got, want) {
		t.Errorf("record = %+v, want %+v", got, want)
	}
}

// TestCheckFileLedger_DuplicateEntryReviewedWins pins that a file the account
// names twice counts as reviewed when either entry says so, whichever comes
// first and whether it names the path exactly or as "./c.go".
func TestCheckFileLedger_DuplicateEntryReviewedWins(t *testing.T) {
	result := &report.ReviewResult{FilesReviewed: []report.FileReview{
		{File: "a.go", Status: report.FileSkipped, Reason: "later"},
		{File: "a.go", Status: report.FileReviewed},
		{File: "b.go", Status: report.FileReviewed},
		{File: "b.go", Status: report.FileSkipped, Reason: "second thoughts"},
		{File: "c.go", Status: report.FileSkipped, Reason: "exact"},
		{File: "./c.go", Status: report.FileReviewed},
	}}
	if n := CheckFileLedger(result, []string{"a.go", "b.go", "c.go"}); n != 0 {
		t.Errorf("CheckFileLedger = %d, want 0: %+v", n, result.Gates.Files)
	}
}

// TestCheckFileLedger_NormalizesDotSlash pins the one path normalization: a
// leading "./" and surrounding spaces, and nothing else. A listed path that
// itself begins or ends with a space matches an entry that echoes it exactly,
// and one that trims it; an entry for a file listed next to it that trims to
// the same path stays that file's.
func TestCheckFileLedger_NormalizesDotSlash(t *testing.T) {
	result := &report.ReviewResult{FilesReviewed: []report.FileReview{
		{File: "./cmd/main.go", Status: report.FileReviewed},
		{File: " internal/x.go ", Status: report.FileReviewed},
		{File: "b/docs/a.md", Status: report.FileReviewed},
		{File: " notes.md", Status: report.FileReviewed},
		{File: "trail.go ", Status: report.FileReviewed},
		{File: "lead.go", Status: report.FileReviewed},
		{File: "pair.go", Status: report.FileReviewed},
	}}
	CheckFileLedger(result, []string{"cmd/main.go", "internal/x.go", "docs/a.md", " notes.md", "trail.go ", " lead.go", "pair.go", " pair.go"})
	want := report.FileGateStats{Changed: 8, Reviewed: 6, Missing: []report.MissingFile{
		{File: "docs/a.md", Status: report.FileUnreported},
		{File: " pair.go", Status: report.FileUnreported},
	}}
	if got := fileStats(t, result); !reflect.DeepEqual(got, want) {
		t.Errorf("record = %+v, want %+v", got, want)
	}
}

// TestCheckFileLedger_EmptyListRecordsNothing pins that a run whose prompt
// listed no files writes no record, so it reads as "not recorded" rather than
// as a run that covered every file.
func TestCheckFileLedger_EmptyListRecordsNothing(t *testing.T) {
	result := &report.ReviewResult{FilesReviewed: []report.FileReview{{File: "a.go", Status: report.FileReviewed}}}
	if n := CheckFileLedger(result, nil); n != 0 {
		t.Errorf("CheckFileLedger = %d, want 0", n)
	}
	if result.Gates != nil {
		t.Errorf("Gates = %+v, want nil", result.Gates)
	}
}

func TestCheckFileLedger_NilResult(t *testing.T) {
	if n := CheckFileLedger(nil, []string{"a.go"}); n != 0 {
		t.Errorf("CheckFileLedger(nil) = %d, want 0", n)
	}
}

// TestCheckFileLedger_LeavesFindingsUntouched pins that the check is a record:
// a missing file demotes, moves and drops nothing.
func TestCheckFileLedger_LeavesFindingsUntouched(t *testing.T) {
	findings := []report.Finding{
		{ID: "C-001", Severity: report.SeverityCritical, File: "b.go", Confidence: report.ConfidenceVerified, SnippetCheck: report.SnippetCheckPassed},
		{ID: "W-001", Severity: report.SeverityWarning, File: "a.go", Confidence: report.ConfidenceLikely},
	}
	result := &report.ReviewResult{Findings: append([]report.Finding(nil), findings...)}
	if n := CheckFileLedger(result, []string{"a.go", "b.go"}); n != 2 {
		t.Errorf("CheckFileLedger = %d, want 2", n)
	}
	if !reflect.DeepEqual(result.Findings, findings) {
		t.Errorf("findings changed:\ngot  %+v\nwant %+v", result.Findings, findings)
	}
}

// TestCheckFileLedger_KeepsOtherGates pins that the record joins the gate
// records already on the result instead of replacing them.
func TestCheckFileLedger_KeepsOtherGates(t *testing.T) {
	snippet := &report.SnippetGateStats{Examined: 2, Demoted: 1}
	result := &report.ReviewResult{Gates: &report.GateStats{Snippet: snippet}}
	CheckFileLedger(result, []string{"a.go"})
	if result.Gates.Snippet != snippet {
		t.Errorf("Snippet = %+v, want the record already there", result.Gates.Snippet)
	}
	if fileStats(t, result).Changed != 1 {
		t.Errorf("Files = %+v, want Changed 1", result.Gates.Files)
	}
}

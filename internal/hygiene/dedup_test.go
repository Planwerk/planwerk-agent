package hygiene

import (
	"errors"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// TestDedupFileless_OverlappingGroups guards against the model returning index
// groups that share an index (e.g. [[0,1],[1,2]]). The prompt asks it to place
// each index in at most one group, but nothing enforces that. Without a
// per-index claim guard, a finding merged-and-marked-for-removal by the first
// group becomes the second group's keep-target, so the third finding's content
// merges only into the doomed finding and is then pruned — silently dropping a
// distinct (and here CRITICAL) finding. Every distinct finding must survive.
func TestDedupFileless_OverlappingGroups(t *testing.T) {
	dedup := func(findings []report.Finding) ([][]int, error) {
		return [][]int{{0, 1}, {1, 2}}, nil
	}
	result := &report.ReviewResult{
		Findings: []report.Finding{
			{Severity: report.SeverityWarning, Title: "alpha", Problem: "p", Action: "a"},
			{Severity: report.SeverityWarning, Title: "beta", Problem: "p", Action: "a"},
			{Severity: report.SeverityCritical, Title: "gamma", Problem: "p", Action: "a"},
		},
	}
	DedupFileless(result, dedup)

	got := make([]string, len(result.Findings))
	titles := map[string]bool{}
	for i, f := range result.Findings {
		got[i] = f.Title
		titles[f.Title] = true
	}
	// alpha absorbs beta via group [0,1]; index 1 is then already claimed, so
	// group [1,2] leaves gamma standalone. gamma must not vanish.
	if !titles["gamma"] {
		t.Fatalf("distinct CRITICAL finding 'gamma' was dropped by overlapping dedup groups; surviving titles = %v", got)
	}
	if !titles["alpha"] {
		t.Errorf("keep-target 'alpha' missing; surviving titles = %v", got)
	}
	if len(result.Findings) != 2 {
		t.Errorf("want 2 findings after folding [0,1] and leaving gamma, got %d: %v", len(result.Findings), got)
	}
}

func TestDedupFileless_FoldsAndBoostsProvenance(t *testing.T) {
	dedup := func(findings []report.Finding) ([][]int, error) {
		return [][]int{{0, 1}}, nil
	}
	result := &report.ReviewResult{
		Findings: []report.Finding{
			{Severity: report.SeverityWarning, Title: "docs drift", Confidence: report.ConfidenceLikely, ConfirmedBy: []string{PassReview}},
			{Severity: report.SeverityWarning, Title: "stale readme", ConfirmedBy: []string{PassAdversarial}},
		},
	}
	DedupFileless(result, dedup)
	if len(result.Findings) != 1 {
		t.Fatalf("want 1 merged finding, got %d", len(result.Findings))
	}
	if len(result.Findings[0].ConfirmedBy) != 2 {
		t.Errorf("merged finding should carry both passes, got %v", result.Findings[0].ConfirmedBy)
	}
}

func TestDedupFileless_NilFnIsNoOp(t *testing.T) {
	result := &report.ReviewResult{
		Findings: []report.Finding{
			{Severity: report.SeverityWarning, Title: "a"},
			{Severity: report.SeverityWarning, Title: "b"},
		},
	}
	DedupFileless(result, nil)
	if len(result.Findings) != 2 {
		t.Errorf("a nil dedup fn must leave findings unmerged, got %d", len(result.Findings))
	}
}

func TestDedupFileless_NilResultIsNoOp(t *testing.T) {
	called := false
	DedupFileless(nil, func([]report.Finding) ([][]int, error) { called = true; return nil, nil })
	if called {
		t.Error("a nil result must not call the dedup fn")
	}
}

func TestDedupFileless_ErrorKeepsFindings(t *testing.T) {
	dedup := func(findings []report.Finding) ([][]int, error) {
		return nil, errors.New("structure tier down")
	}
	result := &report.ReviewResult{
		Findings: []report.Finding{
			{Severity: report.SeverityWarning, Title: "a"},
			{Severity: report.SeverityWarning, Title: "b"},
		},
	}
	DedupFileless(result, dedup)
	if len(result.Findings) != 2 {
		t.Errorf("a failed dedup must keep both findings, got %d", len(result.Findings))
	}
}

func TestDedupFileless_SkipsFindingsWithFiles(t *testing.T) {
	called := false
	dedup := func(findings []report.Finding) ([][]int, error) {
		called = true
		return nil, nil
	}
	// Fewer than two file-less findings: the anchored ones are the merge's job,
	// so the structure tier is never called.
	result := &report.ReviewResult{
		Findings: []report.Finding{
			{Severity: report.SeverityWarning, Title: "a", File: "a.go"},
			{Severity: report.SeverityWarning, Title: "b", File: "b.go"},
			{Severity: report.SeverityWarning, Title: "c"},
		},
	}
	DedupFileless(result, dedup)
	if called {
		t.Error("dedup must not run with fewer than two file-less findings")
	}
}

package report

import "testing"

func TestParseDataBlock(t *testing.T) {
	full := ReviewResult{Findings: []Finding{
		{ID: "C-001", Severity: SeverityCritical, Title: "SQLi", File: "db.go", Line: 10},
	}}
	body := "## Review\n\nsome text\n" + RenderDataBlock(full, "abc123", Usage{})

	sha, findings, ok := ParseDataBlock(body)
	if !ok {
		t.Fatal("expected a parseable data block")
	}
	if sha != "abc123" {
		t.Errorf("sha = %q, want abc123", sha)
	}
	if len(findings) != 1 || findings[0].Title != "SQLi" {
		t.Errorf("findings = %+v, want one SQLi finding", findings)
	}
}

func TestParseDataBlock_Missing(t *testing.T) {
	if _, _, ok := ParseDataBlock("no data block here"); ok {
		t.Error("expected ok=false when no data block is present")
	}
}

func TestFilterPreviouslyReported(t *testing.T) {
	prior := []Finding{
		{Title: "Skipped nit", File: "a.go", Line: 5},
		{Title: "Old fixed bug", File: "b.go", Line: 9},
	}
	current := []Finding{
		// Same as a prior finding, file unchanged → suppressed.
		{Title: "Skipped nit", File: "a.go", Line: 5},
		// Same title/line but file changed since prior → kept (possible regression).
		{Title: "Old fixed bug", File: "b.go", Line: 9},
		// Brand-new finding → kept.
		{Title: "New issue", File: "c.go", Line: 1},
	}
	changed := map[string]bool{"b.go": true}
	isUnchanged := func(file string) bool { return !changed[file] }

	kept, suppressed := FilterPreviouslyReported(current, prior, isUnchanged)
	if len(suppressed) != 1 || suppressed[0].Title != "Skipped nit" {
		t.Errorf("suppressed = %+v, want only the unchanged repeat", suppressed)
	}
	if len(kept) != 2 {
		t.Fatalf("kept = %d, want 2", len(kept))
	}
	gotKept := map[string]bool{}
	for _, f := range kept {
		gotKept[f.Title] = true
	}
	if !gotKept["Old fixed bug"] || !gotKept["New issue"] {
		t.Errorf("kept titles = %v, want changed-file repeat and new finding", gotKept)
	}
}

func TestFilterPreviouslyReported_NoPrior(t *testing.T) {
	current := []Finding{{Title: "x", File: "a.go", Line: 1}}
	kept, suppressed := FilterPreviouslyReported(current, nil, func(string) bool { return true })
	if len(kept) != 1 || len(suppressed) != 0 {
		t.Errorf("with no prior findings nothing is suppressed; kept=%d suppressed=%d", len(kept), len(suppressed))
	}
}

package report

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderAuditMarkdown_HeaderAndVersion(t *testing.T) {
	result := ReviewResult{Summary: "Overall healthy."}

	var buf bytes.Buffer
	r := NewRenderer(&buf)
	r.RenderAuditMarkdown(result, RepoInfo{Owner: "acme", Name: "widget"}, SeverityInfo, "", "v1.2.3")

	out := buf.String()
	if !strings.Contains(out, "# Audit: acme/widget") {
		t.Error("audit header missing or wrong")
	}
	if !strings.Contains(out, "v1.2.3") {
		t.Error("version missing from audit header")
	}
	if strings.Contains(out, "Review:") {
		t.Error("audit output must not render 'Review:' header")
	}
}

// TestRenderAuditMarkdown_LinksToolWithVersion locks the header attribution into
// the shared footer shape: planwerk-review rendered as a Markdown link to the
// repository, immediately followed by the build version.
func TestRenderAuditMarkdown_LinksToolWithVersion(t *testing.T) {
	var buf bytes.Buffer
	NewRenderer(&buf).RenderAuditMarkdown(ReviewResult{Summary: "ok"}, RepoInfo{Owner: "a", Name: "b"}, SeverityInfo, "", "v1.2.3")

	want := "[planwerk-review](https://github.com/planwerk/planwerk-review) v1.2.3"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("audit header should link planwerk-review and name the version %q\n%s", want, buf.String())
	}
}

func TestRenderAuditMarkdown_VerdictHealthy(t *testing.T) {
	result := ReviewResult{Summary: "Clean codebase."}

	var buf bytes.Buffer
	r := NewRenderer(&buf)
	r.RenderAuditMarkdown(result, RepoInfo{Owner: "a", Name: "b"}, SeverityInfo, "", "v0.0.1")

	out := buf.String()
	if !strings.Contains(out, "Codebase healthy") {
		t.Error("healthy codebase should produce 'Codebase healthy' verdict")
	}
	if !strings.Contains(out, "verdict=HEALTHY") {
		t.Error("machine-readable verdict should be HEALTHY when no findings")
	}
}

func TestRenderAuditMarkdown_VerdictActionRequired(t *testing.T) {
	result := ReviewResult{
		Findings: []Finding{
			{ID: "B-001", Severity: SeverityBlocking, Title: "Secret exposed", File: "config.go", Line: 10, Problem: "secret in code", Action: "move to env"},
			{ID: "C-001", Severity: SeverityCritical, Title: "SQL injection", File: "db.go", Line: 42, Problem: "unescaped input", Action: "use prepared statements"},
		},
	}

	var buf bytes.Buffer
	r := NewRenderer(&buf)
	r.RenderAuditMarkdown(result, RepoInfo{Owner: "a", Name: "b"}, SeverityInfo, "", "v0.0.1")

	out := buf.String()
	if !strings.Contains(out, "Action required") {
		t.Error("blocking/critical findings should produce 'Action required' verdict")
	}
	if !strings.Contains(out, "verdict=ACTION-REQUIRED") {
		t.Error("machine-readable verdict should be ACTION-REQUIRED")
	}
	if strings.Contains(out, "Do not merge") {
		t.Error("audit output must not use PR-specific 'Do not merge' phrasing")
	}
	if !strings.Contains(out, "B-001: Secret exposed") {
		t.Error("finding should be rendered")
	}
	if !strings.Contains(out, "blocking=1 critical=1 warning=0 info=0") {
		t.Error("machine-readable counts missing or wrong")
	}
}

func TestRenderAuditMarkdown_VerdictImprovementsSuggested(t *testing.T) {
	result := ReviewResult{
		Findings: []Finding{
			{ID: "W-001", Severity: SeverityWarning, Title: "Missing doc", File: "foo.go", Problem: "no doc", Action: "add docs"},
		},
	}

	var buf bytes.Buffer
	r := NewRenderer(&buf)
	r.RenderAuditMarkdown(result, RepoInfo{Owner: "a", Name: "b"}, SeverityInfo, "", "v0.0.1")

	out := buf.String()
	if !strings.Contains(out, "Improvements suggested") {
		t.Error("warning-only audit should produce 'Improvements suggested' verdict")
	}
	if !strings.Contains(out, "verdict=IMPROVEMENTS-SUGGESTED") {
		t.Error("machine-readable verdict should be IMPROVEMENTS-SUGGESTED")
	}
}

func TestRenderAuditMarkdown_RespectsMinSeverity(t *testing.T) {
	result := ReviewResult{
		Findings: []Finding{
			{ID: "C-001", Severity: SeverityCritical, Title: "Crit", File: "a.go", Problem: "p", Action: "a"},
			{ID: "I-001", Severity: SeverityInfo, Title: "Info", File: "b.go", Problem: "p", Action: "a"},
		},
	}

	var buf bytes.Buffer
	r := NewRenderer(&buf)
	r.RenderAuditMarkdown(result, RepoInfo{Owner: "a", Name: "b"}, SeverityCritical, "", "v0")

	out := buf.String()
	if !strings.Contains(out, "C-001: Crit") {
		t.Error("critical finding should be rendered")
	}
	if strings.Contains(out, "I-001: Info") {
		t.Error("info finding should be filtered out by min-severity=critical")
	}
}

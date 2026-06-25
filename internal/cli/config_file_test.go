package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-review/internal/report"
)

const (
	sevWarning  = "warning"
	sevCritical = "critical"
)

func TestLoadFileConfigMissingFile(t *testing.T) {
	cfg, present, err := LoadFileConfig(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("unexpected error for missing file: %v", err)
	}
	if present {
		t.Fatalf("present = true for missing file")
	}
	if !reflect.DeepEqual(cfg, FileConfig{}) {
		t.Fatalf("cfg = %+v, want zero value", cfg)
	}
}

func TestLoadFileConfigValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `
review:
  min-severity: warning
  max-patterns: 30
  max-findings: 20
  format: markdown
  patterns:
    - ./custom-review-patterns
propose:
  max-patterns: 60
  format: issues
audit:
  min-severity: critical
  issue-min-severity: blocking
  max-patterns: 40
  max-findings: 25
  format: json
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, present, err := LoadFileConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !present {
		t.Fatalf("present = false, want true")
	}
	if got := deref(cfg.Review.MinSeverity); got != sevWarning {
		t.Fatalf("review.min-severity = %q, want warning", got)
	}
	if got := derefInt(cfg.Review.MaxPatterns); got != 30 {
		t.Fatalf("review.max-patterns = %d, want 30", got)
	}
	if got := derefInt(cfg.Review.MaxFindings); got != 20 {
		t.Fatalf("review.max-findings = %d, want 20", got)
	}
	if !reflect.DeepEqual(cfg.Review.Patterns, []string{"./custom-review-patterns"}) {
		t.Fatalf("review.patterns = %v", cfg.Review.Patterns)
	}
	if got := derefInt(cfg.Propose.MaxPatterns); got != 60 {
		t.Fatalf("propose.max-patterns = %d, want 60", got)
	}
	if got := deref(cfg.Propose.Format); got != "issues" {
		t.Fatalf("propose.format = %q, want issues", got)
	}
	if got := deref(cfg.Audit.MinSeverity); got != sevCritical {
		t.Fatalf("audit.min-severity = %q, want critical", got)
	}
	if got := deref(cfg.Audit.IssueMinSeverity); got != "blocking" {
		t.Fatalf("audit.issue-min-severity = %q, want blocking", got)
	}
}

func TestLoadFileConfigWiki(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `
wiki:
  enabled: false
  repo: acme/widgets
  ref: v2
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, present, err := LoadFileConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !present {
		t.Fatalf("present = false, want true")
	}
	if cfg.Wiki.Enabled == nil || *cfg.Wiki.Enabled {
		t.Fatalf("wiki.enabled = %v, want explicit false", cfg.Wiki.Enabled)
	}
	if got := deref(cfg.Wiki.Repo); got != "acme/widgets" {
		t.Fatalf("wiki.repo = %q, want acme/widgets", got)
	}
	if got := deref(cfg.Wiki.Ref); got != "v2" {
		t.Fatalf("wiki.ref = %q, want v2", got)
	}
}

func TestLoadFileConfigCapture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("capture:\n  wiki: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, present, err := LoadFileConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !present {
		t.Fatalf("present = false, want true")
	}
	if cfg.Capture.Wiki == nil || !*cfg.Capture.Wiki {
		t.Fatalf("capture.wiki = %v, want explicit true", cfg.Capture.Wiki)
	}
}

// TestLoadFileConfigCaptureAbsent proves the edge: with no capture section the
// pointer stays nil, so the resolver can tell "absent" from an explicit false.
func TestLoadFileConfigCaptureAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("wiki:\n  enabled: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadFileConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Capture.Wiki != nil {
		t.Fatalf("capture.wiki = %v, want nil when the section is absent", cfg.Capture.Wiki)
	}
}

func TestLoadFileConfigCaptureUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("capture:\n  bogus-field: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadFileConfig(path); err == nil {
		t.Fatalf("expected error for unknown capture key, got nil")
	}
}

func TestLoadFileConfigWikiUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("wiki:\n  bogus-field: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadFileConfig(path); err == nil {
		t.Fatalf("expected error for unknown wiki key, got nil")
	}
}

func TestLoadFileConfigMalformedYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("review:\n  max-patterns: : :\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := LoadFileConfig(path)
	if err == nil {
		t.Fatalf("expected error for malformed YAML, got nil")
	}
	if !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("error %q does not mention parse context", err)
	}
}

func TestLoadFileConfigUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("review:\n  bogus-field: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := LoadFileConfig(path)
	if err == nil {
		t.Fatalf("expected error for unknown key, got nil")
	}
}

func TestLoadFileConfigEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, present, err := LoadFileConfig(path)
	if err != nil {
		t.Fatalf("unexpected error for empty file: %v", err)
	}
	if !present {
		t.Fatalf("present = false, want true for empty file")
	}
	if !reflect.DeepEqual(cfg, FileConfig{}) {
		t.Fatalf("cfg = %+v, want zero value", cfg)
	}
}

func TestApplyReviewFillsUnsetFlags(t *testing.T) {
	fc := FileConfig{Review: ReviewFileConfig{
		Patterns:    []string{"./from-config"},
		MinSeverity: ptr(sevWarning),
		Format:      ptr("json"),
		MaxFindings: ptrInt(42),
	}}
	cfg := Config{Format: "markdown"}
	var sev string
	fc.ApplyReview(&cfg, &sev, neverChanged)

	if !reflect.DeepEqual(cfg.PatternDirs, []string{"./from-config"}) {
		t.Fatalf("PatternDirs = %v", cfg.PatternDirs)
	}
	if sev != sevWarning {
		t.Fatalf("minSeverity = %q, want warning", sev)
	}
	if cfg.Format != "json" {
		t.Fatalf("Format = %q, want json", cfg.Format)
	}
	if cfg.MaxFindings != 42 {
		t.Fatalf("MaxFindings = %d, want 42", cfg.MaxFindings)
	}
}

func TestApplyReviewKeepsFlagValues(t *testing.T) {
	fc := FileConfig{Review: ReviewFileConfig{
		Patterns:    []string{"./from-config"},
		MinSeverity: ptr(sevWarning),
		Format:      ptr("json"),
		MaxFindings: ptrInt(42),
	}}
	cfg := Config{
		PatternDirs: []string{"./from-flag"},
		Format:      "markdown",
		MaxFindings: 7,
	}
	sev := sevCritical
	// All flags changed — file config must not overwrite anything.
	fc.ApplyReview(&cfg, &sev, alwaysChanged)

	if !reflect.DeepEqual(cfg.PatternDirs, []string{"./from-flag"}) {
		t.Fatalf("PatternDirs = %v, want flag value", cfg.PatternDirs)
	}
	if sev != sevCritical {
		t.Fatalf("minSeverity = %q, want critical", sev)
	}
	if cfg.Format != "markdown" {
		t.Fatalf("Format = %q, want markdown", cfg.Format)
	}
	if cfg.MaxFindings != 7 {
		t.Fatalf("MaxFindings = %d, want 7", cfg.MaxFindings)
	}
}

func TestApplyAuditFillsSeverities(t *testing.T) {
	fc := FileConfig{Audit: AuditFileConfig{
		MinSeverity:      ptr(sevWarning),
		IssueMinSeverity: ptr("blocking"),
	}}
	cfg := AuditConfig{
		MinSeverity:      report.SeverityInfo,
		IssueMinSeverity: report.SeverityWarning,
	}
	var sev, issueSev string
	fc.ApplyAudit(&cfg, &sev, &issueSev, neverChanged)

	if sev != sevWarning {
		t.Fatalf("minSeverity = %q, want warning", sev)
	}
	if issueSev != "blocking" {
		t.Fatalf("issueMinSeverity = %q, want blocking", issueSev)
	}
}

func TestApplyProposeFormat(t *testing.T) {
	fc := FileConfig{Propose: ProposeFileConfig{Format: ptr("issues")}}
	cfg := ProposeConfig{Format: "markdown"}
	fc.ApplyPropose(&cfg, neverChanged)
	if cfg.Format != "issues" {
		t.Fatalf("Format = %q, want issues", cfg.Format)
	}
}

func neverChanged(string) bool  { return false }
func alwaysChanged(string) bool { return true }

func ptr(s string) *string { return &s }
func ptrInt(i int) *int    { return &i }

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

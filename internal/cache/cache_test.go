package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/planwerk/planwerk-agent/internal/report"
)

func TestKey(t *testing.T) {
	k1 := Key("owner", "repo", 1, "abc123")
	k2 := Key("owner", "repo", 1, "abc123")
	k3 := Key("owner", "repo", 1, "def456")

	if k1 != k2 {
		t.Error("same inputs should produce same key")
	}
	if k1 == k3 {
		t.Error("different SHA should produce different key")
	}
	if len(k1) != 32 {
		t.Errorf("key length = %d, want 32 hex chars", len(k1))
	}
}

func TestRepoKey(t *testing.T) {
	k1 := RepoKey("owner", "repo", "abc123")
	k2 := RepoKey("owner", "repo", "abc123")
	k3 := RepoKey("owner", "repo", "def456")
	k4 := RepoKey("other", "repo", "abc123")

	if k1 != k2 {
		t.Error("same inputs should produce same key")
	}
	if k1 == k3 {
		t.Error("different HEAD SHA should produce different key")
	}
	if k1 == k4 {
		t.Error("different owner should produce different key")
	}
	if len(k1) != 32 {
		t.Errorf("key length = %d, want 32 hex chars", len(k1))
	}
}

func TestAuditKey(t *testing.T) {
	k1 := AuditKey("owner", "repo", "abc123")
	k2 := AuditKey("owner", "repo", "abc123")
	k3 := AuditKey("owner", "repo", "def456")
	k4 := AuditKey("owner", "repo", "abc123", "min=critical")

	if k1 != k2 {
		t.Error("same inputs should produce same key")
	}
	if k1 == k3 {
		t.Error("different HEAD SHA should produce different key")
	}
	if k1 == k4 {
		t.Error("different flags should produce different key")
	}
	if len(k1) != 32 {
		t.Errorf("key length = %d, want 32 hex chars", len(k1))
	}
}

func TestAuditKey_DistinctFromRepoKey(t *testing.T) {
	// AuditKey and RepoKey must namespace differently so audit and propose
	// caches cannot collide for the same repo+SHA.
	auditK := AuditKey("owner", "repo", "abc123")
	proposeK := RepoKey("owner", "repo", "abc123")
	if auditK == proposeK {
		t.Error("AuditKey and RepoKey must use distinct namespaces")
	}
}

func TestPutGet_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(SetDir(dir))

	result := &report.ReviewResult{Summary: "hello"}
	if err := Put("k1", CommandReview, result); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok := Get("k1", 0)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.Summary != "hello" {
		t.Errorf("Summary = %q, want %q", got.Summary, "hello")
	}
}

func TestGet_RejectsEntriesBeyondMaxAge(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(SetDir(dir))

	// Write entry with a frozen writtenAt 10 days in the past.
	past := time.Now().Add(-10 * 24 * time.Hour)
	t.Cleanup(SetNow(func() time.Time { return past }))
	if err := Put("k1", CommandReview, &report.ReviewResult{Summary: "old"}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Restore real clock; 7-day TTL should reject, 30-day TTL should accept.
	now = time.Now
	if _, ok := Get("k1", 7*24*time.Hour); ok {
		t.Error("entry older than max-age should miss")
	}
	if _, ok := Get("k1", 30*24*time.Hour); !ok {
		t.Error("entry within max-age should hit")
	}
	if _, ok := Get("k1", 0); !ok {
		t.Error("zero max-age should disable TTL")
	}
}

func TestGet_LegacyEntryWithoutEnvelopeTreatedAsMiss(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(SetDir(dir))

	// Write a pre-envelope file: just the ReviewResult JSON.
	legacy, _ := json.Marshal(&report.ReviewResult{Summary: "legacy"})
	if err := os.WriteFile(filepath.Join(dir, "legacy.json"), legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := Get("legacy", 0); ok {
		t.Error("legacy entry without envelope must be treated as a cache miss")
	}
}

func TestPutRawGetRaw_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(SetDir(dir))

	payload := []byte(`{"hello":"world"}`)
	if err := PutRaw("k1", CommandPropose, payload); err != nil {
		t.Fatalf("PutRaw: %v", err)
	}
	got, ok := GetRaw("k1", 0)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if string(got) != string(payload) {
		t.Errorf("GetRaw = %q, want %q", got, payload)
	}
}

func TestClear_ScopedByCommand(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(SetDir(dir))

	if err := PutRaw("review-key", CommandReview, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := PutRaw("propose-key", CommandPropose, []byte(`{"b":2}`)); err != nil {
		t.Fatal(err)
	}
	if err := PutRaw("audit-key", CommandAudit, []byte(`{"c":3}`)); err != nil {
		t.Fatal(err)
	}

	if err := Clear(CommandPropose); err != nil {
		t.Fatalf("Clear(propose): %v", err)
	}
	if _, ok := GetRaw("review-key", 0); !ok {
		t.Error("review entry should survive a propose-scoped clear")
	}
	if _, ok := GetRaw("audit-key", 0); !ok {
		t.Error("audit entry should survive a propose-scoped clear")
	}
	if _, ok := GetRaw("propose-key", 0); ok {
		t.Error("propose entry should have been cleared")
	}

	if err := Clear(""); err != nil {
		t.Fatalf("Clear(all): %v", err)
	}
	if _, ok := GetRaw("review-key", 0); ok {
		t.Error("unscoped clear should remove all entries")
	}
}

func TestStats(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(SetDir(dir))

	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	t.Cleanup(SetNow(func() time.Time { return now }))

	// Fresh propose entry (now).
	if err := PutRaw("fresh", CommandPropose, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	// Review entry from 3 days ago.
	SetNow(func() time.Time { return now.Add(-3 * 24 * time.Hour) })
	if err := PutRaw("review", CommandReview, []byte(`{"b":2}`)); err != nil {
		t.Fatal(err)
	}
	// Audit entry from 40 days ago.
	SetNow(func() time.Time { return now.Add(-40 * 24 * time.Hour) })
	if err := PutRaw("audit", CommandAudit, []byte(`{"c":3}`)); err != nil {
		t.Fatal(err)
	}
	SetNow(func() time.Time { return now })

	stats, err := Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Total != 3 {
		t.Errorf("Total = %d, want 3", stats.Total)
	}
	if stats.ByCommand[CommandPropose].Count != 1 || stats.ByCommand[CommandReview].Count != 1 || stats.ByCommand[CommandAudit].Count != 1 {
		t.Errorf("per-command counts wrong: %+v", stats.ByCommand)
	}
	if stats.Ages.LessThanDay != 1 {
		t.Errorf("LessThanDay = %d, want 1", stats.Ages.LessThanDay)
	}
	if stats.Ages.LessThanWeek != 1 {
		t.Errorf("LessThanWeek = %d, want 1", stats.Ages.LessThanWeek)
	}
	if stats.Ages.OlderThanMonth != 1 {
		t.Errorf("OlderThanMonth = %d, want 1", stats.Ages.OlderThanMonth)
	}
	if stats.Newest == nil || stats.Newest.Key != "fresh" {
		t.Errorf("Newest = %+v, want key=fresh", stats.Newest)
	}
	if stats.Oldest == nil || stats.Oldest.Key != "audit" {
		t.Errorf("Oldest = %+v, want key=audit", stats.Oldest)
	}
}

func TestInspect(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(SetDir(dir))

	payload := []byte(`{"ok":true}`)
	if err := PutRaw("k1", CommandAudit, payload); err != nil {
		t.Fatal(err)
	}
	meta, data, err := Inspect("k1")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if meta.Command != CommandAudit {
		t.Errorf("Command = %q, want %q", meta.Command, CommandAudit)
	}
	if string(data) != string(payload) {
		t.Errorf("payload = %q, want %q", data, payload)
	}
	if meta.WrittenAt.IsZero() {
		t.Error("WrittenAt should be populated")
	}

	if _, _, err := Inspect("missing"); err == nil {
		t.Error("Inspect(missing) should return an error")
	}
}

// TestKeys_ScopeToTheRunFingerprint is the regression test for the stale hits:
// the key answered "same repo, same commit, same command flags?" and served an
// entry produced by a different model tier, so re-running with a stronger model
// returned the weaker one's findings for the whole TTL.
func TestKeys_ScopeToTheRunFingerprint(t *testing.T) {
	restore := SetRunFingerprint("sonnet", "medium")
	sonnetReview := Key("o", "r", 1, "sha")
	sonnetPropose := RepoKey("o", "r", "sha")
	sonnetAudit := AuditKey("o", "r", "sha")
	sonnetGlossary := GlossaryKey("o", "r", "sha")
	restore()

	restore = SetRunFingerprint("fable", "xhigh")
	defer restore()

	for _, tc := range []struct{ name, before, after string }{
		{"review", sonnetReview, Key("o", "r", 1, "sha")},
		{"propose", sonnetPropose, RepoKey("o", "r", "sha")},
		{"audit", sonnetAudit, AuditKey("o", "r", "sha")},
		{"glossary", sonnetGlossary, GlossaryKey("o", "r", "sha")},
	} {
		if tc.before == tc.after {
			t.Errorf("%s key is identical across model tiers (%s)", tc.name, tc.after)
		}
	}
	if got := RunFingerprint(); got != "fable|xhigh" {
		t.Errorf("RunFingerprint() = %q, want the recorded tiers", got)
	}
}

// TestSetRunFingerprint_RestoresPrevious keeps the seam usable from tests the
// way SetDir and SetNow are.
func TestSetRunFingerprint_RestoresPrevious(t *testing.T) {
	before := Key("o", "r", 1, "sha")
	restore := SetRunFingerprint("opus", "xhigh")
	if Key("o", "r", 1, "sha") == before {
		t.Error("fingerprint had no effect on the key")
	}
	restore()
	if got := Key("o", "r", 1, "sha"); got != before {
		t.Errorf("key after restore = %s, want %s", got, before)
	}
}

package domains

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeOverride creates .planwerk/domains.md under a fresh temp dir and returns
// the dir, so each test states only the content it cares about.
func writeOverride(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	planwerkDir := filepath.Join(dir, ".planwerk")
	if err := os.MkdirAll(planwerkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planwerkDir, "domains.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDefault_NotEmpty(t *testing.T) {
	if strings.TrimSpace(Default()) == "" {
		t.Fatal("embedded default domain list is empty")
	}
}

// TestDefault_CoversEveryDomain pins the seven domains the plan prompt's
// "every domain appears exactly once" rule is written against. Dropping one
// here silently shrinks the sweep, so the list is asserted by name.
func TestDefault_CoversEveryDomain(t *testing.T) {
	for _, name := range []string{
		"Data & schema",
		"Compatibility & contracts",
		"Failure & recovery",
		"Observability",
		"Security & trust boundaries",
		"Performance & cost",
		"Operability & configuration",
	} {
		if !strings.Contains(Default(), "**"+name+"**") {
			t.Errorf("default domain list is missing the %q domain", name)
		}
	}
}

// TestDefault_ExcludesTestingAndDocs locks the deliberate omission: the plan
// covers both in its own Test Plan and Documentation Plan sections, so listing
// them here would duplicate an instruction that already has one home.
func TestDefault_ExcludesTestingAndDocs(t *testing.T) {
	for _, name := range []string{"**Testing", "**Documentation"} {
		if strings.Contains(Default(), name) {
			t.Errorf("default domain list must not carry a %q domain — the plan covers it in its own section", name)
		}
	}
}

func TestLoad_NoRepoOverride(t *testing.T) {
	if got := Load(t.TempDir()); got != Default() {
		t.Error("Load without an override should return the default list")
	}
}

func TestLoad_EmptyDir(t *testing.T) {
	if got := Load(""); got != Default() {
		t.Error("Load with an empty dir should return the default list")
	}
}

func TestLoad_NonexistentDir(t *testing.T) {
	if got := Load("/nonexistent/path"); got != Default() {
		t.Error("Load with a nonexistent dir should return the default list")
	}
}

func TestLoad_RepoOverride(t *testing.T) {
	custom := "- **Tenancy** — the change reads or writes data another tenant can see.\n"
	if got := Load(writeOverride(t, custom)); got != custom {
		t.Errorf("Load should return the repo override, got: %q", got)
	}
}

// TestLoad_EmptyOverrideFallsBack guards the failure that would be worst here:
// an empty override deleting the sweep instead of replacing it.
func TestLoad_EmptyOverrideFallsBack(t *testing.T) {
	if got := Load(writeOverride(t, "   \n\t\n")); got != Default() {
		t.Error("a whitespace-only override should fall back to the default list")
	}
}

func TestLoad_OversizedOverrideFallsBack(t *testing.T) {
	if got := Load(writeOverride(t, strings.Repeat("x", maxFileSize+1))); got != Default() {
		t.Error("an override larger than maxFileSize should fall back to the default list")
	}
}

// TestLoad_SymlinkNotFollowed keeps a committed symlink from redirecting the
// read outside the checkout (design decision 42).
func TestLoad_SymlinkNotFollowed(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("- **Smuggled** — should never be read.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	planwerkDir := filepath.Join(dir, ".planwerk")
	if err := os.MkdirAll(planwerkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(planwerkDir, "domains.md")); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
	got := Load(dir)
	if got != Default() {
		t.Error("a symlinked override must be treated as absent")
	}
	if strings.Contains(got, "Smuggled") {
		t.Error("Load followed a symlink out of the checkout")
	}
}

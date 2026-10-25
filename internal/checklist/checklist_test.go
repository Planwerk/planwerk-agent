package checklist

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/gitref/gitreftest"
)

func TestDefault(t *testing.T) {
	content := Default()
	if content == "" {
		t.Fatal("embedded default checklist is empty")
	}
	if !strings.Contains(content, "CRITICAL") {
		t.Error("default checklist should contain CRITICAL section")
	}
	if !strings.Contains(content, "INFORMATIONAL") {
		t.Error("default checklist should contain INFORMATIONAL section")
	}
}

func TestLoad_NoRepoOverride(t *testing.T) {
	// With a non-existent dir, should return default
	content := Load("/nonexistent/path")
	if content != Default() {
		t.Error("Load with nonexistent dir should return default checklist")
	}
}

func TestLoad_EmptyDir(t *testing.T) {
	content := Load("")
	if content != Default() {
		t.Error("Load with empty dir should return default checklist")
	}
}

func TestLoad_RepoOverride(t *testing.T) {
	tmpDir := t.TempDir()
	planwerkDir := filepath.Join(tmpDir, ".planwerk")
	if err := os.MkdirAll(planwerkDir, 0o755); err != nil {
		t.Fatal(err)
	}

	customChecklist := "## Custom Checklist\n\n- [ ] Custom check item\n"
	if err := os.WriteFile(filepath.Join(planwerkDir, "checklist.md"), []byte(customChecklist), 0o644); err != nil {
		t.Fatal(err)
	}

	content := Load(tmpDir)
	if content != customChecklist {
		t.Errorf("Load should return repo override, got: %s", content)
	}
}

func TestDefault_ContainsTestCoverageCheck(t *testing.T) {
	content := Default()
	if !strings.Contains(content, "Test Coverage for New Code") {
		t.Error("default checklist should contain 'Test Coverage for New Code' item")
	}
}

func TestDefault_ContainsDocumentationCheck(t *testing.T) {
	content := Default()
	if !strings.Contains(content, "Documentation Completeness") {
		t.Error("default checklist should contain 'Documentation Completeness' item")
	}
}

func TestDefault_TestCoverageInSemanticPass(t *testing.T) {
	content := Default()
	semanticIdx := strings.Index(content, "SEMANTIC")
	infoIdx := strings.Index(content, "INFORMATIONAL")
	testCoverageIdx := strings.Index(content, "Test Coverage for New Code")
	if semanticIdx == -1 || infoIdx == -1 || testCoverageIdx == -1 {
		t.Fatal("missing expected sections in checklist")
	}
	if testCoverageIdx < semanticIdx || testCoverageIdx > infoIdx {
		t.Error("'Test Coverage for New Code' should be in SEMANTIC pass (between SEMANTIC and INFORMATIONAL)")
	}
}

func TestDefault_DocCompletionInSemanticPass(t *testing.T) {
	content := Default()
	semanticIdx := strings.Index(content, "SEMANTIC")
	infoIdx := strings.Index(content, "INFORMATIONAL")
	docIdx := strings.Index(content, "Documentation Completeness")
	if semanticIdx == -1 || infoIdx == -1 || docIdx == -1 {
		t.Fatal("missing expected sections in checklist")
	}
	if docIdx < semanticIdx || docIdx > infoIdx {
		t.Error("'Documentation Completeness' should be in SEMANTIC pass (between SEMANTIC and INFORMATIONAL)")
	}
}

func TestDefault_TestQualityRenamed(t *testing.T) {
	content := Default()
	if !strings.Contains(content, "Test Quality:") {
		t.Error("default checklist should contain renamed 'Test Quality' item in Pass 3")
	}
	if strings.Contains(content, "Test Gaps:") {
		t.Error("default checklist should no longer contain 'Test Gaps' (renamed to 'Test Quality')")
	}
}

func TestLoad_EmptyRepoOverrideFallsBack(t *testing.T) {
	tmpDir := t.TempDir()
	planwerkDir := filepath.Join(tmpDir, ".planwerk")
	if err := os.MkdirAll(planwerkDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write an empty file — should fall back to default
	if err := os.WriteFile(filepath.Join(planwerkDir, "checklist.md"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	content := Load(tmpDir)
	if content != Default() {
		t.Error("Load with empty override file should return default checklist")
	}
}

// TestLoad_AppendsFragmentsInNameOrder: the .planwerk/checklist.d/ files follow
// the default in file-name order, each after a blank line and trimmed of its
// trailing newlines.
func TestLoad_AppendsFragmentsInNameOrder(t *testing.T) {
	dir := t.TempDir()
	gitreftest.Write(t, dir, ".planwerk/checklist.d/20-b.md", "- [ ] b\n")
	gitreftest.Write(t, dir, ".planwerk/checklist.d/10-a.md", "### Project\n\n- [ ] a\n\n")

	want := strings.TrimRight(Default(), "\n") + "\n\n### Project\n\n- [ ] a\n\n- [ ] b\n"
	if got := Load(dir); got != want {
		t.Errorf("Load = %q, want %q", got, want)
	}
}

func TestLoad_AppendsFragmentsToTheOverride(t *testing.T) {
	dir := t.TempDir()
	gitreftest.Write(t, dir, ".planwerk/checklist.md", "## Custom\n")
	gitreftest.Write(t, dir, ".planwerk/checklist.d/10-a.md", "- [ ] a\n")

	if got, want := Load(dir), "## Custom\n\n- [ ] a\n"; got != want {
		t.Errorf("Load = %q, want %q", got, want)
	}
}

// TestLoad_SkipsWhatIsNotAFragment: only regular *.md files directly in the
// directory with something besides whitespace in them are appended.
func TestLoad_SkipsWhatIsNotAFragment(t *testing.T) {
	dir := t.TempDir()
	gitreftest.Write(t, dir, ".planwerk/checklist.d/10-a.md", "- [ ] a\n")
	gitreftest.Write(t, dir, ".planwerk/checklist.d/notes.txt", "- [ ] notes\n")
	gitreftest.Write(t, dir, ".planwerk/checklist.d/sub/x.md", "- [ ] nested\n")
	gitreftest.Write(t, dir, ".planwerk/checklist.d/00-empty.md", "  \n")
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("- [ ] outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, ".planwerk", "checklist.d", "link.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	want := strings.TrimRight(Default(), "\n") + "\n\n- [ ] a\n"
	if got := Load(dir); got != want {
		t.Errorf("Load = %q, want only 10-a.md appended to the default", got)
	}
}

// TestLoad_StopsAppendingAtTheSizeCap: the fragment that would take the
// fragments' combined size past 64 KB is skipped together with every fragment
// after it, and the warning names it.
func TestLoad_StopsAppendingAtTheSizeCap(t *testing.T) {
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	dir := t.TempDir()
	gitreftest.Write(t, dir, ".planwerk/checklist.d/01-a.md", "- [ ] a\n")
	gitreftest.Write(t, dir, ".planwerk/checklist.d/02-big.md", strings.Repeat("x", maxFileSize))
	gitreftest.Write(t, dir, ".planwerk/checklist.d/03-c.md", "- [ ] c\n")

	got := Load(dir)
	if !strings.Contains(got, "- [ ] a") {
		t.Error("Load dropped the fragment that fits under the cap")
	}
	if strings.Contains(got, "xxx") {
		t.Error("Load appended the fragment that crosses the cap")
	}
	if strings.Contains(got, "- [ ] c") {
		t.Error("Load appended a fragment after the one that crossed the cap")
	}
	if log := logBuf.String(); !strings.Contains(log, "exceed the 64 KB limit") || !strings.Contains(log, "file=02-big.md") {
		t.Errorf("expected a warning naming 02-big.md, got:\n%s", log)
	}
}

package gitref

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/gitref/gitreftest"
)

// TestShow_ReadsTheCommittedBlobNotTheWorkingTree is the property the package
// exists for: a working-tree edit (a pull request's head) does not change what
// Show returns for the base ref.
func TestShow_ReadsTheCommittedBlobNotTheWorkingTree(t *testing.T) {
	dir := gitreftest.Repo(t, map[string]string{".planwerk/checklist.md": "base checklist\n"})
	if err := os.WriteFile(filepath.Join(dir, ".planwerk", "checklist.md"), []byte("rewritten by the PR\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := Show(dir, "main", ".planwerk/checklist.md", 0)
	if !ok || got != "base checklist\n" {
		t.Fatalf("Show = %q, %v; want the committed blob", got, ok)
	}
}

func TestShow_AbsentPathEmptyRefAndOversize(t *testing.T) {
	dir := gitreftest.Repo(t, map[string]string{"TODOS.md": "- [ ] one\n"})

	if _, ok := Show(dir, "main", "missing.md", 0); ok {
		t.Error("Show reported a path that does not exist at the ref")
	}
	if _, ok := Show(dir, "", "TODOS.md", 0); ok {
		t.Error("Show read with an empty ref")
	}
	if _, ok := Show(dir, "main", "TODOS.md", 3); ok {
		t.Error("Show returned a blob larger than maxBytes")
	}
}

func TestMaterialize_WritesTheDirectoryAsCommitted(t *testing.T) {
	dir := gitreftest.Repo(t, map[string]string{
		".planwerk/review_patterns/a.md":     "pattern a\n",
		".planwerk/review_patterns/sub/b.md": "pattern b\n",
		"other.md":                           "not requested\n",
	})
	if err := os.WriteFile(filepath.Join(dir, ".planwerk", "review_patterns", "a.md"), []byte("rewritten by the PR\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	root, cleanup, ok := Materialize(dir, "main", ".planwerk/review_patterns")
	if !ok {
		t.Fatal("Materialize failed for a committed directory")
	}
	for rel, want := range map[string]string{
		".planwerk/review_patterns/a.md":     "pattern a\n",
		".planwerk/review_patterns/sub/b.md": "pattern b\n",
	} {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", rel, got, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "other.md")); err == nil {
		t.Error("Materialize wrote a file outside the requested directory")
	}

	cleanup()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("cleanup left %s behind", root)
	}
}

func TestMaterialize_AbsentDirectory(t *testing.T) {
	dir := gitreftest.Repo(t, map[string]string{"README.md": "hi\n"})

	root, cleanup, ok := Materialize(dir, "main", ".claude/skills")
	defer cleanup()
	if ok || root != "" {
		t.Errorf("Materialize = %q, %v; want nothing for a directory absent at the ref", root, ok)
	}
}

package gitref

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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

// TestListFiles_ListsTheCommittedRegularFiles: the regular files directly in
// the directory as committed at ref, executable ones included, and nothing
// else, even when ref's .gitattributes would keep the directory out of a
// git archive.
func TestListFiles_ListsTheCommittedRegularFiles(t *testing.T) {
	dir := gitreftest.Repo(t, map[string]string{
		".gitattributes":                 "/.planwerk export-ignore\n",
		".planwerk/checklist.d/10-a.md":  "- [ ] a\n",
		".planwerk/checklist.d/30-c.txt": "c\n",
		".planwerk/checklist.d/sub/d.md": "- [ ] d\n",
		"other.md":                       "not requested\n",
	})
	fragments := filepath.Join(dir, ".planwerk", "checklist.d")
	gitreftest.Write(t, dir, ".planwerk/checklist.d/20-run.sh", "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(fragments, "20-run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("10-a.md", filepath.Join(fragments, "15-link.md")); err != nil {
		t.Logf("symlinks unavailable, the symlink case is not checked: %v", err)
	}
	commitAll(t, dir)
	gitreftest.Write(t, dir, ".planwerk/checklist.d/40-pr.md", "added by the PR\n")

	got := ListFiles(dir, "main", ".planwerk/checklist.d")
	want := []File{{Name: "10-a.md", Size: 8}, {Name: "20-run.sh", Size: 10}, {Name: "30-c.txt", Size: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListFiles = %+v, want %+v", got, want)
	}
}

func TestListFiles_NothingThere(t *testing.T) {
	dir := gitreftest.Repo(t, map[string]string{"README.md": "hi\n"})

	if got := ListFiles(dir, "main", ".planwerk/checklist.d"); got != nil {
		t.Errorf("ListFiles = %+v for a directory absent at the ref, want nil", got)
	}
	if got := ListFiles(dir, "", "."); got != nil {
		t.Errorf("ListFiles = %+v with an empty ref, want nil", got)
	}
	if got := ListFiles(dir, "no-such-ref", "."); got != nil {
		t.Errorf("ListFiles = %+v for a ref git cannot resolve, want nil", got)
	}
}

// commitAll commits the working tree of a gitreftest.Repo, for the entries
// (symlinks, executable bits) that gitreftest.Repo cannot write.
func commitAll(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "more"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
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

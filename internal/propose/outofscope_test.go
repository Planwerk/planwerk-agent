package propose

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func mkOOSDir(t *testing.T, repoDir string) string {
	t.Helper()
	oos := filepath.Join(repoDir, ".planwerk", "out-of-scope")
	if err := os.MkdirAll(oos, 0o755); err != nil {
		t.Fatalf("creating out-of-scope dir: %v", err)
	}
	return oos
}

func writeOOSFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func TestLoadOutOfScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T, repoDir string)
		want  []OutOfScopeEntry
	}{
		{
			name:  "missing directory returns nil",
			setup: func(t *testing.T, repoDir string) {},
			want:  nil,
		},
		{
			name: "empty directory returns nil",
			setup: func(t *testing.T, repoDir string) {
				mkOOSDir(t, repoDir)
			},
			want: nil,
		},
		{
			name: "path is a file, not a directory, returns nil",
			setup: func(t *testing.T, repoDir string) {
				if err := os.MkdirAll(filepath.Join(repoDir, ".planwerk"), 0o755); err != nil {
					t.Fatalf("creating .planwerk: %v", err)
				}
				writeOOSFile(t, filepath.Join(repoDir, ".planwerk", "out-of-scope"), "not a dir")
			},
			want: nil,
		},
		{
			name: "non-md files and subdirectories are ignored",
			setup: func(t *testing.T, repoDir string) {
				oos := mkOOSDir(t, repoDir)
				writeOOSFile(t, filepath.Join(oos, "notes.txt"), "ignored")
				if err := os.MkdirAll(filepath.Join(oos, "nested"), 0o755); err != nil {
					t.Fatalf("mkdir nested: %v", err)
				}
				writeOOSFile(t, filepath.Join(oos, "nested", "deep.md"), "# Deep\n\nignored too")
			},
			want: nil,
		},
		{
			name: "multiple entries returned in filename order",
			setup: func(t *testing.T, repoDir string) {
				oos := mkOOSDir(t, repoDir)
				writeOOSFile(t, filepath.Join(oos, "b.md"), "# Beta idea\n\nrejected B")
				writeOOSFile(t, filepath.Join(oos, "a.md"), "# Alpha idea\n\nrejected A")
			},
			want: []OutOfScopeEntry{
				{Name: "Alpha idea", Body: "# Alpha idea\n\nrejected A"},
				{Name: "Beta idea", Body: "# Beta idea\n\nrejected B"},
			},
		},
		{
			name: "name falls back to filename when no heading and body is trimmed",
			setup: func(t *testing.T, repoDir string) {
				oos := mkOOSDir(t, repoDir)
				writeOOSFile(t, filepath.Join(oos, "no-heading.md"), "\nJust prose, no heading.\n\n")
			},
			want: []OutOfScopeEntry{
				{Name: "no-heading", Body: "Just prose, no heading."},
			},
		},
		{
			name: "symlinked .md entry is skipped, not followed off-host",
			setup: func(t *testing.T, repoDir string) {
				oos := mkOOSDir(t, repoDir)
				secret := filepath.Join(repoDir, "secret.txt")
				writeOOSFile(t, secret, "AWS_SECRET_ACCESS_KEY=leak")
				if err := os.Symlink(secret, filepath.Join(oos, "aws.md")); err != nil {
					t.Fatalf("symlink entry: %v", err)
				}
				writeOOSFile(t, filepath.Join(oos, "real.md"), "# Real\n\nrejected")
			},
			want: []OutOfScopeEntry{
				{Name: "Real", Body: "# Real\n\nrejected"},
			},
		},
		{
			name: "symlinked out-of-scope directory is not followed",
			setup: func(t *testing.T, repoDir string) {
				target := filepath.Join(repoDir, "elsewhere")
				if err := os.MkdirAll(target, 0o755); err != nil {
					t.Fatalf("mkdir elsewhere: %v", err)
				}
				writeOOSFile(t, filepath.Join(target, "leak.md"), "# Leak\n\nsecret notes")
				if err := os.MkdirAll(filepath.Join(repoDir, ".planwerk"), 0o755); err != nil {
					t.Fatalf("mkdir .planwerk: %v", err)
				}
				if err := os.Symlink(target, filepath.Join(repoDir, ".planwerk", "out-of-scope")); err != nil {
					t.Fatalf("symlink dir: %v", err)
				}
			},
			want: nil,
		},
		{
			name: "oversized entry is skipped",
			setup: func(t *testing.T, repoDir string) {
				oos := mkOOSDir(t, repoDir)
				writeOOSFile(t, filepath.Join(oos, "huge.md"), strings.Repeat("A", maxOutOfScopeBytes+1))
				writeOOSFile(t, filepath.Join(oos, "small.md"), "# Small\n\nok")
			},
			want: []OutOfScopeEntry{
				{Name: "Small", Body: "# Small\n\nok"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repoDir := t.TempDir()
			tc.setup(t, repoDir)
			got, err := LoadOutOfScope(repoDir)
			if err != nil {
				t.Fatalf("LoadOutOfScope returned error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("LoadOutOfScope = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestLoadOutOfScopeEntryCap(t *testing.T) {
	t.Parallel()
	repoDir := t.TempDir()
	oos := mkOOSDir(t, repoDir)
	for i := 0; i <= maxOutOfScopeEntries; i++ {
		writeOOSFile(t, filepath.Join(oos, fmt.Sprintf("e%04d.md", i)), "# E\n\nrejected")
	}

	got, err := LoadOutOfScope(repoDir)
	if err != nil {
		t.Fatalf("LoadOutOfScope returned error: %v", err)
	}
	if len(got) != maxOutOfScopeEntries {
		t.Errorf("loaded %d entries, want cap of %d", len(got), maxOutOfScopeEntries)
	}
}

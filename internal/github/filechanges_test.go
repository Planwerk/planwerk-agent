package github

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// initFileChangesRepo builds a temp repo whose HEAD commit, on top of
// refs/remotes/origin/main, modifies keep.go, adds a non-ASCII path with a
// space, deletes gone.go, renames old_name.go to new_name.go with one line
// edited, and changes the binary logo.bin. The repo's own config asks git to
// quote paths, to skip rename detection and to print paths relative to the
// working directory, so DiffFileChanges has to override all three.
func initFileChangesRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "t@example.com")
	git(t, dir, "config", "user.name", "tester")
	git(t, dir, "config", "core.quotePath", "true")
	git(t, dir, "config", "diff.renames", "false")
	git(t, dir, "config", "diff.relative", "true")
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	renamed := "package x\n\nfunc a() {}\nfunc b() {}\nfunc c() {}\nfunc d() {}\nfunc e() {}\nfunc f() {}\nfunc g() {}\n"
	writeRepoFile(t, dir, "keep.go", "package x\n// v1\n")
	writeRepoFile(t, dir, "gone.go", "package x\n")
	writeRepoFile(t, dir, "old_name.go", renamed)
	writeRepoFile(t, dir, "logo.bin", "\x00\x01\x02\x03")
	writeRepoFile(t, dir, "sub/unchanged.go", "package sub\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")
	git(t, dir, "update-ref", "refs/remotes/origin/main", gitOut(t, dir, "rev-parse", "HEAD"))

	writeRepoFile(t, dir, "keep.go", "package x\n// v2\n// v3\n")
	writeRepoFile(t, dir, "añ b.go", "package x\n\nconst name = \"añ\"\n")
	git(t, dir, "rm", "-q", "gone.go")
	git(t, dir, "mv", "old_name.go", "new_name.go")
	writeRepoFile(t, dir, "new_name.go", strings.Replace(renamed, "func g() {}", "func g() { _ = 1 }", 1))
	writeRepoFile(t, dir, "logo.bin", "\x00\x01\x02\x04")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "head")
	return dir
}

func TestDiffFileChanges(t *testing.T) {
	want := map[string]FileChange{
		"keep.go":     {Path: "keep.go", Status: FileModified, Added: 2, Deleted: 1},
		"añ b.go":     {Path: "añ b.go", Status: FileAdded, Added: 3},
		"gone.go":     {Path: "gone.go", Status: FileDeleted, Deleted: 1},
		"new_name.go": {Path: "new_name.go", OldPath: "old_name.go", Status: FileRenamed, Added: 1, Deleted: 1},
		"logo.bin":    {Path: "logo.bin", Status: FileModified, Binary: true},
	}
	assertChanges := func(t *testing.T, files []FileChange) {
		t.Helper()
		got := make(map[string]FileChange, len(files))
		for _, f := range files {
			got[f.Path] = f
		}
		if len(files) != len(want) || !reflect.DeepEqual(got, want) {
			t.Errorf("DiffFileChanges =\n%+v\nwant (in any order)\n%+v", files, want)
		}
	}

	t.Run("success", func(t *testing.T) {
		dir := initFileChangesRepo(t)
		files, err := client.DiffFileChanges(dir, "main")
		if err != nil {
			t.Fatalf("DiffFileChanges returned error: %v", err)
		}
		assertChanges(t, files)
	})

	// diff.relative would list only the files under the working directory,
	// relative to it; --no-relative keeps the whole change at top-level paths,
	// the set the review session's diff file holds.
	t.Run("from a subdirectory", func(t *testing.T) {
		dir := initFileChangesRepo(t)
		files, err := client.DiffFileChanges(filepath.Join(dir, "sub"), "main")
		if err != nil {
			t.Fatalf("DiffFileChanges returned error: %v", err)
		}
		assertChanges(t, files)
	})

	t.Run("missing remote", func(t *testing.T) {
		dir := t.TempDir()
		git(t, dir, "init", "-q")
		git(t, dir, "config", "user.email", "t@example.com")
		git(t, dir, "config", "user.name", "tester")
		writeRepoFile(t, dir, "a.go", "package x\n")
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-q", "-m", "only")

		files, err := client.DiffFileChanges(dir, "main")
		if err == nil {
			t.Fatal("expected an error when origin/main is missing, got nil")
		}
		if files != nil {
			t.Errorf("files = %v, want nil on error", files)
		}
		for _, want := range []string{"diff --no-ext-diff", "--name-status", "origin/main...HEAD"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name the git command (%q)", err, want)
			}
		}
	})

	t.Run("empty inputs", func(t *testing.T) {
		for _, in := range [][2]string{{"", ""}, {"", "main"}, {t.TempDir(), ""}} {
			files, err := client.DiffFileChanges(in[0], in[1])
			if files != nil || err != nil {
				t.Errorf("DiffFileChanges(%q, %q) = %v, %v; want nil, nil", in[0], in[1], files, err)
			}
		}
	})
}

func TestParseNameStatusZ(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		want []FileChange
	}{
		{"empty", "", nil},
		{"modified", "M\x00a.go\x00", []FileChange{{Path: "a.go", Status: FileModified}}},
		{"no trailing NUL", "A\x00a.go", []FileChange{{Path: "a.go", Status: FileAdded}}},
		{"deleted", "D\x00gone.go\x00", []FileChange{{Path: "gone.go", Status: FileDeleted}}},
		{"rename with score", "R085\x00old.go\x00new.go\x00", []FileChange{{Path: "new.go", OldPath: "old.go", Status: FileRenamed}}},
		{"copy with score", "C075\x00src.go\x00dst.go\x00", []FileChange{{Path: "dst.go", OldPath: "src.go", Status: FileCopied}}},
		{"type change", "T\x00link\x00", []FileChange{{Path: "link", Status: FileTypeChanged}}},
		{"unknown letter", "X\x00odd.go\x00", []FileChange{{Path: "odd.go", Status: FileModified}}},
		{"tab and newline in a path", "A\x00a\tb\nc.go\x00", []FileChange{{Path: "a\tb\nc.go", Status: FileAdded}}},
		{
			"several entries in order",
			"R100\x00x.go\x00y.go\x00M\x00b.go\x00A\x00a.go\x00",
			[]FileChange{
				{Path: "y.go", OldPath: "x.go", Status: FileRenamed},
				{Path: "b.go", Status: FileModified},
				{Path: "a.go", Status: FileAdded},
			},
		},
		{"truncated rename", "M\x00a.go\x00R090\x00only-old.go", []FileChange{{Path: "a.go", Status: FileModified}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseNameStatusZ([]byte(tc.out)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseNameStatusZ(%q) = %+v, want %+v", tc.out, got, tc.want)
			}
		})
	}
}

func TestParseNumstatZ(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		want map[string]lineCounts
	}{
		{"empty", "", map[string]lineCounts{}},
		{"counts", "2\t1\tkeep.go\x00", map[string]lineCounts{"keep.go": {added: 2, deleted: 1}}},
		{"binary", "-\t-\tlogo.bin\x00", map[string]lineCounts{"logo.bin": {binary: true}}},
		{"rename keyed by the new path", "1\t1\t\x00old.go\x00new.go\x00", map[string]lineCounts{"new.go": {added: 1, deleted: 1}}},
		{"tab inside a path", "3\t0\ta\tb.go\x00", map[string]lineCounts{"a\tb.go": {added: 3}}},
		{
			// The old path of a rename holds tabs, and must not be read as an
			// entry of its own.
			"rename followed by an entry",
			"0\t0\t\x00x\t9\ty.go\x00z.go\x005\t2\tm.go\x00",
			map[string]lineCounts{"z.go": {}, "m.go": {added: 5, deleted: 2}},
		},
		{"truncated rename", "1\t1\t\x00old.go", map[string]lineCounts{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseNumstatZ([]byte(tc.out)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseNumstatZ(%q) = %+v, want %+v", tc.out, got, tc.want)
			}
		})
	}
}

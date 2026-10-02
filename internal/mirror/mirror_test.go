package mirror

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/github"
)

func TestDir(t *testing.T) {
	root := t.TempDir()
	got, err := Dir(root, "Acme", "Widgets")
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if want := filepath.Join(root, "acme", "widgets"); got != want {
		t.Errorf("Dir = %q, want %q (owner and name in lowercase)", got, want)
	}

	// "." and ".." pass the repository reference parser and would leave the
	// root.
	for _, tc := range []struct{ owner, name, want string }{
		{"..", "x", "invalid repository ../x for a mirror directory"},
		{"acme", ".", "invalid repository acme/. for a mirror directory"},
		{".", "x", "invalid repository ./x for a mirror directory"},
		{"acme", "..", "invalid repository acme/.. for a mirror directory"},
	} {
		if dir, err := Dir(root, tc.owner, tc.name); err == nil || err.Error() != tc.want {
			t.Errorf("Dir(%q, %q) = %q, %v, want the error %q", tc.owner, tc.name, dir, err, tc.want)
		}
	}
}

func TestDefaultRoot(t *testing.T) {
	t.Run("under the user cache directory", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CACHE_HOME", home)
		base, err := os.UserCacheDir()
		if err != nil {
			t.Fatalf("os.UserCacheDir: %v", err)
		}
		got, err := DefaultRoot()
		if want := filepath.Join(base, "planwerk-agent", "brain"); err != nil || got != want {
			t.Errorf("DefaultRoot = %q, %v, want %q", got, err, want)
		}
	})

	// The mirror holds GitHub's text unchanged, so there is no fallback to the
	// temp directory, which every local user can write to.
	t.Run("an error without a user cache directory", func(t *testing.T) {
		t.Setenv("HOME", "")
		t.Setenv("XDG_CACHE_HOME", "")
		if _, err := os.UserCacheDir(); err == nil {
			t.Skip("the platform resolves a cache directory without HOME")
		}
		got, err := DefaultRoot()
		if err == nil || got != "" || !strings.HasPrefix(err.Error(), "resolving the user cache directory for the mirror: ") {
			t.Errorf("DefaultRoot = %q, %v, want an error and no path", got, err)
		}
	})
}

func TestItemPath(t *testing.T) {
	dir := filepath.Join("m", "acme", "widgets")
	if got, want := ItemPath(dir, github.ItemKindIssue, 7), filepath.Join(dir, "issues", "7.md"); got != want {
		t.Errorf("issue path = %q, want %q", got, want)
	}
	if got, want := ItemPath(dir, github.ItemKindPull, 9), filepath.Join(dir, "pulls", "9.md"); got != want {
		t.Errorf("pull request path = %q, want %q", got, want)
	}
}

func TestItemFiles(t *testing.T) {
	dir := t.TempDir()
	if files, err := ItemFiles(dir, github.ItemKindIssue); files != nil || err != nil {
		t.Errorf("ItemFiles of a missing directory = %v, %v, want nil, nil", files, err)
	}

	issues := filepath.Join(dir, "issues")
	pulls := filepath.Join(dir, "pulls")
	// Beside the items, issues/ holds what an interrupted write leaves behind,
	// a file of another extension, and a directory with the items' extension.
	for _, d := range []string{issues, pulls, filepath.Join(issues, "old.md")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{
		filepath.Join(issues, "7.md"), filepath.Join(issues, "10.md"),
		filepath.Join(issues, "3.md.1234.tmp"), filepath.Join(issues, "notes.txt"),
		filepath.Join(pulls, "9.md"),
	} {
		if err := os.WriteFile(f, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		kind string
		want []string
	}{
		{github.ItemKindIssue, []string{filepath.Join(issues, "10.md"), filepath.Join(issues, "7.md")}},
		{github.ItemKindPull, []string{filepath.Join(pulls, "9.md")}},
	} {
		got, err := ItemFiles(dir, tc.kind)
		if err != nil || strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
			t.Errorf("ItemFiles(%s) = %v, %v, want %v", tc.kind, got, err, tc.want)
		}
	}

	t.Run("a directory that cannot be read", func(t *testing.T) {
		dir := t.TempDir()
		blocked := filepath.Join(dir, "pulls")
		if err := os.WriteFile(blocked, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		files, err := ItemFiles(dir, github.ItemKindPull)
		if err == nil || files != nil || !strings.HasPrefix(err.Error(), "reading "+blocked+": ") {
			t.Errorf("ItemFiles = %v, %v, want an error that names the directory", files, err)
		}
	})
}

func TestLoadState(t *testing.T) {
	writeState := func(t *testing.T, content string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	t.Run("a missing file is a fresh state", func(t *testing.T) {
		dir := t.TempDir()
		st, exists, err := LoadState(dir, testRepo)
		if err != nil || exists {
			t.Fatalf("LoadState = %+v, %v, %v, want a fresh state", st, exists, err)
		}
		if want := (State{Version: 1, Repo: testRepo}); *st != want {
			t.Errorf("state = %+v, want %+v", *st, want)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("LoadState must only read; the directory now holds %d entries", len(entries))
		}
	})

	t.Run("a saved state loads back", func(t *testing.T) {
		dir := t.TempDir()
		want := State{Version: 1, Repo: testRepo, SyncedAt: "2026-10-02T09:00:00Z",
			Items: ItemsState{Cursor: "2026-10-02T08:17:02Z"}, Wiki: WikiState{Repo: testRepo, Commit: "1a2b3c4"}}
		if err := want.Save(dir); err != nil {
			t.Fatalf("Save: %v", err)
		}
		// The repository is compared without case, as GitHub does.
		st, exists, err := LoadState(dir, "Acme/Widgets")
		if err != nil || !exists || *st != want {
			t.Errorf("LoadState = %+v, %v, %v, want %+v", st, exists, err, want)
		}
		info, err := os.Stat(filepath.Join(dir, stateFileName))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("state.json mode = %v, %v, want 0600", info.Mode().Perm(), err)
		}
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
			t.Errorf("the directory holds %d entries after a save (%v), want state.json alone: the temporary file must be gone", len(entries), err)
		}
		raw, _ := os.ReadFile(filepath.Join(dir, stateFileName))
		for _, key := range []string{`"version": 1`, `"repo": "acme/widgets"`, `"synced_at"`, `"items"`, `"cursor"`, `"wiki"`, `"commit"`} {
			if !strings.Contains(string(raw), key) {
				t.Errorf("state.json lacks %s:\n%s", key, raw)
			}
		}
	})

	for _, tc := range []struct{ name, content, repo, want string }{
		{"malformed JSON", "{", testRepo, "parsing "},
		{"another version", `{"version": 2, "repo": "acme/widgets"}`, testRepo, "unsupported mirror state version 2 in "},
		{"another repository", `{"version": 1, "repo": "acme/widgets"}`, "acme/gadgets", " holds the mirror of acme/widgets, not acme/gadgets"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeState(t, tc.content)
			st, exists, err := LoadState(dir, tc.repo)
			if err == nil || st != nil || exists {
				t.Fatalf("LoadState = %+v, %v, %v, want an error alone", st, exists, err)
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), dir) {
				t.Errorf("err = %q, want it to contain %q and the directory", err, tc.want)
			}
		})
	}

	t.Run("another version names the rebuild", func(t *testing.T) {
		_, _, err := LoadState(writeState(t, `{"version": 2}`), testRepo)
		if err == nil || !strings.HasSuffix(err.Error(), "; run brain sync --full to rebuild") {
			t.Errorf("err = %v, want it to name brain sync --full", err)
		}
	})
}

// TestSave_WritesThroughNoPlantedFile covers a link someone placed where a
// temporary file with a fixed name would be: the save leaves the link's
// target alone and writes a regular file of its own.
func TestSave_WritesThroughNoPlantedFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, stateFileName+".tmp")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}

	st := State{Version: 1, Repo: testRepo}
	if err := st.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "kept" {
		t.Errorf("the link's target = %q, %v, want it untouched", data, err)
	}
	info, err := os.Lstat(filepath.Join(dir, stateFileName))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Errorf("state.json: mode %v, %v, want a regular file of mode 0600", info.Mode(), err)
	}
}

// TestWriteFileAtomic_ConcurrentWritersOfOnePath covers two runs that write
// one file at the same time: every write succeeds, the file holds all of one
// writer's data, and no temporary file stays behind.
func TestWriteFileAtomic_ConcurrentWritersOfOnePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "7.md")
	contents := []string{strings.Repeat("a", 1<<16), strings.Repeat("b", 1<<16)}
	errs := make([]error, len(contents))
	var wg sync.WaitGroup
	for i, content := range contents {
		wg.Go(func() {
			for range 100 {
				if errs[i] = writeFileAtomic(path, []byte(content)); errs[i] != nil {
					return
				}
			}
		})
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("writer %d: %v", i, err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil || (string(got) != contents[0] && string(got) != contents[1]) {
		t.Errorf("the file holds %d bytes (%v), want all of one writer's data", len(got), err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Errorf("the directory holds %d entries (%v), want the file alone", len(entries), err)
	}
}

func TestSave_MissingDirectoryIsError(t *testing.T) {
	st := State{Version: 1, Repo: testRepo}
	if err := st.Save(filepath.Join(t.TempDir(), "absent")); err == nil || !strings.HasPrefix(err.Error(), "writing ") {
		t.Errorf("err = %v, want a write error", err)
	}
}

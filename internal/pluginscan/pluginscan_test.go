package pluginscan

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTargets_KeepsTheRegularFilesInsideTheCheckout: a deleted file, a
// directory, a path that climbs out, and a file reached through a symlink
// that leaves the checkout are dropped; the rest come back sorted, once each,
// behind "./".
func TestTargets_KeepsTheRegularFilesInsideTheCheckout(t *testing.T) {
	outside := t.TempDir()
	writeFile(t, outside, "secret.html", "<h1>secret</h1>")
	repo := t.TempDir()
	writeFile(t, repo, "web/ok.html", "<p>")
	writeFile(t, repo, "-flag.html", "<p>")
	if err := os.Symlink(outside, filepath.Join(repo, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.html"), filepath.Join(repo, "page.html")); err != nil {
		t.Fatal(err)
	}
	got := Targets(repo, []string{"web/ok.html", "linked/secret.html", "page.html", "gone.html", "web", "../" + filepath.Base(outside) + "/secret.html", "-flag.html", "web/ok.html"})
	want := []string{"./-flag.html", "./web/ok.html"}
	if !slices.Equal(got, want) {
		t.Errorf("Targets = %v, want %v", got, want)
	}
	if p := Path(got[1]); p != "web/ok.html" {
		t.Errorf("Path(%q) = %q, want web/ok.html", got[1], p)
	}
}

func TestLimitedBuffer_DropsWhatIsPastItsCap(t *testing.T) {
	var buf bytes.Buffer
	l := &LimitedBuffer{Buf: &buf, Max: 4}
	for _, s := range []string{"ab", "cdef", "gh"} {
		if n, err := l.Write([]byte(s)); n != len(s) || err != nil {
			t.Fatalf("Write(%q) = %d, %v; want every byte reported written", s, n, err)
		}
	}
	if buf.String() != "abcd" {
		t.Errorf("kept %q, want abcd", buf.String())
	}
}

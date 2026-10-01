// Package patternstest holds the helpers shared by the tests of the commands
// that hand a wiki's project memory to a session: the page a wiki seam
// returns, and the checks on the directory patterns.MaterializeMemory writes
// for a run.
package patternstest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/patterns"
)

// MemoryPage is the one project-memory page the wiki seams return in the
// memory tests.
func MemoryPage() patterns.MemoryPage {
	return patterns.MemoryPage{Name: "pin-dependencies.md", Title: "Pin every dependency", Body: "# Pin every dependency"}
}

// IsolateTempDir points TMPDIR at a fresh directory for the test and returns
// it, so a memory directory a run left behind is found by AssertNoMemoryDir.
func IsolateTempDir(t testing.TB) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	return tmp
}

// AssertNoMemoryDir fails when tmp holds a project memory directory.
func AssertNoMemoryDir(t testing.TB, tmp string) {
	t.Helper()
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("reading %s: %v", tmp, err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), patterns.MemoryDirPrefix) {
			t.Errorf("the run left the memory directory %s behind", e.Name())
		}
	}
}

// AssertMemoryPageReadable fails when the page of MemoryPage is not on disk
// under mem.Dir with its body and a trailing newline.
func AssertMemoryPageReadable(t testing.TB, mem patterns.MemoryCatalog) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(mem.Dir, MemoryPage().Name))
	if err != nil {
		t.Errorf("the memory page must be readable while the session runs: %v", err)
		return
	}
	if string(data) != MemoryPage().Body+"\n" {
		t.Errorf("memory page = %q, want the page body and a newline", data)
	}
}

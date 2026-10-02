package search

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/mirror"
)

// testBinary is the path the stub lookup returns for this binary.
const testBinary = "/usr/local/bin/planwerk-agent"

// binaryAt returns a lookup that answers path.
func binaryAt(path string) func() (string, error) {
	return func() (string, error) { return path, nil }
}

// surfaceMirror returns a root with a finished mirror of acme/widgets under
// it, and the mirror directory.
func surfaceMirror(t *testing.T) (root, dir string) {
	t.Helper()
	root = t.TempDir()
	dir, err := mirror.Dir(root, "acme", "widgets")
	if err != nil {
		t.Fatal(err)
	}
	newMirrorAt(t, dir)
	threeFiles(t, dir)
	return root, dir
}

func TestResolveSurface_AFinishedMirror(t *testing.T) {
	root, dir := surfaceMirror(t)
	logs := captureLogs(t)

	got := resolveSurface("acme", "widgets", root, binaryAt(testBinary))
	want := Surface{
		Command:  testBinary + " brain search acme/widgets",
		SyncedAt: testSyncedAt,
		Revision: "2026-10-01T08:00:00Z@1a2b3c4d5e6f70819293a4b5c6d7e8f901234567",
	}
	if got != want {
		t.Errorf("surface = %+v, want %+v", got, want)
	}
	if !got.Enabled() {
		t.Error("the surface of a finished mirror must be enabled")
	}
	// The index is built here, before any session searches.
	if _, err := os.Stat(filepath.Join(dir, IndexFileName)); err != nil {
		t.Errorf("resolving the surface must build the index: %v", err)
	}
	reopen(t, dir, 0, 0)
	if !strings.Contains(logs.String(), "the sessions can search the mirror") || strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("log = %q, want the surface announced and no warning", logs)
	}
}

func TestResolveSurface_NoFinishedMirror(t *testing.T) {
	const warning = "the brain is enabled, but this repository has no finished mirror; the sessions get no search"

	t.Run("no mirror", func(t *testing.T) {
		root := t.TempDir()
		logs := captureLogs(t)
		if got := resolveSurface("acme", "widgets", root, binaryAt(testBinary)); got != (Surface{}) {
			t.Errorf("surface = %+v, want the zero surface", got)
		}
		// The error names the command to run.
		for _, want := range []string{warning, "repo=acme/widgets", `err="no mirror of acme/widgets at `, `run \"planwerk-agent brain sync acme/widgets\" first`} {
			if !strings.Contains(logs.String(), want) {
				t.Errorf("log = %q, want it to hold %q", logs, want)
			}
		}
		if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
			t.Errorf("resolving the surface of a missing mirror must create nothing: %v, %v", entries, err)
		}
	})

	t.Run("a mirror whose first sync never finished", func(t *testing.T) {
		root, dir := surfaceMirror(t)
		st, _, err := mirror.LoadState(dir, testRepo)
		if err != nil {
			t.Fatal(err)
		}
		st.SyncedAt = ""
		if err := st.Save(dir); err != nil {
			t.Fatal(err)
		}
		logs := captureLogs(t)
		if got := resolveSurface("acme", "widgets", root, binaryAt(testBinary)); got != (Surface{}) {
			t.Errorf("surface = %+v, want the zero surface", got)
		}
		if !strings.Contains(logs.String(), warning) || !strings.Contains(logs.String(), `has never finished a sync; run \"planwerk-agent brain sync acme/widgets\" again`) {
			t.Errorf("log = %q, want the warning with the command to run", logs)
		}
		if _, err := os.Stat(filepath.Join(dir, IndexFileName)); !os.IsNotExist(err) {
			t.Errorf("no index must be built for an unfinished mirror: %v", err)
		}
	})

	t.Run("a state file that does not parse", func(t *testing.T) {
		root, dir := surfaceMirror(t)
		writeFile(t, filepath.Join(dir, "state.json"), "{")
		logs := captureLogs(t)
		if got := resolveSurface("acme", "widgets", root, binaryAt(testBinary)); got != (Surface{}) {
			t.Errorf("surface = %+v, want the zero surface", got)
		}
		if !strings.Contains(logs.String(), warning) || !strings.Contains(logs.String(), "err=") {
			t.Errorf("log = %q, want the warning with the error", logs)
		}
	})

	t.Run("a repository that leaves the root", func(t *testing.T) {
		logs := captureLogs(t)
		if got := resolveSurface("..", "widgets", t.TempDir(), binaryAt(testBinary)); got != (Surface{}) {
			t.Errorf("surface = %+v, want the zero surface", got)
		}
		if !strings.Contains(logs.String(), warning) {
			t.Errorf("log = %q, want the warning", logs)
		}
	})
}

func TestResolveSurface_ABinaryPathNoRuleCanCarry(t *testing.T) {
	const warning = "the brain is enabled, but the path of this binary cannot be written into a permission rule; the sessions get no search"
	for name, executable := range map[string]func() (string, error){
		"the lookup fails":      func() (string, error) { return "", errors.New("no path of this binary") },
		"a path with a space":   binaryAt("/opt/my tools/planwerk-agent"),
		"a Windows path":        binaryAt(`C:\bin\planwerk-agent.exe`),
		"a path with a quote":   binaryAt(`/opt/"x"/planwerk-agent`),
		"a path with a bracket": binaryAt("/opt/x)/planwerk-agent"),
		"an empty path":         binaryAt(""),
	} {
		t.Run(name, func(t *testing.T) {
			root, dir := surfaceMirror(t)
			logs := captureLogs(t)
			if got := resolveSurface("acme", "widgets", root, executable); got != (Surface{}) {
				t.Errorf("surface = %+v, want the zero surface", got)
			}
			if !strings.Contains(logs.String(), warning) {
				t.Errorf("log = %q, want the warning", logs)
			}
			// A lookup that failed is named; a path that is refused is no error.
			if got, want := strings.Contains(logs.String(), `err="no path of this binary"`), name == "the lookup fails"; got != want {
				t.Errorf("log = %q, names the lookup error = %v, want %v", logs, got, want)
			}
			if _, err := os.Stat(filepath.Join(dir, IndexFileName)); !os.IsNotExist(err) {
				t.Errorf("no index must be built when the sessions get no search: %v", err)
			}
		})
	}
}

func TestResolveSurface_AnIndexThatCannotBeOpened(t *testing.T) {
	root, dir := surfaceMirror(t)
	// A directory in the place of the index.
	writeFile(t, filepath.Join(dir, IndexFileName, "keep"), "")
	logs := captureLogs(t)

	if got := resolveSurface("acme", "widgets", root, binaryAt(testBinary)); got != (Surface{}) {
		t.Errorf("surface = %+v, want the zero surface", got)
	}
	if want := "the brain is enabled, but the search index could not be built; the sessions get no search"; !strings.Contains(logs.String(), want) {
		t.Errorf("log = %q, want it to hold %q", logs, want)
	}
}

func TestSurface_CacheFlag(t *testing.T) {
	s := Surface{Command: testBinary + " brain search acme/widgets", Revision: "2026-10-01T08:00:00Z@1a2b3c4"}
	if got, want := s.CacheFlag(), "brain=2026-10-01T08:00:00Z@1a2b3c4"; got != want {
		t.Errorf("CacheFlag = %q, want %q", got, want)
	}
	// A surface that is not enabled has no part in a key, whatever it holds.
	if got := (Surface{Revision: "2026-10-01T08:00:00Z@1a2b3c4"}).CacheFlag(); got != "" {
		t.Errorf("CacheFlag of a surface without a command = %q, want none", got)
	}
}

func TestSurfaceFor(t *testing.T) {
	want := Surface{Command: testBinary + " brain search acme/widgets", SyncedAt: testSyncedAt}
	var calls []string
	resolve := func(owner, name string) Surface {
		calls = append(calls, owner+"/"+name)
		return want
	}

	if got := SurfaceFor(true, resolve, "acme", "widgets"); got != want {
		t.Errorf("SurfaceFor of a run that opted in = %+v, want %+v", got, want)
	}
	// A run that did not opt in resolves nothing.
	if got := SurfaceFor(false, resolve, "acme", "gadgets"); got != (Surface{}) {
		t.Errorf("SurfaceFor of a run that did not opt in = %+v, want the zero surface", got)
	}
	if !slices.Equal(calls, []string{"acme/widgets"}) {
		t.Errorf("resolve was called for %v, want acme/widgets alone", calls)
	}
}

func TestSurface_AllowRule(t *testing.T) {
	s := Surface{Command: testBinary + " brain search acme/widgets"}
	if got, want := s.AllowRule(), "Bash(/usr/local/bin/planwerk-agent brain search acme/widgets:*)"; got != want {
		t.Errorf("AllowRule = %q, want %q", got, want)
	}
	if got := (Surface{}).AllowRule(); got != "" || (Surface{}).Enabled() {
		t.Errorf("AllowRule of the zero surface = %q, want none and no search", got)
	}
}

package patterns

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestEmbeddedCatalogMatchesOnDisk walks the on-disk catalog under
// internal/patterns/patterns/ and the embedded fs.FS, then asserts the sets of
// relative .md paths are equal. It fails if a new .md is added on disk but not
// embedded (e.g. a misconfigured embed pattern) or vice versa.
func TestEmbeddedCatalogMatchesOnDisk(t *testing.T) {
	const diskRoot = "patterns" // relative to the package dir (internal/patterns)

	onDisk := map[string]bool{}
	err := filepath.WalkDir(diskRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, err := filepath.Rel(diskRoot, path)
		if err != nil {
			return err
		}
		onDisk[filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walking on-disk catalog: %v", err)
	}
	if len(onDisk) == 0 {
		t.Fatal("found no .md files on disk; test setup is wrong")
	}

	embedded := map[string]bool{}
	err = fs.WalkDir(embeddedFS, diskRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		embedded[strings.TrimPrefix(path, diskRoot+"/")] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walking embedded catalog: %v", err)
	}

	for rel := range onDisk {
		if !embedded[rel] {
			t.Errorf("on-disk pattern %q is missing from the embedded catalog", rel)
		}
	}
	for rel := range embedded {
		if !onDisk[rel] {
			t.Errorf("embedded pattern %q has no on-disk counterpart", rel)
		}
	}
}

// TestLoadEmbedded_ParsesKnownPatterns pins a couple of stable pattern names so
// a renamed or dropped embed pattern surfaces immediately, and verifies every
// embedded pattern carries the synthetic "embedded:" FilePath prefix the
// catalog-URL builder depends on.
func TestLoadEmbedded_ParsesKnownPatterns(t *testing.T) {
	pats, err := loadEmbedded()
	if err != nil {
		t.Fatalf("loadEmbedded: %v", err)
	}
	if len(pats) < 80 {
		t.Errorf("loadEmbedded returned %d patterns, want >= 80", len(pats))
	}

	names := map[string]bool{}
	for _, p := range pats {
		names[p.Name] = true
		if !strings.HasPrefix(p.FilePath, "embedded:patterns/") {
			t.Errorf("pattern %q FilePath = %q, want embedded:patterns/ prefix", p.Name, p.FilePath)
		}
	}
	for _, want := range []string{"Go Error Wrapping", "YAGNI - You Aren't Gonna Need It"} {
		if !names[want] {
			t.Errorf("loadEmbedded missing expected pattern %q", want)
		}
	}
}

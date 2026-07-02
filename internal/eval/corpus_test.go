package eval

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile writes content to path, creating parent directories.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// writeValidCase writes a well-formed non-clean case under root/name and returns
// its directory. base/, head/, and a one-finding expected.json are all present.
func writeValidCase(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	writeFile(t, filepath.Join(dir, "base", "main.go.txt"), "package main\n")
	writeFile(t, filepath.Join(dir, "head", "main.go.txt"), "package main // changed\n")
	writeFile(t, filepath.Join(dir, "expected.json"), `{
  "description": "seeded bug",
  "clean": false,
  "findings": [{"file": "main.go", "line": 1, "severity": "WARNING", "keywords": ["bug"]}]
}`)
	return dir
}

func TestLoadCaseValid(t *testing.T) {
	root := t.TempDir()
	dir := writeValidCase(t, root, "sample")

	c, err := LoadCase(dir)
	if err != nil {
		t.Fatalf("LoadCase: %v", err)
	}
	if c.Name != "sample" {
		t.Errorf("Name = %q, want %q", c.Name, "sample")
	}
	if c.Expected.Clean {
		t.Error("Clean = true, want false")
	}
	if got := len(c.Expected.Findings); got != 1 {
		t.Fatalf("findings = %d, want 1", got)
	}
	if kw := c.Expected.Findings[0].Keywords; len(kw) != 1 || kw[0] != "bug" {
		t.Errorf("keywords = %v, want [bug]", kw)
	}
}

func TestLoadCaseErrors(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, dir string)
	}{
		{
			name: "missing base",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "head", "main.go.txt"), "package main\n")
				writeFile(t, filepath.Join(dir, "expected.json"), `{"clean": true, "findings": []}`)
			},
		},
		{
			name: "missing head",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "base", "main.go.txt"), "package main\n")
				writeFile(t, filepath.Join(dir, "expected.json"), `{"clean": true, "findings": []}`)
			},
		},
		{
			name: "base is a file not a dir",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "base"), "not a dir\n")
				writeFile(t, filepath.Join(dir, "head", "main.go.txt"), "package main\n")
				writeFile(t, filepath.Join(dir, "expected.json"), `{"clean": true, "findings": []}`)
			},
		},
		{
			name: "missing expected.json",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "base", "main.go.txt"), "package main\n")
				writeFile(t, filepath.Join(dir, "head", "main.go.txt"), "package main\n")
			},
		},
		{
			name: "malformed json",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "base", "main.go.txt"), "package main\n")
				writeFile(t, filepath.Join(dir, "head", "main.go.txt"), "package main\n")
				writeFile(t, filepath.Join(dir, "expected.json"), `{"clean": false, `)
			},
		},
		{
			name: "unknown field",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "base", "main.go.txt"), "package main\n")
				writeFile(t, filepath.Join(dir, "head", "main.go.txt"), "package main\n")
				writeFile(t, filepath.Join(dir, "expected.json"), `{"clean": true, "findings": [], "typo": 1}`)
			},
		},
		{
			name: "non-clean with zero findings",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "base", "main.go.txt"), "package main\n")
				writeFile(t, filepath.Join(dir, "head", "main.go.txt"), "package main\n")
				writeFile(t, filepath.Join(dir, "expected.json"), `{"clean": false, "findings": []}`)
			},
		},
		{
			name: "clean with findings",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "base", "main.go.txt"), "package main\n")
				writeFile(t, filepath.Join(dir, "head", "main.go.txt"), "package main\n")
				writeFile(t, filepath.Join(dir, "expected.json"), `{"clean": true, "findings": [{"file": "main.go", "line": 1, "severity": "WARNING", "keywords": ["x"]}]}`)
			},
		},
		{
			name: "finding without file",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "base", "main.go.txt"), "package main\n")
				writeFile(t, filepath.Join(dir, "head", "main.go.txt"), "package main\n")
				writeFile(t, filepath.Join(dir, "expected.json"), `{"clean": false, "findings": [{"line": 1, "severity": "WARNING", "keywords": ["x"]}]}`)
			},
		},
		{
			name: "finding without keywords",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "base", "main.go.txt"), "package main\n")
				writeFile(t, filepath.Join(dir, "head", "main.go.txt"), "package main\n")
				writeFile(t, filepath.Join(dir, "expected.json"), `{"clean": false, "findings": [{"file": "main.go", "line": 1, "severity": "WARNING"}]}`)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "case")
			tt.setup(t, dir)
			if _, err := LoadCase(dir); err == nil {
				t.Fatalf("LoadCase(%s) = nil error, want error", tt.name)
			}
		})
	}
}

// TestShippedCorpusValid loads the real corpus that ships with the repo,
// asserting it is well-formed. It runs in normal CI (no model calls) so a
// malformed case or a typo'd expected.json is caught before `make eval` ever
// spends tokens.
func TestShippedCorpusValid(t *testing.T) {
	cases, err := LoadCorpus("corpus")
	if err != nil {
		t.Fatalf("shipped corpus does not load: %v", err)
	}
	if len(cases) < 6 {
		t.Fatalf("shipped corpus has %d cases, want at least 6", len(cases))
	}
	clean := 0
	for _, c := range cases {
		if c.Expected.Clean {
			clean++
		}
	}
	if clean != 1 {
		t.Errorf("shipped corpus has %d clean cases, want exactly 1", clean)
	}
}

func TestLoadCorpus(t *testing.T) {
	t.Run("loads and sorts cases", func(t *testing.T) {
		root := t.TempDir()
		writeValidCase(t, root, "zebra")
		writeValidCase(t, root, "alpha")
		// A stray non-directory entry must be ignored, not loaded as a case.
		writeFile(t, filepath.Join(root, "README.md"), "ignore me\n")

		cases, err := LoadCorpus(root)
		if err != nil {
			t.Fatalf("LoadCorpus: %v", err)
		}
		if len(cases) != 2 {
			t.Fatalf("cases = %d, want 2", len(cases))
		}
		if cases[0].Name != "alpha" || cases[1].Name != "zebra" {
			t.Errorf("order = [%s %s], want [alpha zebra]", cases[0].Name, cases[1].Name)
		}
	})

	t.Run("empty corpus errors", func(t *testing.T) {
		if _, err := LoadCorpus(t.TempDir()); err == nil {
			t.Fatal("LoadCorpus(empty) = nil error, want error")
		}
	})

	t.Run("missing corpus dir errors", func(t *testing.T) {
		if _, err := LoadCorpus(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
			t.Fatal("LoadCorpus(missing) = nil error, want error")
		}
	})

	t.Run("one bad case fails the whole load", func(t *testing.T) {
		root := t.TempDir()
		writeValidCase(t, root, "good")
		bad := filepath.Join(root, "bad")
		writeFile(t, filepath.Join(bad, "head", "main.go.txt"), "package main\n")
		writeFile(t, filepath.Join(bad, "expected.json"), `{"clean": true, "findings": []}`)

		if _, err := LoadCorpus(root); err == nil {
			t.Fatal("LoadCorpus with a bad case = nil error, want error")
		}
	})
}

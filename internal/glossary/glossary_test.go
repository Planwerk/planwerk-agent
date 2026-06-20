package glossary

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile writes content to <dir>/<rel>, creating parent directories.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating dir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", rel, err)
	}
}

func TestLoad(t *testing.T) {
	const rootBody = "# Billing\n\n## Language\n\n**Invoice**: a billed statement.\n"

	tests := []struct {
		name       string
		setup      func(t *testing.T, dir string)
		wantNil    bool
		wantSource string
		wantBody   string
	}{
		{
			name:    "absent repo returns nil",
			setup:   func(*testing.T, string) {},
			wantNil: true,
		},
		{
			name: "root CONTEXT.md is loaded and trimmed",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "CONTEXT.md", "\n\n"+rootBody+"\n\n")
			},
			wantSource: "CONTEXT.md",
			wantBody:   strings.TrimSpace(rootBody),
		},
		{
			name: "secondary .planwerk/context.md is loaded when root is absent",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, filepath.Join(".planwerk", "context.md"), rootBody)
			},
			wantSource: filepath.Join(".planwerk", "context.md"),
			wantBody:   strings.TrimSpace(rootBody),
		},
		{
			name: "root CONTEXT.md wins over .planwerk/context.md",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "CONTEXT.md", "# Root\n")
				writeFile(t, dir, filepath.Join(".planwerk", "context.md"), "# Secondary\n")
			},
			wantSource: "CONTEXT.md",
			wantBody:   "# Root",
		},
		{
			name: "oversized file is skipped and treated as absent",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "CONTEXT.md", strings.Repeat("a", maxGlossaryBytes+1))
			},
			wantNil: true,
		},
		{
			name: "whitespace-only file is treated as absent",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "CONTEXT.md", "   \n\t\n  ")
			},
			wantNil: true,
		},
		{
			name: "symlink at the glossary path is not followed",
			setup: func(t *testing.T, dir string) {
				// A real target with valid content sits outside the probed path;
				// the symlink must NOT be followed, so Load treats it as absent.
				target := filepath.Join(dir, "elsewhere.md")
				if err := os.WriteFile(target, []byte(rootBody), 0o644); err != nil {
					t.Fatalf("writing symlink target: %v", err)
				}
				if err := os.Symlink(target, filepath.Join(dir, "CONTEXT.md")); err != nil {
					t.Fatalf("creating symlink: %v", err)
				}
			},
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(t, dir)

			g, err := Load(dir)
			if err != nil {
				t.Fatalf("Load returned error: %v", err)
			}
			if tc.wantNil {
				if g != nil {
					t.Fatalf("expected nil glossary, got %+v", g)
				}
				return
			}
			if g == nil {
				t.Fatal("expected a glossary, got nil")
			}
			if g.Source != tc.wantSource {
				t.Errorf("Source = %q, want %q", g.Source, tc.wantSource)
			}
			if g.Body != tc.wantBody {
				t.Errorf("Body = %q, want %q", g.Body, tc.wantBody)
			}
		})
	}
}

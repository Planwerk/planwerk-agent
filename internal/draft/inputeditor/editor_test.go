package inputeditor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveEditor_Precedence(t *testing.T) {
	tests := []struct {
		name   string
		visual string
		editor string
		want   string
	}{
		{"visual wins over editor", "myvisual", "myeditor", "myvisual"},
		{"editor when visual unset", "", "myeditor", "myeditor"},
		{"vi fallback when neither set", "", "", "vi"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("VISUAL", tt.visual)
			t.Setenv("EDITOR", tt.editor)
			if got := resolveEditor(); got != tt.want {
				t.Errorf("resolveEditor() = %q, want %q", got, tt.want)
			}
		})
	}
}

// writeFakeEditor writes an executable shell script to a temp file and returns
// its path. The script body receives the file to edit as $1.
func writeFakeEditor(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-editor.sh")
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("writing fake editor: %v", err)
	}
	return path
}

func TestEditorHandoff_RoundTrip(t *testing.T) {
	editor := writeFakeEditor(t, `printf 'edited by fake editor\n' > "$1"`)
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", editor)

	path, err := writeDraftTemp("seed buffer")
	if err != nil {
		t.Fatalf("writeDraftTemp: %v", err)
	}
	if err := editorCommand(path).Run(); err != nil {
		t.Fatalf("running fake editor: %v", err)
	}
	got, err := readDraftTemp(path)
	if err != nil {
		t.Fatalf("readDraftTemp: %v", err)
	}
	if want := "edited by fake editor\n"; got != want {
		t.Errorf("round-trip content = %q, want %q", got, want)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("temp file %s should be removed after readDraftTemp", path)
	}
}

func TestEditorHandoff_SeedsTempWithCurrentBuffer(t *testing.T) {
	// The fake editor appends a marker so we can prove it saw the seeded buffer.
	editor := writeFakeEditor(t, `printf ' + appended' >> "$1"`)
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", editor)

	path, err := writeDraftTemp("original idea")
	if err != nil {
		t.Fatalf("writeDraftTemp: %v", err)
	}
	if err := editorCommand(path).Run(); err != nil {
		t.Fatalf("running fake editor: %v", err)
	}
	got, err := readDraftTemp(path)
	if err != nil {
		t.Fatalf("readDraftTemp: %v", err)
	}
	if want := "original idea + appended"; got != want {
		t.Errorf("seeded round-trip = %q, want %q", got, want)
	}
}

func TestEditorHandoff_NonZeroExit(t *testing.T) {
	editor := writeFakeEditor(t, "exit 1")
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", editor)

	path, err := writeDraftTemp("seed buffer")
	if err != nil {
		t.Fatalf("writeDraftTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	if err := editorCommand(path).Run(); err == nil {
		t.Fatal("expected a non-nil error when the editor exits non-zero")
	}
}

package inputeditor

import (
	"fmt"
	"os"
)

// resolveEditor returns the command to launch for the $EDITOR escape hatch.
// It honors the same precedence git uses: $VISUAL, then $EDITOR, then a "vi"
// fallback when neither is set.
func resolveEditor() string {
	if v := os.Getenv("VISUAL"); v != "" {
		return v
	}
	if e := os.Getenv("EDITOR"); e != "" {
		return e
	}
	return "vi"
}

// writeDraftTemp writes content to a fresh temporary Markdown file and returns
// its path. The caller is responsible for running the editor on it and calling
// readDraftTemp, which removes the file.
func writeDraftTemp(content string) (string, error) {
	f, err := os.CreateTemp("", "draft-*.md")
	if err != nil {
		return "", fmt.Errorf("creating editor temp file: %w", err)
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("seeding editor temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("closing editor temp file: %w", err)
	}
	return f.Name(), nil
}

// readDraftTemp reads the editor temp file at path and removes it. It always
// removes the file, even on a read error, so a handoff never leaks temp files.
func readDraftTemp(path string) (string, error) {
	defer func() { _ = os.Remove(path) }()
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading edited draft: %w", err)
	}
	return string(data), nil
}

package brain

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// captureLogs routes the default logger into a buffer for the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &logBuf
}

// writeTree writes files (slash paths relative to root) with their content.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDiscoverDecisionDocs(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"docs/adr/0001-x.md":                   "x",
		"docs/Decisions/a.md":                  "a",
		"docs/explanation/design-decisions.md": "d",
		"docs/guide.md":                        "g",
		"docs/adr/notes.txt":                   "n",
		".git/decisions.md":                    "git",
		"elsewhere/target.md":                  "t",
	})
	if err := os.Symlink(filepath.Join(root, "elsewhere", "target.md"), filepath.Join(root, "docs", "decisions.md")); err != nil {
		t.Fatal(err)
	}

	got, err := discoverDecisionDocs(root)
	if err != nil {
		t.Fatalf("discoverDecisionDocs: %v", err)
	}
	want := []string{"docs/Decisions/a.md", "docs/adr/0001-x.md", "docs/explanation/design-decisions.md"}
	if !slices.Equal(got, want) {
		t.Errorf("discovered %v\nwant       %v", got, want)
	}
}

func TestDiscoverDecisionDocs_NoMatch(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"README.md": "r", "docs/guide.md": "g"})

	paths, err := resolveDecisionDocs(root, nil, false)
	if err != nil {
		t.Fatalf("resolveDecisionDocs: %v", err)
	}
	if chunks := loadDocChunks(root, paths); len(chunks) != 0 {
		t.Errorf("chunks = %+v, want none", chunks)
	}
}

func TestResolveDecisionDocs(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeTree(t, root, map[string]string{"docs/adr/0001-x.md": "x", "notes/why.md": "w"})
	writeTree(t, outside, map[string]string{"secret.md": "s"})
	if err := os.Symlink(filepath.Join(root, "notes", "why.md"), filepath.Join(root, "link.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}

	t.Run("explicit paths replace discovery", func(t *testing.T) {
		got, err := resolveDecisionDocs(root, []string{"notes/why.md"}, false)
		if err != nil || !slices.Equal(got, []string{"notes/why.md"}) {
			t.Errorf("resolveDecisionDocs = %v, %v, want [notes/why.md]", got, err)
		}
	})

	t.Run("none reads no document", func(t *testing.T) {
		got, err := resolveDecisionDocs(root, nil, true)
		if err != nil || len(got) != 0 {
			t.Errorf("resolveDecisionDocs = %v, %v, want none", got, err)
		}
	})

	for _, p := range []string{
		"missing.md",
		"../x.md",
		filepath.Join(root, "notes", "why.md"), // absolute
		"notes/../notes/why.md",                // not canonical
		"docs/adr",                             // a directory
		"link.md",                              // a symlink
		"out/secret.md",                        // leaves the checkout through a symlinked directory
		"",
	} {
		t.Run("rejects "+p, func(t *testing.T) {
			_, err := resolveDecisionDocs(root, []string{p}, false)
			want := `decision document "` + p + `" not found in the repository`
			if err == nil || err.Error() != want {
				t.Errorf("err = %v, want %q", err, want)
			}
		})
	}
}

func TestChunkDocument(t *testing.T) {
	t.Parallel()

	t.Run("a 40 KiB document of short lines yields three chunks", func(t *testing.T) {
		t.Parallel()
		line := strings.Repeat("x", 63) + "\n"
		text := strings.Repeat(line, 40<<10/len(line))
		chunks := chunkDocument("docs/adr/big.md", text)
		if len(chunks) != 3 {
			t.Fatalf("got %d chunks, want 3", len(chunks))
		}
		var joined strings.Builder
		for i, c := range chunks {
			if len(c.Text) > docChunkBytes {
				t.Errorf("chunk %d is %d bytes, above the cap of %d", i+1, len(c.Text), docChunkBytes)
			}
			if c.Path != "docs/adr/big.md" || c.Index != i+1 || c.Count != 3 {
				t.Errorf("chunk %d = path %q index %d count %d", i+1, c.Path, c.Index, c.Count)
			}
			joined.WriteString(c.Text)
		}
		if joined.String() != text {
			t.Error("the chunks do not concatenate to the document")
		}
	})

	t.Run("a single 20 KiB line is one chunk", func(t *testing.T) {
		t.Parallel()
		text := strings.Repeat("x", 20<<10)
		chunks := chunkDocument("d.md", text)
		if len(chunks) != 1 || chunks[0].Text != text {
			t.Errorf("got %d chunks, want the line as one chunk", len(chunks))
		}
	})

	t.Run("an overlong line between short ones stands alone", func(t *testing.T) {
		t.Parallel()
		long := strings.Repeat("x", 20<<10) + "\n"
		chunks := chunkDocument("d.md", "before\n"+long+"after\n")
		if len(chunks) != 3 || chunks[1].Text != long {
			t.Errorf("got %d chunks, want the long line as the second of three", len(chunks))
		}
	})

	t.Run("an empty document yields no chunk", func(t *testing.T) {
		t.Parallel()
		if chunks := chunkDocument("d.md", ""); len(chunks) != 0 {
			t.Errorf("chunks = %+v, want none", chunks)
		}
	})

	t.Run("appending a line changes the last chunk's key only", func(t *testing.T) {
		t.Parallel()
		line := strings.Repeat("x", 63) + "\n"
		text := strings.Repeat(line, 40<<10/len(line))
		before := chunkDocument("d.md", text)
		after := chunkDocument("d.md", text+"one more decision\n")
		if len(before) != 3 || len(after) != 3 {
			t.Fatalf("got %d and %d chunks, want 3 and 3", len(before), len(after))
		}
		if before[0].key() != after[0].key() || before[1].key() != after[1].key() {
			t.Error("an earlier chunk's key changed")
		}
		if before[2].key() == after[2].key() {
			t.Error("the last chunk's key did not change")
		}
		if want := "doc-d.md@"; !strings.HasPrefix(after[2].key(), want) || len(after[2].key()) != len(want)+12 {
			t.Errorf("key = %q, want %q and 12 hex characters", after[2].key(), want)
		}
	})
}

func TestLoadDocChunks_SkipsWithAWarning(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"docs/adr/ok.md":      "kept\n",
		"docs/adr/huge.md":    strings.Repeat("x", maxDecisionDocBytes+1),
		"docs/adr/a--b.md":    "dashes\n",
		"docs/adr/a space.md": "space\n",
		"docs/adr/\x1b[2J.md": "escape\n",
	})
	logs := captureLogs(t)

	chunks := loadDocChunks(root, []string{"docs/adr/a space.md", "docs/adr/a--b.md", "docs/adr/\x1b[2J.md", "docs/adr/gone.md", "docs/adr/huge.md", "docs/adr/ok.md"})
	if len(chunks) != 1 || chunks[0].Path != "docs/adr/ok.md" || chunks[0].Text != "kept\n" {
		t.Errorf("chunks = %+v, want only docs/adr/ok.md", chunks)
	}
	// A path that failed the check is logged quoted, so a control character in
	// it reaches the log as its escape; the text handler quotes that once more.
	for _, want := range []string{
		`skipping oversized decision document" path=docs/adr/huge.md`,
		`path="\"docs/adr/a--b.md\""`,
		`path="\"docs/adr/a space.md\""`,
		`path="\"docs/adr/\\x1b[2J.md\""`,
		`skipping unreadable decision document" path=docs/adr/gone.md`,
	} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, logs.String())
		}
	}
	if n := strings.Count(logs.String(), "level=WARN"); n != 5 {
		t.Errorf("got %d warnings, want 5:\n%s", n, logs.String())
	}
}

// FuzzChunkDocument checks the two properties the chunking promises for any
// text: the chunks concatenate to the text, and a chunk above the cap is one
// line.
func FuzzChunkDocument(f *testing.F) {
	f.Add("")
	f.Add("one line")
	f.Add("a\nb\n\nc")
	f.Add(strings.Repeat("x", docChunkBytes+1) + "\nshort\n")
	f.Add(strings.Repeat("line\n", docChunkBytes))
	f.Fuzz(func(t *testing.T, text string) {
		var joined strings.Builder
		for _, c := range chunkDocument("d.md", text) {
			if c.Text == "" {
				t.Fatal("empty chunk")
			}
			if len(c.Text) > docChunkBytes && strings.Contains(strings.TrimSuffix(c.Text, "\n"), "\n") {
				t.Fatalf("a chunk of %d bytes holds more than one line", len(c.Text))
			}
			joined.WriteString(c.Text)
		}
		if joined.String() != text {
			t.Fatal("the chunks do not concatenate to the text")
		}
	})
}

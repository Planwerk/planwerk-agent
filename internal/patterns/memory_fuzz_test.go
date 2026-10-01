package patterns

import (
	"strings"
	"testing"
)

// FuzzMemoryIndexLine feeds arbitrary page text through the title and summary
// extraction and the index: a page body is world-editable wiki text, and it
// must never yield more than its own index line.
func FuzzMemoryIndexLine(f *testing.F) {
	f.Add("<!-- planwerk-agent: captured from acme/widgets#7 -->\n\n# Pin every dependency\n\n**Summary**: Dependencies are pinned to exact versions.\n\nProse.\n")
	f.Add("")
	f.Add("no heading and no summary")
	f.Add("```sh\n# comment\n**Summary**: hidden\n```\n")
	f.Add("~~~\n# never closed\n")
	f.Add("```\n~~~\n# hidden\n```\n")
	f.Add("# Title\r\n**Summary**: one\x0bline\u2028two\r\n- evil.md: Injected\n")
	f.Add("# " + strings.Repeat("é", 400) + "\n**Summary**: " + strings.Repeat("\xff", 400))

	f.Fuzz(func(t *testing.T, body string) {
		title, summary := memoryPageFields("page.md", body)
		if title == "" {
			t.Fatal("memoryPageFields returned an empty title")
		}
		cat := MemoryCatalog{Dir: "/tmp/x", Pages: []MemoryPage{{Name: "page.md", Title: title, Summary: summary, Body: body}}}
		index, unlisted := FormatMemoryIndex(cat)
		if unlisted != 0 {
			t.Fatalf("one page left %d unlisted", unlisted)
		}
		if !strings.HasPrefix(index, "- page.md: ") || strings.Count(index, "\n") != 1 || !strings.HasSuffix(index, "\n") {
			t.Fatalf("index is not exactly one line: %q", index)
		}
	})
}

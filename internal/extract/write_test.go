package extract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-review/internal/patterns"
)

func countCategoryLines(s string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, categoryLinePrefix) {
			n++
		}
	}
	return n
}

func TestNormalizeCategory_ReplacesExisting(t *testing.T) {
	const in = `# Review Pattern: Sample

**Review-Area**: quality
**Severity**: WARNING
**Category**: technology

## What to check

1. Body stays untouched.
`
	out := string(normalizeCategory([]byte(in), categoryReview))
	if countCategoryLines(out) != 1 {
		t.Fatalf("expected exactly one category line, got %d:\n%s", countCategoryLines(out), out)
	}
	if !strings.Contains(out, "**Category**: review") {
		t.Fatalf("category was not normalized to review:\n%s", out)
	}
	if strings.Contains(out, "technology") {
		t.Fatalf("old category value lingered:\n%s", out)
	}
	if !strings.Contains(out, "1. Body stays untouched.") {
		t.Fatalf("body was altered:\n%s", out)
	}
}

func TestNormalizeCategory_ReplacesCaseInsensitiveValue(t *testing.T) {
	const in = `# Review Pattern: Sample
**Severity**: INFO
**Category**: Technology

## What to check
`
	out := string(normalizeCategory([]byte(in), categoryReview))
	if !strings.Contains(out, "**Category**: review") || strings.Contains(out, "Technology") {
		t.Fatalf("a capitalized value was not normalized:\n%s", out)
	}
}

func TestNormalizeCategory_InsertsWhenAbsent(t *testing.T) {
	const in = `# Review Pattern: Sample

**Review-Area**: quality
**Severity**: WARNING

## What to check

1. Step.
`
	out := string(normalizeCategory([]byte(in), categoryReview))
	if countCategoryLines(out) != 1 {
		t.Fatalf("expected one inserted category line, got %d:\n%s", countCategoryLines(out), out)
	}
	// The inserted line must sit before the body so Parse reads it as
	// frontmatter, and the result must round-trip through the parser.
	p, err := patterns.Parse(out)
	if err != nil {
		t.Fatalf("normalized pattern does not parse: %v\n%s", err, out)
	}
	if p.Category != categoryReview {
		t.Fatalf("parsed category = %q, want %q", p.Category, categoryReview)
	}
}

func TestNormalizeCategory_LeavesBodyExampleUntouched(t *testing.T) {
	// A meta-pattern whose body documents frontmatter and declares no frontmatter
	// category: a "**Category**: technology" example sits inside a fenced code
	// block in the body. The unbounded scan would rewrite that body example as if
	// it were the header; the bounded scan must leave it alone and insert a real
	// frontmatter category instead.
	const in = "# Review Pattern: Meta\n" +
		"**Severity**: INFO\n" +
		"\n" +
		"## What to check\n" +
		"\n" +
		"Frontmatter looks like:\n" +
		"\n" +
		"```\n" +
		"**Category**: technology\n" +
		"```\n"

	out := string(normalizeCategory([]byte(in), categoryReview))
	// One inserted frontmatter line plus the preserved body example = two.
	if countCategoryLines(out) != 2 {
		t.Fatalf("expected an inserted frontmatter line and the body example kept, got %d category lines:\n%s", countCategoryLines(out), out)
	}
	if !strings.Contains(out, "**Severity**: INFO\n**Category**: review\n") {
		t.Fatalf("frontmatter category was not inserted before the body:\n%s", out)
	}
	if !strings.Contains(out, "```\n**Category**: technology\n```") {
		t.Fatalf("body example was rewritten but must stay untouched:\n%s", out)
	}
}

func TestNormalizeCategory_PreservesCRLFLineEndings(t *testing.T) {
	const in = "# Review Pattern: Sample\r\n" +
		"**Severity**: INFO\r\n" +
		"**Category**: technology\r\n" +
		"\r\n" +
		"## What to check\r\n"

	out := string(normalizeCategory([]byte(in), categoryReview))
	if !strings.Contains(out, "**Category**: review\r\n") {
		t.Fatalf("CRLF category line was not normalized with its line ending:\n%q", out)
	}
	// No line ending may be left as a lone LF (mixed endings) on a CRLF file.
	if strings.Contains(strings.ReplaceAll(out, "\r\n", ""), "\n") {
		t.Fatalf("CRLF file gained a lone LF (mixed line endings):\n%q", out)
	}
}

func TestWriteWorkingTree_WritesVerbatim(t *testing.T) {
	base := t.TempDir()
	entries := []entry{{Stem: "sample-one", Raw: []byte(samplePattern)}}

	written, err := writeWorkingTree(base, planwerkPatternsSubdir, entries, nil, false)
	if err != nil {
		t.Fatalf("writeWorkingTree: %v", err)
	}
	if len(written) != 1 || written[0].Path != ".planwerk/review_patterns/sample-one.md" || written[0].Replaced {
		t.Fatalf("unexpected written results: %+v", written)
	}
	got, err := os.ReadFile(filepath.Join(base, ".planwerk", "review_patterns", "sample-one.md"))
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if string(got) != samplePattern {
		t.Fatalf("file was not written verbatim:\n%s", got)
	}
}

func TestWriteWorkingTree_AppliesTransformAndRoundTrips(t *testing.T) {
	base := t.TempDir()
	entries := []entry{{Stem: "sample-one", Raw: []byte(samplePattern)}}
	transform := func(raw []byte) []byte { return normalizeCategory(raw, categoryReview) }

	written, err := writeWorkingTree(base, catalogReviewSubdir, entries, transform, false)
	if err != nil {
		t.Fatalf("writeWorkingTree: %v", err)
	}
	if want := "internal/patterns/patterns/review/sample-one.md"; written[0].Path != want {
		t.Fatalf("written path = %q, want %q", written[0].Path, want)
	}
	got, err := os.ReadFile(filepath.Join(base, catalogReviewSubdir, "sample-one.md"))
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	p, err := patterns.Parse(string(got))
	if err != nil {
		t.Fatalf("catalog file does not parse: %v", err)
	}
	if p.Category != categoryReview {
		t.Fatalf("catalog category = %q, want %q", p.Category, categoryReview)
	}
}

func TestWriteWorkingTree_RefusesOverwriteWithoutFlag(t *testing.T) {
	base := t.TempDir()
	entries := []entry{{Stem: "sample-one", Raw: []byte("wiki bytes\n")}}

	// Seed a trusted file the wiki stem collides with.
	dir := filepath.Join(base, planwerkPatternsSubdir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("seeding dir: %v", err)
	}
	const trusted = "trusted bytes\n"
	dst := filepath.Join(dir, "sample-one.md")
	if err := os.WriteFile(dst, []byte(trusted), 0o600); err != nil {
		t.Fatalf("seeding file: %v", err)
	}

	_, err := writeWorkingTree(base, planwerkPatternsSubdir, entries, nil, false)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected an overwrite-refusal error, got %v", err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != trusted {
		t.Fatalf("collision must not clobber the trusted file, got:\n%s", got)
	}
}

func TestWriteWorkingTree_OverwriteReplacesAndFlagsResult(t *testing.T) {
	base := t.TempDir()
	entries := []entry{{Stem: "sample-one", Raw: []byte("wiki bytes\n")}}

	dir := filepath.Join(base, planwerkPatternsSubdir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("seeding dir: %v", err)
	}
	dst := filepath.Join(dir, "sample-one.md")
	if err := os.WriteFile(dst, []byte("old bytes\n"), 0o600); err != nil {
		t.Fatalf("seeding file: %v", err)
	}

	written, err := writeWorkingTree(base, planwerkPatternsSubdir, entries, nil, true)
	if err != nil {
		t.Fatalf("writeWorkingTree with overwrite: %v", err)
	}
	if len(written) != 1 || !written[0].Replaced {
		t.Fatalf("expected the result flagged as replaced, got %+v", written)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "wiki bytes\n" {
		t.Fatalf("--overwrite must replace the file, got:\n%s", got)
	}
}

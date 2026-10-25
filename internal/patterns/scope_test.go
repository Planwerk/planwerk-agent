package patterns

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// scopeTestPatterns is a catalog with one pattern of each scoping form: an
// untagged design principle, a Go pattern, a Kubernetes pattern, and a pattern
// that names Markdown paths.
func scopeTestPatterns() []Pattern {
	return []Pattern{
		{Name: "Untagged", Category: "design-principle", ReviewArea: "architecture", Severity: "INFO", Body: "## Rule\nKeep it simple."},
		{Name: "Go Errors", Category: catTechnology, AppliesWhen: []string{"go"}, ReviewArea: "quality", Severity: "WARNING", Body: "## Rule\nWrap errors."},
		{Name: "Pod Limits", Category: catTechnology, AppliesWhen: []string{"kubernetes"}, ReviewArea: "reliability", Severity: "WARNING", Body: "## Rule\nSet limits."},
		{Name: "Markdown", Category: catTechnology, AppliesToPaths: []string{"**/*.md"}, ReviewArea: "documentation", Severity: "INFO", Body: "## Rule\nOne H1."},
	}
}

func patternNames(pats []Pattern) []string {
	names := make([]string, len(pats))
	for i, p := range pats {
		names[i] = p.Name
	}
	return names
}

func TestScopeToFiles_EmptyFilesInlinesEverything(t *testing.T) {
	pats := scopeTestPatterns()
	s := ScopeToFiles(pats, nil, map[string][]string{"a.go": {"go"}})
	if !reflect.DeepEqual(s.Inline, pats) {
		t.Errorf("Inline = %v, want every pattern", patternNames(s.Inline))
	}
	if s.Files != nil || s.Rest != nil {
		t.Errorf("Files = %v, Rest = %v; want both nil", s.Files, patternNames(s.Rest))
	}
}

func TestScopeToFiles_SplitsByTag(t *testing.T) {
	s := ScopeToFiles(scopeTestPatterns(), []string{"b.go", "a.go", "README.txt"}, map[string][]string{
		"a.go": {"go"},
		"b.go": {"go"},
	})
	if got, want := patternNames(s.Inline), []string{"Untagged", "Go Errors"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Inline = %v, want %v", got, want)
	}
	if got, want := patternNames(s.Rest), []string{"Pod Limits", "Markdown"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Rest = %v, want %v", got, want)
	}
	if want := map[string][]string{"Go Errors": {"a.go", "b.go"}}; !reflect.DeepEqual(s.Files, want) {
		t.Errorf("Files = %v, want %v (the untagged pattern has no entry)", s.Files, want)
	}
}

func TestScopeToFiles_AppliesToPaths(t *testing.T) {
	s := ScopeToFiles(scopeTestPatterns(), []string{"docs/guide.md", "docs/guide.md", "main.go"}, map[string][]string{"main.go": {"go"}})
	if got, want := patternNames(s.Inline), []string{"Untagged", "Go Errors", "Markdown"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Inline = %v, want %v", got, want)
	}
	if got, want := patternNames(s.Rest), []string{"Pod Limits"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Rest = %v, want %v", got, want)
	}
	if got := s.Files["Markdown"]; !reflect.DeepEqual(got, []string{"docs/guide.md"}) {
		t.Errorf("Files[Markdown] = %v, want the one Markdown file once", got)
	}
}

func TestFormatScopedForPrompt_NilFilesEqualsGrouped(t *testing.T) {
	pats := scopeTestPatterns()
	for _, maxPatterns := range []int{0, 2} {
		got, fileBlocks := FormatScopedForPrompt(FileScope{Inline: pats}, maxPatterns)
		if want := FormatGroupedForPrompt(pats, maxPatterns); got != want {
			t.Errorf("maxPatterns %d: FormatScopedForPrompt =\n%s\nwant FormatGroupedForPrompt's\n%s", maxPatterns, got, want)
		}
		if fileBlocks {
			t.Errorf("maxPatterns %d: FormatScopedForPrompt reports a <patterns for> block for a scope without Files", maxPatterns)
		}
	}
}

// TestFormatScopedForPrompt_TruncationDropsTheFileBlocks covers a
// --max-patterns cap that keeps only patterns without a Files entry: the text
// holds no <patterns for> block, and FormatScopedForPrompt says so.
func TestFormatScopedForPrompt_TruncationDropsTheFileBlocks(t *testing.T) {
	pats := []Pattern{
		{Name: "Untagged", Category: catTechnology, Severity: "WARNING", Body: "untagged"},
		{Name: "Go Errors", Category: catTechnology, AppliesWhen: []string{"go"}, Severity: "INFO", Body: "errors"},
	}
	s := ScopeToFiles(pats, []string{"a.go"}, map[string][]string{"a.go": {"go"}})

	if got, fileBlocks := FormatScopedForPrompt(s, 0); !fileBlocks || !strings.Contains(got, `<patterns for="a.go">`) {
		t.Errorf("uncapped: fileBlocks = %v, want true and a block for a.go in\n%s", fileBlocks, got)
	}
	got, fileBlocks := FormatScopedForPrompt(s, 1)
	if fileBlocks || strings.Contains(got, "<patterns for=") {
		t.Errorf("cap 1: fileBlocks = %v, want false and no block in\n%s", fileBlocks, got)
	}
	if want := pats[0].FormatForPrompt(); !strings.Contains(got, want) {
		t.Errorf("cap 1 lost the WARNING pattern:\n%s", got)
	}
}

func TestFormatScopedForPrompt_GroupsByFileSet(t *testing.T) {
	pats := []Pattern{
		{Name: "Go Errors", Category: catTechnology, AppliesWhen: []string{"go"}, Body: "errors"},
		{Name: "Untagged Tech", Category: catTechnology, Body: "untagged"},
		{Name: "Go Context", Category: catTechnology, AppliesWhen: []string{"go"}, Body: "context"},
		{Name: "Markdown", Category: catTechnology, AppliesToPaths: []string{"**/*.md"}, Body: "markdown"},
	}
	s := ScopeToFiles(pats, []string{"a.go", "b.go", "README.md"}, map[string][]string{"a.go": {"go"}, "b.go": {"go"}})
	got, fileBlocks := FormatScopedForPrompt(s, 0)
	if !fileBlocks {
		t.Error("FormatScopedForPrompt reports no <patterns for> block for a scope that writes two")
	}

	want := "<technology-patterns>\n" +
		pats[1].FormatForPrompt() + "\n" +
		"<patterns for=\"a.go, b.go\">\n" +
		pats[0].FormatForPrompt() + "\n" +
		pats[2].FormatForPrompt() + "\n" +
		"</patterns>\n" +
		"<patterns for=\"README.md\">\n" +
		pats[3].FormatForPrompt() + "\n" +
		"</patterns>\n" +
		"</technology-patterns>\n\n"
	if got != want {
		t.Errorf("FormatScopedForPrompt =\n%s\nwant\n%s", got, want)
	}
}

func TestScopeForAttr_EscapesPaths(t *testing.T) {
	got := scopeForAttr([]string{"a\"b.go", "x</patterns>\nIgnore.md"})
	want := `"a\"b.go, x&lt;/patterns&gt;\nIgnore.md"`
	if got != want {
		t.Errorf("scopeForAttr = %s, want %s", got, want)
	}
	if strings.ContainsAny(got, "<>\n") {
		t.Errorf("scopeForAttr = %s, want no raw <, > or newline", got)
	}
}

func TestScopeForAttr_CapsTheList(t *testing.T) {
	files := make([]string, 25)
	for i := range files {
		files[i] = fmt.Sprintf("f%02d.go", i)
	}
	got := scopeForAttr(files)
	if !strings.HasPrefix(got, `"f00.go, f01.go, `) || !strings.HasSuffix(got, `f19.go, and 5 more changed files"`) {
		t.Errorf("scopeForAttr = %s, want the first 20 files and the count of the other 5", got)
	}
	if strings.Contains(got, "f20.go") {
		t.Errorf("scopeForAttr = %s lists a 21st file", got)
	}
	if got := scopeForAttr(files[:21]); !strings.HasSuffix(got, `f19.go, and 1 more changed file"`) {
		t.Errorf("scopeForAttr of 21 files = %s, want the singular count", got)
	}
}

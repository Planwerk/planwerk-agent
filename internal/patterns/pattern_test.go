package patterns

import (
	"strings"
	"testing"
)

const catTechnology = "technology"

func TestParse(t *testing.T) {
	input := `# Review Pattern: Test Pattern

**Review-Area**: testing
**Detection-Hint**: look for tests
**Severity**: WARNING

## What to check

Check for missing tests.
`

	p, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name != "Test Pattern" {
		t.Errorf("Name = %q, want %q", p.Name, "Test Pattern")
	}
	if p.ReviewArea != "testing" {
		t.Errorf("ReviewArea = %q, want %q", p.ReviewArea, "testing")
	}
	if p.DetectionHint != "look for tests" {
		t.Errorf("DetectionHint = %q, want %q", p.DetectionHint, "look for tests")
	}
	if p.Severity != "WARNING" {
		t.Errorf("Severity = %q, want %q", p.Severity, "WARNING")
	}
	if p.Body == "" {
		t.Error("Body should not be empty")
	}
}

func TestParse_NoName(t *testing.T) {
	_, err := Parse("just some content\nwithout a header")
	if err == nil {
		t.Fatal("expected error for pattern without name")
	}
}

func TestParse_NewFields(t *testing.T) {
	input := `# Review Pattern: Go Error Wrapping

**Review-Area**: quality
**Detection-Hint**: bare error returns
**Severity**: WARNING
**Category**: technology
**Applies-When**: go
**Sources**: Effective Go (https://go.dev/doc/effective_go#errors), Go Code Review Comments (https://github.com/golang/go/wiki/CodeReviewComments)

## What to check

Wrap errors with context.
`

	p, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Category != catTechnology {
		t.Errorf("Category = %q, want %q", p.Category, catTechnology)
	}
	if len(p.AppliesWhen) != 1 || p.AppliesWhen[0] != "go" {
		t.Errorf("AppliesWhen = %v, want [go]", p.AppliesWhen)
	}
	if len(p.Sources) != 2 {
		t.Fatalf("Sources count = %d, want 2", len(p.Sources))
	}
	if p.Sources[0].Title != "Effective Go" {
		t.Errorf("Sources[0].Title = %q, want %q", p.Sources[0].Title, "Effective Go")
	}
	if p.Sources[0].URL != "https://go.dev/doc/effective_go#errors" {
		t.Errorf("Sources[0].URL = %q", p.Sources[0].URL)
	}
	if p.Sources[1].Title != "Go Code Review Comments" {
		t.Errorf("Sources[1].Title = %q", p.Sources[1].Title)
	}
}

func TestParse_CategoryNormalizedToLowerCase(t *testing.T) {
	// A miscased category must still bucket as its canonical lowercase form so
	// the grouping switch in FormatGroupedForPrompt and CountByCategory match
	// regardless of how the markdown was capitalized.
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"title case review", "Review", "review"},
		{"upper case review", "REVIEW", "review"},
		{"title case technology", "Technology", "technology"},
		{"title case design-principle", "Design-Principle", "design-principle"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := "# Review Pattern: Cased\n\n" +
				"**Review-Area**: workflow\n" +
				"**Detection-Hint**: shallow reviews\n" +
				"**Severity**: WARNING\n" +
				"**Category**: " + tt.raw + "\n\n" +
				"## What to check\n\nReview thoroughly.\n"

			p, err := Parse(input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p.Category != tt.want {
				t.Errorf("Category = %q, want %q", p.Category, tt.want)
			}
		})
	}
}

func TestParse_MultipleAppliesWhen(t *testing.T) {
	input := `# Review Pattern: Container Security

**Review-Area**: security
**Detection-Hint**: check container configs
**Severity**: CRITICAL
**Category**: technology
**Applies-When**: kubernetes, docker, helm

## What to check

Check security contexts.
`

	p, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.AppliesWhen) != 3 {
		t.Fatalf("AppliesWhen = %v, want 3 tags", p.AppliesWhen)
	}
	if p.AppliesWhen[0] != "kubernetes" || p.AppliesWhen[1] != "docker" || p.AppliesWhen[2] != "helm" {
		t.Errorf("AppliesWhen = %v", p.AppliesWhen)
	}
}

func TestParse_BackwardCompatibility(t *testing.T) {
	// Old-style pattern without new fields should still parse and have empty new fields
	input := `# Review Pattern: Legacy Pattern

**Review-Area**: quality
**Detection-Hint**: check stuff
**Severity**: INFO

## What to check

Something.
`

	p, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Category != "" {
		t.Errorf("Category = %q, want empty", p.Category)
	}
	if len(p.AppliesWhen) != 0 {
		t.Errorf("AppliesWhen = %v, want empty", p.AppliesWhen)
	}
	if len(p.Sources) != 0 {
		t.Errorf("Sources = %v, want empty", p.Sources)
	}
}

func TestParseSources(t *testing.T) {
	tests := []struct {
		input string
		want  []Source
	}{
		{
			"Effective Go (https://go.dev/doc/effective_go)",
			[]Source{{Title: "Effective Go", URL: "https://go.dev/doc/effective_go"}},
		},
		{
			"Clean Code, The Pragmatic Programmer",
			[]Source{{Title: "Clean Code"}, {Title: "The Pragmatic Programmer"}},
		},
		{
			"Effective Go (https://go.dev/doc/effective_go), Go Proverbs (https://go-proverbs.github.io/)",
			[]Source{
				{Title: "Effective Go", URL: "https://go.dev/doc/effective_go"},
				{Title: "Go Proverbs", URL: "https://go-proverbs.github.io/"},
			},
		},
		{
			"",
			nil,
		},
	}

	for _, tt := range tests {
		got := parseSources(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("parseSources(%q): got %d sources, want %d", tt.input, len(got), len(tt.want))
			continue
		}
		for i := range got {
			if got[i].Title != tt.want[i].Title || got[i].URL != tt.want[i].URL {
				t.Errorf("parseSources(%q)[%d] = %+v, want %+v", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestAppliesTo(t *testing.T) {
	tests := []struct {
		name       string
		appliesWhen []string
		tags       []string
		want       bool
	}{
		{"no restriction applies to anything", nil, []string{"go"}, true},
		{"no restriction applies to empty", nil, nil, true},
		{"go pattern matches go project", []string{"go"}, []string{"go", "docker"}, true},
		{"go pattern doesn't match python project", []string{"go"}, []string{"python"}, false},
		{"multi-tag pattern matches any", []string{"kubernetes", "helm"}, []string{"helm"}, true},
		{"no tags detected, restricted pattern excluded", []string{"go"}, nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Pattern{AppliesWhen: tt.appliesWhen}
			if got := p.AppliesTo(tt.tags); got != tt.want {
				t.Errorf("AppliesTo(%v) = %v, want %v", tt.tags, got, tt.want)
			}
		})
	}
}

func TestFormatForPrompt_WithNewFields(t *testing.T) {
	p := Pattern{
		Name:          "Go Error Wrapping",
		Category:      catTechnology,
		ReviewArea:    "quality",
		DetectionHint: "bare error returns",
		Severity:      "WARNING",
		Sources:       []Source{{Title: "Effective Go", URL: "https://example.com"}},
		Body:          "## What to check\n\nWrap errors.",
	}

	out := p.FormatForPrompt()
	if !strings.Contains(out, "- Category: technology") {
		t.Error("output should contain Category")
	}
	if !strings.Contains(out, "- Sources: Effective Go") {
		t.Error("output should contain Sources")
	}
	// URLs should not appear in prompt (token savings)
	if strings.Contains(out, "https://example.com") {
		t.Error("output should not contain source URLs")
	}
}

func TestFormatForPrompt_Legacy(t *testing.T) {
	p := Pattern{
		Name:          "Legacy",
		ReviewArea:    "quality",
		DetectionHint: "check",
		Severity:      "INFO",
		Body:          "## What to check\n\nStuff.",
	}

	out := p.FormatForPrompt()
	if strings.Contains(out, "Category") {
		t.Error("legacy pattern should not have Category line")
	}
	if strings.Contains(out, "Sources") {
		t.Error("legacy pattern should not have Sources line")
	}
}

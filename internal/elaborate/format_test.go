package elaborate

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// sharedFormatPath is the skill-side specification of the house issue format.
// The `elaborate` skill (plugins/planwerk/skills/elaborate) renders an issue
// body straight from this document, while BuildIssueBody renders it from Go.
// Both must agree, because `plan` and `implement` read whichever one produced
// the issue.
const sharedFormatPath = "../../plugins/planwerk/shared/issue-format.md"

// elaboratedDepthHeading opens the half of the shared spec that describes the
// elaborated body. The draft-depth half above it also shows `## Description` and
// `## Motivation` in its example, so the ordering assertions must be scoped
// below this heading or a reorder in the elaborated example would match the
// draft example's headings instead and pass.
const elaboratedDepthHeading = "## Depth 2 — elaborated"

// elaboratedSections is the ordered section contract. It is asserted against
// BuildIssueBody's output and against the shared format document, so a section
// renamed, reordered, or dropped on either side fails here rather than silently
// producing two issue formats.
var elaboratedSections = []string{
	"## Description",
	"## Motivation",
	"## User Stories",
	"## Affected Areas",
	"## Acceptance Criteria",
	"## Non-Goals",
	"## References",
}

// fullResult populates every section so BuildIssueBody emits all of them,
// including the optional User Stories block.
func fullResult() *Result {
	return &Result{
		Title:       "Add snapshot tests for prompt builders",
		Header:      "**Category**: feature | **Scope**: Medium",
		Description: "Lock the prompt surface with golden files.",
		Motivation:  "Prompt drift otherwise ships unreviewed.",
		UserStories: []Story{
			{Role: "maintainer", Want: "failing tests on prompt drift", SoThat: "mutations cannot ship silently", Criteria: []string{"A golden file exists for every builder"}},
		},
		AffectedAreas:      []string{"internal/claude (golden tests)"},
		AcceptanceCriteria: []string{"Run go test ./internal/claude and see a golden file per builder"},
		NonGoals:           []string{"Rewriting the prompts themselves — out of scope for this issue"},
		References:         []string{"internal/claude/prompts_golden_test.go"},
	}
}

// TestBuildIssueBody_MatchesSharedFormat is the drift guard between the Go
// renderer and the skill's format specification. `elaborate` exists as both a
// headless command and an interactive skill; nothing but this test stops the two
// from rendering different issue bodies.
func TestBuildIssueBody_MatchesSharedFormat(t *testing.T) {
	spec, err := os.ReadFile(filepath.Clean(sharedFormatPath))
	if err != nil {
		t.Fatalf("reading the shared issue-format spec: %v", err)
	}
	specText := string(spec)
	elaboratedSpec := specSectionFrom(t, specText, elaboratedDepthHeading)

	body := BuildIssueBody(fullResult())

	t.Run("the spec names every section the renderer emits, in order", func(t *testing.T) {
		assertOrderedSections(t, elaboratedSpec, elaboratedSections, "the elaborated-depth spec")
	})

	t.Run("the renderer emits every section the spec names, in order", func(t *testing.T) {
		assertOrderedSections(t, body, elaboratedSections, "BuildIssueBody output")
	})

	t.Run("the header line is preserved above the first section", func(t *testing.T) {
		if !strings.HasPrefix(body, "**Category**: feature | **Scope**: Medium\n\n## Description") {
			t.Errorf("body must open with the Category/Scope header line, then Description; got:\n%s", firstLines(body, 3))
		}
	})

	t.Run("acceptance criteria render as checkboxes", func(t *testing.T) {
		if !strings.Contains(body, "- [ ] Run go test") {
			t.Error("acceptance criteria must render as `- [ ]` checkboxes")
		}
		if !strings.Contains(specText, "- [ ]") {
			t.Error("the shared spec must document the `- [ ]` checkbox form")
		}
	})

	t.Run("the footer names the tool and the model", func(t *testing.T) {
		if !strings.Contains(body, "_Elaborated by [planwerk-agent](") {
			t.Error("body must end with the `Elaborated by [planwerk-agent](…)` footer")
		}
		if !strings.Contains(specText, "Elaborated by") {
			t.Error("the shared spec must document the `Elaborated by` footer verb")
		}
	})
}

// specSectionFrom returns the part of the spec at and below the given heading,
// so an ordering assertion cannot be satisfied by a heading that appears in an
// earlier, unrelated example.
func specSectionFrom(t *testing.T, spec, heading string) string {
	t.Helper()
	at := indexOfHeading(spec, heading)
	if at < 0 {
		t.Fatalf("the shared spec no longer carries the %q heading; update elaboratedDepthHeading", heading)
	}
	return spec[at:]
}

// assertOrderedSections fails when a heading is missing from text, or appears
// out of the documented order. It reports the first offender rather than the
// whole diff, since a single reorder cascades into every later section.
func assertOrderedSections(t *testing.T, text string, headings []string, what string) {
	t.Helper()
	prev := -1
	prevHeading := ""
	for _, h := range headings {
		at := indexOfHeading(text, h)
		if at < 0 {
			t.Errorf("%s is missing the %q section", what, h)
			continue
		}
		if at < prev {
			t.Errorf("%s has %q before %q; the documented order is %v", what, h, prevHeading, headings)
		}
		prev, prevHeading = at, h
	}
}

// indexOfHeading finds a Markdown heading at the start of a line, so a mention
// of "## Non-Goals" inside a fenced example does not count as the section
// itself appearing early. It returns -1 when the heading is absent.
func indexOfHeading(text, heading string) int {
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(heading) + `\s*$`)
	loc := re.FindStringIndex(text)
	if loc == nil {
		return -1
	}
	return loc[0]
}

// firstLines returns at most n leading lines of s, for a readable failure.
func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

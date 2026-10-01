package capture

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// updateGolden regenerates the golden files under testdata/. Run
// `go test ./internal/capture -update` after an intentional render change.
var updateGolden = flag.Bool("update", false, "regenerate capture render golden files")

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating golden dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s: %v (run `go test ./internal/capture -update` to generate)", path, err)
	}
	if got != string(want) {
		t.Errorf("%s differs from golden %s.\nRun `go test ./internal/capture -update` if intentional.\n\n--- want ---\n%s\n--- got ---\n%s", name, path, string(want), got)
	}
}

func goldenProvenance() Provenance {
	return Provenance{Repo: "planwerk/planwerk-agent", Issue: 138}
}

func goldenResult() CaptureResult {
	return CaptureResult{
		Model:      "claude-opus-5-5",
		WikiRepo:   "planwerk/planwerk-agent",
		WikiCommit: "abc1234def5678",
		Patterns: []ProposedPage{
			{
				Path:       "review_patterns/escape-untrusted-fences.md",
				Kind:       KindPattern,
				Title:      "Escape untrusted fences before injecting into prompts",
				Body:       "# Review Pattern: Escape untrusted fences\n\n**Review-Area**: security\n**Severity**: WARNING\n\n## What to check\n\nUntrusted bodies injected into a fenced prompt block must have the closing delimiter escaped.\n\n## Why it matters\n\nA crafted body can close the fence early and smuggle instructions to the model.",
				Rationale:  "The same fence-escaping fix recurred across the sync and capture prompt builders.",
				Confidence: "likely",
			},
		},
		Memory: []ProposedPage{
			{
				Path:      "memory/capture-is-propose-only.md",
				Kind:      KindMemory,
				Title:     "Capture is propose-only",
				Body:      "The capture pass authors candidate wiki pages but never pushes them; the gated write-back is a separate step.",
				Rationale: "Durable design decision drawn from the plan and the implementation report.",
				IsUpdate:  true,
			},
		},
	}
}

// TestRenderMarkdown_Populated locks the full report shape: both sections, the
// new/update labels, the rendered pages with their provenance markers, and the
// propose-only footer.
func TestRenderMarkdown_Populated(t *testing.T) {
	var buf bytes.Buffer
	NewRenderer(&buf).RenderMarkdown(goldenResult(), goldenProvenance(), "e1efd0d")
	assertGolden(t, "render_populated", buf.String())
}

// TestRenderMarkdown_Empty locks the "nothing new to propose" shape — the header
// still renders, with no sections and no footer noise.
func TestRenderMarkdown_Empty(t *testing.T) {
	var buf bytes.Buffer
	NewRenderer(&buf).RenderMarkdown(CaptureResult{Model: "claude-opus-5-5"}, goldenProvenance(), "e1efd0d")
	assertGolden(t, "render_empty", buf.String())
}

// TestRenderMarkdown_BodyWithInnerFenceIsNotCorrupted proves a model-authored
// body that itself contains a ```go code fence does not terminate the wrapper
// early: the wrapper grows to four backticks so the inner fence is carried
// verbatim instead of closing the block and spilling the rest as live markdown.
func TestRenderMarkdown_BodyWithInnerFenceIsNotCorrupted(t *testing.T) {
	result := CaptureResult{
		Model: "claude-opus-5-5",
		Patterns: []ProposedPage{
			{
				Path:  "review_patterns/fence-aware.md",
				Kind:  KindPattern,
				Title: "Fence-aware",
				Body:  "## Detection-Hint\n\n```go\nfmt.Fprintln(w, \"```\")\n```\n\n## What to check\n\nLook at the wrapper.",
			},
		},
	}
	var buf bytes.Buffer
	NewRenderer(&buf).RenderMarkdown(result, goldenProvenance(), "e1efd0d")
	got := buf.String()

	if !strings.Contains(got, "````markdown\n") {
		t.Errorf("expected a four-backtick wrapper around a body containing ```go, got:\n%s", got)
	}
	// The inner fence and everything after it must survive inside the wrapper —
	// a three-backtick wrapper would close at the first ```go and drop the rest.
	if !strings.Contains(got, "```go\n") || !strings.Contains(got, "## What to check") {
		t.Errorf("inner fence or trailing section was swallowed:\n%s", got)
	}
}

// TestRenderPage_PrependsMarkerAndPreservesPattern proves RenderPage prepends the
// stable provenance marker while leaving the "# Review Pattern:" header intact —
// the authored bytes are untouched below the marker.
func TestRenderPage_PrependsMarkerAndPreservesPattern(t *testing.T) {
	p := goldenResult().Patterns[0]
	got := RenderPage(p, goldenProvenance())
	if !strings.HasPrefix(got, "<!-- planwerk-agent: captured from planwerk/planwerk-agent#138 -->\n\n") {
		t.Fatalf("page does not start with the provenance marker:\n%s", got)
	}
	if !strings.Contains(got, "# Review Pattern: Escape untrusted fences") {
		t.Errorf("pattern header did not survive rendering:\n%s", got)
	}
}

// TestRenderPage_Idempotent proves the marker carries no volatile component:
// rendering the same page with the same Provenance is byte-identical, so a
// re-run updates the page in place rather than churning it.
func TestRenderPage_Idempotent(t *testing.T) {
	p := goldenResult().Memory[0]
	first := RenderPage(p, goldenProvenance())
	second := RenderPage(p, goldenProvenance())
	if first != second {
		t.Errorf("RenderPage is not deterministic:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// TestRenderWithSource proves the page form: the marker for the source, a blank
// line, and the body with its trailing newlines folded into one.
func TestRenderWithSource(t *testing.T) {
	got := RenderWithSource("body\n\n", "o/r#4")
	want := "<!-- planwerk-agent: captured from o/r#4 -->\n\nbody\n"
	if got != want {
		t.Errorf("RenderWithSource = %q, want %q", got, want)
	}
	if got := RenderWithSource("", "o/r@abc"); got != "<!-- planwerk-agent: captured from o/r@abc -->\n\n\n" {
		t.Errorf("RenderWithSource of an empty body = %q", got)
	}
}

// TestMarkerFor_MatchesProvenance proves an issue's marker has one form,
// whichever of the two renders it.
func TestMarkerFor_MatchesProvenance(t *testing.T) {
	if got, want := (Provenance{Repo: "o/r", Issue: 4}).Marker(), MarkerFor("o/r#4"); got != want {
		t.Errorf("Provenance.Marker() = %q, MarkerFor = %q", got, want)
	}
}

// TestStripMarker covers both readers of a marker: StripMarker returns the
// page without it, and SplitMarker the source it names as well.
func TestStripMarker(t *testing.T) {
	marker := MarkerFor("o/r#4")
	for _, tc := range []struct {
		name, page, wantSource, want string
	}{
		{"rendered page", RenderWithSource("body\n\n", "o/r#4"), "o/r#4", "body\n"},
		{"no marker", "# Title\n\nbody\n", "", "# Title\n\nbody\n"},
		{"marker without a blank line", marker + "\nbody\n", "o/r#4", "body\n"},
		{"only one blank line is removed", marker + "\n\n\nbody\n", "o/r#4", "\nbody\n"},
		{"marker only", marker, "o/r#4", ""},
		{"marker of a commit", RenderWithSource("body", "o/r@abc"), "o/r@abc", "body\n"},
		{"marker line that ends in a carriage return", marker + "\r\nbody\n", "o/r#4", "body\n"},
		{"page with CRLF line endings", marker + "\r\n\r\nbody\r\n", "o/r#4", "body\r\n"},
		{"marker line with a trailing space", marker + " \nbody\n", "o/r#4", "body\n"},
		{"marker without the space before the terminator", "<!-- planwerk-agent: captured from o/r#4-->\nbody\n", "o/r#4", "body\n"},
		{"text after the terminator", marker + " -->\nbody\n", "o/r#4", "body\n"},
		{"marker on a later line", "body\n" + marker + "\n", "", "body\n" + marker + "\n"},
		{"empty", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := StripMarker(tc.page); got != tc.want {
				t.Errorf("StripMarker(%q) = %q, want %q", tc.page, got, tc.want)
			}
			if source, body := SplitMarker(tc.page); source != tc.wantSource || body != tc.want {
				t.Errorf("SplitMarker(%q) = %q, %q, want %q, %q", tc.page, source, body, tc.wantSource, tc.want)
			}
		})
	}
}

// FuzzStripMarker checks that stripping a rendered page gives back the body
// RenderWithSource wrote, whatever the body holds.
func FuzzStripMarker(f *testing.F) {
	for _, seed := range []string{"", "body", "body\n\n", "\n\nbody", "<!-- planwerk-agent: captured from x -->\n\nbody"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		want := strings.TrimRight(body, "\n") + "\n"
		if got := StripMarker(RenderWithSource(body, "o/r#4")); got != want {
			t.Errorf("StripMarker(RenderWithSource(%q)) = %q, want %q", body, got, want)
		}
	})
}

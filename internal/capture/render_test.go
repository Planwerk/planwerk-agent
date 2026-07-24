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
		Model:      "claude-opus-5",
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
	NewRenderer(&buf).RenderMarkdown(CaptureResult{Model: "claude-opus-5"}, goldenProvenance(), "e1efd0d")
	assertGolden(t, "render_empty", buf.String())
}

// TestRenderMarkdown_BodyWithInnerFenceIsNotCorrupted proves a model-authored
// body that itself contains a ```go code fence does not terminate the wrapper
// early: the wrapper grows to four backticks so the inner fence is carried
// verbatim instead of closing the block and spilling the rest as live markdown.
func TestRenderMarkdown_BodyWithInnerFenceIsNotCorrupted(t *testing.T) {
	result := CaptureResult{
		Model: "claude-opus-5",
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

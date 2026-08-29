package address

import (
	"bytes"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/github/githubtest"
	"io"
	"strings"
	"testing"
)

// barePromptRunner returns a Runner whose GitHub fake clones into a throwaway
// dir so PrintBarePrompt can run detect.Technologies + patterns.LoadFiltered
// without hitting the network.
func barePromptRunner(t *testing.T) *Runner {
	t.Helper()
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, Dir: t.TempDir()}
	return newRunner(gh, &fakeClaude{})
}

func TestPrintBarePrompt_WritesPromptForRef(t *testing.T) {
	r := barePromptRunner(t)
	build := func(ctx BareContext) string {
		return "BARE repo=" + ctx.RepoFullName + " pr=" + itoa(ctx.PRNumber)
	}
	var buf bytes.Buffer
	if err := r.PrintBarePrompt(&buf, Options{PRRef: "https://github.com/owner/repo/pull/42"}, build); err != nil {
		t.Fatalf("PrintBarePrompt returned %v, want nil", err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "BARE repo=owner/repo pr=42") {
		t.Errorf("expected rendered bare prompt with parsed ref, got: %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("output should end with a newline, got: %q", out)
	}
}

func TestPrintBarePrompt_RejectsBadRef(t *testing.T) {
	r := barePromptRunner(t)
	err := r.PrintBarePrompt(io.Discard, Options{PRRef: "not-a-ref"}, func(BareContext) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "parsing PR ref") {
		t.Fatalf("expected parsing error, got %v", err)
	}
}

func TestPrintBarePrompt_RequiresBuilder(t *testing.T) {
	r := barePromptRunner(t)
	err := r.PrintBarePrompt(io.Discard, Options{PRRef: "owner/repo#1"}, nil)
	if err == nil || !strings.Contains(err.Error(), "prompt builder") {
		t.Fatalf("expected builder-required error, got %v", err)
	}
}

package claude

import (
	"slices"
	"testing"
)

// TestWithAllowedTools_PreApprovesWebTools locks in the contract the read-only
// `claude -p` sessions (plan, draft, propose, …) depend on: WebSearch and
// WebFetch are pre-approved via --allowed-tools so a non-interactive session
// can use them without a permission prompt. Without the flag every web call is
// silently auto-denied. The bare "WebFetch" entry (no domain specifier) must be
// kept verbatim — a domain-scoped rule would auto-deny on unlisted domains.
func TestWithAllowedTools_PreApprovesWebTools(t *testing.T) {
	got := withAllowedTools([]string{"-p", "--model", "opus"})

	idx := slices.Index(got, "--allowed-tools")
	if idx == -1 {
		t.Fatalf("withAllowedTools did not append --allowed-tools; got %v", got)
	}
	tools := got[idx+1:]
	for _, want := range []string{"WebSearch", "WebFetch"} {
		if !slices.Contains(tools, want) {
			t.Errorf("allowed tools = %v, want them to include %s", tools, want)
		}
	}
}

// TestWithAllowedTools_NoFlagWhenEmpty documents the guard: an empty tool list
// leaves the args untouched rather than emitting a dangling --allowed-tools
// flag with no value.
func TestWithAllowedTools_NoFlagWhenEmpty(t *testing.T) {
	restore := claudeAllowedTools
	claudeAllowedTools = nil
	t.Cleanup(func() { claudeAllowedTools = restore })

	base := []string{"-p", "--model", "opus"}
	got := withAllowedTools(base)
	if slices.Contains(got, "--allowed-tools") {
		t.Errorf("withAllowedTools emitted the flag for an empty list; got %v", got)
	}
	if len(got) != len(base) {
		t.Errorf("withAllowedTools changed args for an empty list; got %v, want %v", got, base)
	}
}

func TestExtractText_ParsesResultAndModel(t *testing.T) {
	raw := []byte(`{"type":"result","result":"the answer","model":"claude-opus-4-8"}`)
	text, model := extractText(raw)
	if text != "the answer" {
		t.Errorf("text = %q, want %q", text, "the answer")
	}
	if model != "claude-opus-4-8" {
		t.Errorf("model = %q, want %q", model, "claude-opus-4-8")
	}
}

func TestExtractText_ModelEmptyWhenEnvelopeOmitsIt(t *testing.T) {
	raw := []byte(`{"result":"just text"}`)
	text, model := extractText(raw)
	if text != "just text" {
		t.Errorf("text = %q, want %q", text, "just text")
	}
	if model != "" {
		t.Errorf("model = %q, want empty when the envelope omits it", model)
	}
}

func TestExtractText_FallsBackToRawOnNonEnvelope(t *testing.T) {
	raw := []byte("not json at all")
	text, model := extractText(raw)
	if text != "not json at all" {
		t.Errorf("text = %q, want the raw output verbatim", text)
	}
	if model != "" {
		t.Errorf("model = %q, want empty on a non-envelope payload", model)
	}
}

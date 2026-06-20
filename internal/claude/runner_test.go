package claude

import (
	"slices"
	"strings"
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

func TestExtractText(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantText  string
		wantModel string
		wantUsage tokenUsage
		wantCost  float64
		wantErr   bool
	}{
		{
			name:      "valid envelope",
			raw:       `{"type":"result","result":"the answer","model":"claude-opus-4-8"}`,
			wantText:  "the answer",
			wantModel: "claude-opus-4-8",
		},
		{
			// The full envelope carries usage and cost; all four token fields
			// and the cost must parse.
			name: "valid envelope with usage and cost",
			raw: `{"result":"the answer","usage":{"input_tokens":3180,"output_tokens":4,` +
				`"cache_read_input_tokens":15626,"cache_creation_input_tokens":2464},"total_cost_usd":0.049015}`,
			wantText: "the answer",
			wantUsage: tokenUsage{
				InputTokens:         3180,
				OutputTokens:        4,
				CacheReadTokens:     15626,
				CacheCreationTokens: 2464,
			},
			wantCost: 0.049015,
		},
		{
			// A missing model must stay a successful parse with an empty model,
			// not an error — the envelope legitimately omits it.
			name:     "valid envelope omits model",
			raw:      `{"result":"just text"}`,
			wantText: "just text",
		},
		{
			// An envelope that omits usage/cost must decode them to zero, not
			// fail — older or newer CLI versions may not emit them.
			name:      "valid envelope omits usage and cost",
			raw:       `{"result":"just text"}`,
			wantText:  "just text",
			wantUsage: tokenUsage{},
			wantCost:  0,
		},
		{
			// Usage-schema drift must not fail the call: a usage object reshaped
			// into an array can no longer decode into tokenUsage, but the result
			// the envelope carried must still come through with usage at zero.
			name:      "reshaped usage does not fail result extraction",
			raw:       `{"result":"the answer","usage":[1,2,3],"total_cost_usd":0.5}`,
			wantText:  "the answer",
			wantUsage: tokenUsage{},
			wantCost:  0.5,
		},
		{
			// A cost emitted as a string can no longer decode into float64, but it
			// must degrade to zero rather than fail the whole envelope; usage still
			// decodes independently.
			name:      "stringified cost does not fail result extraction",
			raw:       `{"result":"the answer","usage":{"input_tokens":10},"total_cost_usd":"0.42"}`,
			wantText:  "the answer",
			wantUsage: tokenUsage{InputTokens: 10},
			wantCost:  0,
		},
		{
			// Empty input is not a valid envelope; json.Unmarshal rejects it.
			name:    "empty input",
			raw:     "",
			wantErr: true,
		},
		{
			name:    "malformed envelope",
			raw:     "not json at all",
			wantErr: true,
		},
		{
			// A valid-JSON prefix that is cut off mid-stream fails to parse.
			name:    "partial envelope",
			raw:     `{"result":"the answer"`,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			text, model, usage, cost, err := extractText([]byte(tc.raw))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("extractText(%q) = nil error, want an error", tc.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("extractText(%q) returned unexpected error: %v", tc.raw, err)
			}
			if text != tc.wantText {
				t.Errorf("text = %q, want %q", text, tc.wantText)
			}
			if model != tc.wantModel {
				t.Errorf("model = %q, want %q", model, tc.wantModel)
			}
			if usage != tc.wantUsage {
				t.Errorf("usage = %+v, want %+v", usage, tc.wantUsage)
			}
			if cost != tc.wantCost {
				t.Errorf("cost = %v, want %v", cost, tc.wantCost)
			}
		})
	}
}

// TestExtractText_ErrorIncludesTruncatedRaw locks in that a parse failure
// surfaces a wrapped error carrying the first 200 bytes of the raw output for
// debugging, and that the embedded raw output is truncated rather than dumped
// in full.
func TestExtractText_ErrorIncludesTruncatedRaw(t *testing.T) {
	raw := []byte(strings.Repeat("x", 300))
	_, _, _, _, err := extractText(raw)
	if err == nil {
		t.Fatal("extractText on invalid JSON = nil error, want an error")
	}
	if !strings.Contains(err.Error(), "parse output envelope") {
		t.Errorf("error %q does not mention parse output envelope", err)
	}
	if !strings.Contains(err.Error(), strings.Repeat("x", 200)) {
		t.Errorf("error %q does not include the first 200 bytes of raw output", err)
	}
	if strings.Contains(err.Error(), strings.Repeat("x", 201)) {
		t.Errorf("error %q includes more than 200 bytes; head did not truncate", err)
	}
}

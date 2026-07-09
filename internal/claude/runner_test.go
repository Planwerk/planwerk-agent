package claude

import (
	"errors"
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

// TestWithReadOnlyDenied_DeniesWriteTools locks in the harness-level guarantee
// the read-only analysis passes depend on: when readOnly is true the write
// tools are removed from the model's context via --disallowed-tools, so a pass
// whose contract is to analyze (never mutate) the checkout cannot edit a file
// even if steered into trying.
func TestWithReadOnlyDenied_DeniesWriteTools(t *testing.T) {
	got := withReadOnlyDenied([]string{"-p", "--model", "opus"}, true)

	idx := slices.Index(got, "--disallowed-tools")
	if idx == -1 {
		t.Fatalf("withReadOnlyDenied did not append --disallowed-tools; got %v", got)
	}
	tools := got[idx+1:]
	for _, want := range []string{"Edit", "Write", "NotebookEdit"} {
		if !slices.Contains(tools, want) {
			t.Errorf("disallowed tools = %v, want them to include %s", tools, want)
		}
	}
}

// TestWithReadOnlyDenied_NoFlagWhenMutating documents that the mutating sessions
// (implement, fix, address, rebase, finalize) pass readOnly=false and so keep
// the write tools — the flag must not be emitted for them.
func TestWithReadOnlyDenied_NoFlagWhenMutating(t *testing.T) {
	base := []string{"-p", "--model", "opus"}
	got := withReadOnlyDenied(base, false)
	if slices.Contains(got, "--disallowed-tools") {
		t.Errorf("withReadOnlyDenied emitted the flag for a mutating session; got %v", got)
	}
	if len(got) != len(base) {
		t.Errorf("withReadOnlyDenied changed args for a mutating session; got %v, want %v", got, base)
	}
}

// TestWithReadOnlyDenied_PrecedesAllowedTools verifies the ordering invariant:
// --disallowed-tools is appended before --allowed-tools so the latter stays the
// trailing variadic flag. The --allowed-tools token must terminate the
// --disallowed-tools value list, so no allowed tool leaks into the denied set.
func TestWithReadOnlyDenied_PrecedesAllowedTools(t *testing.T) {
	args := withReadOnlyDenied([]string{"-p", "--model", "opus"}, true)
	args = withAllowedTools(args)

	denyIdx := slices.Index(args, "--disallowed-tools")
	allowIdx := slices.Index(args, "--allowed-tools")
	if denyIdx == -1 || allowIdx == -1 {
		t.Fatalf("expected both --disallowed-tools and --allowed-tools; got %v", args)
	}
	if denyIdx > allowIdx {
		t.Fatalf("--disallowed-tools must precede --allowed-tools; got %v", args)
	}
	denied := args[denyIdx+1 : allowIdx]
	if slices.Contains(denied, "WebSearch") || slices.Contains(denied, "WebFetch") {
		t.Errorf("allowed web tools leaked into the denied set %v", denied)
	}
}

// TestHermeticArgs_IsolatesUserConfig locks in the default-on isolation: a
// Client built with the compiled-in defaults appends --setting-sources project
// and --strict-mcp-config so orchestrated sessions ignore the invoking user's
// global ~/.claude settings and MCP servers, keeping reviews reproducible.
func TestHermeticArgs_IsolatesUserConfig(t *testing.T) {
	c := NewClient()
	got := c.hermeticArgs([]string{"-p", "--model", "opus"})

	idx := slices.Index(got, "--setting-sources")
	if idx == -1 || idx+1 >= len(got) || got[idx+1] != "project" {
		t.Errorf("hermeticArgs did not append --setting-sources project; got %v", got)
	}
	if !slices.Contains(got, "--strict-mcp-config") {
		t.Errorf("hermeticArgs did not append --strict-mcp-config; got %v", got)
	}
}

// TestHermeticArgs_InheritWhenEnabled documents the escape hatch: a Client built
// with WithInheritUserConfig(true) leaves the args untouched so a session can
// load user-global config (for an environment whose claude auth depends on it).
func TestHermeticArgs_InheritWhenEnabled(t *testing.T) {
	c := NewClient(WithInheritUserConfig(true))
	base := []string{"-p", "--model", "opus"}
	got := c.hermeticArgs(base)

	if slices.Contains(got, "--setting-sources") || slices.Contains(got, "--strict-mcp-config") {
		t.Errorf("hermeticArgs emitted isolation flags despite WithInheritUserConfig(true); got %v", got)
	}
	if len(got) != len(base) {
		t.Errorf("hermeticArgs changed args when inheriting; got %v, want %v", got, base)
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
			// With --json-schema the CLI carries the validated object in
			// structured_output; extractText must prefer it over result.
			name:     "structured_output preferred over result",
			raw:      `{"result":"prose fallback","structured_output":{"findings":null,"summary":"s"}}`,
			wantText: `{"findings":null,"summary":"s"}`,
		},
		{
			// A literal null structured_output is treated as absent, so result wins.
			name:     "null structured_output falls back to result",
			raw:      `{"result":"the answer","structured_output":null}`,
			wantText: "the answer",
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

// rateLimitEnvelope and maxTurnsEnvelope are the two failure envelopes Claude
// Code actually emits, captured verbatim from `claude -p --output-format json`.
// Both exit non-zero with an EMPTY stderr, which is the whole reason
// claudeRunError exists. Note that the rate-limit envelope reports
// subtype "success" while setting is_error — envelopeFailure must not quote that
// subtype back as the reason.
const (
	rateLimitEnvelope = `{"type":"result","subtype":"success","is_error":true,"api_error_status":429,"result":"You've reached your Fable 5 limit. Run /usage-credits to continue or switch models with /model.","stop_reason":"stop_sequence"}`
	maxTurnsEnvelope  = `{"type":"result","subtype":"error_max_turns","is_error":true,"num_turns":2,"stop_reason":"tool_use"}`
)

// TestEnvelopeFailure locks in the diagnosis of a failed `claude -p` turn from
// the result envelope, which is the only place the CLI records the reason: it
// writes the envelope to stdout and leaves stderr empty.
func TestEnvelopeFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			// The reported bug: the planning session runs on fable, the limit is
			// hit, and the operator saw only "exit status 1\nstderr: ".
			name: "api error carries status and reason",
			raw:  rateLimitEnvelope,
			want: "api error 429: You've reached your Fable 5 limit. Run /usage-credits to continue or switch models with /model.",
		},
		{
			// A harness-level abort carries no result text at all; the subtype is
			// the only thing naming the failure.
			name: "max turns names itself through subtype",
			raw:  maxTurnsEnvelope,
			want: "error_max_turns",
		},
		{
			// A status with no accompanying prose must still surface the status.
			name: "status without reason",
			raw:  `{"is_error":true,"subtype":"success","api_error_status":529}`,
			want: "api error 529",
		},
		{
			// is_error unset means the turn succeeded: never manufacture a failure
			// out of a good envelope, or every successful call would error.
			name: "successful envelope is not a failure",
			raw:  `{"type":"result","subtype":"success","is_error":false,"result":"## Implementation Plan (issue #585)"}`,
		},
		{
			// An envelope that omits is_error entirely (older CLI) is a success.
			name: "envelope without is_error is not a failure",
			raw:  `{"result":"the answer"}`,
		},
		{
			// is_error set but nothing names the reason: report nothing so the
			// caller falls back to stderr, then to the raw stdout head.
			name: "failed turn with no reason yields nothing",
			raw:  `{"is_error":true,"subtype":"success"}`,
		},
		{
			// A literal null status must not render as "api error null".
			name: "null status is treated as absent",
			raw:  `{"is_error":true,"subtype":"error_during_execution","api_error_status":null}`,
			want: "error_during_execution",
		},
		{
			name: "non-envelope output yields nothing",
			raw:  "claude: command failed",
		},
		{
			name: "empty output yields nothing",
			raw:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := envelopeFailure([]byte(tc.raw)); got != tc.want {
				t.Errorf("envelopeFailure(%s) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestClaudeRunError_SurfacesEnvelopeReason is the regression test for the
// reported failure: `implement` died with "claude: exit status 1\nstderr: " and
// no hint that the fable rate limit was hit. The reason lives on stdout, so the
// error must carry it — and must name the model, since the envelope talks about
// "Fable 5" while the operator only ever typed `implement`.
func TestClaudeRunError_SurfacesEnvelopeReason(t *testing.T) {
	t.Parallel()

	err := claudeRunError(errors.New("exit status 1"), "fable", []byte(rateLimitEnvelope), nil)

	for _, want := range []string{"exit status 1", "model fable", "api error 429", "You've reached your Fable 5 limit"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

// TestClaudeRunError_PrefersEnvelopeOverStderr documents the precedence: when
// the CLI diagnosed the failure itself, that beats whatever noise it also wrote
// to stderr.
func TestClaudeRunError_PrefersEnvelopeOverStderr(t *testing.T) {
	t.Parallel()

	err := claudeRunError(errors.New("exit status 1"), "opus", []byte(maxTurnsEnvelope), []byte("some warning\n"))

	if !strings.Contains(err.Error(), "error_max_turns") {
		t.Errorf("error %q does not carry the envelope reason", err)
	}
	if strings.Contains(err.Error(), "some warning") {
		t.Errorf("error %q quoted stderr even though the envelope diagnosed the failure", err)
	}
}

// TestClaudeRunError_FallsBackToStderr covers the failures that never reach the
// API and so produce no envelope — an unknown flag, a rejected argument. Those
// do land on stderr, and dropping them would be the same bug in reverse.
func TestClaudeRunError_FallsBackToStderr(t *testing.T) {
	t.Parallel()

	err := claudeRunError(errors.New("exit status 2"), "opus", nil, []byte("  error: unknown option '--nope'\n"))

	if !strings.Contains(err.Error(), "unknown option '--nope'") {
		t.Errorf("error %q does not carry stderr", err)
	}
	if strings.Contains(err.Error(), "no failure envelope") {
		t.Errorf("error %q claimed there was no output despite stderr", err)
	}
}

// TestClaudeRunError_FallsBackToStdoutHead keeps a claude that crashes with
// plain text on stdout (not an envelope) and an empty stderr diagnosable, and
// bounds how much of it lands in the error.
func TestClaudeRunError_FallsBackToStdoutHead(t *testing.T) {
	t.Parallel()

	err := claudeRunError(errors.New("exit status 1"), "opus", []byte(strings.Repeat("x", 300)), nil)

	if !strings.Contains(err.Error(), strings.Repeat("x", 200)) {
		t.Errorf("error %q does not include the first 200 bytes of stdout", err)
	}
	if strings.Contains(err.Error(), strings.Repeat("x", 201)) {
		t.Errorf("error %q includes more than 200 bytes; head did not truncate", err)
	}
}

// TestClaudeRunError_NoOutputAtAll documents the last resort: say so explicitly
// rather than emitting the trailing "stderr: " that hid the real problem.
func TestClaudeRunError_NoOutputAtAll(t *testing.T) {
	t.Parallel()

	err := claudeRunError(errors.New("signal: killed"), "opus", nil, nil)

	if !strings.Contains(err.Error(), "no failure envelope on stdout and no stderr output") {
		t.Errorf("error %q does not explain the absent diagnostic", err)
	}
	if !strings.Contains(err.Error(), "signal: killed") {
		t.Errorf("error %q lost the underlying error", err)
	}
}

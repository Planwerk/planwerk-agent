package claude

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/diffscope"
	"github.com/planwerk/planwerk-agent/internal/gitref/gitreftest"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
	"github.com/planwerk/planwerk-agent/internal/report/schema"
	"github.com/planwerk/planwerk-agent/internal/search"
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

// TestWithAgents_AppendsFlag locks the orchestrated implement session's
// contract: a non-empty agents JSON reaches the CLI verbatim as the --agents
// value, so the inline implementer definition (worker model, effort, prompt)
// arrives without any file being written into the target checkout.
func TestWithAgents_AppendsFlag(t *testing.T) {
	const agentsJSON = `{"implementer":{"model":"opus"}}`
	got := withAgents([]string{"-p", "--model", "fable"}, agentsJSON)

	idx := slices.Index(got, "--agents")
	if idx == -1 || idx+1 >= len(got) {
		t.Fatalf("withAgents did not append --agents with a value; got %v", got)
	}
	if got[idx+1] != agentsJSON {
		t.Errorf("--agents value = %q, want the JSON passed in %q", got[idx+1], agentsJSON)
	}
}

// TestWithAgents_NoFlagWhenEmpty documents the guard: an empty agents JSON —
// every session except the orchestrated implement one — leaves the args
// untouched rather than emitting a dangling --agents flag with no value.
func TestWithAgents_NoFlagWhenEmpty(t *testing.T) {
	base := []string{"-p", "--model", "opus"}
	got := withAgents(base, "")
	if slices.Contains(got, "--agents") {
		t.Errorf("withAgents emitted the flag for an empty value; got %v", got)
	}
	if len(got) != len(base) {
		t.Errorf("withAgents changed args for an empty value; got %v, want %v", got, base)
	}
}

// TestWithSession locks the session-identity flags the completion nudge
// depends on: a pinned fresh session emits --session-id, a resumed one emits
// --resume with the same id, and the ordinary one-shot call (no id) emits
// neither — keeping the historical invocation byte-for-byte unchanged. A
// resume flag without an id must also emit nothing: --resume without a value
// would open the CLI's interactive picker, which a headless run can never
// answer.
func TestWithSession(t *testing.T) {
	base := []string{"-p", "--model", "opus"}
	const id = "123e4567-e89b-42d3-a456-426614174000"

	cases := []struct {
		name     string
		spec     runSpec
		wantFlag string
	}{
		{"fresh pinned session", runSpec{sessionID: id}, "--session-id"},
		{"resumed session", runSpec{sessionID: id, resume: true}, "--resume"},
		{"no session id", runSpec{}, ""},
		{"resume without id emits nothing", runSpec{resume: true}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := withSession(base, tc.spec)
			if tc.wantFlag == "" {
				if len(got) != len(base) {
					t.Fatalf("withSession changed args without a session id; got %v", got)
				}
				return
			}
			idx := slices.Index(got, tc.wantFlag)
			if idx == -1 || idx+1 >= len(got) || got[idx+1] != id {
				t.Fatalf("withSession = %v, want %s %s appended", got, tc.wantFlag, id)
			}
			for _, other := range []string{"--session-id", "--resume"} {
				if other != tc.wantFlag && slices.Contains(got, other) {
					t.Errorf("withSession emitted %s alongside %s; got %v", other, tc.wantFlag, got)
				}
			}
		})
	}
}

// TestNewSessionID verifies the generated id is the RFC 4122 version-4 UUID
// the CLI's --session-id flag requires, and that consecutive calls do not
// collide (each session must be individually resumable).
func TestNewSessionID(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	a, b := newSessionID(), newSessionID()
	for _, id := range []string{a, b} {
		if !re.MatchString(id) {
			t.Errorf("newSessionID() = %q, not a v4 UUID", id)
		}
	}
	if a == b {
		t.Errorf("consecutive session ids collided: %q", a)
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
// and --strict-mcp-config to a mutating session, so it ignores the invoking
// user's global ~/.claude settings and MCP servers, keeping runs reproducible.
func TestHermeticArgs_IsolatesUserConfig(t *testing.T) {
	c := NewClient()
	got := c.hermeticArgs([]string{"-p", "--model", "opus"}, false)

	idx := slices.Index(got, "--setting-sources")
	if idx == -1 || idx+1 >= len(got) || got[idx+1] != "project" {
		t.Errorf("hermeticArgs did not append --setting-sources project; got %v", got)
	}
	if !slices.Contains(got, "--strict-mcp-config") {
		t.Errorf("hermeticArgs did not append --strict-mcp-config; got %v", got)
	}
}

// TestHermeticArgs_InheritWhenEnabled documents the escape hatch: a Client built
// with WithInheritUserConfig(true) leaves a mutating session's args untouched
// so it can load user-global config (for an environment whose claude auth
// depends on it).
func TestHermeticArgs_InheritWhenEnabled(t *testing.T) {
	c := NewClient(WithInheritUserConfig(true))
	base := []string{"-p", "--model", "opus"}
	got := c.hermeticArgs(base, false)

	if slices.Contains(got, "--setting-sources") || slices.Contains(got, "--strict-mcp-config") {
		t.Errorf("hermeticArgs emitted isolation flags despite WithInheritUserConfig(true); got %v", got)
	}
	if len(got) != len(base) {
		t.Errorf("hermeticArgs changed args when inheriting; got %v, want %v", got, base)
	}
}

// TestHermeticArgs_ReadOnlyLoadsNoSettingsFile locks in that a read-only
// session gets an empty setting-source list in one element, so neither the
// user's settings nor the checkout's .claude/settings.json reach it, and that
// it still loads no MCP servers (decision 123).
func TestHermeticArgs_ReadOnlyLoadsNoSettingsFile(t *testing.T) {
	got := NewClient().hermeticArgs([]string{"-p"}, true)

	want := []string{"-p", "--setting-sources=", "--strict-mcp-config"}
	if !slices.Equal(got, want) {
		t.Errorf("hermeticArgs(read-only) = %q, want %q", got, want)
	}
}

// TestHermeticArgs_ReadOnlyInheritsOnlyUserSettings covers the escape hatch on
// a read-only session: it keeps the user's settings, where the authentication
// lives, and still drops the checkout's (decision 123).
func TestHermeticArgs_ReadOnlyInheritsOnlyUserSettings(t *testing.T) {
	got := NewClient(WithInheritUserConfig(true)).hermeticArgs([]string{"-p"}, true)

	want := []string{"-p", "--setting-sources", "user"}
	if !slices.Equal(got, want) {
		t.Errorf("hermeticArgs(read-only, inherit) = %q, want %q", got, want)
	}
}

// TestCLIJSONSchema_StripsDialectFromEmbeddedSchema is the regression guard for
// the bug that failed every schema-constrained session: schema.FinderOutput
// declares $schema 2020-12, and the CLI's draft-07 validator rejects the whole
// --json-schema flag with "no schema with key or ref …/draft/2020-12/schema"
// before the model runs. The wire copy must carry no dialect declaration while
// keeping the document otherwise intact.
func TestCLIJSONSchema_StripsDialectFromEmbeddedSchema(t *testing.T) {
	got := cliJSONSchema(string(schema.FinderOutput))

	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got), &fields); err != nil {
		t.Fatalf("cliJSONSchema produced invalid JSON: %v", err)
	}
	if _, ok := fields["$schema"]; ok {
		t.Errorf("cliJSONSchema left the $schema dialect declaration in the wire copy; the CLI rejects it")
	}
	for _, want := range []string{"properties", "required", "$defs", "type"} {
		if _, ok := fields[want]; !ok {
			t.Errorf("cliJSONSchema dropped %q; only $schema may be removed", want)
		}
	}
}

// TestCLIJSONSchema_KeepsNestedPropertyOrder guards the finding's property
// order on the wire. The session fills the --json-schema document in the order
// its properties are listed, which puts a finding's evidence before its labels
// (decision 136). cliJSONSchema re-encodes only the top level, so the wire
// copy must list a finding's properties exactly as the embedded schema does; a
// re-encode through map[string]any would sort them alphabetically.
func TestCLIJSONSchema_KeepsNestedPropertyOrder(t *testing.T) {
	type schemaDefs struct {
		Defs map[string]struct {
			Properties json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	var wire, embedded schemaDefs
	if err := json.Unmarshal([]byte(cliJSONSchema(string(schema.FinderOutput))), &wire); err != nil {
		t.Fatalf("decoding the wire copy: %v", err)
	}
	if err := json.Unmarshal(schema.FinderOutput, &embedded); err != nil {
		t.Fatalf("decoding schema.FinderOutput: %v", err)
	}
	got := objectKeys(t, wire.Defs["finding"].Properties)
	want := objectKeys(t, embedded.Defs["finding"].Properties)
	if !slices.Equal(got, want) {
		t.Errorf("the wire copy lists a finding's properties as %q, the embedded schema as %q", got, want)
	}
}

// TestCLIJSONSchema_PassesThroughNonObjects documents the guard: a document that
// is not a JSON object — including the empty string every schema-less caller
// passes — is returned untouched, so the empty value still suppresses the flag
// and a genuinely malformed schema reaches the CLI to be reported there.
func TestCLIJSONSchema_PassesThroughNonObjects(t *testing.T) {
	for _, doc := range []string{"", "not json", "[1,2]"} {
		if got := cliJSONSchema(doc); got != doc {
			t.Errorf("cliJSONSchema(%q) = %q, want it unchanged", doc, got)
		}
	}
}

// TestCLIJSONSchema_LeavesSchemalessDocumentUnchanged keeps the helper a no-op
// for a document that already declares no dialect, rather than round-tripping it
// through the JSON encoder for nothing.
func TestCLIJSONSchema_LeavesSchemalessDocumentUnchanged(t *testing.T) {
	doc := `{"type":"object","properties":{"ok":{"type":"boolean"}}}`
	if got := cliJSONSchema(doc); got != doc {
		t.Errorf("cliJSONSchema(%q) = %q, want it unchanged", doc, got)
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

// TestClaudeArgs_StreamsEverySession locks every session to the event stream
// runClaudeStream reads: a full session and a structuring one both ask for
// --output-format stream-json --verbose, and the spec-driven flags survive.
func TestClaudeArgs_StreamsEverySession(t *testing.T) {
	full := runSpec{model: "opus", effort: "high", permissionMode: "plan", jsonSchema: `{"type":"object"}`, readOnly: true, agentsJSON: "{}", appendSystemPrompt: "contract", sessionID: "0b6f4a8e-6a4c-4c1e-9a55-3f0d2c1b7e21", addDirs: []string{"/tmp/planwerk-agent-patterns-test"}}
	structuring := runSpec{model: "sonnet", effort: "medium", readOnly: true, noTools: true, jsonSchema: `{"type":"object"}`, sessionID: "0b6f4a8e-6a4c-4c1e-9a55-3f0d2c1b7e21"}
	auto := runSpec{model: "opus", effort: "xhigh", permissionMode: claudeAutoPermissionMode, jsonSchema: `{"type":"object"}`, sessionID: "0b6f4a8e-6a4c-4c1e-9a55-3f0d2c1b7e21"}

	for _, tc := range []struct {
		name string
		spec runSpec
		want []string
	}{
		{"a full session", full, []string{"--session-id", "--permission-mode", "--add-dir", "--append-system-prompt", "--json-schema"}},
		{"a structuring session", structuring, []string{"--session-id", "--permission-mode", "--json-schema"}},
		{"an auto-mode session", auto, []string{"--session-id", "--permission-mode", "--json-schema"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := NewClient().claudeArgs(tc.spec)
			i := slices.Index(args, "--output-format")
			if i == -1 || !slices.Equal(args[i+1:i+3], []string{"stream-json", "--verbose"}) {
				t.Fatalf("argv %v lacks --output-format stream-json --verbose", args)
			}
			if slices.Contains(args, "json") {
				t.Errorf("argv %v still asks for the buffered json envelope", args)
			}
			for _, flag := range tc.want {
				if !slices.Contains(args, flag) {
					t.Errorf("argv lost the spec-driven flag %s: %v", flag, args)
				}
			}
		})
	}
}

// TestClaudeArgs_NoToolsReplacesTheToolFlags pins the structuring tier's
// isolation at the argv level: --tools with an empty value removes the whole
// built-in toolset, and it stands in place of the deny/allow pair rather than
// beside it. It must stay the trailing flag, since --tools is variadic and any
// value after it would be read as a tool name.
func TestClaudeArgs_NoToolsReplacesTheToolFlags(t *testing.T) {
	t.Parallel()

	args := NewClient().claudeArgs(runSpec{model: "sonnet", effort: "medium", readOnly: true, noTools: true})

	idx := slices.Index(args, "--tools")
	if idx == -1 {
		t.Fatalf("claudeArgs did not emit --tools for a no-tools spec; got %v", args)
	}
	if idx != len(args)-2 || args[idx+1] != "" {
		t.Errorf("--tools must be the trailing flag with one empty value; got %v", args)
	}
	if slices.Contains(args, "--disallowed-tools") || slices.Contains(args, "--allowed-tools") {
		t.Errorf("a no-tools session must emit neither tool list; got %v", args)
	}
	// The isolation the other flags provide is unaffected.
	if !slices.Contains(args, "--setting-sources=") || !slices.Contains(args, "--strict-mcp-config") {
		t.Errorf("a no-tools session lost its hermetic flags; got %v", args)
	}
}

// TestClaudeArgs_ReadOnlyDisablesProjectHooks locks in that a read-only session
// (an analysis pass or a structuring pass) switches off every hook, so a
// .claude/settings.json in the checkout under review cannot run a command as the
// operator, while a mutating session keeps the repository's own hooks.
func TestClaudeArgs_ReadOnlyDisablesProjectHooks(t *testing.T) {
	t.Parallel()

	for _, spec := range []runSpec{
		{model: "opus", effort: "xhigh", readOnly: true},
		{model: "sonnet", effort: "medium", readOnly: true, noTools: true},
	} {
		args := NewClient().claudeArgs(spec)
		i := slices.Index(args, "--settings")
		if i == -1 || i+1 >= len(args) || args[i+1] != readOnlyHookSettings {
			t.Errorf("read-only spec %+v: want --settings %s; got %v", spec, readOnlyHookSettings, args)
		}
	}

	args := NewClient().claudeArgs(runSpec{model: "opus", effort: "xhigh", permissionMode: claudeAutoPermissionMode})
	if slices.Contains(args, "--settings") {
		t.Errorf("a mutating session must keep the project's hooks; got %v", args)
	}
}

// TestClaudeArgs_ReadOnlyRunsInDontAsk locks in that a read-only session (an
// analysis pass or a structuring pass) names its permission mode, so a
// permissions.defaultMode in the checkout under review cannot choose one for
// it, while a mutating session keeps its own mode and gets no second flag
// (decision 123).
func TestClaudeArgs_ReadOnlyRunsInDontAsk(t *testing.T) {
	t.Parallel()

	for _, spec := range []runSpec{
		{model: "opus", effort: "xhigh", readOnly: true},
		{model: "sonnet", effort: "medium", readOnly: true, noTools: true},
	} {
		args := NewClient().claudeArgs(spec)
		i := slices.Index(args, "--permission-mode")
		if i == -1 || i+1 >= len(args) || args[i+1] != claudeReadOnlyPermissionMode {
			t.Errorf("read-only spec %+v: want --permission-mode %s; got %v", spec, claudeReadOnlyPermissionMode, args)
		}
	}

	args := NewClient().claudeArgs(runSpec{model: "opus", effort: "xhigh", permissionMode: claudeAutoPermissionMode})
	i := slices.Index(args, "--permission-mode")
	if i == -1 || i+1 >= len(args) || args[i+1] != claudeAutoPermissionMode {
		t.Errorf("a mutating session must keep --permission-mode %s; got %v", claudeAutoPermissionMode, args)
	}
	if slices.Contains(args[i+1:], "--permission-mode") {
		t.Errorf("a mutating session carries a second --permission-mode; got %v", args)
	}
}

// TestClaudeArgs_PersistsOnlyTheSessionsThatEdit locks in which sessions
// Claude Code saves a transcript for: a read-only or structuring session runs
// with --no-session-persistence, once and ahead of the variadic tool lists,
// while a session that edits, resumed or not, keeps its transcript, and
// WithKeepTranscripts(true) keeps every one (decision 161).
func TestClaudeArgs_PersistsOnlyTheSessionsThatEdit(t *testing.T) {
	t.Parallel()

	const flag = "--no-session-persistence"
	const id = "123e4567-e89b-42d3-a456-426614174000"
	readOnly := []struct {
		name string
		spec runSpec
	}{
		{"a read-only session", runSpec{model: "opus", effort: "xhigh", readOnly: true}},
		{"a structuring session", runSpec{model: "sonnet", effort: "medium", readOnly: true, noTools: true}},
		{"a finder session", runSpec{model: "opus", effort: "xhigh", readOnly: true, jsonSchema: `{"type":"object"}`, tools: []string{"Grep", "Read"}}},
	}
	editing := []struct {
		name string
		spec runSpec
	}{
		{"an auto-mode session", runSpec{model: "opus", effort: "xhigh", permissionMode: claudeAutoPermissionMode}},
		{"a resumed auto-mode session", runSpec{model: "opus", effort: "xhigh", permissionMode: claudeAutoPermissionMode, sessionID: id, resume: true}},
		// The zero spec is neither readOnly nor noTools, so it is a session
		// that edits.
		{"the zero spec", runSpec{}},
	}

	for _, tc := range readOnly {
		args := NewClient().claudeArgs(tc.spec)
		if n := countArg(args, flag); n != 1 {
			t.Errorf("%s: want %s exactly once, got %d; argv %v", tc.name, flag, n, args)
			continue
		}
		i := slices.Index(args, flag)
		for _, list := range []string{"--tools", "--disallowed-tools", "--allowed-tools"} {
			if j := slices.Index(args, list); j != -1 && j < i {
				t.Errorf("%s: %s must precede the variadic %s; argv %v", tc.name, flag, list, args)
			}
		}
	}
	if args := NewClient().claudeArgs(readOnly[1].spec); !slices.Equal(args[len(args)-2:], []string{"--tools", ""}) {
		t.Errorf("a structuring session must still end with --tools \"\"; argv %v", args)
	}
	for _, tc := range editing {
		if args := NewClient().claudeArgs(tc.spec); slices.Contains(args, flag) {
			t.Errorf("%s must keep its transcript; argv %v", tc.name, args)
		}
	}

	keep := NewClient(WithKeepTranscripts(true))
	for _, tc := range append(readOnly, editing...) {
		if args := keep.claudeArgs(tc.spec); slices.Contains(args, flag) {
			t.Errorf("%s under WithKeepTranscripts(true) must keep its transcript; argv %v", tc.name, args)
		}
	}

	// The session id and the persistence flag are independent: a read-only
	// session pins its id and still saves nothing.
	if args := NewClient().claudeArgs(runSpec{model: "opus", effort: "xhigh", readOnly: true}); !slices.Contains(args, flag) || slices.Contains(args, "--session-id") {
		t.Errorf("a read-only session without an id wants %s and no --session-id; argv %v", flag, args)
	}
	if args := NewClient().claudeArgs(runSpec{model: "opus", effort: "xhigh", readOnly: true, sessionID: id}); !slices.Contains(args, flag) || !slices.Contains(args, "--session-id") {
		t.Errorf("a read-only session with an id wants both %s and --session-id; argv %v", flag, args)
	}
}

// countArg counts the elements of args equal to arg.
func countArg(args []string, arg string) int {
	n := 0
	for _, a := range args {
		if a == arg {
			n++
		}
	}
	return n
}

// TestClaudeArgs_ToolFlagsSurviveWithoutNoTools guards the other direction: every
// session that is not a structuring pass keeps the deny/allow pair exactly as
// before, and carries no --tools while its spec names no tool list.
func TestClaudeArgs_ToolFlagsSurviveWithoutNoTools(t *testing.T) {
	t.Parallel()

	args := NewClient().claudeArgs(runSpec{model: "opus", effort: "xhigh", readOnly: true})

	denyIdx := slices.Index(args, "--disallowed-tools")
	allowIdx := slices.Index(args, "--allowed-tools")
	if denyIdx == -1 || allowIdx == -1 || denyIdx > allowIdx {
		t.Fatalf("read-only session lost the deny-then-allow tool flags; got %v", args)
	}
	if slices.Contains(args, "--tools") {
		t.Errorf("--tools must stay off a session whose spec names no tool list; got %v", args)
	}
	if !slices.Equal(args[denyIdx+1:allowIdx], claudeReadOnlyDeniedTools) {
		t.Errorf("denied tools = %v, want %v", args[denyIdx+1:allowIdx], claudeReadOnlyDeniedTools)
	}
}

// TestClaudeArgs_ToolListPrecedesTheDenyList pins where a --finder-tools list
// lands: one comma-joined --tools value ahead of --disallowed-tools, whose
// token closes the variadic list, with the deny list unchanged and
// --allowed-tools still last. A structuring spec ignores the list and keeps
// its trailing empty --tools alone.
func TestClaudeArgs_ToolListPrecedesTheDenyList(t *testing.T) {
	t.Parallel()

	args := NewClient().claudeArgs(runSpec{model: "opus", effort: "xhigh", readOnly: true, tools: []string{"Grep", "Read"}})
	toolsIdx := slices.Index(args, "--tools")
	denyIdx := slices.Index(args, "--disallowed-tools")
	allowIdx := slices.Index(args, "--allowed-tools")
	if toolsIdx == -1 || denyIdx != toolsIdx+2 || args[toolsIdx+1] != "Grep,Read" {
		t.Fatalf("want --tools Grep,Read directly before --disallowed-tools; got %v", args)
	}
	if allowIdx < denyIdx || !slices.Equal(args[denyIdx+1:allowIdx], claudeReadOnlyDeniedTools) {
		t.Errorf("deny list = %v, want %v ahead of --allowed-tools; got %v", args[denyIdx+1:allowIdx], claudeReadOnlyDeniedTools, args)
	}
	if tail := []string{"--allowed-tools", "WebSearch", "WebFetch"}; !slices.Equal(args[len(args)-len(tail):], tail) {
		t.Errorf("--allowed-tools must stay the trailing flag; got %v", args)
	}

	noTools := NewClient().claudeArgs(runSpec{model: "sonnet", effort: "medium", readOnly: true, noTools: true, tools: []string{"Grep", "Read"}})
	if i := slices.Index(noTools, "--tools"); i != len(noTools)-2 || noTools[i+1] != "" || slices.Contains(noTools, "Grep,Read") {
		t.Errorf("a structuring spec must keep only its trailing --tools \"\"; got %v", noTools)
	}
}

// TestParseToolList pins what --finder-tools accepts: comma-separated tool
// names, trimmed, sorted and deduplicated, and nil for a blank value. An empty
// entry, a lowercase name, a permission rule, another separator and the CLI's
// "default" keyword are refused with the entry quoted.
func TestParseToolList(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in      string
		want    []string
		wantErr string
	}{
		{in: "", want: nil},
		{in: "  ", want: nil},
		{in: " Read, Grep ,Read", want: []string{"Grep", "Read"}},
		{in: "WebFetch,Bash,Glob", want: []string{"Bash", "Glob", "WebFetch"}},
		{in: "Read,,Grep", wantErr: `empty tool name in "Read,,Grep"`},
		{in: "read", wantErr: `tool "read" is not a built-in tool name`},
		{in: "Bash(git:*)", wantErr: `tool "Bash(git:*)" is not a built-in tool name`},
		{in: "Read;Grep", wantErr: `tool "Read;Grep" is not a built-in tool name`},
		{in: "default", wantErr: `tool "default" is not a built-in tool name`},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseToolList(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("ParseToolList(%q) error = %v, want one containing %q", tc.in, err, tc.wantErr)
				}
				return
			}
			if err != nil || !slices.Equal(got, tc.want) || (tc.want == nil) != (got == nil) {
				t.Errorf("ParseToolList(%q) = %#v, %v; want %#v", tc.in, got, err, tc.want)
			}
		})
	}
}

// TestClaudeArgs_SearchRuleFollowsTheWebTools pins the permission rule of
// `brain search`: a read-only session carries it as the last allowed tool, a
// read-only session without one ends its argv with the two web tools, and a
// session that edits, or one without tools, never carries it.
func TestClaudeArgs_SearchRuleFollowsTheWebTools(t *testing.T) {
	t.Parallel()

	rule := goldenBrain().AllowRule()
	c := NewClient()
	readOnly := runSpec{model: "opus", effort: "xhigh", readOnly: true}

	without := c.claudeArgs(readOnly)
	if tail := []string{"--allowed-tools", "WebSearch", "WebFetch"}; len(without) < len(tail) || !slices.Equal(without[len(without)-len(tail):], tail) {
		t.Fatalf("a read-only session without a rule must end in %v; got %v", tail, without)
	}

	withRule := readOnly
	withRule.searchRule = rule
	if got, want := c.claudeArgs(withRule), append(slices.Clone(without), rule); !slices.Equal(got, want) {
		t.Errorf("a read-only session with a rule: argv %v, want %v", got, want)
	}

	for name, spec := range map[string]runSpec{
		"a session that edits":     {model: "opus", effort: "xhigh", permissionMode: claudeAutoPermissionMode, searchRule: rule},
		"a session without a mode": {model: "opus", effort: "xhigh", searchRule: rule},
		"a session without tools":  {model: "sonnet", effort: "xhigh", readOnly: true, noTools: true, searchRule: rule},
	} {
		noRule := spec
		noRule.searchRule = ""
		if got, want := c.claudeArgs(spec), c.claudeArgs(noRule); !slices.Equal(got, want) || slices.Contains(got, rule) {
			t.Errorf("%s must not carry the rule: argv %v, want %v", name, got, want)
		}
	}
}

// TestAdapters_HandTheSearchRuleToTheSessionsThatSearch proves the five
// read-only sessions that plan and judge carry the rule of their context's
// surface, and carry none without one. The bootstrap analysis and a session
// that runs through runClaude never carry one, whatever their context holds.
func TestAdapters_HandTheSearchRuleToTheSessionsThatSearch(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name, label string
		run         func(c *Client, surface search.Surface)
	}{
		{"Review", "review", func(c *Client, surface search.Surface) {
			ctx := goldenReviewContext()
			ctx.Brain = surface
			_, _ = c.Review("", ctx)
		}},
		{"Audit", "audit", func(c *Client, surface search.Surface) {
			ctx := goldenAuditContext()
			ctx.Brain = surface
			_, _ = c.Audit("", ctx)
		}},
		{"Plan", "plan", func(c *Client, surface search.Surface) {
			ctx := goldenImplementContext()
			ctx.Brain = surface
			_, _, _ = c.Plan("", ctx)
		}},
		{"Propose", "analysis", func(c *Client, surface search.Surface) {
			ctx := goldenAnalysisContext()
			ctx.Brain = surface
			_, _ = c.Propose("", ctx)
		}},
		{"Elaborate", "elaborate", func(c *Client, surface search.Surface) {
			ctx := goldenElaborateContext()
			ctx.Brain = surface
			_, _ = c.Elaborate("", ctx)
		}},
	}
	// firstArgs runs an adapter and returns the argv of its first session.
	firstArgs := func(t *testing.T, label string, run func(c *Client)) []string {
		t.Helper()
		c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
			return "", "", nil
		})
		run(c)
		if len(*calls) == 0 {
			t.Fatal("the adapter ran no session")
		}
		first := (*calls)[0].spec
		if first.label != label {
			t.Fatalf("first session has label %q, want %q", first.label, label)
		}
		return c.claudeArgs(first)
	}
	hasBashRule := func(args []string) bool {
		return slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, "Bash(") })
	}

	for _, row := range rows {
		t.Run(row.name+"/with a surface", func(t *testing.T) {
			t.Parallel()
			args := firstArgs(t, row.label, func(c *Client) { row.run(c, goldenBrain()) })
			if args[len(args)-1] != goldenBrain().AllowRule() {
				t.Errorf("the session must end its allowed tools with the rule; argv %v", args)
			}
		})
		t.Run(row.name+"/without a surface", func(t *testing.T) {
			t.Parallel()
			if args := firstArgs(t, row.label, func(c *Client) { row.run(c, search.Surface{}) }); hasBashRule(args) {
				t.Errorf("a session without a surface must carry no Bash rule; argv %v", args)
			}
		})
	}

	t.Run("BootstrapUnit/carries no rule", func(t *testing.T) {
		t.Parallel()
		args := firstArgs(t, "bootstrap-unit", func(c *Client) { _, _ = c.BootstrapUnit("", goldenBootstrapUnitContext()) })
		if hasBashRule(args) {
			t.Errorf("the bootstrap analysis must carry no Bash rule; argv %v", args)
		}
	})

	// The reviewer runs through runClaude, which hands every session the zero
	// surface.
	t.Run("ReviewElaboration/carries no rule", func(t *testing.T) {
		t.Parallel()
		ctx := goldenElaborateContext()
		ctx.Brain = goldenBrain()
		args := firstArgs(t, "elaborate-review", func(c *Client) { _, _ = c.ReviewElaboration("", ctx, "## Description\n\nA draft.\n") })
		if hasBashRule(args) {
			t.Errorf("the reviewer session must carry no Bash rule; argv %v", args)
		}
	})
}

// TestClaudeArgs_AddDirFollowsThePermissionMode pins where the pattern catalog
// directory and the project memory directory land in the argv. --add-dir is
// variadic, so one flag carries every directory, and the last directory must be
// followed by another flag, never by a token the CLI could read as one more
// directory.
func TestClaudeArgs_AddDirFollowsThePermissionMode(t *testing.T) {
	t.Parallel()

	args := NewClient().claudeArgs(runSpec{model: "opus", effort: "xhigh", permissionMode: claudeAutoPermissionMode, addDirs: []string{"/x/y"}})
	i := slices.Index(args, "--permission-mode")
	if i == -1 || i+4 >= len(args) {
		t.Fatalf("argv lacks --permission-mode followed by --add-dir, its value and a flag; got %v", args)
	}
	if args[i+2] != "--add-dir" || args[i+3] != "/x/y" || !strings.HasPrefix(args[i+4], "--") {
		t.Errorf("want --add-dir /x/y right after the permission mode, then a flag; got %v", args)
	}

	// Two directories follow the one flag, in order, and a flag follows them.
	args = NewClient().claudeArgs(runSpec{model: "opus", effort: "xhigh", permissionMode: claudeAutoPermissionMode, addDirs: []string{"/a", "/b"}})
	i = slices.Index(args, "--permission-mode")
	if i == -1 || i+5 >= len(args) {
		t.Fatalf("argv lacks --permission-mode followed by --add-dir, two values and a flag; got %v", args)
	}
	if !slices.Equal(args[i+2:i+5], []string{"--add-dir", "/a", "/b"}) || !strings.HasPrefix(args[i+5], "--") {
		t.Errorf("want --add-dir /a /b right after the permission mode, then a flag; got %v", args)
	}
	if slices.Contains(args[i+3:], "--add-dir") {
		t.Errorf("argv carries more than one --add-dir; got %v", args)
	}

	// A read-only session, in claudeReadOnlyPermissionMode, still gets the directory.
	args = NewClient().claudeArgs(runSpec{model: "opus", effort: "xhigh", readOnly: true, addDirs: []string{"/x/y"}})
	j := slices.Index(args, "--add-dir")
	if j == -1 || j+2 >= len(args) || args[j+1] != "/x/y" || !strings.HasPrefix(args[j+2], "--") {
		t.Errorf("a read-only spec must carry --add-dir /x/y and then a flag; got %v", args)
	}
}

// TestClaudeArgs_NoAddDirWhenEmpty covers a session without a pattern catalog
// directory and without a project memory directory: its argv carries no
// --add-dir at all. An empty entry beside a set one is dropped.
func TestClaudeArgs_NoAddDirWhenEmpty(t *testing.T) {
	t.Parallel()

	for _, spec := range []runSpec{
		{model: "opus", effort: "xhigh", readOnly: true},
		{model: "opus", effort: "xhigh", permissionMode: claudeAutoPermissionMode},
		{model: "opus", effort: "xhigh", readOnly: true, addDirs: []string{}},
		{model: "opus", effort: "xhigh", readOnly: true, addDirs: []string{"", ""}},
	} {
		if args := NewClient().claudeArgs(spec); slices.Contains(args, "--add-dir") || slices.Contains(args, "") {
			t.Errorf("spec %+v without a directory emitted --add-dir or an empty argument; got %v", spec, args)
		}
	}

	args := NewClient().claudeArgs(runSpec{model: "opus", effort: "xhigh", readOnly: true, addDirs: []string{"", "/b"}})
	j := slices.Index(args, "--add-dir")
	if j == -1 || j+2 >= len(args) || args[j+1] != "/b" || !strings.HasPrefix(args[j+2], "--") {
		t.Errorf("want --add-dir /b and then a flag; got %v", args)
	}
	if slices.Contains(args, "") {
		t.Errorf("the empty directory must be dropped; got %v", args)
	}
}

// TestClaudeArgs_NoToolsNeverOpensADirectory guards the structuring tier's
// isolation: a noTools spec ignores addDirs, so the session is handed no
// directory besides structureWorkDir, and --tools "" stays the trailing flag.
func TestClaudeArgs_NoToolsNeverOpensADirectory(t *testing.T) {
	t.Parallel()

	args := NewClient().claudeArgs(runSpec{model: "sonnet", effort: "medium", readOnly: true, noTools: true, addDirs: []string{"/x/y", "/z"}})
	if slices.Contains(args, "--add-dir") || slices.Contains(args, "/x/y") || slices.Contains(args, "/z") {
		t.Errorf("a no-tools session must not be handed a directory; got %v", args)
	}
	if n := len(args); n < 2 || args[n-2] != "--tools" || args[n-1] != "" {
		t.Errorf("--tools with one empty value must stay the trailing flag; got %v", args)
	}
}

// TestClaudeArgs_AppendSystemPromptPrecedesThePermissionMode pins where the
// implement session's status contract lands in the argv: one element, newlines
// intact, before the permission mode, so --add-dir still directly follows the
// permission mode's value.
func TestClaudeArgs_AppendSystemPromptPrecedesThePermissionMode(t *testing.T) {
	t.Parallel()

	const catalogDir = "/tmp/catalog"
	args := NewClient().claudeArgs(runSpec{model: "opus", effort: "xhigh", permissionMode: claudeAutoPermissionMode, addDirs: []string{catalogDir}, appendSystemPrompt: "x\ny"})
	i := slices.Index(args, "--append-system-prompt")
	p := slices.Index(args, "--permission-mode")
	if i == -1 || p == -1 || i+1 >= len(args) {
		t.Fatalf("argv lacks --append-system-prompt or --permission-mode; got %v", args)
	}
	if args[i+1] != "x\ny" {
		t.Errorf("--append-system-prompt value = %q, want the text as one element %q", args[i+1], "x\ny")
	}
	if i > p {
		t.Errorf("--append-system-prompt must precede --permission-mode; got %v", args)
	}
	if p+3 >= len(args) || args[p+2] != "--add-dir" || args[p+3] != catalogDir {
		t.Errorf("want --add-dir %s right after the permission mode; got %v", catalogDir, args)
	}
}

// TestClaudeArgs_NoAppendSystemPromptWhenEmpty covers every session but
// implement: an empty field emits no flag, and a structuring session keeps
// --tools "" as its trailing pair.
func TestClaudeArgs_NoAppendSystemPromptWhenEmpty(t *testing.T) {
	t.Parallel()

	for _, spec := range []runSpec{
		{model: "opus", effort: "xhigh", permissionMode: claudeAutoPermissionMode},
		{model: "sonnet", effort: "medium", readOnly: true, noTools: true},
	} {
		args := NewClient().claudeArgs(spec)
		if slices.Contains(args, "--append-system-prompt") {
			t.Errorf("spec %+v without appendSystemPrompt emitted --append-system-prompt; got %v", spec, args)
		}
		if n := len(args); spec.noTools && (n < 2 || args[n-2] != "--tools" || args[n-1] != "") {
			t.Errorf("--tools with one empty value must stay the trailing flag; got %v", args)
		}
	}
}

// TestAdapters_OpenTheCatalogDirectory pins which sessions are handed the
// pattern catalog directory. Every adapter whose prompt renders the catalog
// index passes ctx.Catalog to its session, and the completion nudge's
// resumed turns keep it; so does the review, whose ctx.Catalog only
// --scope-patterns sets (decision 141). The structuring and repair passes
// behind an adapter get no directory, and neither does the audit session or
// an unscoped review.
func TestAdapters_OpenTheCatalogDirectory(t *testing.T) {
	t.Parallel()

	const catalogDir = "/tmp/planwerk-agent-patterns-test"
	rows := []struct {
		name, label, wantDir string // wantDir is the one directory the session opens, "" for none
		run                  func(c *Client)
	}{
		{"Plan", "plan", catalogDir, func(c *Client) {
			ctx := goldenImplementContext()
			ctx.Catalog.Dir = catalogDir
			_, _, _ = c.Plan("", ctx)
		}},
		{"Implement", "implement", catalogDir, func(c *Client) {
			ctx := goldenImplementContext()
			ctx.Catalog.Dir = catalogDir
			_, _, _ = c.Implement("", ctx)
		}},
		{"ApplySimplifications", "simplify-apply", catalogDir, func(c *Client) {
			ctx := goldenSimplifyApplyContext()
			ctx.Catalog.Dir = catalogDir
			_, _, _ = c.ApplySimplifications("", ctx)
		}},
		{"ApplyReview", "review-apply", catalogDir, func(c *Client) {
			ctx := goldenReviewApplyContext()
			ctx.Catalog.Dir = catalogDir
			_, _, _ = c.ApplyReview("", ctx)
		}},
		{"Fix", "fix", catalogDir, func(c *Client) {
			ctx := goldenFixContext()
			ctx.Catalog.Dir = catalogDir
			_, _, _ = c.Fix("", ctx)
		}},
		{"Address", "address", catalogDir, func(c *Client) {
			ctx := goldenAddressContext()
			ctx.Catalog.Dir = catalogDir
			_, _ = c.Address("", ctx)
		}},
		{"ResolveRebaseConflict", "rebase-conflict", catalogDir, func(c *Client) {
			ctx := goldenRebaseConflictContext()
			ctx.Catalog.Dir = catalogDir
			_, _ = c.ResolveRebaseConflict("", ctx)
		}},
		{"AnalyzeRebasedCommits", "rebase-analysis", catalogDir, func(c *Client) {
			ctx := goldenRebaseAnalysisContext()
			ctx.Catalog.Dir = catalogDir
			_, _ = c.AnalyzeRebasedCommits("", ctx)
		}},
		{"ApplyRebaseAdjustments", "rebase-apply", catalogDir, func(c *Client) {
			ctx := goldenRebaseApplyContext()
			ctx.Catalog.Dir = catalogDir
			_, _ = c.ApplyRebaseAdjustments("", ctx)
		}},
		{"Elaborate", "elaborate", catalogDir, func(c *Client) {
			ctx := goldenElaborateContext()
			ctx.Catalog.Dir = catalogDir
			_, _ = c.Elaborate("", ctx)
		}},
		{"Propose", "analysis", catalogDir, func(c *Client) {
			ctx := goldenAnalysisContext()
			ctx.Catalog.Dir = catalogDir
			_, _ = c.Propose("", ctx)
		}},
		{"GapAnalysis", "gap-analysis", catalogDir, func(c *Client) {
			ctx := goldenGapAnalysisContext()
			ctx.Catalog.Dir = catalogDir
			_, _ = c.GapAnalysis("", ctx)
		}},
		{"ReviewPrepared", "review-prepared", catalogDir, func(c *Client) {
			ctx := goldenReviewPreparedContext(false)
			ctx.Catalog.Dir = catalogDir
			_, _ = c.ReviewPrepared("", ctx)
		}},
		{"Audit", "audit", "", func(c *Client) {
			_, _ = c.Audit("", goldenAuditContext())
		}},
		// The review's diff directory follows the catalog's;
		// TestFinders_ReadTheWrittenDiff covers it.
		{"Review", "review", "", func(c *Client) {
			ctx := goldenReviewContext()
			ctx.Diff = diffscope.Diff{}
			_, _ = c.Review("", ctx)
		}},
		{"Review/scoped", "review", catalogDir, func(c *Client) {
			ctx := goldenScopedReviewContext()
			ctx.Catalog.Dir = catalogDir
			ctx.Diff = diffscope.Diff{}
			_, _ = c.Review("", ctx)
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
				return "", "", nil
			})
			row.run(c)

			if len(*calls) == 0 {
				t.Fatal("the adapter ran no session")
			}
			first := (*calls)[0].spec
			firstDirs := nonEmptyDirs(first.addDirs)
			if first.label != row.label || !slices.Equal(firstDirs, nonEmptyDirs([]string{row.wantDir})) {
				t.Errorf("first session has label %q, addDirs %q; want label %q, directory %q", first.label, firstDirs, row.label, row.wantDir)
			}
			for i, call := range *calls {
				dirs := nonEmptyDirs(call.spec.addDirs)
				switch {
				case call.spec.noTools && len(dirs) != 0:
					t.Errorf("call %d (%s) is a structuring session but carries addDirs %q", i+1, call.spec.label, dirs)
				case !call.spec.noTools && !slices.Equal(dirs, firstDirs):
					t.Errorf("call %d (%s) carries addDirs %q, want the first session's %q", i+1, call.spec.label, dirs, firstDirs)
				}
			}
		})
	}
}

// addDirValues returns the directories that follow --add-dir in args, up to
// the next flag. It returns nil when the flag is absent.
func addDirValues(args []string) []string {
	i := slices.Index(args, "--add-dir")
	if i == -1 {
		return nil
	}
	var dirs []string
	for _, a := range args[i+1:] {
		if strings.HasPrefix(a, "--") {
			break
		}
		dirs = append(dirs, a)
	}
	return dirs
}

// TestAdapters_OpenTheMemoryDirectory pins which sessions are handed the
// project memory directory, on the argv their spec produces. The seven adapters
// whose prompt renders the memory index pass ctx.Memory to their session: the
// review and the audit open that directory alone; the plan, the propose
// analysis, the elaboration, the fix, and the address session open it after
// the pattern catalog directory. Without a memory the argv is what it was: the
// pattern catalog directory where the session has one, and no --add-dir for
// the review and the audit. The elaboration reviewer is handed neither.
func TestAdapters_OpenTheMemoryDirectory(t *testing.T) {
	t.Parallel()

	const catalogDir, memoryDir = "/p", "/m"
	rows := []struct {
		name, label string
		catalogDirs []string // what the session opens without a memory
		run         func(c *Client, mem patterns.MemoryCatalog)
	}{
		{"Review", "review", nil, func(c *Client, mem patterns.MemoryCatalog) {
			ctx := goldenReviewContext()
			ctx.Memory = mem
			// The diff directory follows the memory's; TestFinders_ReadTheWrittenDiff covers it.
			ctx.Diff = diffscope.Diff{}
			_, _ = c.Review("", ctx)
		}},
		{"Audit", "audit", nil, func(c *Client, mem patterns.MemoryCatalog) {
			ctx := goldenAuditContext()
			ctx.Memory = mem
			_, _ = c.Audit("", ctx)
		}},
		{"Plan", "plan", []string{catalogDir}, func(c *Client, mem patterns.MemoryCatalog) {
			ctx := goldenImplementContext()
			ctx.Catalog.Dir = catalogDir
			ctx.Memory = mem
			_, _, _ = c.Plan("", ctx)
		}},
		{"Propose", "analysis", []string{catalogDir}, func(c *Client, mem patterns.MemoryCatalog) {
			ctx := goldenAnalysisContext()
			ctx.Catalog.Dir = catalogDir
			ctx.Memory = mem
			_, _ = c.Propose("", ctx)
		}},
		{"Elaborate", "elaborate", []string{catalogDir}, func(c *Client, mem patterns.MemoryCatalog) {
			ctx := goldenElaborateContext()
			ctx.Catalog.Dir = catalogDir
			ctx.Memory = mem
			_, _ = c.Elaborate("", ctx)
		}},
		{"Fix", "fix", []string{catalogDir}, func(c *Client, mem patterns.MemoryCatalog) {
			ctx := goldenFixContext()
			ctx.Catalog.Dir = catalogDir
			ctx.Memory = mem
			_, _, _ = c.Fix("", ctx)
		}},
		{"Address", "address", []string{catalogDir}, func(c *Client, mem patterns.MemoryCatalog) {
			ctx := goldenAddressContext()
			ctx.Catalog.Dir = catalogDir
			ctx.Memory = mem
			_, _ = c.Address("", ctx)
		}},
	}
	for _, row := range rows {
		for _, tc := range []struct {
			name string
			mem  patterns.MemoryCatalog
			want []string
		}{
			{"with a memory directory", patterns.MemoryCatalog{Dir: memoryDir, Pages: goldenMemory().Pages}, append(slices.Clone(row.catalogDirs), memoryDir)},
			{"without a memory", patterns.MemoryCatalog{}, row.catalogDirs},
		} {
			t.Run(row.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
					return "", "", nil
				})
				row.run(c, tc.mem)

				if len(*calls) == 0 {
					t.Fatal("the adapter ran no session")
				}
				first := (*calls)[0].spec
				if first.label != row.label {
					t.Fatalf("first session has label %q, want %q", first.label, row.label)
				}
				args := c.claudeArgs(first)
				if got := addDirValues(args); !slices.Equal(got, tc.want) {
					t.Errorf("--add-dir carries %q, want %q; argv %v", got, tc.want, args)
				}
				if len(tc.want) == 0 && slices.Contains(args, "--add-dir") {
					t.Errorf("a session without a directory must carry no --add-dir; argv %v", args)
				}
				if tc.mem.Dir == "" && slices.Contains(args, memoryDir) {
					t.Errorf("a session without a memory must not name %s; argv %v", memoryDir, args)
				}
			})
		}
	}

	// The reviewer scores executability: a context that carries a memory
	// directory still yields an argv that names neither it nor the pattern
	// catalog directory.
	t.Run("ReviewElaboration/opens no directory", func(t *testing.T) {
		t.Parallel()
		c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
			return `{"score": 9, "gaps": [], "to_reach_ten": ""}`, "", nil
		})
		ctx := goldenElaborateContext()
		ctx.Catalog.Dir = catalogDir
		ctx.Memory = patterns.MemoryCatalog{Dir: memoryDir, Pages: goldenMemory().Pages}
		if _, err := c.ReviewElaboration("", ctx, "## Description\n\nA draft.\n"); err != nil {
			t.Fatalf("ReviewElaboration returned error: %v", err)
		}

		if len(*calls) == 0 {
			t.Fatal("the reviewer ran no session")
		}
		first := (*calls)[0].spec
		if first.label != "elaborate-review" {
			t.Fatalf("first session has label %q, want elaborate-review", first.label)
		}
		args := c.claudeArgs(first)
		if slices.Contains(args, "--add-dir") || slices.Contains(args, memoryDir) || slices.Contains(args, catalogDir) {
			t.Errorf("the reviewer session must open no directory; argv %v", args)
		}
	})
}

// TestFixAndAddress_KeepTheirSessionModeWithTheMemory pins what the fix and
// address adapters kept when their spec gained the memory directory
// (autoMemorySpec): both still run in auto permission mode with the write
// tools, and the fix session still runs under the completion nudge, which
// resumes the same session with the same directories when it ends without its
// report.
func TestFixAndAddress_KeepTheirSessionModeWithTheMemory(t *testing.T) {
	t.Parallel()
	mem := patterns.MemoryCatalog{Dir: "/tmp/planwerk-agent-memory-test", Pages: goldenMemory().Pages}

	t.Run("fix resumes under the completion nudge", func(t *testing.T) {
		t.Parallel()
		c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
			return "prose without a report", "", nil
		})
		ctx := goldenFixContext()
		ctx.Memory = mem
		_, _, _ = c.Fix("", ctx)

		if len(*calls) < 2 {
			t.Fatalf("fix ran %d session(s), want the initial one and a nudge", len(*calls))
		}
		first, second := (*calls)[0].spec, (*calls)[1].spec
		if first.permissionMode != claudeAutoPermissionMode || first.readOnly || first.sessionID == "" {
			t.Errorf("fix spec = %+v, want auto mode, the write tools, and a pinned session", first)
		}
		if !slices.Contains(first.addDirs, mem.Dir) {
			t.Errorf("fix spec opens %q, want the memory directory %q among them", first.addDirs, mem.Dir)
		}
		if !second.resume || second.sessionID != first.sessionID || !slices.Equal(second.addDirs, first.addDirs) {
			t.Errorf("nudge spec = %+v, want a resume of session %q that keeps the directories %q", second, first.sessionID, first.addDirs)
		}
	})

	t.Run("address keeps auto mode and the write tools", func(t *testing.T) {
		t.Parallel()
		c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) { return "", "", nil })
		ctx := goldenAddressContext()
		ctx.Memory = mem
		_, _ = c.Address("", ctx)

		if len(*calls) == 0 {
			t.Fatal("address ran no session")
		}
		first := (*calls)[0].spec
		if first.label != "address" {
			t.Fatalf("first session has label %q, want address", first.label)
		}
		if first.permissionMode != claudeAutoPermissionMode || first.readOnly {
			t.Errorf("address spec = %+v, want auto mode with the write tools", first)
		}
	})
}

// TestFinders_ConstrainTheOutputSchema pins the finder passes' single session:
// each adapter runs one read-only session that keeps its tools and carries
// schema.FinderOutput, and the empty output it returns decodes without a
// structuring or repair session behind it. The review and the audit run on the
// main model and effort, the other five on the finder tier, and all seven load
// the --finder-tools list, and no list at all without the option.
func TestFinders_ConstrainTheOutputSchema(t *testing.T) {
	t.Parallel()

	const finderModel = "haiku"
	finderTools := []string{"Grep", "Read"}
	rows := []struct {
		name, label, model, effort string
		run                        func(c *Client) (*report.ReviewResult, error)
	}{
		{"Review", "review", DefaultClaudeModel, DefaultClaudeEffort, func(c *Client) (*report.ReviewResult, error) {
			return c.Review("", goldenReviewContext())
		}},
		{"Audit", "audit", DefaultClaudeModel, DefaultClaudeEffort, func(c *Client) (*report.ReviewResult, error) {
			return c.Audit("", goldenAuditContext())
		}},
		{"AdversarialReview", "adversarial", finderModel, testWorkerEffort, func(c *Client) (*report.ReviewResult, error) {
			return c.AdversarialReview("", "develop", "", nil, 0)
		}},
		{"SpecialistReview", "specialist-security", finderModel, testWorkerEffort, func(c *Client) (*report.ReviewResult, error) {
			return c.SpecialistReview("", "develop", specialistByKey(t, "security"), nil, 0)
		}},
		{"FeatureCompliance", "compliance", finderModel, testWorkerEffort, func(c *Client) (*report.ReviewResult, error) {
			return c.FeatureCompliance("", "develop", goldenFeature())
		}},
		{"SimplifyFindings", "simplify", finderModel, testWorkerEffort, func(c *Client) (*report.ReviewResult, error) {
			return c.SimplifyFindings("", "develop")
		}},
		{"VerifyImplementation", "verify-implementation", finderModel, testWorkerEffort, func(c *Client) (*report.ReviewResult, error) {
			return c.VerifyImplementation("", "t", "b", "develop")
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
				return emptyFinderOutputJSON, "", nil
			}, WithFinderModel(finderModel), WithFinderEffort(testWorkerEffort), WithFinderTools(finderTools))

			result, err := row.run(c)
			if err != nil {
				t.Fatalf("adapter returned error: %v", err)
			}
			if len(result.Findings) != 0 {
				t.Errorf("got %d findings, want 0", len(result.Findings))
			}
			if len(*calls) != 1 {
				t.Fatalf("adapter ran %d sessions, want 1", len(*calls))
			}
			spec := (*calls)[0].spec
			if spec.label != row.label || spec.model != row.model || spec.effort != row.effort {
				t.Errorf("label/model/effort = %q/%q/%q, want %q/%q/%q", spec.label, spec.model, spec.effort, row.label, row.model, row.effort)
			}
			if spec.jsonSchema != string(schema.FinderOutput) {
				t.Errorf("jsonSchema is not schema.FinderOutput: %.80q", spec.jsonSchema)
			}
			if spec.noTools || !spec.readOnly {
				t.Errorf("noTools=%v readOnly=%v, want a read-only session that keeps its tools", spec.noTools, spec.readOnly)
			}
			if !slices.Equal(spec.tools, finderTools) {
				t.Errorf("tools = %v, want the --finder-tools list %v", spec.tools, finderTools)
			}

			c, calls = scriptedClient(t, func(int, runSpec, string) (string, string, error) {
				return emptyFinderOutputJSON, "", nil
			})
			if _, err := row.run(c); err != nil {
				t.Fatalf("adapter without --finder-tools returned error: %v", err)
			}
			if spec := (*calls)[0].spec; spec.tools != nil || slices.Contains(c.claudeArgs(spec), "--tools") {
				t.Errorf("without --finder-tools: tools = %v, argv %v; want no tool list", spec.tools, c.claudeArgs(spec))
			}
		})
	}
}

// TestFinderTools_LeaveTheOtherSessionsAlone proves the scope of
// --finder-tools: the coverage map, claim verification and an analysis session
// keep Claude Code's default set when it is set.
// TestFinders_ConstrainTheOutputSchema covers the seven passes it applies to.
func TestFinderTools_LeaveTheOtherSessionsAlone(t *testing.T) {
	t.Parallel()

	others := []struct {
		name, output string
		run          func(c *Client) error
	}{
		{"CoverageMap", `{"entries":[]}`, func(c *Client) error {
			_, err := c.CoverageMap("", "develop")
			return err
		}},
		{"VerifyFindingClaims", `{"verdicts":[]}`, func(c *Client) error {
			_, err := c.VerifyFindingClaims("", []report.Finding{{Severity: report.SeverityCritical, Title: "t", File: "a.go", Line: 1}})
			return err
		}},
		{"runClaude", "prose", func(c *Client) error {
			_, _, err := c.runClaude("", "prompt", "propose", noCatalog)
			return err
		}},
	}
	for _, row := range others {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
				return row.output, "", nil
			}, WithFinderTools([]string{"Grep", "Read"}))
			if err := row.run(c); err != nil {
				t.Fatalf("session returned error: %v", err)
			}
			if len(*calls) != 1 {
				t.Fatalf("ran %d sessions, want 1", len(*calls))
			}
			if spec := (*calls)[0].spec; spec.tools != nil {
				t.Errorf("%s session tools = %v, want nil: --finder-tools covers only the passes that emit findings", spec.label, spec.tools)
			}
		})
	}
}

// TestFinders_SessionErrorIsWrappedAndSkipsDecode covers a finder session that
// fails: the adapter wraps the error with its pass and neither decodes nor
// repairs anything.
func TestFinders_SessionErrorIsWrappedAndSkipsDecode(t *testing.T) {
	t.Parallel()
	sessionErr := errors.New("claude exited 1")
	c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
		return "", "", sessionErr
	})

	_, err := c.AdversarialReview("", "develop", "", nil, 0)
	if !errors.Is(err, sessionErr) || err.Error() != "running adversarial review: claude exited 1" {
		t.Errorf("err = %v, want running adversarial review: %v", err, sessionErr)
	}
	if len(*calls) != 1 {
		t.Errorf("ran %d sessions, want only the failed finder session", len(*calls))
	}
}

// TestSchemaSessions_CarryTheirSchema pins the sessions besides the finders
// that receive their schema via --json-schema (decision 160): the rebase
// analysis on every run, read-only on the main tier, and the address session
// when it is given no --claude-plugin plugin, in auto mode with the write
// tools. A valid result, the null slice the schema allows included, decodes in
// that one session. The repair decode stays the backstop for a result that
// arrives as text: an empty one fails without a repair, a truncated one is
// repaired toward the schema on the structure tier, and a failed session is
// wrapped and decodes nothing. An address session given a plugin runs without
// the schema.
func TestSchemaSessions_CarryTheirSchema(t *testing.T) {
	t.Parallel()

	adapters := []struct {
		name string
		// decodeLabel is the label the adapter decodes under; its repair
		// session runs as decodeLabel-repair.
		decodeLabel, schemaTitle, valid, sessionErrPrefix string
		run                                               func(t *testing.T, c *Client) (summary string, err error)
		checkSpec                                         func(t *testing.T, c *Client, spec runSpec)
	}{
		{
			name:             "rebase analysis",
			decodeLabel:      "structured rebase-analysis",
			schemaTitle:      `"title": "RebaseAnalysis"`,
			valid:            `{"commits":null,"summary":"s","recommendation":"r"}`,
			sessionErrPrefix: "analyzing rebased commits: ",
			run: func(_ *testing.T, c *Client) (string, error) {
				result, err := c.AnalyzeRebasedCommits("", goldenRebaseAnalysisContext())
				if err != nil {
					return "", err
				}
				return result.Summary, nil
			},
			checkSpec: func(t *testing.T, c *Client, spec runSpec) {
				t.Helper()
				if spec.label != "rebase-analysis" || spec.model != c.model || spec.effort != c.effort {
					t.Errorf("label/model/effort = %q/%q/%q, want rebase-analysis on the main tier %q/%q", spec.label, spec.model, spec.effort, c.model, c.effort)
				}
				if !spec.readOnly || spec.noTools {
					t.Errorf("readOnly=%v noTools=%v, want a read-only session that keeps its tools", spec.readOnly, spec.noTools)
				}
				if spec.jsonSchema != string(schema.RebaseAnalysis) {
					t.Errorf("jsonSchema is not schema.RebaseAnalysis: %.80q", spec.jsonSchema)
				}
			},
		},
		{
			name:             "address",
			decodeLabel:      "structured address-result",
			schemaTitle:      `"title": "AddressResult"`,
			valid:            `{"threads":null,"summary":"s","status":"DONE"}`,
			sessionErrPrefix: "running address: ",
			run: func(t *testing.T, c *Client) (string, error) {
				t.Helper()
				result, err := c.Address("", goldenAddressContext())
				if err != nil {
					return "", err
				}
				if result.Status != report.StatusDone {
					t.Errorf("status = %q, want %s", result.Status, report.StatusDone)
				}
				return result.Summary, nil
			},
			checkSpec: func(t *testing.T, _ *Client, spec runSpec) {
				t.Helper()
				if spec.label != "address" || spec.permissionMode != claudeAutoPermissionMode || spec.readOnly {
					t.Errorf("label=%q permissionMode=%q readOnly=%v, want address in auto mode with the write tools", spec.label, spec.permissionMode, spec.readOnly)
				}
				if spec.jsonSchema != string(schema.AddressResult) {
					t.Errorf("jsonSchema is not schema.AddressResult: %.80q", spec.jsonSchema)
				}
			},
		},
	}
	for _, a := range adapters {
		t.Run(a.name, func(t *testing.T) {
			t.Parallel()

			t.Run("a valid result decodes in one session", func(t *testing.T) {
				t.Parallel()
				// The finder and plan tiers differ from the main tier here, so a
				// spec built on either of them fails checkSpec.
				c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
					return a.valid, "", nil
				}, WithFinderModel("haiku"), WithFinderEffort("low"), WithPlanModel("fable"), WithPlanEffort("high"))
				summary, err := a.run(t, c)
				if err != nil {
					t.Fatalf("adapter returned error: %v", err)
				}
				if summary != "s" {
					t.Errorf("summary = %q, want s", summary)
				}
				if len(*calls) != 1 {
					t.Fatalf("ran %d sessions, want 1", len(*calls))
				}
				a.checkSpec(t, c, (*calls)[0].spec)
			})

			t.Run("an empty result fails without a repair", func(t *testing.T) {
				t.Parallel()
				c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
					return "", "", nil
				})
				_, err := a.run(t, c)
				if want := "parsing " + a.decodeLabel + " as JSON: the output holds no JSON value"; err == nil || !strings.HasPrefix(err.Error(), want) {
					t.Errorf("err = %v, want it to start with %q", err, want)
				}
				if len(*calls) != 1 {
					t.Errorf("ran %d sessions, want 1 and no repair", len(*calls))
				}
			})

			t.Run("a truncated result is repaired toward the schema", func(t *testing.T) {
				t.Parallel()
				truncated := strings.TrimSuffix(a.valid, "}")
				c, calls := scriptedClient(t, func(call int, _ runSpec, _ string) (string, string, error) {
					if call == 1 {
						return truncated, "", nil
					}
					return a.valid, "", nil
				})
				summary, err := a.run(t, c)
				if err != nil {
					t.Fatalf("adapter returned error: %v", err)
				}
				if summary != "s" {
					t.Errorf("summary = %q after the repair, want s", summary)
				}
				if len(*calls) != 2 {
					t.Fatalf("ran %d sessions, want the session and one repair", len(*calls))
				}
				repair := (*calls)[1]
				if !repair.spec.noTools || repair.spec.label != a.decodeLabel+"-repair" {
					t.Errorf("repair noTools=%v label=%q, want a structure-tier session labelled %q", repair.spec.noTools, repair.spec.label, a.decodeLabel+"-repair")
				}
				for _, want := range []string{"The corrected JSON MUST match this JSON Schema:", a.schemaTitle} {
					if !strings.Contains(repair.prompt, want) {
						t.Errorf("repair prompt lacks %q", want)
					}
				}
			})

			t.Run("a failed session is wrapped and decodes nothing", func(t *testing.T) {
				t.Parallel()
				sessionErr := errors.New("claude exited 1")
				c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
					return "", "", sessionErr
				})
				_, err := a.run(t, c)
				if !errors.Is(err, sessionErr) || err.Error() != a.sessionErrPrefix+"claude exited 1" {
					t.Errorf("err = %v, want %sclaude exited 1", err, a.sessionErrPrefix)
				}
				if len(*calls) != 1 {
					t.Errorf("ran %d sessions, want only the failed one", len(*calls))
				}
			})
		})
	}

	t.Run("an address session given a plugin runs without it", func(t *testing.T) {
		t.Parallel()
		c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
			return `{"threads":null,"summary":"s","status":"DONE"}`, "", nil
		}, WithPlugins(testPlugins))
		if _, err := c.Address("", goldenAddressContext()); err != nil {
			t.Fatalf("Address returned error: %v", err)
		}
		if len(*calls) != 1 {
			t.Fatalf("ran %d sessions, want 1", len(*calls))
		}
		spec := (*calls)[0].spec
		if spec.jsonSchema != "" {
			t.Errorf("jsonSchema = %.80q, want none for a session given a plugin", spec.jsonSchema)
		}
		if args := c.claudeArgs(spec); !slices.Contains(args, "--plugin-dir") || slices.Contains(args, "--json-schema") {
			t.Errorf("argv %v, want --plugin-dir and no --json-schema", args)
		}
	})
}

// diffTestRepo returns a checkout whose origin/develop is the base commit and
// whose HEAD changes app.go and go.sum on top of it, the shape a pull request
// clone has.
func diffTestRepo(t *testing.T) string {
	t.Helper()
	dir := gitreftest.Repo(t, map[string]string{"app.go": "package app\n", "go.sum": "a v1 h1:x\n"})
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	base := git("rev-parse", "HEAD")
	gitreftest.Write(t, dir, "app.go", "package app\n\nfunc Changed() {}\n")
	gitreftest.Write(t, dir, "go.sum", "a v2 h1:y\n")
	git("commit", "-q", "-am", "head")
	git("update-ref", "refs/remotes/origin/develop", base)
	return dir
}

// TestFinders_ReadTheWrittenDiff proves every diff-scoped finder reads a diff
// file: the five finder-tier passes write one for their session, hand the
// session its directory, name the file and what --diff-exclusions left out in
// the prompt, record the exclusions on their result, and remove the directory
// once the session returns. The review reads the diff its caller wrote and
// writes none, and the audit is handed no diff directory (decision 137).
func TestFinders_ReadTheWrittenDiff(t *testing.T) {
	t.Parallel()
	dir := diffTestRepo(t)
	wantExcluded := []report.ExcludedFile{{Path: "go.sum", Reason: report.ExcludedLockfile}}

	rows := []struct {
		name string
		run  func(c *Client) (*report.ReviewResult, error)
	}{
		{"AdversarialReview", func(c *Client) (*report.ReviewResult, error) {
			return c.AdversarialReview(dir, "develop", "", nil, 0)
		}},
		{"SpecialistReview", func(c *Client) (*report.ReviewResult, error) {
			return c.SpecialistReview(dir, "develop", specialistByKey(t, "security"), nil, 0)
		}},
		{"FeatureCompliance", func(c *Client) (*report.ReviewResult, error) {
			return c.FeatureCompliance(dir, "develop", goldenFeature())
		}},
		{"SimplifyFindings", func(c *Client) (*report.ReviewResult, error) {
			return c.SimplifyFindings(dir, "develop")
		}},
		{"VerifyImplementation", func(c *Client) (*report.ReviewResult, error) {
			return c.VerifyImplementation(dir, "t", "b", "develop")
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			var diffDir, diffText string
			c, calls := scriptedClient(t, func(_ int, spec runSpec, _ string) (string, string, error) {
				// Read the diff while the session runs: it must exist then.
				for _, d := range spec.addDirs {
					if strings.HasPrefix(filepath.Base(d), diffscope.DirPrefix) {
						diffDir = d
						data, err := os.ReadFile(filepath.Join(d, "branch.diff"))
						if err != nil {
							t.Errorf("the diff must be readable while the session runs: %v", err)
						}
						diffText = string(data)
					}
				}
				return `{"findings":[],"summary":"","recommendation":"","excluded_files":[{"path":"app.go","reason":"generated"}]}`, "", nil
			}, WithDiffExclusions(true))

			result, err := row.run(c)
			if err != nil {
				t.Fatalf("adapter returned error: %v", err)
			}
			if diffDir == "" {
				t.Fatalf("the session was handed no %s directory; addDirs %q", diffscope.DirPrefix, (*calls)[0].spec.addDirs)
			}
			if !strings.Contains(diffText, "diff --git a/app.go b/app.go") || strings.Contains(diffText, "go.sum") {
				t.Errorf("the diff must carry app.go and leave out go.sum:\n%s", diffText)
			}
			prompt := (*calls)[0].prompt
			if !strings.Contains(prompt, "The diff is in `"+filepath.Join(diffDir, "branch.diff")+"`") || !strings.Contains(prompt, "<excluded-files>\ngo.sum (lock file)\n</excluded-files>") {
				t.Errorf("the prompt must name the diff file and the excluded go.sum:\n%s", prompt)
			}
			if !slices.Equal(result.ExcludedFiles, wantExcluded) {
				t.Errorf("ExcludedFiles = %+v, want %+v, never what the session emitted", result.ExcludedFiles, wantExcluded)
			}
			if _, err := os.Stat(diffDir); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("after the call, stat %s = %v, want not exist", diffDir, err)
			}
		})
	}

	t.Run("Review reads the diff its caller wrote", func(t *testing.T) {
		t.Parallel()
		c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
			return emptyFinderOutputJSON, "", nil
		}, WithDiffExclusions(true))
		ctx := goldenReviewContext()
		ctx.Diff = goldenExcludedDiff()
		result, err := c.Review(dir, ctx)
		if err != nil {
			t.Fatalf("Review: %v", err)
		}
		spec := (*calls)[0].spec
		if !slices.Equal(nonEmptyDirs(spec.addDirs), []string{ctx.Diff.Dir}) {
			t.Errorf("addDirs = %q, want only the caller's diff directory", spec.addDirs)
		}
		if !strings.Contains((*calls)[0].prompt, "The diff is in `"+ctx.Diff.File+"`") {
			t.Error("the review prompt must name the caller's diff file")
		}
		if !slices.Equal(result.ExcludedFiles, ctx.Diff.Excluded) {
			t.Errorf("ExcludedFiles = %+v, want the caller's %+v", result.ExcludedFiles, ctx.Diff.Excluded)
		}
	})

	t.Run("Review writes no diff of its own", func(t *testing.T) {
		t.Parallel()
		c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
			return emptyFinderOutputJSON, "", nil
		}, WithDiffExclusions(true))
		ctx := goldenReviewContext()
		ctx.Diff = diffscope.Diff{}
		result, err := c.Review(dir, ctx)
		if err != nil {
			t.Fatalf("Review: %v", err)
		}
		if dirs := nonEmptyDirs((*calls)[0].spec.addDirs); len(dirs) != 0 {
			t.Errorf("addDirs = %q, want none for the zero Diff", dirs)
		}
		if !strings.Contains((*calls)[0].prompt, "Run `git diff origin/develop...HEAD` to see the cumulative diff") {
			t.Error("the zero Diff must keep the git command in the review prompt")
		}
		if result.ExcludedFiles != nil {
			t.Errorf("ExcludedFiles = %+v, want nil", result.ExcludedFiles)
		}
	})

	t.Run("Audit is handed no diff directory", func(t *testing.T) {
		t.Parallel()
		c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
			return emptyFinderOutputJSON, "", nil
		}, WithDiffExclusions(true))
		if _, err := c.Audit(dir, goldenAuditContext()); err != nil {
			t.Fatalf("Audit: %v", err)
		}
		if dirs := nonEmptyDirs((*calls)[0].spec.addDirs); len(dirs) != 0 {
			t.Errorf("addDirs = %q, want none: the audit has no diff", dirs)
		}
	})
}

// TestRunClaudeStructure_BuildsAToolLessSpecWithoutSchema pins the structure
// tier's spec now that no structuring call carries a schema: no tools, no
// --json-schema, and the structure model and effort.
func TestRunClaudeStructure_BuildsAToolLessSpecWithoutSchema(t *testing.T) {
	t.Parallel()
	c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
		return "{}", "", nil
	}, WithStructureModel(repairTierModel), WithStructureEffort(repairTierEffort))

	if _, _, err := c.runClaudeStructure("prompt", "elaborate-structure"); err != nil {
		t.Fatalf("runClaudeStructure: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("ran %d sessions, want 1", len(*calls))
	}
	spec := (*calls)[0].spec
	if !spec.noTools || spec.jsonSchema != "" {
		t.Errorf("noTools=%v jsonSchema=%q, want a tool-less session without a schema", spec.noTools, spec.jsonSchema)
	}
	if spec.model != repairTierModel || spec.effort != repairTierEffort {
		t.Errorf("model/effort = %q/%q, want the structure tier %q/%q", spec.model, spec.effort, repairTierModel, repairTierEffort)
	}
}

// TestStructureWorkDir_IsAnEmptyDirectoryOfOurOwn covers what the structuring
// sessions are given instead of the operator's working directory: a directory
// this tool owns, under the same cache root the result cache uses, that exists
// and holds nothing for a session to read.
func TestStructureWorkDir_IsAnEmptyDirectoryOfOurOwn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)                                   // darwin: $HOME/Library/Caches
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache")) // linux

	dir, err := structureWorkDir()
	if err != nil {
		t.Fatalf("structureWorkDir: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(dir), "planwerk-agent/structure-workdir") {
		t.Errorf("dir = %q, want it under the tool's own cache directory", dir)
	}
	if !strings.HasPrefix(dir, home) {
		t.Errorf("dir = %q, want it under the redirected cache root %q", dir, home)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("the working directory was not created: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("working directory holds %d entries, want none for a transcription pass", len(entries))
	}
	// Resolving it twice is the same directory, so a run leaves one session
	// transcript behind rather than one per structuring call.
	again, err := structureWorkDir()
	if err != nil || again != dir {
		t.Errorf("second call returned (%q, %v), want the same directory", again, err)
	}
}

// TestBrainAnalysisTier locks the bootstrap analysis's tier: haiku and xhigh
// without an option, the option's value with one, and the default again for
// an empty option, so a misconfigured flag cannot select an empty model or
// effort. The main tier stays out of it in both directions.
func TestBrainAnalysisTier(t *testing.T) {
	t.Parallel()
	const analysisModel, analysisEffort = "analysis-tier-model", "low"
	const wantModel, wantEffort = "haiku", "xhigh"

	if DefaultBrainAnalysisModel != wantModel || DefaultBrainAnalysisEffort != wantEffort {
		t.Errorf("compiled-in tier = %q/%q, want %s/%s", DefaultBrainAnalysisModel, DefaultBrainAnalysisEffort, wantModel, wantEffort)
	}
	if model, effort := NewClient(WithModel(testTierOverride)).BrainAnalysisTier(); model != DefaultBrainAnalysisModel || effort != DefaultBrainAnalysisEffort {
		t.Errorf("default tier = %q/%q, want the compiled-in one, not the main tier", model, effort)
	}
	c := NewClient(WithModel(testTierOverride), WithBrainAnalysisModel(analysisModel), WithBrainAnalysisEffort(analysisEffort))
	if model, effort := c.BrainAnalysisTier(); model != analysisModel || effort != analysisEffort {
		t.Errorf("tier = %q/%q, want %q/%q", model, effort, analysisModel, analysisEffort)
	}
	if c.model != testTierOverride || c.effort != DefaultClaudeEffort {
		t.Errorf("the analysis options changed the main tier: %q/%q", c.model, c.effort)
	}
	if model, effort := c.BrainReviewTier(); model != DefaultBrainReviewModel || effort != DefaultBrainReviewEffort {
		t.Errorf("the analysis options changed the review tier: %q/%q", model, effort)
	}
	c = NewClient(WithBrainAnalysisModel(""), WithBrainAnalysisEffort(""))
	if model, effort := c.BrainAnalysisTier(); model != DefaultBrainAnalysisModel || effort != DefaultBrainAnalysisEffort {
		t.Errorf("empty options gave %q/%q, want the defaults", model, effort)
	}
}

// TestRunClaudeBrainAnalysis_Spec proves the bootstrap analysis runs read-only
// on its own tier with only the pages directory readable.
func TestRunClaudeBrainAnalysis_Spec(t *testing.T) {
	t.Parallel()
	const analysisModel, analysisEffort, cloneDir, pagesDir = "analysis-tier-model", "low", "/clone", "/state/pages"
	c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
		return "proposals", testResolvedModel, nil
	}, WithModel(testTierOverride), WithBrainAnalysisModel(analysisModel), WithBrainAnalysisEffort(analysisEffort))

	text, model, err := c.runClaudeBrainAnalysis(cloneDir, "distill this", "bootstrap-unit", patterns.MemoryCatalog{Dir: pagesDir})
	if err != nil || text != "proposals" || model != testResolvedModel {
		t.Fatalf("runClaudeBrainAnalysis = %q, %q, %v", text, model, err)
	}
	if len(*calls) != 1 {
		t.Fatalf("ran %d sessions, want 1", len(*calls))
	}
	spec := (*calls)[0].spec
	if spec.model != analysisModel || spec.effort != analysisEffort {
		t.Errorf("tier = %q/%q, want the analysis tier %q/%q", spec.model, spec.effort, analysisModel, analysisEffort)
	}
	if !spec.readOnly || spec.noTools || spec.permissionMode != "" || spec.searchRule != "" {
		t.Errorf("spec = %+v, want a read-only session without a permission mode of its own and without brain search", spec)
	}
	if spec.dir != cloneDir || spec.label != "bootstrap-unit" || !slices.Equal(spec.addDirs, []string{pagesDir}) {
		t.Errorf("dir %q, label %q, addDirs %v", spec.dir, spec.label, spec.addDirs)
	}
}

// TestBrainReviewTier locks the page review's tier: haiku and xhigh without
// an option, the option's value with one, and the default again for an empty
// option, so a misconfigured flag cannot select an empty model or effort.
func TestBrainReviewTier(t *testing.T) {
	t.Parallel()
	const reviewModel, reviewEffort = "review-tier-model", "low"
	const wantModel, wantEffort = "haiku", "xhigh"

	if DefaultBrainReviewModel != wantModel || DefaultBrainReviewEffort != wantEffort {
		t.Errorf("compiled-in tier = %q/%q, want %s/%s", DefaultBrainReviewModel, DefaultBrainReviewEffort, wantModel, wantEffort)
	}
	if model, effort := NewClient().BrainReviewTier(); model != DefaultBrainReviewModel || effort != DefaultBrainReviewEffort {
		t.Errorf("default tier = %q/%q, want the compiled-in one", model, effort)
	}
	c := NewClient(WithModel(testTierOverride), WithBrainReviewModel(reviewModel), WithBrainReviewEffort(reviewEffort))
	if model, effort := c.BrainReviewTier(); model != reviewModel || effort != reviewEffort {
		t.Errorf("tier = %q/%q, want %q/%q", model, effort, reviewModel, reviewEffort)
	}
	if c.model != testTierOverride || c.effort != DefaultClaudeEffort {
		t.Errorf("the review options changed the main tier: %q/%q", c.model, c.effort)
	}
	c = NewClient(WithBrainReviewModel(""), WithBrainReviewEffort(""))
	if model, effort := c.BrainReviewTier(); model != DefaultBrainReviewModel || effort != DefaultBrainReviewEffort {
		t.Errorf("empty options gave %q/%q, want the defaults", model, effort)
	}
}

// TestRunClaudeBrainReview_Spec proves the page review runs read-only on its
// own tier with the pages directory readable, and that the CLI is told so.
func TestRunClaudeBrainReview_Spec(t *testing.T) {
	t.Parallel()
	const reviewModel, reviewEffort, pagesDir = "review-tier-model", "low", "/state/pages"
	c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
		return "verdicts", testResolvedModel, nil
	}, WithModel(testTierOverride), WithBrainReviewModel(reviewModel), WithBrainReviewEffort(reviewEffort))

	text, model, err := c.runClaudeBrainReview("/clone", "review this", "bootstrap-review", patterns.MemoryCatalog{Dir: pagesDir})
	if err != nil || text != "verdicts" || model != testResolvedModel {
		t.Fatalf("runClaudeBrainReview = %q, %q, %v", text, model, err)
	}
	if len(*calls) != 1 {
		t.Fatalf("ran %d sessions, want 1", len(*calls))
	}
	spec := (*calls)[0].spec
	if spec.model != reviewModel || spec.effort != reviewEffort {
		t.Errorf("tier = %q/%q, want the review tier %q/%q", spec.model, spec.effort, reviewModel, reviewEffort)
	}
	if !spec.readOnly || spec.noTools || spec.permissionMode != "" {
		t.Errorf("spec = %+v, want a read-only session without a permission mode of its own", spec)
	}
	if spec.dir != "/clone" || spec.label != "bootstrap-review" || !slices.Equal(spec.addDirs, []string{pagesDir}) {
		t.Errorf("dir %q, label %q, addDirs %v", spec.dir, spec.label, spec.addDirs)
	}

	args := c.claudeArgs(spec)
	i := slices.Index(args, "--disallowed-tools")
	if i == -1 || i+3 >= len(args) || !slices.Equal(args[i+1:i+4], []string{"Edit", "Write", "NotebookEdit"}) {
		t.Errorf("argv must deny the write tools; got %v", args)
	}
	if j := slices.Index(args, "--add-dir"); j == -1 || args[j+1] != pagesDir {
		t.Errorf("argv must carry --add-dir %s; got %v", pagesDir, args)
	}
	if m := slices.Index(args, "--model"); m == -1 || args[m+1] != reviewModel {
		t.Errorf("argv must carry --model %s; got %v", reviewModel, args)
	}
}

// TestRunClaudeBrainReview_SessionErrorSurfaces proves a failed review session
// returns its error and no text.
func TestRunClaudeBrainReview_SessionErrorSurfaces(t *testing.T) {
	t.Parallel()
	boom := errors.New("api error 429")
	c, _ := scriptedClient(t, func(int, runSpec, string) (string, string, error) { return "", "", boom })

	text, _, err := c.runClaudeBrainReview("/clone", "review this", "bootstrap-review", patterns.MemoryCatalog{})
	if !errors.Is(err, boom) || text != "" {
		t.Errorf("runClaudeBrainReview = %q, %v, want the session's error", text, err)
	}
}

// TestReviewPlan_RunsOnThePlanTierAndOpensNothing locks where the plan review
// runs: one read-only session labeled plan-review on the planning tier, which
// opens neither the pattern catalog nor the memory directory and carries no
// search rule, whatever the context it is handed carries.
func TestReviewPlan_RunsOnThePlanTierAndOpensNothing(t *testing.T) {
	t.Parallel()

	// The planning tier this test configures, its own constants for the
	// reason testWorkerModel's comment gives.
	const (
		catalogDir      = "/tmp/planwerk-agent-patterns-test"
		memoryDir       = "/tmp/planwerk-agent-memory-test"
		planTierModel   = "fable"
		planTierEffort  = "max"
		reportedModelID = "claude-fable-5-1"
	)
	c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
		return `{"gaps": [], "to_reach_ten": "", "score": 9}`, reportedModelID, nil
	}, WithPlanModel(planTierModel), WithPlanEffort(planTierEffort))
	ctx := goldenImplementContext()
	ctx.Catalog.Dir = catalogDir
	ctx.Memory = patterns.MemoryCatalog{Dir: memoryDir, Pages: goldenMemory().Pages}
	ctx.Brain = search.Surface{Command: "planwerk-agent brain search planwerk/planwerk-agent"}
	_, model, err := c.ReviewPlan("", ctx, "## Implementation Plan (issue #42)\n\nSTATUS: PLAN_READY")
	if err != nil {
		t.Fatalf("ReviewPlan returned error: %v", err)
	}
	if model != reportedModelID {
		t.Errorf("ReviewPlan returned model %q, want the session's %q for the comment footer", model, reportedModelID)
	}

	if len(*calls) != 1 {
		t.Fatalf("the reviewer ran %d sessions, want 1", len(*calls))
	}
	spec := (*calls)[0].spec
	if spec.label != "plan-review" || spec.model != planTierModel || spec.effort != planTierEffort || !spec.readOnly {
		t.Errorf("session label %q, model %q, effort %q, readOnly %t; want plan-review, %s, %s, true", spec.label, spec.model, spec.effort, spec.readOnly, planTierModel, planTierEffort)
	}
	if spec.searchRule != "" {
		t.Errorf("the plan review must carry no search rule; got %q", spec.searchRule)
	}
	args := c.claudeArgs(spec)
	if slices.Contains(args, "--add-dir") || slices.Contains(args, catalogDir) || slices.Contains(args, memoryDir) {
		t.Errorf("the plan review session must open no directory; argv %v", args)
	}
}

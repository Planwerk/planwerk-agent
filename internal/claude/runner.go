package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/planwerk/planwerk-review/internal/attribution"
)

const (
	// DefaultClaudeTimeout is the compiled-in default for the maximum time
	// allowed for a single Claude Code invocation. Override with
	// SetTimeout (driven by the --claude-timeout flag / PLANWERK_CLAUDE_TIMEOUT
	// env var) when long-running prompts such as audit/elaborate/implement
	// need more headroom.
	DefaultClaudeTimeout = 15 * time.Minute
	// DefaultClaudeModel is the compiled-in default model passed to Claude
	// Code via --model. The "opus" alias runs the latest Opus release
	// automatically, without re-pinning on each model bump; Opus follows
	// instructions more literally than smaller models, which matches the
	// strict MUST/NEVER style used throughout the review prompts. Override
	// with SetModel (driven by the --claude-model flag / PLANWERK_CLAUDE_MODEL
	// env var) to run reviews on a different model, e.g. "fable".
	DefaultClaudeModel = "opus"
	// DefaultPlanModel is the compiled-in default model for the implement
	// command's planning session. The "fable" alias runs the latest Claude
	// Fable release — Anthropic's most capable model. Planning is a single
	// read-only session whose output steers the entire implementation, so
	// the strongest reasoning pays off most there, while the implement
	// session itself stays on the cheaper DefaultClaudeModel. Override with
	// SetPlanModel (driven by the implement command's --plan-model flag /
	// PLANWERK_PLAN_MODEL env var).
	DefaultPlanModel = "fable"
	// DefaultClaudeEffort is the compiled-in default reasoning effort.
	// "xhigh" is Claude Code's own default and the recommended setting for
	// coding and agentic workloads; "max" buys little on top of it and
	// tends toward overthinking. Override with SetEffort (driven by the
	// --claude-effort flag / PLANWERK_CLAUDE_EFFORT env var), e.g. "max"
	// for the largest thinking budget on latency-tolerant one-off runs.
	DefaultClaudeEffort = "xhigh"
	// DefaultPlanEffort is the compiled-in default reasoning effort for the
	// implement command's planning session. "max" buys the largest thinking
	// budget: planning is a single read-only session whose output steers the
	// entire implementation, so the deepest reasoning pays off most there —
	// the same reasoning behind running it on the stronger DefaultPlanModel
	// while the implement session stays on the cheaper DefaultClaudeEffort.
	// The latency "max" adds is also tolerable here because planning is
	// one-shot and not on the implement session's critical loop. Override
	// with SetPlanEffort (driven by the implement command's --plan-effort
	// flag / PLANWERK_PLAN_EFFORT env var).
	DefaultPlanEffort = "max"
	// claudeAutoPermissionMode is the --permission-mode value the implement
	// command passes to its orchestrated `claude -p` session so tool calls
	// run without an interactive confirmation. "auto" is Claude Code's auto
	// mode: a background classifier vets each action and blocks anything
	// irreversible, destructive, or aimed outside the repository (force
	// push, pushing to main, data exfiltration) while letting the routine
	// work of an implementation — edits, tests, commits, pushing a fresh
	// feature branch, opening a draft PR — proceed unattended. The implement
	// session is one-shot and non-interactive (no human to approve each
	// step), so auto mode is preferred over bypassPermissions precisely
	// because it keeps those safety checks. Read-only commands (review,
	// audit, …) keep the default mode by passing an empty permission mode
	// to runClaude. Requires Claude Code v2.1.83+; see
	// https://code.claude.com/docs/en/auto-mode-config.
	claudeAutoPermissionMode = "auto"
)

// claudeAllowedTools are pre-approved on every orchestrated `claude -p` session
// via --allowed-tools so the non-interactive sessions may use them without a
// permission prompt. WebSearch and WebFetch both require permission by default,
// and a non-interactive session has no human to grant it, so without this the
// read-only sessions (plan, draft, propose, elaborate, audit, …) would have
// every web call silently auto-denied. WebSearch finds sources; WebFetch reads
// the pages it surfaces — the pair lets a session verify, say, the current
// version or deprecation status of a dependency. The bare "WebFetch" entry
// carries no domain specifier, which pre-approves every domain (equivalent to
// WebFetch(domain:*)); a domain-scoped rule would instead prompt — and thus
// auto-deny — on every domain outside the list.
//
// --allowed-tools only ADDS these to the auto-approve list; it does NOT
// restrict the rest of the toolset. The auto-mode sessions (implement, fix,
// address, rebase) already get both from the auto classifier's read-only-HTTP
// allowance, so listing them here is redundant-but-harmless for them and
// load-bearing for the default-mode ones.
var claudeAllowedTools = []string{"WebSearch", "WebFetch"}

// withAllowedTools appends the --allowed-tools flag followed by every entry in
// claudeAllowedTools (a no-op when the list is empty). Both the JSON runner
// (runClaudeWithPermission) and the streaming runner (runClaudeStream) route
// their args through it so the two paths can never drift on which tools a
// session may use. The prompt is fed on stdin, never as a positional argument,
// so a trailing variadic flag is safe — there is no positional for the flag to
// swallow.
func withAllowedTools(args []string) []string {
	if len(claudeAllowedTools) == 0 {
		return args
	}
	args = append(args, "--allowed-tools")
	return append(args, claudeAllowedTools...)
}

// claudeTimeout is the effective per-invocation timeout. It defaults to
// DefaultClaudeTimeout and is overridable at startup via SetTimeout.
var claudeTimeout = DefaultClaudeTimeout

// claudeModel is the effective model passed to Claude Code via --model. It
// defaults to DefaultClaudeModel and is overridable at startup via SetModel.
var claudeModel = DefaultClaudeModel

// planModel is the effective model for the implement command's planning
// session. It defaults to DefaultPlanModel and is overridable at startup
// via SetPlanModel.
var planModel = DefaultPlanModel

// claudeEffort is the effective reasoning effort passed to Claude Code via
// --effort. It defaults to DefaultClaudeEffort and is overridable at startup
// via SetEffort.
var claudeEffort = DefaultClaudeEffort

// planEffort is the effective reasoning effort for the implement command's
// planning session. It defaults to DefaultPlanEffort and is overridable at
// startup via SetPlanEffort.
var planEffort = DefaultPlanEffort

// showOutput toggles live streaming of Claude Code output. When false
// (the default), runClaude buffers the result via --output-format json.
// When true, runClaude delegates to runClaudeStream which uses
// --output-format stream-json --verbose and surfaces assistant text and
// tool activity to a streamSink as it arrives.
var showOutput bool

// SetShowOutput installs b as the package-level streaming toggle used by
// every runClaude invocation. The CLI sets this once at startup; tests
// use the returned restore function to revert to the previous value.
func SetShowOutput(b bool) (restore func()) {
	old := showOutput
	showOutput = b
	return func() { showOutput = old }
}

// ShowOutput reports whether live streaming of Claude Code output is
// currently enabled. Exposed primarily for the CLI test suite to verify
// that PLANWERK_SHOW_CLAUDE_OUTPUT and --show-claude-output route into
// the package-level toggle.
func ShowOutput() bool { return showOutput }

// SetTimeout installs d as the per-invocation Claude Code timeout used by
// every subsequent runClaude / runClaudeStream call. A non-positive d is
// ignored and the previous value is preserved — that keeps a misconfigured
// flag from silently disabling the timeout. The returned restore function
// reverts to the previous value; the CLI test suite uses it to scope
// changes to a single test.
func SetTimeout(d time.Duration) (restore func()) {
	old := claudeTimeout
	if d > 0 {
		claudeTimeout = d
	}
	return func() { claudeTimeout = old }
}

// Timeout reports the currently effective per-invocation Claude Code
// timeout. Exposed primarily for the CLI test suite to verify that
// --claude-timeout / PLANWERK_CLAUDE_TIMEOUT route into the package-level
// value.
func Timeout() time.Duration { return claudeTimeout }

// SetModel installs m as the model passed to Claude Code via --model by every
// subsequent runClaude / runClaudeStream call. An empty m is ignored and the
// previous value is preserved — that keeps a misconfigured flag from silently
// selecting an empty model. The returned restore function reverts to the
// previous value; the CLI test suite uses it to scope changes to a single
// test.
func SetModel(m string) (restore func()) {
	old := claudeModel
	if m != "" {
		claudeModel = m
	}
	return func() { claudeModel = old }
}

// Model reports the currently effective model passed to Claude Code. Exposed
// primarily for the CLI test suite to verify that --claude-model /
// PLANWERK_CLAUDE_MODEL route into the package-level value.
func Model() string { return claudeModel }

// SetPlanModel installs m as the model used by Plan sessions (the implement
// command's planning phase). An empty m is ignored and the previous value is
// preserved — that keeps a misconfigured flag from silently selecting an
// empty model. The returned restore function reverts to the previous value;
// the CLI test suite uses it to scope changes to a single test.
func SetPlanModel(m string) (restore func()) {
	old := planModel
	if m != "" {
		planModel = m
	}
	return func() { planModel = old }
}

// PlanModel reports the currently effective planning model. Exposed
// primarily for the CLI test suite to verify that --plan-model /
// PLANWERK_PLAN_MODEL route into the package-level value.
func PlanModel() string { return planModel }

// SetEffort installs e as the reasoning effort passed to Claude Code via
// --effort by every subsequent runClaude / runClaudeStream call. An empty e
// is ignored and the previous value is preserved — that keeps a misconfigured
// flag from silently selecting an empty effort. The returned restore function
// reverts to the previous value; the CLI test suite uses it to scope changes
// to a single test.
func SetEffort(e string) (restore func()) {
	old := claudeEffort
	if e != "" {
		claudeEffort = e
	}
	return func() { claudeEffort = old }
}

// Effort reports the currently effective reasoning effort passed to Claude
// Code. Exposed primarily for the CLI test suite to verify that
// --claude-effort / PLANWERK_CLAUDE_EFFORT route into the package-level value.
func Effort() string { return claudeEffort }

// SetPlanEffort installs e as the reasoning effort used by Plan sessions (the
// implement command's planning phase). An empty e is ignored and the previous
// value is preserved — that keeps a misconfigured flag from silently selecting
// an empty effort. The returned restore function reverts to the previous
// value; the CLI test suite uses it to scope changes to a single test.
func SetPlanEffort(e string) (restore func()) {
	old := planEffort
	if e != "" {
		planEffort = e
	}
	return func() { planEffort = old }
}

// PlanEffort reports the currently effective planning reasoning effort.
// Exposed primarily for the CLI test suite to verify that --plan-effort /
// PLANWERK_PLAN_EFFORT route into the package-level value.
func PlanEffort() string { return planEffort }

// runClaude invokes claude in the given directory on its default permission
// mode and returns the extracted text response. Use it for the read-only
// analysis steps (review, audit, structure, repair, …) that do not mutate
// the checkout.
func runClaude(dir, prompt, label string) (string, error) {
	return runClaudeWithPermission(dir, prompt, label, "", claudeModel, claudeEffort)
}

// runClaudePlan is runClaude on the dedicated planning model (PlanModel,
// default "fable") at the dedicated planning effort (PlanEffort, default
// "max"). The implement command's planning session uses it: the session only
// reads the checkout and emits the implementation plan as text, so it keeps
// the default (read-only) permission mode while the strongest-reasoning model
// thinks at the largest budget — the one session where that depth steers the
// whole implementation.
func runClaudePlan(dir, prompt, label string) (string, error) {
	return runClaudeWithPermission(dir, prompt, label, "", planModel, planEffort)
}

// runClaudeAuto is runClaude with claudeAutoPermissionMode, letting the
// session edit files and run git/gh/test commands without an interactive
// approval. The implement command uses it: its orchestrated, one-shot
// `claude -p` session runs unattended inside a checkout and must commit,
// push a feature branch, and open a PR without a human confirming each
// step. The auto-mode classifier still vets every action.
func runClaudeAuto(dir, prompt, label string) (string, error) {
	return runClaudeWithPermission(dir, prompt, label, claudeAutoPermissionMode, claudeModel, claudeEffort)
}

// runClaudeWithPermission is the shared implementation behind runClaude,
// runClaudePlan, and runClaudeAuto. permissionMode, when non-empty, is
// passed to claude as --permission-mode; an empty value leaves claude on
// its default mode. model is the --model value and effort the --effort value
// (callers pass claudeModel/claudeEffort, or planModel/planEffort for the
// planning session). Every invocation also pre-approves claudeAllowedTools via
// --allowed-tools (see withAllowedTools). The label tags elapsed-time progress
// updates (or per-line stream prefixes when streaming is enabled).
//
// When showOutput is false it uses --output-format json and captures the
// full result via cmd.Output(). When showOutput is true it delegates to
// runClaudeStream, which uses --output-format stream-json --verbose and
// surfaces output incrementally; the periodic heartbeat is skipped in that
// mode because the stream itself is the heartbeat.
func runClaudeWithPermission(dir, prompt, label, permissionMode, model, effort string) (string, error) {
	if showOutput {
		return runClaudeStream(dir, prompt, label, permissionMode, model, effort)
	}

	ctx, cancel := context.WithTimeout(context.Background(), claudeTimeout)
	defer cancel()

	stopProgress := startProgress(label)
	defer stopProgress()

	args := []string{
		"-p",
		"--model", model,
		"--effort", effort,
		"--output-format", "json",
	}
	if permissionMode != "" {
		args = append(args, "--permission-mode", permissionMode)
	}
	args = withAllowedTools(args)
	cmd := exec.CommandContext(ctx, "claude", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdin = strings.NewReader(prompt)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("claude: %w\nstderr: %s", err, exitErr.Stderr)
		}
		return "", fmt.Errorf("claude: %w", err)
	}
	text, model, err := extractText(out)
	if err != nil {
		return "", err
	}
	// Record the exact model id the envelope reports so the artifact footers
	// (rendered after this call returns) name the model that produced them,
	// mirroring the streaming path in runClaudeStream.
	if model != "" {
		attribution.SetModel(model)
	}
	return text, nil
}

type claudeResponse struct {
	Result string `json:"result"`
	// Model is the resolved model id the CLI reports in the JSON envelope
	// (e.g. "claude-opus-4-8"). It is the non-streaming counterpart of the
	// streamEvent init model and feeds the attribution footers.
	Model string `json:"model,omitempty"`
}

// extractText extracts the response text and the resolved model id from
// Claude's JSON output envelope. When the output is not the expected envelope it
// returns an error wrapping the parse failure and a truncated copy of the raw
// output. Failing loudly keeps a changed CLI wire format (schema rename, error
// envelope, OAuth challenge) from being silently treated as the assistant's
// reply, which would otherwise produce nonsense findings or empty reports.
func extractText(raw []byte) (text, model string, err error) {
	var resp claudeResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", "", fmt.Errorf("claude: parse output envelope: %w; first 200 bytes: %q", err, head(raw, 200))
	}
	return resp.Result, resp.Model, nil
}

// head returns the first n bytes of b, or all of b when it is shorter. It
// bounds the raw output embedded in parse-failure errors so logs stay readable.
func head(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}

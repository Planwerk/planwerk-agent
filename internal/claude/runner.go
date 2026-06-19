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

// Client runs Claude Code sessions with a fixed configuration. Each Client
// owns its own timeout, model, and effort settings, so independent runners can
// execute concurrently without sharing mutable state — the injectable
// counterpart to the package-level configuration this type replaces. Construct
// one with NewClient and thread it through the runners.
type Client struct {
	timeout    time.Duration
	model      string
	planModel  string
	effort     string
	planEffort string
	showOutput bool
}

// Option configures a Client. Pass any number of options to NewClient; later
// options win when two set the same field.
type Option func(*Client)

// NewClient returns a Client seeded with the compiled-in defaults
// (DefaultClaudeTimeout/Model/Effort and the planning defaults), then applies
// opts. With no options it behaves exactly as the historical package defaults.
func NewClient(opts ...Option) *Client {
	c := &Client{
		timeout:    DefaultClaudeTimeout,
		model:      DefaultClaudeModel,
		planModel:  DefaultPlanModel,
		effort:     DefaultClaudeEffort,
		planEffort: DefaultPlanEffort,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// WithTimeout sets the per-invocation Claude Code timeout. A non-positive d is
// ignored and the default is preserved — that keeps a misconfigured flag from
// silently disabling the timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// WithModel sets the model passed to Claude Code via --model. An empty m is
// ignored so a misconfigured flag cannot select an empty model.
func WithModel(m string) Option {
	return func(c *Client) {
		if m != "" {
			c.model = m
		}
	}
}

// WithPlanModel sets the model used by Plan sessions (the implement command's
// planning phase). An empty m is ignored.
func WithPlanModel(m string) Option {
	return func(c *Client) {
		if m != "" {
			c.planModel = m
		}
	}
}

// WithEffort sets the reasoning effort passed to Claude Code via --effort. An
// empty e is ignored so a misconfigured flag cannot select an empty effort.
func WithEffort(e string) Option {
	return func(c *Client) {
		if e != "" {
			c.effort = e
		}
	}
}

// WithPlanEffort sets the reasoning effort used by Plan sessions (the implement
// command's planning phase). An empty e is ignored.
func WithPlanEffort(e string) Option {
	return func(c *Client) {
		if e != "" {
			c.planEffort = e
		}
	}
}

// WithShowOutput toggles live streaming of Claude Code output. When false (the
// default), runClaude buffers the result via --output-format json. When true,
// runClaude delegates to runClaudeStream which uses
// --output-format stream-json --verbose and surfaces assistant text and tool
// activity to a streamSink as it arrives.
func WithShowOutput(b bool) Option {
	return func(c *Client) { c.showOutput = b }
}

// runClaude invokes claude in the given directory on its default permission
// mode and returns the extracted text response. Use it for the read-only
// analysis steps (review, audit, structure, repair, …) that do not mutate
// the checkout.
func (c *Client) runClaude(dir, prompt, label string) (string, error) {
	return c.runClaudeWithPermission(dir, prompt, label, "", c.model, c.effort)
}

// runClaudePlan is runClaude on the dedicated planning model (planModel,
// default "fable") at the dedicated planning effort (planEffort, default
// "max"). The implement command's planning session uses it: the session only
// reads the checkout and emits the implementation plan as text, so it keeps
// the default (read-only) permission mode while the strongest-reasoning model
// thinks at the largest budget — the one session where that depth steers the
// whole implementation.
func (c *Client) runClaudePlan(dir, prompt, label string) (string, error) {
	return c.runClaudeWithPermission(dir, prompt, label, "", c.planModel, c.planEffort)
}

// runClaudeAuto is runClaude with claudeAutoPermissionMode, letting the
// session edit files and run git/gh/test commands without an interactive
// approval. The implement command uses it: its orchestrated, one-shot
// `claude -p` session runs unattended inside a checkout and must commit,
// push a feature branch, and open a PR without a human confirming each
// step. The auto-mode classifier still vets every action.
func (c *Client) runClaudeAuto(dir, prompt, label string) (string, error) {
	return c.runClaudeWithPermission(dir, prompt, label, claudeAutoPermissionMode, c.model, c.effort)
}

// runClaudeWithPermission is the shared implementation behind runClaude,
// runClaudePlan, and runClaudeAuto. permissionMode, when non-empty, is
// passed to claude as --permission-mode; an empty value leaves claude on
// its default mode. model is the --model value and effort the --effort value
// (callers pass c.model/c.effort, or c.planModel/c.planEffort for the
// planning session). Every invocation also pre-approves claudeAllowedTools via
// --allowed-tools (see withAllowedTools). The label tags elapsed-time progress
// updates (or per-line stream prefixes when streaming is enabled).
//
// When c.showOutput is false it uses --output-format json and captures the
// full result via cmd.Output(). When c.showOutput is true it delegates to
// runClaudeStream, which uses --output-format stream-json --verbose and
// surfaces output incrementally; the periodic heartbeat is skipped in that
// mode because the stream itself is the heartbeat.
func (c *Client) runClaudeWithPermission(dir, prompt, label, permissionMode, model, effort string) (string, error) {
	if c.showOutput {
		return c.runClaudeStream(dir, prompt, label, permissionMode, model, effort)
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
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

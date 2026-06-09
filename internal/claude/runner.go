package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
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
	// DefaultClaudeEffort is the compiled-in default reasoning effort. "max"
	// gives the model the largest thinking budget — reviews are
	// latency-tolerant and benefit from the extra reasoning on tricky
	// findings. Override with SetEffort (driven by the --claude-effort flag /
	// PLANWERK_CLAUDE_EFFORT env var).
	DefaultClaudeEffort = "max"
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

// claudeTimeout is the effective per-invocation timeout. It defaults to
// DefaultClaudeTimeout and is overridable at startup via SetTimeout.
var claudeTimeout = DefaultClaudeTimeout

// claudeModel is the effective model passed to Claude Code via --model. It
// defaults to DefaultClaudeModel and is overridable at startup via SetModel.
var claudeModel = DefaultClaudeModel

// claudeEffort is the effective reasoning effort passed to Claude Code via
// --effort. It defaults to DefaultClaudeEffort and is overridable at startup
// via SetEffort.
var claudeEffort = DefaultClaudeEffort

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

// runClaude invokes claude in the given directory on its default permission
// mode and returns the extracted text response. Use it for the read-only
// analysis steps (review, audit, structure, repair, …) that do not mutate
// the checkout.
func runClaude(dir, prompt, label string) (string, error) {
	return runClaudeWithPermission(dir, prompt, label, "")
}

// runClaudeAuto is runClaude with claudeAutoPermissionMode, letting the
// session edit files and run git/gh/test commands without an interactive
// approval. The implement command uses it: its orchestrated, one-shot
// `claude -p` session runs unattended inside a checkout and must commit,
// push a feature branch, and open a PR without a human confirming each
// step. The auto-mode classifier still vets every action.
func runClaudeAuto(dir, prompt, label string) (string, error) {
	return runClaudeWithPermission(dir, prompt, label, claudeAutoPermissionMode)
}

// runClaudeWithPermission is the shared implementation behind runClaude and
// runClaudeAuto. permissionMode, when non-empty, is passed to claude as
// --permission-mode; an empty value leaves claude on its default mode. The
// label tags elapsed-time progress updates (or per-line stream prefixes
// when streaming is enabled).
//
// When showOutput is false it uses --output-format json and captures the
// full result via cmd.Output(). When showOutput is true it delegates to
// runClaudeStream, which uses --output-format stream-json --verbose and
// surfaces output incrementally; the periodic heartbeat is skipped in that
// mode because the stream itself is the heartbeat.
func runClaudeWithPermission(dir, prompt, label, permissionMode string) (string, error) {
	if showOutput {
		return runClaudeStream(dir, prompt, label, permissionMode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), claudeTimeout)
	defer cancel()

	stopProgress := startProgress(label)
	defer stopProgress()

	args := []string{
		"-p",
		"--model", claudeModel,
		"--effort", claudeEffort,
		"--output-format", "json",
	}
	if permissionMode != "" {
		args = append(args, "--permission-mode", permissionMode)
	}
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
	return extractText(out)
}

type claudeResponse struct {
	Result string `json:"result"`
}

// extractText extracts the text content from Claude's JSON output envelope.
func extractText(raw []byte) (string, error) {
	var resp claudeResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		// Fall back to treating the entire output as the response text
		return string(raw), nil
	}
	return resp.Result, nil
}

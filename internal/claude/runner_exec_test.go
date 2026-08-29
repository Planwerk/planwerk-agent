package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClaude puts an executable named `claude` on PATH for the duration of the
// test. body is the shell script that stands in for the CLI, so a test can
// script an exact stdout envelope, stderr output, exit code, or a hang. Without
// it neither runner is ever exercised against a real process, which is how the
// timeout diagnosis and the usage accounting on a failed turn both went unseen.
func fakeClaude(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("writing fake claude: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// failureEnvelope is what Claude Code writes to stdout when a turn fails: the
// result envelope with is_error set, a usage block, and a non-zero exit.
const failureEnvelope = `{"is_error":true,"subtype":"error_max_turns",` +
	`"usage":{"input_tokens":1200,"output_tokens":300,"cache_read_input_tokens":40},` +
	`"total_cost_usd":0.42}`

func TestRunClaude_TimeoutNamesTheDeadline(t *testing.T) {
	fakeClaude(t, "sleep 30\n")
	c := NewClient(WithTimeout(200 * time.Millisecond))

	start := time.Now()
	_, _, err := c.runClaudeWithPermission(runSpec{label: "review", model: "opus", effort: "xhigh"}, "prompt")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected an error when the invocation outlives the timeout")
	}
	// The kill reaches `claude` but not the grandchild holding the stdout pipe,
	// so without WaitDelay this returns only when the 30s sleep ends.
	if elapsed > 10*time.Second {
		t.Errorf("returned after %s: the deadline did not bound the wait", elapsed)
	}
	if !strings.Contains(err.Error(), "timed out after") {
		t.Errorf("error = %q, want it to name the timeout rather than the kill signal", err)
	}
	if strings.Contains(err.Error(), "signal: killed") {
		t.Errorf("error = %q still reports the kill signal, which reads like a crashed binary", err)
	}
}

func TestRunClaudeStream_TimeoutNamesTheDeadline(t *testing.T) {
	fakeClaude(t, "sleep 30\n")
	streamSinkFn = func() streamSink { return slogStreamSink{} }
	t.Cleanup(func() { streamSinkFn = newDefaultStreamSink })
	c := NewClient(WithTimeout(200*time.Millisecond), WithShowOutput(true))

	start := time.Now()
	_, _, err := c.runClaudeWithPermission(runSpec{label: "review", model: "opus", effort: "xhigh"}, "prompt")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected an error when the streaming invocation outlives the timeout")
	}
	if elapsed > 10*time.Second {
		t.Errorf("returned after %s: the deadline did not bound the wait", elapsed)
	}
	if !strings.Contains(err.Error(), "timed out after") {
		t.Errorf("error = %q, want it to name the timeout", err)
	}
}

func TestRunClaude_CountsUsageOfAFailedTurn(t *testing.T) {
	fakeClaude(t, "cat >/dev/null\nprintf '%s' '"+failureEnvelope+"'\nexit 1\n")
	c := NewClient()

	_, _, err := c.runClaudeWithPermission(runSpec{label: "implement", model: "opus", effort: "xhigh"}, "prompt")
	if err == nil {
		t.Fatal("expected an error for a failed turn")
	}
	if !strings.Contains(err.Error(), "error_max_turns") {
		t.Errorf("error = %q, want the envelope's reason", err)
	}

	got := c.UsageTotals()
	if got.Calls != 1 {
		t.Errorf("Calls = %d, want 1 — a failed turn still spent its tokens", got.Calls)
	}
	if got.InputTokens != 1200 || got.OutputTokens != 300 || got.CacheReadTokens != 40 {
		t.Errorf("usage = %+v, want the envelope's counts", got)
	}
	if got.CostUSD != 0.42 {
		t.Errorf("CostUSD = %v, want 0.42", got.CostUSD)
	}
	if len(got.Passes) != 1 || got.Passes[0].Pass != "implement" {
		t.Errorf("passes = %+v, want the spend attributed to the implement pass", got.Passes)
	}
}

func TestRunClaudeStream_CountsUsageOfAFailedTurn(t *testing.T) {
	event := `{"type":"result","is_error":true,"subtype":"error_max_turns",` +
		`"usage":{"input_tokens":900,"output_tokens":100},"total_cost_usd":0.25}`
	fakeClaude(t, "cat >/dev/null\nprintf '%s\\n' '"+event+"'\nexit 1\n")
	streamSinkFn = func() streamSink { return slogStreamSink{} }
	t.Cleanup(func() { streamSinkFn = newDefaultStreamSink })
	c := NewClient(WithShowOutput(true))

	_, _, err := c.runClaudeWithPermission(runSpec{label: "fix", model: "opus", effort: "xhigh"}, "prompt")
	if err == nil {
		t.Fatal("expected an error for a failed turn")
	}
	got := c.UsageTotals()
	if got.Calls != 1 || got.InputTokens != 900 || got.OutputTokens != 100 || got.CostUSD != 0.25 {
		t.Errorf("usage = %+v, want the failed turn counted once with its own figures", got)
	}
}

// TestRunClaude_UsageUncountedWhenEnvelopeCarriesNone guards the other
// direction: a crash with no envelope must not record a phantom call.
func TestRunClaude_UsageUncountedWhenEnvelopeCarriesNone(t *testing.T) {
	fakeClaude(t, "cat >/dev/null\necho 'unknown flag' >&2\nexit 2\n")
	c := NewClient()

	if _, _, err := c.runClaudeWithPermission(runSpec{label: "review", model: "opus", effort: "xhigh"}, "prompt"); err == nil {
		t.Fatal("expected an error")
	}
	if got := c.UsageTotals(); got.Calls != 0 {
		t.Errorf("Calls = %d, want 0 — a crash with no usage block is not a billable call", got.Calls)
	}
}

// TestRunClaudeStream_KeepsStderrTail covers the pipe contract: exec must have
// finished copying stderr before Wait returns, or the tail — the whole
// diagnosis on the error path — can be lost.
func TestRunClaudeStream_KeepsStderrTail(t *testing.T) {
	fakeClaude(t, "cat >/dev/null\ni=0\nwhile [ $i -lt 200 ]; do echo \"stderr line $i\" >&2; i=$((i+1)); done\nexit 1\n")
	streamSinkFn = func() streamSink { return slogStreamSink{} }
	t.Cleanup(func() { streamSinkFn = newDefaultStreamSink })
	c := NewClient(WithShowOutput(true))

	_, _, err := c.runClaudeWithPermission(runSpec{label: "review", model: "opus", effort: "xhigh"}, "prompt")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "stderr line 199") {
		t.Errorf("error dropped the stderr tail: %q", err)
	}
}

// TestRunClaude_FeedsPromptOnStdin pins the invocation shape both runners
// share: the prompt travels on stdin, never as an argument.
func TestRunClaude_FeedsPromptOnStdin(t *testing.T) {
	dir := t.TempDir()
	seen := filepath.Join(dir, "stdin")
	fakeClaude(t, "cat > "+seen+"\nprintf '%s' '{\"result\":\"ok\",\"model\":\"claude-opus-5\"}'\n")
	c := NewClient()

	out, model, err := c.runClaudeWithPermission(runSpec{label: "review", model: "opus", effort: "xhigh"}, "the prompt body")
	if err != nil {
		t.Fatalf("runClaudeWithPermission: %v", err)
	}
	if out != "ok" || model != "claude-opus-5" {
		t.Errorf("out = %q, model = %q", out, model)
	}
	raw, err := os.ReadFile(seen)
	if err != nil {
		t.Fatalf("reading recorded stdin: %v", err)
	}
	if strings.TrimSpace(string(raw)) != "the prompt body" {
		t.Errorf("stdin = %q, want the prompt", raw)
	}
}

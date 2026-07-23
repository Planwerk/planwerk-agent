package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// streamMaxLineBytes caps a single NDJSON line from claude. The final
// `result` event carries the full assistant text and can grow into the
// megabytes for long reviews; bufio.Scanner's 64 KiB default would
// silently truncate it.
const streamMaxLineBytes = 16 * 1024 * 1024

// streamEvent is a tolerant view over the NDJSON lines emitted by
// `claude --output-format stream-json --verbose`. We decode only the
// fields we act on; unknown event types and extra fields are ignored
// so a future CLI version that adds events does not break the loop.
type streamEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype,omitempty"`
	// IsError marks a `result` event the CLI emitted for a failed turn (a hit
	// rate limit, an exhausted turn budget). handleStreamLine only reads it to
	// decide whether to keep the raw line around: the diagnosis itself is left
	// to envelopeFailure, which parses that line with the same decoder the
	// buffered runner uses on its envelope.
	IsError bool `json:"is_error,omitempty"`
	// Model is the resolved model id the CLI reports on the `system`/`init`
	// event (e.g. "claude-opus-4-8"). The orchestrator only ever passes a model
	// alias ("opus") via --model, so this event is the one place the exact id
	// becomes known; the runner records it for the attribution footers.
	Model   string `json:"model,omitempty"`
	Message struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
			Name string `json:"name,omitempty"`
		} `json:"content"`
	} `json:"message,omitempty"`
	Result string `json:"result,omitempty"`
	// StructuredOutput is the schema-validated object carried on the `result`
	// event when the session ran with --json-schema, the streaming counterpart of
	// the buffered envelope's structured_output. handleStreamLine prefers it over
	// Result when present so a structuring pass reads the constrained output; an
	// absent field stays nil and Result is used instead.
	StructuredOutput json.RawMessage `json:"structured_output,omitempty"`
	// Usage and TotalCostUSD are carried on the `result` event (the streaming
	// counterpart of the buffered envelope's top-level fields) and hold the
	// run's cumulative token counts and the CLI's own estimated cost. Both are
	// captured raw and decoded best-effort in handleStreamLine so a usage-schema
	// change on the result event — a reshaped usage object, a stringified cost —
	// never makes the line unparseable and discards the result text it shares. An
	// absent block stays nil, so a non-`result` event contributes nothing.
	Usage        json.RawMessage `json:"usage,omitempty"`
	TotalCostUSD json.RawMessage `json:"total_cost_usd,omitempty"`
}

// streamSink decides where streamed events are surfaced. Implementations
// choose between TTY-friendly stderr lines and structured slog records,
// mirroring the dual-mode behavior of startProgress.
type streamSink interface {
	starting(label string)
	text(label, s string)
	tool(label, name string)
	toolResult(label string)
}

// streamSinkFn returns the active sink. Overridable in tests.
var streamSinkFn = newDefaultStreamSink

func newDefaultStreamSink() streamSink {
	if stderrIsTerminalFn() {
		return ttyStreamSink{w: os.Stderr}
	}
	return slogStreamSink{}
}

type ttyStreamSink struct{ w io.Writer }

func (s ttyStreamSink) starting(label string) {
	progressMu.Lock()
	defer progressMu.Unlock()
	_, _ = fmt.Fprintf(s.w, "  [%s] streaming...\n", label)
}

func (s ttyStreamSink) text(label, t string) {
	t = strings.TrimRight(t, "\n")
	if t == "" {
		return
	}
	progressMu.Lock()
	defer progressMu.Unlock()
	for _, line := range strings.Split(t, "\n") {
		_, _ = fmt.Fprintf(s.w, "  [%s] %s\n", label, line)
	}
}

func (s ttyStreamSink) tool(label, name string) {
	progressMu.Lock()
	defer progressMu.Unlock()
	_, _ = fmt.Fprintf(s.w, "  [%s] tool: %s\n", label, name)
}

func (s ttyStreamSink) toolResult(label string) {
	progressMu.Lock()
	defer progressMu.Unlock()
	_, _ = fmt.Fprintf(s.w, "  [%s] tool_result\n", label)
}

type slogStreamSink struct{}

func (slogStreamSink) starting(label string) {
	slog.Info("claude stream", "label", label, "kind", "start")
}

func (slogStreamSink) text(label, t string) {
	t = strings.TrimRight(t, "\n")
	if t == "" {
		return
	}
	slog.Info("claude stream", "label", label, "kind", "text", "text", t)
}

func (slogStreamSink) tool(label, name string) {
	slog.Info("claude stream", "label", label, "kind", "tool_use", "tool", name)
}

func (slogStreamSink) toolResult(label string) {
	slog.Info("claude stream", "label", label, "kind", "tool_result")
}

// runClaudeStream invokes claude with --output-format stream-json --verbose
// and surfaces assistant text and tool activity through a streamSink as
// it arrives. The final assistant text is returned. The method is the
// streaming counterpart of runClaudeWithPermission and shares its timeout,
// effort, permission-mode, and isolation handling via the same runSpec shape:
// spec.permissionMode, when non-empty, is passed to claude as
// --permission-mode; spec.model/spec.effort are the --model/--effort values
// the caller selected; spec.readOnly denies the write tools on the analysis
// passes; spec.agentsJSON, when non-empty, carries the inline subagent
// definitions passed via --agents; spec.sessionID/spec.resume pin or resume
// the CLI session for the completion nudge. It routes through the same
// hermeticArgs, withReadOnlyDenied, withAllowedTools, withAgents, and
// withSession helpers as the buffered path so the two runners cannot drift on
// which flags an isolation-, tool-, or session-level decision emits.
func (c *Client) runClaudeStream(spec runSpec, prompt string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	args := []string{
		"-p",
		"--model", spec.model,
		"--effort", spec.effort,
		"--output-format", "stream-json",
		"--verbose",
	}
	if spec.permissionMode != "" {
		args = append(args, "--permission-mode", spec.permissionMode)
	}
	if spec.jsonSchema != "" {
		args = append(args, "--json-schema", spec.jsonSchema)
	}
	args = withSession(args, spec)
	args = withAgents(args, spec.agentsJSON)
	args = c.hermeticArgs(args)
	args = withReadOnlyDenied(args, spec.readOnly)
	args = withAllowedTools(args)
	cmd := exec.CommandContext(ctx, "claude", args...)
	if spec.dir != "" {
		cmd.Dir = spec.dir
	}
	cmd.Stdin = strings.NewReader(prompt)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", fmt.Errorf("claude stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", "", fmt.Errorf("claude stderr pipe: %w", err)
	}

	sink := streamSinkFn()
	sink.starting(spec.label)

	if err := cmd.Start(); err != nil {
		return "", "", fmt.Errorf("claude start: %w", err)
	}

	var (
		stderrBuf bytes.Buffer
		wg        sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&stderrBuf, stderr)
	}()

	// resolvedModel is the exact id the session reported on its init event;
	// it is returned so the caller threads it per-run into the artifact footers
	// instead of a package-level global, not the alias passed via --model.
	// failLine is the raw `result` event of a failed turn (nil otherwise), which
	// carries the reason the CLI never writes to stderr.
	finalResult, accText, resolvedModel, usage, cost, failLine, scanErr := readStream(stdout, spec.label, sink)

	waitErr := cmd.Wait()
	wg.Wait()

	if scanErr != nil {
		return "", "", fmt.Errorf("claude stream read: %w\nstderr: %s", scanErr, stderrBuf.String())
	}
	if waitErr != nil {
		return "", "", claudeRunError(waitErr, spec.model, failLine, stderrBuf.Bytes())
	}

	// Count the call once the stream read cleanly, before the empty-result
	// check, so it mirrors the buffered path (which counts on a successful
	// envelope parse regardless of whether the result text is empty).
	c.addUsage(usage, cost)

	if finalResult != "" {
		return finalResult, resolvedModel, nil
	}
	if accText != "" {
		return accText, resolvedModel, nil
	}
	return "", "", fmt.Errorf("claude stream produced no result\nstderr: %s", stderrBuf.String())
}

// readStream consumes NDJSON events from r until EOF, dispatching to
// sink as they arrive. It returns the final result string captured from
// a "result" event, the accumulated assistant text as a defensive
// fallback for schema drift, the resolved model id reported on the init
// event (empty if the stream never announced one), the cumulative token
// usage and estimated cost from the "result" event (zero if it carried
// none), the raw "result" event of a failed turn (nil when the turn
// succeeded), and any read error.
func readStream(r io.Reader, label string, sink streamSink) (final, acc, model string, usage tokenUsage, cost float64, failLine []byte, err error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), streamMaxLineBytes)

	var (
		finalBuf strings.Builder
		accBuf   strings.Builder
	)
	for scanner.Scan() {
		handleStreamLine(scanner.Bytes(), label, sink, &accBuf, &finalBuf, &model, &usage, &cost, &failLine)
	}
	if err := scanner.Err(); err != nil {
		return finalBuf.String(), accBuf.String(), model, usage, cost, failLine, err
	}
	return finalBuf.String(), accBuf.String(), model, usage, cost, failLine, nil
}

// handleStreamLine parses one NDJSON line and dispatches its events to
// the sink. Malformed lines are logged at debug level and skipped so a
// single bad line does not abort the stream. When the line carries the
// resolved model id (the `system`/`init` event), it is recorded in *model so
// the caller can stamp the attribution footers with the exact model. The
// `result` event's cumulative token usage and estimated cost are recorded in
// *usage and *cost when those pointers are non-nil, and a `result` event that
// reports a failed turn is copied verbatim into *failLine so the caller can
// diagnose the exit with envelopeFailure. The copy is taken only on failure —
// scanner.Bytes() is reused across lines, and a successful run must not pay for
// a second copy of a result that can reach megabytes.
func handleStreamLine(line []byte, label string, sink streamSink, accBuf, finalBuf *strings.Builder, model *string, usage *tokenUsage, cost *float64, failLine *[]byte) {
	if len(bytes.TrimSpace(line)) == 0 {
		return
	}
	var ev streamEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		slog.Debug("claude stream unparseable line", "label", label, "err", err)
		return
	}
	switch ev.Type {
	case "system":
		// The "starting..." line is emitted once before the loop; the
		// system init event itself is not surfaced to avoid double
		// announcing. The init event does carry the resolved model id,
		// which we record (first value wins) for the attribution footers.
		if model != nil && *model == "" && ev.Model != "" {
			*model = ev.Model
		}
	case "assistant":
		for _, c := range ev.Message.Content {
			switch c.Type {
			case "text":
				if c.Text == "" {
					continue
				}
				accBuf.WriteString(c.Text)
				accBuf.WriteString("\n")
				sink.text(label, c.Text)
			case "tool_use":
				if c.Name != "" {
					sink.tool(label, c.Name)
				}
			}
		}
	case "user":
		for _, c := range ev.Message.Content {
			if c.Type == "tool_result" {
				sink.toolResult(label)
			}
		}
	case "result":
		// A failed turn carries its reason here and nowhere else — the CLI leaves
		// stderr empty. Keep the raw line so runClaudeStream can render it.
		if ev.IsError && failLine != nil {
			*failLine = bytes.Clone(line)
		}
		// Prefer the schema-validated structured_output when the session ran with
		// --json-schema; fall back to the plain result text otherwise.
		if final := preferStructuredOutput(ev.StructuredOutput, ev.Result); final != "" {
			finalBuf.Reset()
			finalBuf.WriteString(final)
		}
		// Decode usage and cost best-effort so a reshaped or stringified block on
		// the result event degrades the figures to zero rather than aborting the
		// line and dropping the result text captured just above.
		if usage != nil && ev.Usage != nil {
			var u tokenUsage
			if json.Unmarshal(ev.Usage, &u) == nil {
				*usage = u
			}
		}
		if cost != nil && ev.TotalCostUSD != nil {
			var v float64
			if json.Unmarshal(ev.TotalCostUSD, &v) == nil {
				*cost = v
			}
		}
	default:
		slog.Debug("claude stream unknown event", "label", label, "type", ev.Type)
	}
}

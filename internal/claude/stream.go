package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
)

// streamMaxLineBytes caps a single NDJSON line from claude. The final
// `result` event carries the full assistant text and can grow into the
// megabytes for long reviews, so the cap sits far above bufio's 64 KiB
// default. readStream skips a longer line instead of failing the session
// (decision 152).
const streamMaxLineBytes = 16 * 1024 * 1024

// streamReadBufferBytes is the buffer readStream reads through. A line that
// fits in it is handed to handleStreamLine without a copy; a longer one is
// assembled in a slice of its own, up to streamMaxLineBytes.
const streamReadBufferBytes = 64 * 1024

// errStreamLineTooLong is what readStreamLine reports for a line longer than
// its limit, once it has drained that line, so the caller can skip it and read
// on.
var errStreamLineTooLong = errors.New("claude stream line over the limit")

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
	// to envelopeFailure, which parses that line with claudeResponse.
	IsError bool `json:"is_error,omitempty"`
	// Model is the resolved model id the CLI reports on the `system`/`init`
	// event (e.g. "claude-opus-5-5"). The orchestrator only ever passes a model
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
	// event when the session ran with --json-schema. handleStreamLine prefers it
	// over Result when present so a session run under --json-schema reads the
	// constrained output; an absent field stays nil and Result is used instead.
	StructuredOutput json.RawMessage `json:"structured_output,omitempty"`
	// Usage and TotalCostUSD are carried on the `result` event and hold the
	// run's cumulative token counts and the CLI's own estimated cost. Both are
	// captured raw and decoded best-effort in handleStreamLine so a usage-schema
	// change on the result event — a reshaped usage object, a stringified cost —
	// never makes the line unparseable and discards the result text it shares. An
	// absent block stays nil, so a non-`result` event contributes nothing.
	Usage        json.RawMessage `json:"usage,omitempty"`
	TotalCostUSD json.RawMessage `json:"total_cost_usd,omitempty"`
	// SessionID is the id of the session the event belongs to; every event
	// carries it, the `system`/`init` event first. Tools is the init event's
	// list of the tool names the session loaded. NumTurns and PermissionDenials
	// are carried on the `result` event: the turns the invocation took, and one
	// {tool_name, tool_use_id, tool_input} entry per tool call Claude Code's
	// permission check refused. All four are captured raw and decoded
	// best-effort in handleStreamLine, for the reason Usage is (decision 152).
	SessionID         json.RawMessage `json:"session_id,omitempty"`
	Tools             json.RawMessage `json:"tools,omitempty"`
	NumTurns          json.RawMessage `json:"num_turns,omitempty"`
	PermissionDenials json.RawMessage `json:"permission_denials,omitempty"`
	// Plugins is the init event's list of the plugins the session loaded,
	// each an object whose name warnUnloadedPlugins checks against the
	// plugins the session was given; captured raw for the reason Tools is
	// (decision 154).
	Plugins json.RawMessage `json:"plugins,omitempty"`
}

// sessionStats records how one `claude -p` invocation spent its turns: its
// tool calls by tool name (a subagent's included), the turns and the
// permission denials its `result` event reports, and its session id.
// runClaudeStream folds it into the pass with addSessionStats (decision 152).
type sessionStats struct {
	toolUses          map[string]int
	numTurns          int
	permissionDenials int
	sessionID         string
}

// addToolUse counts one call of the tool name, creating the map on the first
// call.
func (s *sessionStats) addToolUse(name string) {
	if s.toolUses == nil {
		s.toolUses = make(map[string]int)
	}
	s.toolUses[name]++
}

// isZero reports whether s recorded nothing, so addSessionStats creates no
// pass entry for it.
func (s sessionStats) isZero() bool {
	return len(s.toolUses) == 0 && s.numTurns == 0 && s.permissionDenials == 0 && s.sessionID == ""
}

// streamOutcome is what readStream collected from one session's events: the
// final result text, whether a `result` event arrived at all, the accumulated
// assistant text as a defensive fallback for a stream that carried none, the
// resolved model id from the init event (empty if the stream never announced
// one), the cumulative token usage and estimated cost from the `result` event
// (zero if it carried none), the raw `result` event of a failed turn (nil when
// the turn succeeded), the tool names and the plugin names the init event
// listed (nil when no init event carried the list), and the session's stats.
type streamOutcome struct {
	final, acc    strings.Builder
	sawResult     bool
	model         string
	usage         tokenUsage
	cost          float64
	failLine      []byte
	loadedTools   []string
	loadedPlugins []string
	stats         sessionStats
}

// streamSink decides where streamed events are surfaced. Implementations
// choose between TTY-friendly stderr lines and structured slog records,
// mirroring the dual-mode behavior of startProgress, or drop them.
type streamSink interface {
	starting(label string)
	text(label, s string)
	tool(label, name string)
	toolResult(label string)
}

// newDefaultStreamSink returns the TTY sink when stderr is a terminal and the
// slog sink otherwise.
func newDefaultStreamSink() streamSink {
	if stderrIsTerminal() {
		return ttyStreamSink{w: os.Stderr}
	}
	return slogStreamSink{}
}

// streamSinkFor returns the sink runClaudeStream surfaces a session's events
// through: newDefaultStreamSink under --show-claude-output, and
// quietStreamSink otherwise, where the heartbeat reports progress instead.
func (c *Client) streamSinkFor() streamSink {
	if c.showOutput {
		return newDefaultStreamSink()
	}
	return quietStreamSink{}
}

// quietStreamSink drops every event. A session run without
// --show-claude-output is still read event by event for its result and its
// sessionStats; only the printing is left out.
type quietStreamSink struct{}

func (quietStreamSink) starting(string)     {}
func (quietStreamSink) text(string, string) {}
func (quietStreamSink) tool(string, string) {}
func (quietStreamSink) toolResult(string)   {}

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

// runClaudeStream is the claude CLI runner: it invokes claude with
// --output-format stream-json --verbose, reads every event through
// readStream, and returns the final assistant text and the resolved model id.
// A turn that ended cleanly with a `result` event returns that event's text
// even when it is empty, so the caller's own gate judges it (the completion
// nudge resumes a session that ended its turn without a word). Only a stream
// with no `result` event falls back to the accumulated assistant text, and
// fails when there is none. The sink comes from streamSinkFor, so
// --show-claude-output decides only whether the events are printed; without
// it the elapsed-time heartbeat runs instead. Call runSession, not this
// method: runSession applies sessionFn, resolves a noTools spec's directory,
// normalizes jsonSchema, and pins the session id before delegating here. The
// argv comes from claudeCommand, so every flag a runSpec field selects is
// emitted there. On every exit path, the timeout and a failed turn included,
// the session's stats are folded into the pass with addSessionStats, with
// spec.sessionID standing in when the stream named no session, and a tool of
// spec.tools the init event did not list is logged by warnUnloadedTools
// (decision 152), and a plugin the session was given but did not load by
// warnUnloadedPlugins (decision 154).
func (c *Client) runClaudeStream(spec runSpec, prompt string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	cmd := c.claudeCommand(ctx, spec, prompt)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", fmt.Errorf("claude stdout pipe: %w", err)
	}
	// stderr is buffered by exec itself rather than through StderrPipe: the
	// pipe's contract forbids calling Wait before every read from it has
	// completed, and Wait closes the read end as soon as the child exits, so a
	// copier still draining could lose the tail — precisely on the error path
	// where stderr is the whole diagnosis. Handing exec a writer makes Wait
	// wait for the copy instead.
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	sink := c.streamSinkFor()
	sink.starting(spec.label)
	if !c.showOutput {
		stopProgress := startProgress(spec.label)
		defer stopProgress()
	}

	if err := cmd.Start(); err != nil {
		return "", "", fmt.Errorf("claude start: %w", err)
	}

	// out.model is the exact id the session reported on its init event; it is
	// returned so the caller threads it per-run into the artifact footers
	// instead of a package-level global, not the alias passed via --model.
	// out.failLine is the raw `result` event of a failed turn (nil otherwise),
	// which carries the reason the CLI never writes to stderr.
	out, readErr := readStream(stdout, spec.label, sink)
	if out.stats.sessionID == "" {
		out.stats.sessionID = spec.sessionID
	}
	c.addSessionStats(spec.label, out.stats)
	warnUnloadedTools(slog.Default(), spec.label, spec.tools, out.loadedTools)
	warnUnloadedPlugins(slog.Default(), spec.label, c.sessionPlugins(spec.readOnly || spec.noTools), out.loadedPlugins)

	waitErr := cmd.Wait()

	// The deadline is checked before the read error: when it fires, WaitDelay
	// closes the pipes underneath the reader, so readErr would otherwise
	// report "file already closed" and bury the real reason.
	if timeout := c.timeoutError(ctx, spec.model, stderrBuf.Bytes()); timeout != nil {
		c.addFailureUsage(spec.label, out.failLine)
		return "", "", timeout
	}
	if readErr != nil {
		return "", "", fmt.Errorf("claude stream read: %w\nstderr: %s", readErr, stderrBuf.String())
	}
	if waitErr != nil {
		// A failed turn still spent its tokens: the `result` event carries the
		// usage block, so count it before reporting.
		c.addFailureUsage(spec.label, out.failLine)
		return "", "", claudeRunError(waitErr, spec.model, out.failLine, stderrBuf.Bytes())
	}

	// Count the call once the stream read cleanly, before the empty-result
	// check, so a call that returned no text is still a billed call.
	c.addUsage(spec.label, out.usage, out.cost)

	if out.sawResult {
		return out.final.String(), out.model, nil
	}
	if acc := out.acc.String(); acc != "" {
		return acc, out.model, nil
	}
	return "", "", fmt.Errorf("claude stream produced no result\nstderr: %s", stderrBuf.String())
}

// warnUnloadedTools logs one warning on logger for each name in want that
// loaded, the tool list of the session's init event, does not carry under that
// name or its toolAlias: a --finder-tools entry Claude Code did not load, such
// as a misspelt name. A nil loaded means the stream announced no list, and
// nothing is checked; an empty want checks nothing either (decision 152).
func warnUnloadedTools(logger *slog.Logger, label string, want, loaded []string) {
	if loaded == nil {
		return
	}
	for _, name := range want {
		if !slices.Contains(loaded, name) && !slices.Contains(loaded, toolAlias(name)) {
			logger.Warn("requested tool not loaded by the session", "label", label, "tool", name)
		}
	}
}

// toolAlias returns the other name Claude Code knows a built-in tool by, and
// name itself for a tool that has none. The init event of Claude Code 2.1.306
// and 2.1.310 lists the Agent tool under its legacy name, Task, so a requested
// Agent is loaded when the list carries Task, and the other way round.
func toolAlias(name string) string {
	switch name {
	case "Agent":
		return "Task"
	case "Task":
		return "Agent"
	}
	return name
}

// readStream consumes NDJSON events from r until EOF, dispatching each line
// to handleStreamLine, and returns what it collected. The outcome is never
// nil, so a caller can read the stats of a session whose read failed. A line
// longer than streamMaxLineBytes is skipped with a warning instead of
// aborting the read; if that line was the `result` event, the caller falls
// back to the accumulated assistant text (decision 152). The error is a read
// error of r, nil at EOF.
func readStream(r io.Reader, label string, sink streamSink) (*streamOutcome, error) {
	br := bufio.NewReaderSize(r, streamReadBufferBytes)
	out := &streamOutcome{}
	for {
		line, err := readStreamLine(br, streamMaxLineBytes)
		switch {
		case errors.Is(err, errStreamLineTooLong):
			slog.Warn("claude stream line over the limit skipped", "label", label, "limit", streamMaxLineBytes)
			continue
		case errors.Is(err, io.EOF):
			return out, nil
		case err != nil:
			return out, err
		}
		handleStreamLine(line, label, sink, out)
	}
}

// readStreamLine returns the next line of br without its line ending. A line
// that fits in br's buffer is returned as a slice of it, valid only until the
// next read; a longer one is assembled in a slice of its own. A line longer
// than limit bytes is read to its end without being kept and reported as
// errStreamLineTooLong, so memory stays bounded by limit. The last line is
// returned even without a trailing newline, and io.EOF follows it.
func readStreamLine(br *bufio.Reader, limit int) ([]byte, error) {
	var (
		long []byte // the chunks of a line longer than br's buffer
		size int    // the bytes of the line read so far, its newline included
	)
	for {
		chunk, err := br.ReadSlice('\n')
		size += len(chunk)
		if errors.Is(err, bufio.ErrBufferFull) {
			if size <= limit {
				long = append(long, chunk...)
			}
			continue
		}
		if err != nil && (!errors.Is(err, io.EOF) || size == 0) {
			return nil, err
		}
		if bytes.HasSuffix(chunk, []byte("\n")) {
			size--
		}
		if size > limit {
			return nil, errStreamLineTooLong
		}
		if long != nil {
			long = append(long, chunk...)
			chunk = long
		}
		chunk = bytes.TrimSuffix(chunk, []byte("\n"))
		return bytes.TrimSuffix(chunk, []byte("\r")), nil
	}
}

// handleStreamLine parses one NDJSON line, dispatches its events to the sink,
// and records what it carries in out. Malformed lines are logged at debug
// level and skipped so a single bad line does not abort the stream. The first
// session_id that decodes is kept in out.stats. The `system` event's resolved
// model id is recorded in out.model (first value wins) so the caller can stamp
// the attribution footers with the exact model, and the `init` event's tool
// list in out.loadedTools and its plugin names in out.loadedPlugins. Every
// `tool_use` block of an `assistant` event is counted in out.stats, a
// subagent's included. A `result` event sets
// out.sawResult, and its text, token usage, estimated cost, turn count and
// number of permission denials are recorded best-effort, each set only on a
// clean decode, and a later `result` event overwrites an earlier one. A
// `result` event that reports a failed turn is copied verbatim into
// out.failLine so the caller can diagnose the exit with envelopeFailure. The
// copy is taken only on failure — the line is a slice of the reader's buffer,
// reused across lines, and a successful run must not pay for a second copy of
// a result that can reach megabytes (decision 152).
func handleStreamLine(line []byte, label string, sink streamSink, out *streamOutcome) {
	if len(bytes.TrimSpace(line)) == 0 {
		return
	}
	var ev streamEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		slog.Debug("claude stream unparseable line", "label", label, "err", err)
		return
	}
	if out.stats.sessionID == "" && ev.SessionID != nil {
		var id string
		if json.Unmarshal(ev.SessionID, &id) == nil {
			out.stats.sessionID = id
		}
	}
	switch ev.Type {
	case "system":
		// The "starting..." line is emitted once before the loop; the
		// system init event itself is not surfaced to avoid double
		// announcing. The init event does carry the resolved model id,
		// which we record (first value wins) for the attribution footers.
		if out.model == "" && ev.Model != "" {
			out.model = ev.Model
		}
		if ev.Subtype == "init" && out.loadedTools == nil && ev.Tools != nil {
			var tools []string
			if json.Unmarshal(ev.Tools, &tools) == nil {
				out.loadedTools = tools
			}
		}
		if ev.Subtype == "init" && out.loadedPlugins == nil && ev.Plugins != nil {
			var plugins []struct {
				Name string `json:"name"`
			}
			// A null list decodes to a nil slice and counts as no list, as
			// it does for Tools.
			if json.Unmarshal(ev.Plugins, &plugins) == nil && plugins != nil {
				out.loadedPlugins = make([]string, 0, len(plugins))
				for _, p := range plugins {
					out.loadedPlugins = append(out.loadedPlugins, p.Name)
				}
			}
		}
	case "assistant":
		for _, c := range ev.Message.Content {
			switch c.Type {
			case "text":
				if c.Text == "" {
					continue
				}
				out.acc.WriteString(c.Text)
				out.acc.WriteString("\n")
				sink.text(label, c.Text)
			case "tool_use":
				if c.Name != "" {
					out.stats.addToolUse(c.Name)
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
		out.sawResult = true
		// A failed turn carries its reason here and nowhere else — the CLI leaves
		// stderr empty. Keep the raw line so runClaudeStream can render it.
		if ev.IsError {
			out.failLine = bytes.Clone(line)
		}
		// Prefer the schema-validated structured_output when the session ran with
		// --json-schema; fall back to the plain result text otherwise.
		if final := preferStructuredOutput(ev.StructuredOutput, ev.Result); final != "" {
			out.final.Reset()
			out.final.WriteString(final)
		}
		// Decode the figures best-effort so a reshaped or stringified field on
		// the result event degrades it to zero rather than aborting the line and
		// dropping the result text captured just above.
		if ev.Usage != nil {
			var u tokenUsage
			if json.Unmarshal(ev.Usage, &u) == nil {
				out.usage = u
			}
		}
		if ev.TotalCostUSD != nil {
			var v float64
			if json.Unmarshal(ev.TotalCostUSD, &v) == nil {
				out.cost = v
			}
		}
		if ev.NumTurns != nil {
			var n int
			if json.Unmarshal(ev.NumTurns, &n) == nil {
				out.stats.numTurns = n
			}
		}
		if ev.PermissionDenials != nil {
			var denials []json.RawMessage
			if json.Unmarshal(ev.PermissionDenials, &denials) == nil {
				out.stats.permissionDenials = len(denials)
			}
		}
	default:
		slog.Debug("claude stream unknown event", "label", label, "type", ev.Type)
	}
}

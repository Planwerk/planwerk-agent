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

	"github.com/planwerk/planwerk-review/internal/attribution"
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
// it arrives. The final assistant text is returned. The function is the
// streaming counterpart of runClaudeWithPermission and shares its timeout,
// effort, and permission-mode handling: permissionMode, when non-empty, is
// passed to claude as --permission-mode; model is the --model value and
// effort the --effort value the caller selected (claudeModel/claudeEffort,
// or planModel/planEffort for the planning session).
func runClaudeStream(dir, prompt, label, permissionMode, model, effort string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), claudeTimeout)
	defer cancel()

	args := []string{
		"-p",
		"--model", model,
		"--effort", effort,
		"--output-format", "stream-json",
		"--verbose",
	}
	if permissionMode != "" {
		args = append(args, "--permission-mode", permissionMode)
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdin = strings.NewReader(prompt)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("claude stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("claude stderr pipe: %w", err)
	}

	sink := streamSinkFn()
	sink.starting(label)

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("claude start: %w", err)
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

	finalResult, accText, resolvedModel, scanErr := readStream(stdout, label, sink)

	// Record the exact model id the session reported so the artifact footers
	// (rendered after this call returns) name the model that produced them,
	// not the alias the orchestrator passed via --model.
	if resolvedModel != "" {
		attribution.SetModel(resolvedModel)
	}

	waitErr := cmd.Wait()
	wg.Wait()

	if scanErr != nil {
		return "", fmt.Errorf("claude stream read: %w\nstderr: %s", scanErr, stderrBuf.String())
	}
	if waitErr != nil {
		return "", fmt.Errorf("claude: %w\nstderr: %s", waitErr, stderrBuf.String())
	}

	if finalResult != "" {
		return finalResult, nil
	}
	if accText != "" {
		return accText, nil
	}
	return "", fmt.Errorf("claude stream produced no result\nstderr: %s", stderrBuf.String())
}

// readStream consumes NDJSON events from r until EOF, dispatching to
// sink as they arrive. It returns the final result string captured from
// a "result" event, the accumulated assistant text as a defensive
// fallback for schema drift, the resolved model id reported on the init
// event (empty if the stream never announced one), and any read error.
func readStream(r io.Reader, label string, sink streamSink) (final, acc, model string, err error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), streamMaxLineBytes)

	var (
		finalBuf strings.Builder
		accBuf   strings.Builder
	)
	for scanner.Scan() {
		handleStreamLine(scanner.Bytes(), label, sink, &accBuf, &finalBuf, &model)
	}
	if err := scanner.Err(); err != nil {
		return finalBuf.String(), accBuf.String(), model, err
	}
	return finalBuf.String(), accBuf.String(), model, nil
}

// handleStreamLine parses one NDJSON line and dispatches its events to
// the sink. Malformed lines are logged at debug level and skipped so a
// single bad line does not abort the stream. When the line carries the
// resolved model id (the `system`/`init` event), it is recorded in *model so
// the caller can stamp the attribution footers with the exact model.
func handleStreamLine(line []byte, label string, sink streamSink, accBuf, finalBuf *strings.Builder, model *string) {
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
		if ev.Result != "" {
			finalBuf.Reset()
			finalBuf.WriteString(ev.Result)
		}
	default:
		slog.Debug("claude stream unknown event", "label", label, "type", ev.Type)
	}
}

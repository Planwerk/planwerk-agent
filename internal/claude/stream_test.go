package claude

import (
	"encoding/json"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

// recordingSink captures every event a streamSink would receive so tests
// can assert what was surfaced to the user.
type recordingSink struct {
	mu     sync.Mutex
	events []string
}

func (s *recordingSink) record(kind string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, kind)
}

func (s *recordingSink) starting(label string)   { s.record("start:" + label) }
func (s *recordingSink) text(label, t string)    { s.record("text:" + label + ":" + t) }
func (s *recordingSink) tool(label, name string) { s.record("tool:" + label + ":" + name) }
func (s *recordingSink) toolResult(label string) { s.record("tool_result:" + label) }

func (s *recordingSink) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.events))
	copy(out, s.events)
	return out
}

func TestReadStream_DispatchesAssistantTextAndCapturesResult(t *testing.T) {
	const stream = `{"type":"system","subtype":"init"}
{"type":"assistant","message":{"content":[{"type":"text","text":"Reviewing..."}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read"}]}}
{"type":"user","message":{"content":[{"type":"tool_result"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Done."}]}}
{"type":"result","subtype":"success","result":"final review text"}
`
	sink := &recordingSink{}
	out, err := readStream(strings.NewReader(stream), "review", sink)
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if final := out.final.String(); final != "final review text" {
		t.Errorf("final = %q, want %q", final, "final review text")
	}
	if acc := out.acc.String(); !strings.Contains(acc, "Reviewing...") || !strings.Contains(acc, "Done.") {
		t.Errorf("acc should accumulate assistant text, got %q", acc)
	}

	got := sink.snapshot()
	want := []string{
		"text:review:Reviewing...",
		"tool:review:Read",
		"tool_result:review",
		"text:review:Done.",
	}
	if len(got) != len(want) {
		t.Fatalf("event count = %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestReadStream_PrefersStructuredOutputOnResultEvent(t *testing.T) {
	// With --json-schema the result event carries structured_output; it must win
	// over the plain result text (the buffered path's mirror).
	const stream = `{"type":"system","subtype":"init"}
{"type":"result","result":"prose fallback","structured_output":{"findings":null,"summary":"s"}}
`
	out, err := readStream(strings.NewReader(stream), "structure", &recordingSink{})
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if final := out.final.String(); final != `{"findings":null,"summary":"s"}` {
		t.Errorf("final = %q, want the structured_output object", final)
	}
}

func TestReadStream_CapturesResolvedModelFromInitEvent(t *testing.T) {
	const stream = `{"type":"system","subtype":"init","model":"claude-opus-5-5"}
{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}
{"type":"result","result":"ok"}
`
	out, err := readStream(strings.NewReader(stream), "review", &recordingSink{})
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if out.model != "claude-opus-5-5" {
		t.Errorf("resolved model = %q, want %q", out.model, "claude-opus-5-5")
	}
}

func TestReadStream_ResolvedModelEmptyWhenInitOmitsIt(t *testing.T) {
	const stream = `{"type":"system","subtype":"init"}
{"type":"result","result":"ok"}
`
	out, err := readStream(strings.NewReader(stream), "review", &recordingSink{})
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if out.model != "" {
		t.Errorf("resolved model = %q, want empty when the init event omits it", out.model)
	}
}

func TestReadStream_CapturesUsageFromResultEvent(t *testing.T) {
	const stream = `{"type":"system","subtype":"init"}
{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}
{"type":"result","subtype":"success","result":"ok","usage":{"input_tokens":3180,"output_tokens":4,"cache_read_input_tokens":18090,"cache_creation_input_tokens":0},"total_cost_usd":0.025612}
`
	out, err := readStream(strings.NewReader(stream), "review", &recordingSink{})
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	want := tokenUsage{InputTokens: 3180, OutputTokens: 4, CacheReadTokens: 18090, CacheCreationTokens: 0}
	if out.usage != want {
		t.Errorf("usage = %+v, want %+v", out.usage, want)
	}
	if out.cost != 0.025612 {
		t.Errorf("cost = %v, want 0.025612", out.cost)
	}
}

func TestReadStream_UsageZeroWhenResultEventOmitsIt(t *testing.T) {
	// A result event without a usage object must leave the totals at zero
	// rather than fail — older or newer CLI versions may not emit usage.
	const stream = `{"type":"result","result":"ok"}
`
	out, err := readStream(strings.NewReader(stream), "review", &recordingSink{})
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if (out.usage != tokenUsage{}) {
		t.Errorf("usage = %+v, want zero", out.usage)
	}
	if out.cost != 0 {
		t.Errorf("cost = %v, want 0", out.cost)
	}
}

// TestReadStream_UsageDriftDoesNotDropResultText locks in that a usage-schema
// change on the result event — here a usage object reshaped into an array and a
// cost emitted as a string — does not make the line unparseable. The result text
// shares that line and must survive; usage and cost degrade to zero. Before usage
// and cost were decoded raw and best-effort, the typed fields made the whole line
// fail to parse, so it was skipped as malformed and the result text was lost.
func TestReadStream_UsageDriftDoesNotDropResultText(t *testing.T) {
	const stream = `{"type":"result","result":"final review text","usage":[1,2,3],"total_cost_usd":"oops"}
`
	out, err := readStream(strings.NewReader(stream), "review", &recordingSink{})
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if final := out.final.String(); final != "final review text" {
		t.Errorf("final = %q, want %q", final, "final review text")
	}
	if (out.usage != tokenUsage{}) {
		t.Errorf("usage = %+v, want zero", out.usage)
	}
	if out.cost != 0 {
		t.Errorf("cost = %v, want 0", out.cost)
	}
}

func TestReadStream_FallsBackToAccumulatedTextWhenNoResultEvent(t *testing.T) {
	const stream = `{"type":"assistant","message":{"content":[{"type":"text","text":"partial"}]}}
`
	sink := &recordingSink{}
	out, err := readStream(strings.NewReader(stream), "review", sink)
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if final := out.final.String(); final != "" {
		t.Errorf("final should be empty without a result event, got %q", final)
	}
	if out.sawResult {
		t.Error("sawResult set for a stream without a result event")
	}
	if acc := out.acc.String(); !strings.Contains(acc, "partial") {
		t.Errorf("acc should contain assistant text, got %q", acc)
	}
}

// TestReadStream_RecordsAResultEventWithoutText covers a turn that ended with
// an empty result: the event still counts as the session's result, so
// runClaudeStream returns its empty text rather than the assistant text that
// came before it.
func TestReadStream_RecordsAResultEventWithoutText(t *testing.T) {
	const stream = `{"type":"assistant","message":{"content":[{"type":"text","text":"narration"}]}}
{"type":"result","subtype":"success","result":""}
`
	out, err := readStream(strings.NewReader(stream), "implement", &recordingSink{})
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if !out.sawResult {
		t.Error("sawResult not set for a result event without text")
	}
	if final := out.final.String(); final != "" {
		t.Errorf("final = %q, want the result event's empty text", final)
	}
}

func TestReadStream_TolerantOfMalformedAndUnknownLines(t *testing.T) {
	const stream = `not valid json
{"type":"future_event","payload":42}
{"type":"assistant","message":{"content":[{"type":"text","text":"survived"}]}}
{"type":"result","result":"ok"}
`
	sink := &recordingSink{}
	out, err := readStream(strings.NewReader(stream), "review", sink)
	if err != nil {
		t.Fatalf("readStream should not fail on malformed/unknown lines: %v", err)
	}
	if final := out.final.String(); final != "ok" {
		t.Errorf("final = %q, want %q", final, "ok")
	}
	got := sink.snapshot()
	if len(got) != 1 || got[0] != "text:review:survived" {
		t.Errorf("malformed/unknown lines should be skipped silently; got %v", got)
	}
}

func TestReadStream_LaterResultEventOverwritesEarlier(t *testing.T) {
	const stream = `{"type":"result","result":"first"}
{"type":"result","result":"second"}
`
	out, err := readStream(strings.NewReader(stream), "review", &recordingSink{})
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if final := out.final.String(); final != "second" {
		t.Errorf("final = %q, want %q (later event must win)", final, "second")
	}
}

func TestHandleStreamLine_EmptyAndWhitespaceLinesAreIgnored(t *testing.T) {
	sink := &recordingSink{}
	out := &streamOutcome{}
	handleStreamLine([]byte(""), "review", sink, out)
	handleStreamLine([]byte("   \t  "), "review", sink, out)
	if out.acc.Len() != 0 || out.final.Len() != 0 {
		t.Errorf("blank lines should not modify buffers (acc=%q, final=%q)", out.acc.String(), out.final.String())
	}
	if len(sink.snapshot()) != 0 {
		t.Errorf("blank lines should produce no sink events, got %v", sink.snapshot())
	}
}

func TestHandleStreamLine_SkipsAssistantTextWithEmptyString(t *testing.T) {
	sink := &recordingSink{}
	out := &streamOutcome{}
	const line = `{"type":"assistant","message":{"content":[{"type":"text","text":""}]}}`
	handleStreamLine([]byte(line), "review", sink, out)
	if out.acc.Len() != 0 {
		t.Errorf("empty assistant text must not be accumulated, got %q", out.acc.String())
	}
	if len(sink.snapshot()) != 0 {
		t.Errorf("empty assistant text must not produce sink events, got %v", sink.snapshot())
	}
}

// TestReadStream_CapturesFailedResultLine locks in the streaming counterpart of
// the envelope diagnosis: a `result` event that reports a failed turn is kept
// verbatim, so runClaudeStream can render the reason the CLI never wrote to
// stderr through envelopeFailure.
func TestReadStream_CapturesFailedResultLine(t *testing.T) {
	stream := `{"type":"system","subtype":"init","model":"claude-fable-5-1"}
{"type":"result","subtype":"success","is_error":true,"api_error_status":429,"result":"You've reached your Fable 5 limit."}
`
	out, err := readStream(strings.NewReader(stream), "plan", &recordingSink{})
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if out.failLine == nil {
		t.Fatal("readStream did not capture the failed result event")
	}
	if got, want := envelopeFailure(out.failLine), "api error 429: You've reached your Fable 5 limit."; got != want {
		t.Errorf("envelopeFailure(failLine) = %q, want %q", got, want)
	}
}

// TestReadStream_SuccessfulTurnCapturesNoFailure documents the other half: a
// successful stream pays for no copy of its result event, which can reach
// megabytes, and reports no failure to render.
func TestReadStream_SuccessfulTurnCapturesNoFailure(t *testing.T) {
	stream := `{"type":"result","subtype":"success","is_error":false,"result":"## Implementation Plan (issue #585)"}
`
	out, err := readStream(strings.NewReader(stream), "plan", &recordingSink{})
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if out.failLine != nil {
		t.Errorf("readStream captured a failure line for a successful turn: %s", out.failLine)
	}
	if final := out.final.String(); final != "## Implementation Plan (issue #585)" {
		t.Errorf("final = %q, want the plan text", final)
	}
}

// TestReadStream_CountsToolUsesByName locks in the tool histogram: every
// tool_use block counts under its name, two in one message included, and the
// sink still sees each call.
func TestReadStream_CountsToolUsesByName(t *testing.T) {
	const stream = `{"type":"system","subtype":"init"}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read"},{"type":"tool_use","name":"Grep"}]}}
{"type":"user","message":{"content":[{"type":"tool_result"},{"type":"tool_result"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read"}]}}
{"type":"result","result":"ok"}
`
	sink := &recordingSink{}
	out, err := readStream(strings.NewReader(stream), "review", sink)
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if want := map[string]int{"Read": 2, "Grep": 1}; !maps.Equal(out.stats.toolUses, want) {
		t.Errorf("toolUses = %v, want %v", out.stats.toolUses, want)
	}
	want := []string{"tool:review:Read", "tool:review:Grep", "tool_result:review", "tool_result:review", "tool:review:Read"}
	if got := sink.snapshot(); !slices.Equal(got, want) {
		t.Errorf("sink events = %v, want %v", got, want)
	}
}

// TestReadStream_RecordsTurnsDenialsAndSessionID covers the result event's
// figures: num_turns, the number of permission_denials entries, and the
// session id the init event announced, beside the text and usage as before.
func TestReadStream_RecordsTurnsDenialsAndSessionID(t *testing.T) {
	const stream = `{"type":"system","subtype":"init","session_id":"s-1"}
{"type":"result","result":"ok","num_turns":7,"session_id":"s-1",` +
		`"permission_denials":[{"tool_name":"Bash","tool_use_id":"t1","tool_input":{"command":"git push"}},{"tool_name":"Write","tool_use_id":"t2","tool_input":{}}],` +
		`"usage":{"input_tokens":10,"output_tokens":2},"total_cost_usd":0.5}
`
	out, err := readStream(strings.NewReader(stream), "review", &recordingSink{})
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if want := (sessionStats{numTurns: 7, permissionDenials: 2, sessionID: "s-1"}); !reflect.DeepEqual(out.stats, want) {
		t.Errorf("stats = %+v, want %+v", out.stats, want)
	}
	if final := out.final.String(); final != "ok" {
		t.Errorf("final = %q, want ok", final)
	}
	if want := (tokenUsage{InputTokens: 10, OutputTokens: 2}); out.usage != want || out.cost != 0.5 {
		t.Errorf("usage = %+v, cost = %v, want %+v and 0.5", out.usage, out.cost, want)
	}
}

// TestReadStream_SessionStatsDriftKeepsTheResult mirrors
// TestReadStream_UsageDriftDoesNotDropResultText for the session figures: a
// stringified num_turns, an object where the denial list belongs and a numeric
// session id leave every stat at zero, and the result text survives.
func TestReadStream_SessionStatsDriftKeepsTheResult(t *testing.T) {
	const stream = `{"type":"result","result":"the findings","num_turns":"7","permission_denials":{},"session_id":42}
`
	out, err := readStream(strings.NewReader(stream), "review", &recordingSink{})
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if final := out.final.String(); final != "the findings" {
		t.Errorf("final = %q, want the result text", final)
	}
	if !out.stats.isZero() {
		t.Errorf("stats = %+v, want zero", out.stats)
	}
}

// TestReadStream_RecordsTheInitToolList covers the list warnUnloadedTools checks
// against: the init event's tools, and nil when the stream carries no init
// event.
func TestReadStream_RecordsTheInitToolList(t *testing.T) {
	out, err := readStream(strings.NewReader(`{"type":"system","subtype":"init","tools":["Read","Grep"]}
{"type":"result","result":"ok"}
`), "review", &recordingSink{})
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if want := []string{"Read", "Grep"}; !slices.Equal(out.loadedTools, want) {
		t.Errorf("loadedTools = %v, want %v", out.loadedTools, want)
	}

	out, err = readStream(strings.NewReader(`{"type":"result","result":"ok"}
`), "review", &recordingSink{})
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if out.loadedTools != nil {
		t.Errorf("loadedTools = %v, want nil without an init event", out.loadedTools)
	}
}

// toolResultLine returns a user tool_result event padded to exactly n bytes.
func toolResultLine(t *testing.T, n int) string {
	t.Helper()
	const prefix, suffix = `{"type":"user","message":{"content":[{"type":"tool_result","content":"`, `"}]}}`
	pad := n - len(prefix) - len(suffix)
	if pad < 0 {
		t.Fatalf("a %d-byte line cannot hold the event", n)
	}
	return prefix + strings.Repeat("x", pad) + suffix
}

// TestReadStream_SkipsALineOverTheLimit locks in that a line past
// streamMaxLineBytes, a tool result as large as a big pull request's diff, is
// skipped instead of failing the session, while a line of exactly the limit is
// still read.
func TestReadStream_SkipsALineOverTheLimit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		size     int
		wantSeen bool
	}{
		{"a line of the limit is read", streamMaxLineBytes, true},
		{"a line over the limit is skipped", streamMaxLineBytes + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := toolResultLine(t, tc.size) + "\n" + `{"type":"result","result":"ok"}` + "\n"
			sink := &recordingSink{}
			out, err := readStream(strings.NewReader(stream), "review", sink)
			if err != nil {
				t.Fatalf("readStream: %v", err)
			}
			if final := out.final.String(); final != "ok" {
				t.Errorf("final = %q, want the result after the long line", final)
			}
			if seen := slices.Contains(sink.snapshot(), "tool_result:review"); seen != tc.wantSeen {
				t.Errorf("tool_result reached the sink = %v, want %v", seen, tc.wantSeen)
			}
		})
	}
}

// TestReadStream_AssemblesALineLongerThanTheBuffer covers a line between the
// read buffer and the limit, the size of a long review's result event, and a
// last line without its newline: both are read whole.
func TestReadStream_AssemblesALineLongerThanTheBuffer(t *testing.T) {
	text := strings.Repeat("y", 3*streamReadBufferBytes)
	stream := `{"type":"system","subtype":"init","model":"claude-opus-5-5"}` + "\r\n" +
		`{"type":"result","result":"` + text + `"}`
	out, err := readStream(strings.NewReader(stream), "review", &recordingSink{})
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if out.model != "claude-opus-5-5" {
		t.Errorf("model = %q, want the id of the CRLF-terminated init line", out.model)
	}
	if final := out.final.String(); final != text {
		t.Errorf("final has %d bytes, want the %d-byte result", len(final), len(text))
	}
}

// TestWarnUnloadedTools pins the check of a --finder-tools list against the
// session's init event: one warning per requested name the session did not
// load under that name or its alias, and none without a list to check against
// or a request to check.
func TestWarnUnloadedTools(t *testing.T) {
	for _, tc := range []struct {
		name         string
		want, loaded []string
		wantWarnings []string
	}{
		{"a misspelt name warns once", []string{"Grep", "Raed"}, []string{"Grep", "Read"}, []string{"Raed"}},
		{"Agent is loaded under its legacy name Task", []string{"Agent", "Grep"}, []string{"Grep", "Task"}, nil},
		{"Task is loaded under the name Agent", []string{"Task"}, []string{"Agent", "Grep"}, nil},
		{"Agent without either name warns", []string{"Agent"}, []string{"Grep", "Read"}, []string{"Agent"}},
		{"no init list checks nothing", []string{"Grep", "Raed"}, nil, nil},
		{"no request checks nothing", nil, []string{"Grep", "Read"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf strings.Builder
			warnUnloadedTools(slog.New(slog.NewTextHandler(&buf, nil)), "review", tc.want, tc.loaded)
			lines := strings.FieldsFunc(buf.String(), func(r rune) bool { return r == '\n' })
			if len(lines) != len(tc.wantWarnings) {
				t.Fatalf("logged %d lines, want %d: %q", len(lines), len(tc.wantWarnings), buf.String())
			}
			for i, name := range tc.wantWarnings {
				if !strings.Contains(lines[i], "level=WARN") || !strings.Contains(lines[i], "tool="+name) || !strings.Contains(lines[i], "label=review") {
					t.Errorf("line %d = %q, want a warning naming %s for the review", i, lines[i], name)
				}
			}
		})
	}
}

// TestPreferStructuredOutput covers the choice between the result event's two
// texts: the schema-validated structured_output wins, and a literal null counts
// as absent.
func TestPreferStructuredOutput(t *testing.T) {
	for _, tc := range []struct {
		name       string
		structured string
		result     string
		want       string
	}{
		{"structured_output preferred over result", `{"findings":null,"summary":"s"}`, "prose fallback", `{"findings":null,"summary":"s"}`},
		{"null structured_output falls back to result", "null", "the answer", "the answer"},
		{"absent structured_output falls back to result", "", "the answer", "the answer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := preferStructuredOutput(json.RawMessage(tc.structured), tc.result); got != tc.want {
				t.Errorf("preferStructuredOutput = %q, want %q", got, tc.want)
			}
		})
	}
}

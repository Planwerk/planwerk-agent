package claude

import (
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
	final, acc, _, _, _, err := readStream(strings.NewReader(stream), "review", sink)
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if final != "final review text" {
		t.Errorf("final = %q, want %q", final, "final review text")
	}
	if !strings.Contains(acc, "Reviewing...") || !strings.Contains(acc, "Done.") {
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

func TestReadStream_CapturesResolvedModelFromInitEvent(t *testing.T) {
	const stream = `{"type":"system","subtype":"init","model":"claude-opus-4-8"}
{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}
{"type":"result","result":"ok"}
`
	_, _, model, _, _, err := readStream(strings.NewReader(stream), "review", &recordingSink{})
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if model != "claude-opus-4-8" {
		t.Errorf("resolved model = %q, want %q", model, "claude-opus-4-8")
	}
}

func TestReadStream_ResolvedModelEmptyWhenInitOmitsIt(t *testing.T) {
	const stream = `{"type":"system","subtype":"init"}
{"type":"result","result":"ok"}
`
	_, _, model, _, _, err := readStream(strings.NewReader(stream), "review", &recordingSink{})
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if model != "" {
		t.Errorf("resolved model = %q, want empty when the init event omits it", model)
	}
}

func TestReadStream_CapturesUsageFromResultEvent(t *testing.T) {
	const stream = `{"type":"system","subtype":"init"}
{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}
{"type":"result","subtype":"success","result":"ok","usage":{"input_tokens":3180,"output_tokens":4,"cache_read_input_tokens":18090,"cache_creation_input_tokens":0},"total_cost_usd":0.025612}
`
	_, _, _, usage, cost, err := readStream(strings.NewReader(stream), "review", &recordingSink{})
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	want := tokenUsage{InputTokens: 3180, OutputTokens: 4, CacheReadTokens: 18090, CacheCreationTokens: 0}
	if usage != want {
		t.Errorf("usage = %+v, want %+v", usage, want)
	}
	if cost != 0.025612 {
		t.Errorf("cost = %v, want 0.025612", cost)
	}
}

func TestReadStream_UsageZeroWhenResultEventOmitsIt(t *testing.T) {
	// A result event without a usage object must leave the totals at zero
	// rather than fail — older or newer CLI versions may not emit usage.
	const stream = `{"type":"result","result":"ok"}
`
	_, _, _, usage, cost, err := readStream(strings.NewReader(stream), "review", &recordingSink{})
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if (usage != tokenUsage{}) {
		t.Errorf("usage = %+v, want zero", usage)
	}
	if cost != 0 {
		t.Errorf("cost = %v, want 0", cost)
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
	final, _, _, usage, cost, err := readStream(strings.NewReader(stream), "review", &recordingSink{})
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if final != "final review text" {
		t.Errorf("final = %q, want %q", final, "final review text")
	}
	if (usage != tokenUsage{}) {
		t.Errorf("usage = %+v, want zero", usage)
	}
	if cost != 0 {
		t.Errorf("cost = %v, want 0", cost)
	}
}

func TestReadStream_FallsBackToAccumulatedTextWhenNoResultEvent(t *testing.T) {
	const stream = `{"type":"assistant","message":{"content":[{"type":"text","text":"partial"}]}}
`
	sink := &recordingSink{}
	final, acc, _, _, _, err := readStream(strings.NewReader(stream), "review", sink)
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if final != "" {
		t.Errorf("final should be empty without a result event, got %q", final)
	}
	if !strings.Contains(acc, "partial") {
		t.Errorf("acc should contain assistant text, got %q", acc)
	}
}

func TestReadStream_TolerantOfMalformedAndUnknownLines(t *testing.T) {
	const stream = `not valid json
{"type":"future_event","payload":42}
{"type":"assistant","message":{"content":[{"type":"text","text":"survived"}]}}
{"type":"result","result":"ok"}
`
	sink := &recordingSink{}
	final, _, _, _, _, err := readStream(strings.NewReader(stream), "review", sink)
	if err != nil {
		t.Fatalf("readStream should not fail on malformed/unknown lines: %v", err)
	}
	if final != "ok" {
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
	final, _, _, _, _, err := readStream(strings.NewReader(stream), "review", &recordingSink{})
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if final != "second" {
		t.Errorf("final = %q, want %q (later event must win)", final, "second")
	}
}

func TestHandleStreamLine_EmptyAndWhitespaceLinesAreIgnored(t *testing.T) {
	sink := &recordingSink{}
	var acc, final strings.Builder
	handleStreamLine([]byte(""), "review", sink, &acc, &final, nil, nil, nil)
	handleStreamLine([]byte("   \t  "), "review", sink, &acc, &final, nil, nil, nil)
	if acc.Len() != 0 || final.Len() != 0 {
		t.Errorf("blank lines should not modify buffers (acc=%q, final=%q)", acc.String(), final.String())
	}
	if len(sink.snapshot()) != 0 {
		t.Errorf("blank lines should produce no sink events, got %v", sink.snapshot())
	}
}

func TestHandleStreamLine_SkipsAssistantTextWithEmptyString(t *testing.T) {
	sink := &recordingSink{}
	var acc, final strings.Builder
	const line = `{"type":"assistant","message":{"content":[{"type":"text","text":""}]}}`
	handleStreamLine([]byte(line), "review", sink, &acc, &final, nil, nil, nil)
	if acc.Len() != 0 {
		t.Errorf("empty assistant text must not be accumulated, got %q", acc.String())
	}
	if len(sink.snapshot()) != 0 {
		t.Errorf("empty assistant text must not produce sink events, got %v", sink.snapshot())
	}
}

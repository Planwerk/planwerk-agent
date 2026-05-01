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

func (s *recordingSink) starting(label string)     { s.record("start:" + label) }
func (s *recordingSink) text(label, t string)      { s.record("text:" + label + ":" + t) }
func (s *recordingSink) tool(label, name string)   { s.record("tool:" + label + ":" + name) }
func (s *recordingSink) toolResult(label string)   { s.record("tool_result:" + label) }

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
	final, acc, err := readStream(strings.NewReader(stream), "review", sink)
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

func TestReadStream_FallsBackToAccumulatedTextWhenNoResultEvent(t *testing.T) {
	const stream = `{"type":"assistant","message":{"content":[{"type":"text","text":"partial"}]}}
`
	sink := &recordingSink{}
	final, acc, err := readStream(strings.NewReader(stream), "review", sink)
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
	final, _, err := readStream(strings.NewReader(stream), "review", sink)
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
	final, _, err := readStream(strings.NewReader(stream), "review", &recordingSink{})
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
	handleStreamLine([]byte(""), "review", sink, &acc, &final)
	handleStreamLine([]byte("   \t  "), "review", sink, &acc, &final)
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
	handleStreamLine([]byte(line), "review", sink, &acc, &final)
	if acc.Len() != 0 {
		t.Errorf("empty assistant text must not be accumulated, got %q", acc.String())
	}
	if len(sink.snapshot()) != 0 {
		t.Errorf("empty assistant text must not produce sink events, got %v", sink.snapshot())
	}
}

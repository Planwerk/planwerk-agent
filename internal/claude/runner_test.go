package claude

import "testing"

func TestExtractText_ParsesResultAndModel(t *testing.T) {
	raw := []byte(`{"type":"result","result":"the answer","model":"claude-opus-4-8"}`)
	text, model := extractText(raw)
	if text != "the answer" {
		t.Errorf("text = %q, want %q", text, "the answer")
	}
	if model != "claude-opus-4-8" {
		t.Errorf("model = %q, want %q", model, "claude-opus-4-8")
	}
}

func TestExtractText_ModelEmptyWhenEnvelopeOmitsIt(t *testing.T) {
	raw := []byte(`{"result":"just text"}`)
	text, model := extractText(raw)
	if text != "just text" {
		t.Errorf("text = %q, want %q", text, "just text")
	}
	if model != "" {
		t.Errorf("model = %q, want empty when the envelope omits it", model)
	}
}

func TestExtractText_FallsBackToRawOnNonEnvelope(t *testing.T) {
	raw := []byte("not json at all")
	text, model := extractText(raw)
	if text != "not json at all" {
		t.Errorf("text = %q, want the raw output verbatim", text)
	}
	if model != "" {
		t.Errorf("model = %q, want empty on a non-envelope payload", model)
	}
}

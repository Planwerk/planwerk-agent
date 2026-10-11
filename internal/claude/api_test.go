package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"
)

// fakeAPI is a local stand-in for the Messages and Models endpoints the
// structuring backend calls. messages answers POST /v1/messages; the request
// bodies it saw and the number of model listings are recorded.
type fakeAPI struct {
	models   []map[string]any
	messages func(w http.ResponseWriter, body map[string]any)

	mu       sync.Mutex
	bodies   []map[string]any
	listings int
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		f.mu.Lock()
		f.listings++
		f.mu.Unlock()
		writeJSON(w, map[string]any{"data": f.models, "has_more": false})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/models/"):
		id := strings.TrimPrefix(r.URL.Path, "/v1/models/")
		for _, m := range f.models {
			if m["id"] == id {
				writeJSON(w, m)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, apiErrorBody("not_found_error", "model: "+id))
	case r.Method == http.MethodPost && r.URL.Path == "/v1/messages":
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.bodies = append(f.bodies, body)
		f.mu.Unlock()
		f.messages(w, body)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func apiErrorBody(typ, msg string) map[string]any {
	return map[string]any{"type": "error", "error": map[string]any{"type": typ, "message": msg}}
}

// testModel is one Models API record: line, lifecycle, release date, output
// limit, and the effort levels it accepts (none means no effort support).
func testModel(id, line, lifecycle, created string, maxTokens int, efforts ...string) map[string]any {
	effort := map[string]any{"supported": len(efforts) > 0}
	for _, level := range []string{"low", "medium", "high", "xhigh", "max"} {
		effort[level] = map[string]any{"supported": false}
	}
	for _, level := range efforts {
		effort[level] = map[string]any{"supported": true}
	}
	return map[string]any{
		"id": id, "type": "model", "display_name": id, "line": line, "lifecycle": lifecycle,
		"created_at": created, "max_tokens": maxTokens, "max_input_tokens": 200000,
		"capabilities": map[string]any{"effort": effort},
	}
}

// streamText answers a messages request with one text block streamed as
// server-sent events, ending with stopReason.
func streamText(model, text, stopReason string) func(http.ResponseWriter, map[string]any) {
	return func(w http.ResponseWriter, _ map[string]any) {
		w.Header().Set("Content-Type", "text/event-stream")
		textJSON, _ := json.Marshal(text)
		events := []struct{ name, data string }{
			{"message_start", fmt.Sprintf(`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":%q,"content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":120,"output_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`, model)},
			{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			{"content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%s}}`, textJSON)},
			{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			{"message_delta", fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q,"stop_sequence":null},"usage":{"output_tokens":30}}`, stopReason)},
			{"message_stop", `{"type":"message_stop"}`},
		}
		for _, e := range events {
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.name, e.data)
		}
	}
}

// failWith answers a messages request with an API error.
func failWith(status int, typ, msg string) func(http.ResponseWriter, map[string]any) {
	return func(w http.ResponseWriter, _ map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(apiErrorBody(typ, msg))
	}
}

func newFakeStructurer(t *testing.T, f *fakeAPI) *apiStructurer {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return newAPIStructurer("test-key", option.WithBaseURL(srv.URL), option.WithMaxRetries(0))
}

const (
	apiHaikuID  = "claude-haiku-5-5"
	apiSonnetID = "claude-sonnet-5-5"
	fromCLI     = "from cli"
)

var testModels = []map[string]any{
	testModel("claude-haiku-4-5", "haiku", "active", "2025-10-01T00:00:00Z", 64000),
	testModel(apiHaikuID, "haiku", "active", "2026-09-01T00:00:00Z", 32000, "low", "medium", "high", "xhigh", "max"),
	testModel("claude-haiku-9", "haiku", "deprecated", "2026-10-01T00:00:00Z", 64000, "high"),
	testModel(apiSonnetID, "sonnet", "active", "2026-08-01T00:00:00Z", 128000, "low", "medium", "high", "xhigh", "max"),
}

// TestAPIStructurer_ResolvesAnAliasToTheNewestActiveModelOfItsLine covers the
// alias a Claude Code flag carries: "haiku" runs on the newest active haiku,
// not on an older one nor on a newer deprecated one, with that model's output
// limit and the requested effort, and the listing runs once per run.
func TestAPIStructurer_ResolvesAnAliasToTheNewestActiveModelOfItsLine(t *testing.T) {
	t.Parallel()
	f := &fakeAPI{models: testModels, messages: streamText(apiHaikuID, `{"ok":true}`, "end_turn")}
	a := newFakeStructurer(t, f)

	for range 2 {
		text, model, usage, err := a.run(context.Background(), "haiku", "xhigh", "cast this")
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if text != `{"ok":true}` || model != apiHaikuID {
			t.Errorf("text/model = %q/%q, want the streamed text from claude-haiku-5-5", text, model)
		}
		if usage.InputTokens != 120 || usage.OutputTokens != 30 {
			t.Errorf("usage = %+v, want 120 in / 30 out", usage)
		}
	}
	if f.listings != 1 {
		t.Errorf("listed the models %d times, want once per run", f.listings)
	}
	body := f.bodies[0]
	if body["model"] != apiHaikuID {
		t.Errorf("model = %v, want claude-haiku-5-5", body["model"])
	}
	if body["max_tokens"] != float64(32000) {
		t.Errorf("max_tokens = %v, want the model's own limit 32000", body["max_tokens"])
	}
	if cfg, _ := body["output_config"].(map[string]any); cfg["effort"] != "xhigh" {
		t.Errorf("output_config = %v, want effort xhigh", body["output_config"])
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 1 || !strings.Contains(fmt.Sprint(msgs[0]), "cast this") {
		t.Errorf("messages = %v, want the prompt as the one user message", msgs)
	}
}

// TestAPIStructurer_SendsNoEffortAModelDoesNotAccept covers a full model id
// whose model lacks the effort capability: the request goes out without
// output_config, which such a model would reject, and max_tokens keeps the cap.
func TestAPIStructurer_SendsNoEffortAModelDoesNotAccept(t *testing.T) {
	t.Parallel()
	f := &fakeAPI{models: testModels, messages: streamText(apiSonnetID, "{}", "end_turn")}
	a := newFakeStructurer(t, f)

	if _, _, _, err := a.run(context.Background(), "claude-haiku-4-5", "xhigh", "p"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, ok := f.bodies[0]["output_config"]; ok {
		t.Errorf("output_config = %v, want none for a model without effort", f.bodies[0]["output_config"])
	}
	if _, _, _, err := a.run(context.Background(), apiSonnetID, "medium", "p"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if f.bodies[1]["max_tokens"] != float64(apiMaxTokens) {
		t.Errorf("max_tokens = %v, want the cap %d", f.bodies[1]["max_tokens"], apiMaxTokens)
	}
	if f.listings != 0 {
		t.Errorf("listed the models %d times, want none for full ids", f.listings)
	}
}

// TestAPIStructurer_ErrorsWhenTheResponseIsIncomplete covers the two stop
// reasons whose text is not a whole transcription: the caller must get an
// error and run the prompt through Claude Code.
func TestAPIStructurer_ErrorsWhenTheResponseIsIncomplete(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{"refusal", "max_tokens"} {
		f := &fakeAPI{models: testModels, messages: streamText(apiSonnetID, `{"cut`, reason)}
		a := newFakeStructurer(t, f)
		_, _, usage, err := a.run(context.Background(), "sonnet", "high", "p")
		if err == nil || !strings.Contains(err.Error(), reason) {
			t.Errorf("%s: err = %v, want one naming the stop reason", reason, err)
		}
		if usage.OutputTokens == 0 {
			t.Errorf("%s: usage = %+v, want the spent tokens counted", reason, usage)
		}
	}
}

// TestAPIErrorPersists classifies real SDK errors: the ones every later call
// would repeat switch the API off for the run, a passing failure does not.
func TestAPIErrorPersists(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		answer  func(http.ResponseWriter, map[string]any)
		persist bool
	}{
		{"credit balance", failWith(400, "invalid_request_error", "Your credit balance is too low to access the Anthropic API."), true},
		{"billing", failWith(402, "billing_error", "payment required"), true},
		{"bad key", failWith(401, "authentication_error", "invalid x-api-key"), true},
		{"forbidden", failWith(403, "permission_error", "not allowed"), true},
		{"other bad request", failWith(400, "invalid_request_error", "prompt is too long"), false},
		{"overloaded", failWith(529, "overloaded_error", "overloaded"), false},
	} {
		f := &fakeAPI{models: testModels, messages: tc.answer}
		a := newFakeStructurer(t, f)
		_, _, _, err := a.run(context.Background(), apiSonnetID, "high", "p")
		if err == nil {
			t.Fatalf("%s: run succeeded, want an error", tc.name)
		}
		if got := apiErrorPersists(err); got != tc.persist {
			t.Errorf("%s: apiErrorPersists = %v, want %v (err: %v)", tc.name, got, tc.persist, err)
		}
	}
	if apiErrorPersists(errors.New("connection reset")) {
		t.Error("a transport error must not switch the API off")
	}
}

// TestRunClaudeStructure_PrefersTheAPI covers a structuring call the API
// serves: no Claude Code session runs, and the tokens land on the pass under
// its API label.
func TestRunClaudeStructure_PrefersTheAPI(t *testing.T) {
	t.Parallel()
	c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
		return fromCLI, "", nil
	}, WithStructureModel("haiku"), WithStructureEffort("medium"))
	c.structureAPI = func(_ context.Context, model, effort, prompt string) (string, string, tokenUsage, error) {
		if model != "haiku" || effort != "medium" || prompt != "prompt" {
			t.Errorf("api got %q/%q/%q, want the structure tier and the prompt", model, effort, prompt)
		}
		return "from api", apiHaikuID, tokenUsage{InputTokens: 5, OutputTokens: 2}, nil
	}

	text, model, err := c.runClaudeStructure("prompt", "capture-structure")
	if err != nil || text != "from api" || model != apiHaikuID {
		t.Fatalf("runClaudeStructure = %q, %q, %v; want the API's answer", text, model, err)
	}
	if len(*calls) != 0 {
		t.Errorf("ran %d Claude Code sessions, want none", len(*calls))
	}
	passes := c.UsageTotals().Passes
	if len(passes) != 1 || passes[0].Pass != "capture-structure (api)" || passes[0].InputTokens != 5 {
		t.Errorf("passes = %+v, want the tokens under %q", passes, "capture-structure (api)")
	}
}

// TestRunClaudeStructure_FallsBackToClaudeCode covers the two kinds of API
// failure: a passing one falls back for that call and tries the API again on
// the next, one every later call would repeat falls back for the rest of the
// run.
func TestRunClaudeStructure_FallsBackToClaudeCode(t *testing.T) {
	t.Parallel()
	persistent := &fakeAPI{models: testModels, messages: failWith(400, "invalid_request_error", "Your credit balance is too low to access the Anthropic API.")}
	transient := &fakeAPI{models: testModels, messages: failWith(529, "overloaded_error", "overloaded")}
	for _, tc := range []struct {
		name     string
		api      *fakeAPI
		apiCalls int
	}{
		{"transient", transient, 3},
		{"persistent", persistent, 1},
	} {
		c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
			return fromCLI, apiSonnetID, nil
		}, WithStructureModel(apiSonnetID))
		c.structureAPI = newFakeStructurer(t, tc.api).run

		for range 3 {
			text, _, err := c.runClaudeStructure("prompt", "gap-structure")
			if err != nil || text != fromCLI {
				t.Fatalf("%s: runClaudeStructure = %q, %v; want the Claude Code answer", tc.name, text, err)
			}
		}
		if len(*calls) != 3 {
			t.Errorf("%s: ran %d Claude Code sessions, want 3", tc.name, len(*calls))
		}
		if len(tc.api.bodies) != tc.apiCalls {
			t.Errorf("%s: called the API %d times, want %d", tc.name, len(tc.api.bodies), tc.apiCalls)
		}
	}
}

// TestWithStructureAPI_EmptyKeyKeepsClaudeCode pins that an empty key leaves
// the structuring tier on Claude Code.
func TestWithStructureAPI_EmptyKeyKeepsClaudeCode(t *testing.T) {
	t.Parallel()
	if NewClient(WithStructureAPI("")).structureAPI != nil {
		t.Error("an empty key must not configure the API")
	}
	if NewClient(WithStructureAPI("key")).structureAPI == nil {
		t.Error("a key must configure the API")
	}
}

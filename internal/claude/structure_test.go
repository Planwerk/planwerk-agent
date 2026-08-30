package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/report"
)

type sample struct {
	A int    `json:"a"`
	B string `json:"b"`
}

// emptyTitleFindingJSON is a schema-invalid finding (empty title) reused across
// the repair tests; a shared const keeps goconst from flagging the repetition.
// It is one finding object rather than a whole review because repairFinding
// sends and expects a single finding.
const emptyTitleFindingJSON = `{"title":"","severity":"WARNING","confidence":"likely"}`

// repairedFindingJSON is the same finding with the violation fixed.
const repairedFindingJSON = `{"title":"Fixed","severity":"WARNING","confidence":"likely"}`

func TestDecodeJSONWithRepair_ValidNoRepair(t *testing.T) {
	// The common case: valid JSON decodes without ever invoking the repair path.
	called := false
	restore := repairJSON
	repairJSON = func(*Client, string, error, string, string) (string, error) {
		called = true
		return "", errors.New("repair must not be called for valid JSON")
	}
	t.Cleanup(func() { repairJSON = restore })

	var got sample
	if err := (&Client{}).decodeJSONWithRepair("```json\n{\"a\":5,\"b\":\"x\"}\n```", "test", &got); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("repair was called for valid JSON")
	}
	if got.A != 5 || got.B != "x" {
		t.Errorf("decoded %+v, want {A:5 B:x}", got)
	}
}

func TestDecodeJSONWithRepair_RepairsMalformed(t *testing.T) {
	restore := repairJSON
	repairJSON = func(_ *Client, malformed string, parseErr error, label, schema string) (string, error) {
		if parseErr == nil {
			t.Error("repair should receive the original parse error")
		}
		return `{"a":7,"b":"fixed"}`, nil
	}
	t.Cleanup(func() { repairJSON = restore })

	var got sample
	if err := (&Client{}).decodeJSONWithRepair(`{"a":7,"b":"fixed"`, "test", &got); err != nil {
		t.Fatalf("expected repair to succeed, got: %v", err)
	}
	if got.A != 7 || got.B != "fixed" {
		t.Errorf("decoded %+v after repair, want {A:7 B:fixed}", got)
	}
}

func TestDecodeJSONWithRepair_RepairCallFails(t *testing.T) {
	restore := repairJSON
	repairJSON = func(*Client, string, error, string, string) (string, error) {
		return "", errors.New("claude unavailable")
	}
	t.Cleanup(func() { repairJSON = restore })

	var got sample
	if err := (&Client{}).decodeJSONWithRepair(`not json`, "test", &got); err == nil {
		t.Error("expected an error when repair fails")
	}
}

func TestDecodeJSONWithRepair_RepairStillInvalid(t *testing.T) {
	restore := repairJSON
	repairJSON = func(*Client, string, error, string, string) (string, error) {
		return `still { not json`, nil
	}
	t.Cleanup(func() { repairJSON = restore })

	var got sample
	if err := (&Client{}).decodeJSONWithRepair(`bad`, "test", &got); err == nil {
		t.Error("expected an error when repaired output is still invalid")
	}
}

// A model that prepends a sentence before a fenced JSON block is recovered
// locally — the embedded value is extracted, so no repair call is needed.
func TestDecodeJSONWithRepair_PreambleNoRepair(t *testing.T) {
	called := false
	restore := repairJSON
	repairJSON = func(*Client, string, error, string, string) (string, error) {
		called = true
		return "", errors.New("repair must not be called when the value is recoverable")
	}
	t.Cleanup(func() { repairJSON = restore })

	preamble := "Removing the prose preamble yields valid JSON:\n\n```json\n{\"a\":5,\"b\":\"x\"}\n```"
	var got sample
	if err := (&Client{}).decodeJSONWithRepair(preamble, "test", &got); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("repair was called for a recoverable preamble")
	}
	if got.A != 5 || got.B != "x" {
		t.Errorf("decoded %+v, want {A:5 B:x}", got)
	}
}

// The exact production failure: the initial parse fails, the repair call's
// output itself carries a prose preamble before the fenced JSON ("The error is
// caused by the prose preamble ... Removing it yields valid JSON:"). The retry
// parse must still recover the embedded object instead of choking on 'T'.
func TestDecodeJSONWithRepair_RetryPreamble(t *testing.T) {
	restore := repairJSON
	repairJSON = func(*Client, string, error, string, string) (string, error) {
		return "The error is caused by the prose preamble before the JSON object. " +
			"Removing it yields valid JSON:\n\n```json\n{\"a\":7,\"b\":\"fixed\"}\n```", nil
	}
	t.Cleanup(func() { repairJSON = restore })

	var got sample
	if err := (&Client{}).decodeJSONWithRepair(`{"a":7,"b":"fixed"`, "test", &got); err != nil {
		t.Fatalf("expected the retry preamble to be recovered, got: %v", err)
	}
	if got.A != 7 || got.B != "fixed" {
		t.Errorf("decoded %+v after retry, want {A:7 B:fixed}", got)
	}
}

func TestDecodeJSONWithRepair_TwoRoundsForTwoGlitches(t *testing.T) {
	// One round can only fix the first syntax error Go reports; a payload with
	// two independent glitches needs a second round.
	calls := 0
	restore := repairJSON
	repairJSON = func(_ *Client, malformed string, parseErr error, label, schema string) (string, error) {
		calls++
		if calls == 1 {
			return `{"a":7,"b":"x"`, nil // still missing the closing brace
		}
		return `{"a":7,"b":"x"}`, nil
	}
	t.Cleanup(func() { repairJSON = restore })

	var got sample
	if err := (&Client{}).decodeJSONWithRepair(`{"a":7,"b":"x"`, "test", &got); err != nil {
		t.Fatalf("expected success after two rounds, got %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 repair rounds, got %d", calls)
	}
	if got.A != 7 || got.B != "x" {
		t.Errorf("decoded %+v, want {A:7 B:x}", got)
	}
}

func TestDecodeJSONWithRepair_BoundedRounds(t *testing.T) {
	// A repair that never yields valid JSON must stop after maxRepairRounds.
	calls := 0
	restore := repairJSON
	repairJSON = func(*Client, string, error, string, string) (string, error) {
		calls++
		return `{still broken`, nil
	}
	t.Cleanup(func() { repairJSON = restore })

	var got sample
	if err := (&Client{}).decodeJSONWithRepair(`bad`, "test", &got); err == nil {
		t.Error("expected an error after the repair budget is exhausted")
	}
	if calls != maxRepairRounds {
		t.Errorf("expected %d repair rounds, got %d", maxRepairRounds, calls)
	}
}

func TestDecodeJSONWithRepairSchema_ThreadsSchema(t *testing.T) {
	var gotSchema string
	restore := repairJSON
	repairJSON = func(_ *Client, _ string, _ error, _, schema string) (string, error) {
		gotSchema = schema
		return `{"a":1,"b":"y"}`, nil
	}
	t.Cleanup(func() { repairJSON = restore })

	var got sample
	if err := (&Client{}).decodeJSONWithRepairSchema(`{bad`, "test", `{"type":"object"}`, &got); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotSchema != `{"type":"object"}` {
		t.Errorf("schema not threaded to repair call, got %q", gotSchema)
	}
}

func TestRepairInvalidReview_BoundedRounds(t *testing.T) {
	// A schema repair that never fixes the finding stops after maxRepairRounds.
	calls := 0
	restore := repairInvalidJSON
	repairInvalidJSON = func(*Client, string, error, string) (string, error) {
		calls++
		return emptyTitleFindingJSON, nil // empty title, still invalid
	}
	t.Cleanup(func() { repairInvalidJSON = restore })

	result := &report.ReviewResult{Findings: []report.Finding{{Title: "", Severity: report.SeverityWarning, Confidence: report.ConfidenceLikely}}}
	if err := (&Client{}).repairInvalidReview(result); err == nil {
		t.Error("expected failure after the schema-repair budget is exhausted")
	}
	if calls != maxRepairRounds {
		t.Errorf("expected %d schema-repair rounds, got %d", maxRepairRounds, calls)
	}
}

func TestRepairInvalidReview_SucceedsWithinRounds(t *testing.T) {
	calls := 0
	restore := repairInvalidJSON
	repairInvalidJSON = func(*Client, string, error, string) (string, error) {
		calls++
		if calls == 1 {
			return emptyTitleFindingJSON, nil // still bad
		}
		return repairedFindingJSON, nil
	}
	t.Cleanup(func() { repairInvalidJSON = restore })

	result := &report.ReviewResult{Findings: []report.Finding{{Title: "", Severity: report.SeverityWarning, Confidence: report.ConfidenceLikely}}}
	if err := (&Client{}).repairInvalidReview(result); err != nil {
		t.Fatalf("expected success within the round budget, got %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 rounds, got %d", calls)
	}
	if result.Findings[0].Title != "Fixed" {
		t.Errorf("result not updated to the repaired finding, got %q", result.Findings[0].Title)
	}
}

func TestPersistFailedAnalysis(t *testing.T) {
	raw := "## Findings\n- an expensive analysis worth keeping"
	path := persistFailedAnalysis(raw)
	if path == "" {
		t.Fatal("expected a persisted-analysis path")
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading persisted analysis: %v", err)
	}
	if string(data) != raw {
		t.Errorf("persisted analysis = %q, want %q", data, raw)
	}
}

func TestWrapWithPersistedAnalysis(t *testing.T) {
	cause := errors.New("still invalid after 3 rounds")
	err := wrapWithPersistedAnalysis("raw analysis", cause)
	if !errors.Is(err, cause) {
		t.Error("the underlying cause must be wrapped")
	}
	if !strings.Contains(err.Error(), "re-run structuring") {
		t.Errorf("error must carry the re-structure hint, got %v", err)
	}
}

func TestExtractJSONValue(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain object", `{"a":1}`, `{"a":1}`},
		{"prose preamble + fence", "note:\n```json\n{\"a\":1}\n```", `{"a":1}`},
		{"trailing commentary", `{"a":1}` + "\nThat is the answer.", `{"a":1}`},
		{"brace inside string", `{"k":"a}b{c"}`, `{"k":"a}b{c"}`},
		{"escaped quote inside string", `{"k":"he said \"hi}\""}`, `{"k":"he said \"hi}\""}`},
		{"array of objects", `prefix [{"a":1},{"b":2}] suffix`, `[{"a":1},{"b":2}]`},
		{"no json value", `just prose, nothing structured`, `just prose, nothing structured`},
		{"unbalanced left untouched", `{"a":1`, `{"a":1`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractJSONValue(tc.in); got != tc.want {
				t.Errorf("extractJSONValue(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestWarnOnDroppedFindings(t *testing.T) {
	cases := []struct {
		name        string
		sourceCount int
		emitted     int
		wantWarn    bool
	}{
		{"drop is flagged", 5, 4, true},
		{"exact match is silent", 3, 3, false},
		{"more emitted than reported is silent", 2, 3, false},
		{"no reported count is silent", 0, 0, false},
		{"negative count is silent", -1, 2, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			warned := false
			restore := slogWarnFn
			slogWarnFn = func(string, ...any) { warned = true }
			t.Cleanup(func() { slogWarnFn = restore })

			warnOnDroppedFindings(tc.sourceCount, tc.emitted)
			if warned != tc.wantWarn {
				t.Errorf("warnOnDroppedFindings(%d, %d) warned=%v, want %v", tc.sourceCount, tc.emitted, warned, tc.wantWarn)
			}
		})
	}
}

// TestValidationRepairPromptUsesValidationRules binds the schema-repair prompt
// to report.ValidationRules: every rule string must appear verbatim in the
// prompt, so the two cannot drift.
func TestValidationRepairPromptUsesValidationRules(t *testing.T) {
	prompt := buildValidationRepairPrompt(emptyTitleFindingJSON, errors.New("title is empty"))
	for _, rule := range report.ValidationRules() {
		if !strings.Contains(prompt, rule) {
			t.Errorf("validation-repair prompt should contain the rule %q verbatim", rule)
		}
	}
}

func TestNormalizeTranscribedLabels(t *testing.T) {
	restore := slogWarnFn
	warns := 0
	slogWarnFn = func(string, ...any) { warns++ }
	t.Cleanup(func() { slogWarnFn = restore })

	result := &report.ReviewResult{Findings: []report.Finding{
		{Title: "no labels"}, // both empty → defaulted, 2 warns
		{Title: "labelled", Severity: report.SeverityCritical, Confidence: report.ConfidenceVerified}, // untouched
		{Title: "sev only", Severity: report.SeverityWarning},                                         // confidence empty → 1 warn
	}}
	normalizeTranscribedLabels(result)

	if result.Findings[0].Severity != report.SeverityInfo || result.Findings[0].Confidence != report.ConfidenceUncertain {
		t.Errorf("empty labels must default to INFO/uncertain, got %q/%q", result.Findings[0].Severity, result.Findings[0].Confidence)
	}
	if result.Findings[1].Severity != report.SeverityCritical || result.Findings[1].Confidence != report.ConfidenceVerified {
		t.Errorf("stated labels must be untouched, got %q/%q", result.Findings[1].Severity, result.Findings[1].Confidence)
	}
	if result.Findings[2].Confidence != report.ConfidenceUncertain {
		t.Errorf("empty confidence must default to uncertain, got %q", result.Findings[2].Confidence)
	}
	if warns != 3 {
		t.Errorf("expected 3 warnings (2 for the label-less finding, 1 for the confidence-less one), got %d", warns)
	}
}

func validFinding() report.Finding {
	return report.Finding{
		Title:      "Missing error wrapping",
		Severity:   report.SeverityWarning,
		Confidence: report.ConfidenceVerified,
		Problem:    "p",
		Action:     "a",
	}
}

func TestRepairInvalidReview_ValidNoRepair(t *testing.T) {
	// A schema-valid review must not trigger a repair call.
	restore := repairInvalidJSON
	repairInvalidJSON = func(*Client, string, error, string) (string, error) {
		t.Error("repair must not be called for a valid review")
		return "", errors.New("repair must not be called")
	}
	t.Cleanup(func() { repairInvalidJSON = restore })

	result := &report.ReviewResult{Findings: []report.Finding{validFinding()}}
	if err := (&Client{}).repairInvalidReview(result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRepairInvalidReview_RepairsInvalid(t *testing.T) {
	// An empty-title finding is repaired rather than normalized into a default.
	restore := repairInvalidJSON
	repairInvalidJSON = func(_ *Client, _ string, validationErr error, _ string) (string, error) {
		if validationErr == nil {
			t.Error("repair should receive the validation error")
		}
		return `{"severity":"WARNING","title":"Repaired title","confidence":"verified","problem":"p","action":"a"}`, nil
	}
	t.Cleanup(func() { repairInvalidJSON = restore })

	bad := validFinding()
	bad.Title = ""
	result := &report.ReviewResult{Findings: []report.Finding{bad}}
	if err := (&Client{}).repairInvalidReview(result); err != nil {
		t.Fatalf("expected repair to succeed, got: %v", err)
	}
	if len(result.Findings) != 1 || result.Findings[0].Title != "Repaired title" {
		t.Errorf("finding was not replaced by the repaired one: %+v", result.Findings)
	}
}

func TestRepairInvalidReview_StillInvalid(t *testing.T) {
	// One bounded round: a repair that returns still-invalid data fails loudly.
	restore := repairInvalidJSON
	repairInvalidJSON = func(*Client, string, error, string) (string, error) {
		return `{"severity":"WARNING","title":"","confidence":"verified","problem":"p","action":"a"}`, nil
	}
	t.Cleanup(func() { repairInvalidJSON = restore })

	bad := validFinding()
	bad.Title = ""
	result := &report.ReviewResult{Findings: []report.Finding{bad}}
	err := (&Client{}).repairInvalidReview(result)
	if err == nil {
		t.Fatal("expected an error when the repaired finding is still invalid")
	}
	if !strings.Contains(err.Error(), "still invalid") {
		t.Errorf("error should describe the bounded-repair failure, got: %v", err)
	}
}

func TestRepairInvalidReview_RepairCallFails(t *testing.T) {
	restore := repairInvalidJSON
	repairInvalidJSON = func(*Client, string, error, string) (string, error) {
		return "", errors.New("claude unavailable")
	}
	t.Cleanup(func() { repairInvalidJSON = restore })

	bad := validFinding()
	bad.Title = ""
	result := &report.ReviewResult{Findings: []report.Finding{bad}}
	err := (&Client{}).repairInvalidReview(result)
	if err == nil {
		t.Fatal("expected an error when the repair call fails")
	}
	if !strings.Contains(err.Error(), "finding 0") {
		t.Errorf("error should name which finding failed, got: %v", err)
	}
	if !strings.Contains(err.Error(), "claude unavailable") {
		t.Errorf("error should wrap the repair-call failure, got: %v", err)
	}
}

// TestRepairInvalidReview_RepairsOnlyTheOffendingFinding is the point of the
// per-finding repair: a review whose twentieth finding has an empty title used
// to re-send all twenty, up to three times. Only the offender is sent now, and
// the findings around it come back untouched.
func TestRepairInvalidReview_RepairsOnlyTheOffendingFinding(t *testing.T) {
	var sent []string
	restore := repairInvalidJSON
	repairInvalidJSON = func(_ *Client, invalid string, _ error, _ string) (string, error) {
		sent = append(sent, invalid)
		return `{"title":"Repaired","severity":"CRITICAL","confidence":"likely","problem":"boom"}`, nil
	}
	t.Cleanup(func() { repairInvalidJSON = restore })

	first := validFinding()
	first.Title = "first stays"
	offender := validFinding()
	offender.Title = ""
	offender.Problem = "boom"
	last := validFinding()
	last.Title = "last stays"
	result := &report.ReviewResult{Findings: []report.Finding{first, offender, last}}

	if err := (&Client{}).repairInvalidReview(result); err != nil {
		t.Fatalf("repairInvalidReview: %v", err)
	}

	if len(sent) != 1 {
		t.Fatalf("repair called %d times, want 1 — only the offending finding needs one", len(sent))
	}
	// The payload is one finding object, not the review that contained it.
	if strings.Contains(sent[0], `"findings"`) {
		t.Errorf("payload carries the whole review rather than one finding: %s", sent[0])
	}
	var payload report.Finding
	if err := json.Unmarshal([]byte(sent[0]), &payload); err != nil {
		t.Fatalf("payload does not decode as a single finding: %v (%s)", err, sent[0])
	}
	if payload.Problem != "boom" {
		t.Errorf("payload = %+v, want the offending finding", payload)
	}

	if result.Findings[1].Title != "Repaired" {
		t.Errorf("offender was not replaced: %+v", result.Findings[1])
	}
	if !reflect.DeepEqual(result.Findings[0], first) || !reflect.DeepEqual(result.Findings[2], last) {
		t.Errorf("findings around the offender changed:\n%+v\n%+v", result.Findings[0], result.Findings[2])
	}
}

// TestNormalizeTranscribedLabels_SettlesOffEnumLabels covers the labels a source
// review can state in words the enum does not have. Settling them here is what
// leaves the empty title as the only violation a model is ever asked about.
func TestNormalizeTranscribedLabels_SettlesOffEnumLabels(t *testing.T) {
	var attrs [][]any
	restore := slogWarnFn
	slogWarnFn = func(_ string, a ...any) { attrs = append(attrs, a) }
	t.Cleanup(func() { slogWarnFn = restore })

	result := &report.ReviewResult{Findings: []report.Finding{
		{Title: "off-enum", Severity: "HIGH", Confidence: "certain"},
		// Case is not a violation: ParseSeverity and ParseConfidence fold it.
		{Title: "lower case", Severity: "warning", Confidence: "VERIFIED"},
	}}
	normalizeTranscribedLabels(result)

	if result.Findings[0].Severity != report.SeverityInfo {
		t.Errorf("severity = %q, want INFO for a label the enum does not have", result.Findings[0].Severity)
	}
	if result.Findings[0].Confidence != report.ConfidenceUncertain {
		t.Errorf("confidence = %q, want uncertain", result.Findings[0].Confidence)
	}
	if result.Findings[1].Severity != report.SeverityWarning || result.Findings[1].Confidence != report.ConfidenceVerified {
		t.Errorf("a differently-cased label is not a violation, got %q/%q",
			result.Findings[1].Severity, result.Findings[1].Confidence)
	}
	if len(attrs) != 2 {
		t.Fatalf("logged %d warnings, want 2 — one per rejected label on the first finding", len(attrs))
	}
	// Each warning must name the finding and the label that was rejected, or the
	// operator cannot tell which finding lost its classification.
	for _, a := range attrs {
		joined := fmt.Sprint(a...)
		if !strings.Contains(joined, "off-enum") {
			t.Errorf("warning %v does not name the finding's title", a)
		}
	}
	if !strings.Contains(fmt.Sprint(attrs[0]...), "HIGH") || !strings.Contains(fmt.Sprint(attrs[1]...), "certain") {
		t.Errorf("warnings do not name the rejected labels: %v", attrs)
	}
}

// TestNormalizeTranscribedLabels_EmptyAndNilFindings covers the two shapes that
// carry nothing to normalize: a review with no findings at all, and a nil slice.
func TestNormalizeTranscribedLabels_EmptyAndNilFindings(t *testing.T) {
	restore := slogWarnFn
	slogWarnFn = func(string, ...any) { t.Error("nothing to normalize must log nothing") }
	t.Cleanup(func() { slogWarnFn = restore })

	normalizeTranscribedLabels(&report.ReviewResult{})
	normalizeTranscribedLabels(&report.ReviewResult{Findings: nil})
	normalizeTranscribedLabels(&report.ReviewResult{Findings: []report.Finding{}})
}

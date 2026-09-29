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

// repairedFindingJSON is the same finding with the violation fixed, titled
// repairedFindingTitle.
const (
	repairedFindingTitle = "Fixed"
	repairedFindingJSON  = `{"title":"` + repairedFindingTitle + `","severity":"WARNING","confidence":"likely"}`
)

// repairTierModel and repairTierEffort are the structure tier the repair
// invocation tests configure through WithStructureModel/WithStructureEffort
// and expect on the recorded spec.
const (
	repairTierModel  = "sonnet"
	repairTierEffort = "medium"
)

func TestDecodeJSONWithRepair_ValidNoRepair(t *testing.T) {
	t.Parallel()
	// The common case: valid JSON decodes without ever invoking the repair path.
	c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
		return "", "", errors.New("repair must not be called for valid JSON")
	})

	var got sample
	if err := c.decodeJSONWithRepair("```json\n{\"a\":5,\"b\":\"x\"}\n```", "test", &got); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("repair ran %d sessions for valid JSON, want 0", len(*calls))
	}
	if got.A != 5 || got.B != "x" {
		t.Errorf("decoded %+v, want {A:5 B:x}", got)
	}
}

func TestDecodeJSONWithRepair_RepairsMalformed(t *testing.T) {
	t.Parallel()
	const input = `{"a":7,"b":"fixed"`
	parseErr := json.Unmarshal([]byte(input), new(sample))
	c := &Client{sessionFn: func(_ runSpec, prompt string) (string, string, error) {
		if !strings.Contains(prompt, parseErr.Error()) {
			t.Errorf("repair prompt does not carry the original parse error %q:\n%s", parseErr, prompt)
		}
		return `{"a":7,"b":"fixed"}`, "", nil
	}}

	var got sample
	if err := c.decodeJSONWithRepair(input, "test", &got); err != nil {
		t.Fatalf("expected repair to succeed, got: %v", err)
	}
	if got.A != 7 || got.B != "fixed" {
		t.Errorf("decoded %+v after repair, want {A:7 B:fixed}", got)
	}
}

// TestDecodeJSONWithRepair_RepairCallFails covers a repair session that errors
// out: the decode gives up after that one call and reports the original parse
// error, not the session's.
func TestDecodeJSONWithRepair_RepairCallFails(t *testing.T) {
	t.Parallel()
	c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
		return "", "", errors.New("claude unavailable")
	})

	var got sample
	err := c.decodeJSONWithRepair(`not json`, "test", &got)
	if err == nil {
		t.Fatal("expected an error when repair fails")
	}
	if len(*calls) != 1 {
		t.Errorf("repair ran %d sessions, want 1 — a failed session ends the repair", len(*calls))
	}
	if !strings.HasPrefix(err.Error(), "parsing test as JSON: ") {
		t.Errorf("error = %q, want the parse failure", err)
	}
	if strings.Contains(err.Error(), "claude unavailable") {
		t.Errorf("error = %q wraps the session error, want the original parse error", err)
	}
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Errorf("error = %q does not wrap the original *json.SyntaxError", err)
	}
}

func TestDecodeJSONWithRepair_RepairStillInvalid(t *testing.T) {
	t.Parallel()
	c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
		return `still { not json`, "", nil
	})

	var got sample
	if err := c.decodeJSONWithRepair(`bad`, "test", &got); err == nil {
		t.Error("expected an error when repaired output is still invalid")
	}
	if len(*calls) != maxRepairRounds {
		t.Errorf("repair ran %d sessions, want %d", len(*calls), maxRepairRounds)
	}
}

// A model that prepends a sentence before a fenced JSON block is recovered
// locally — the embedded value is extracted, so no repair call is needed.
func TestDecodeJSONWithRepair_PreambleNoRepair(t *testing.T) {
	t.Parallel()
	c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
		return "", "", errors.New("repair must not be called when the value is recoverable")
	})

	preamble := "Removing the prose preamble yields valid JSON:\n\n```json\n{\"a\":5,\"b\":\"x\"}\n```"
	var got sample
	if err := c.decodeJSONWithRepair(preamble, "test", &got); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("repair ran %d sessions for a recoverable preamble, want 0", len(*calls))
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
	t.Parallel()
	c := &Client{sessionFn: func(runSpec, string) (string, string, error) {
		return "The error is caused by the prose preamble before the JSON object. " +
			"Removing it yields valid JSON:\n\n```json\n{\"a\":7,\"b\":\"fixed\"}\n```", "", nil
	}}

	var got sample
	if err := c.decodeJSONWithRepair(`{"a":7,"b":"fixed"`, "test", &got); err != nil {
		t.Fatalf("expected the retry preamble to be recovered, got: %v", err)
	}
	if got.A != 7 || got.B != "fixed" {
		t.Errorf("decoded %+v after retry, want {A:7 B:fixed}", got)
	}
}

func TestDecodeJSONWithRepair_TwoRoundsForTwoGlitches(t *testing.T) {
	t.Parallel()
	// One round can only fix the first syntax error Go reports; a payload with
	// two independent glitches needs a second round.
	c, calls := scriptedClient(t, func(call int, _ runSpec, _ string) (string, string, error) {
		if call == 1 {
			return `{"a":7,"b":"x"`, "", nil // still missing the closing brace
		}
		return `{"a":7,"b":"x"}`, "", nil
	})

	var got sample
	if err := c.decodeJSONWithRepair(`{"a":7,"b":"x"`, "test", &got); err != nil {
		t.Fatalf("expected success after two rounds, got %v", err)
	}
	if len(*calls) != 2 {
		t.Errorf("expected 2 repair rounds, got %d", len(*calls))
	}
	if got.A != 7 || got.B != "x" {
		t.Errorf("decoded %+v, want {A:7 B:x}", got)
	}
}

func TestDecodeJSONWithRepair_BoundedRounds(t *testing.T) {
	t.Parallel()
	// A repair that never yields valid JSON must stop after maxRepairRounds.
	c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
		return `{still broken`, "", nil
	})

	var got sample
	err := c.decodeJSONWithRepair(`bad`, "test", &got)
	if err == nil {
		t.Fatal("expected an error after the repair budget is exhausted")
	}
	if len(*calls) != maxRepairRounds {
		t.Errorf("expected %d repair rounds, got %d", maxRepairRounds, len(*calls))
	}
	if want := fmt.Sprintf("parsing test as JSON after %d repair rounds", maxRepairRounds); !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}

// TestDecodeJSONWithRepair_EmptyRepairOutput covers a repair session that
// answers with no text at all: each empty answer fails to parse and spends a
// round, so the budget still bounds the loop.
func TestDecodeJSONWithRepair_EmptyRepairOutput(t *testing.T) {
	t.Parallel()
	c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
		return "", "", nil
	})

	var got sample
	err := c.decodeJSONWithRepair(`bad`, "test", &got)
	if err == nil {
		t.Fatal("expected an error when every repair answer is empty")
	}
	if len(*calls) != maxRepairRounds {
		t.Errorf("expected %d repair rounds, got %d", maxRepairRounds, len(*calls))
	}
	if want := fmt.Sprintf("parsing test as JSON after %d repair rounds", maxRepairRounds); !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}

func TestDecodeJSONWithRepairSchema_ThreadsSchema(t *testing.T) {
	t.Parallel()
	var prompts []string
	c := &Client{sessionFn: func(_ runSpec, prompt string) (string, string, error) {
		prompts = append(prompts, prompt)
		return `{"a":1,"b":"y"}`, "", nil
	}}

	var got sample
	if err := c.decodeJSONWithRepairSchema(`{bad`, "test", `{"type":"object"}`, &got); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prompts) != 1 || !strings.Contains(prompts[0], `{"type":"object"}`) {
		t.Errorf("schema not threaded to the repair prompt, got %q", prompts)
	}
}

// TestRepairJSON_RunsOnTheStructureTier pins the invocation the JSON repair
// assembles: the structure tier's model and effort, the tool-less read-only
// spec, a label derived from the caller's, and a prompt that carries both the
// parse error and the target schema.
func TestRepairJSON_RunsOnTheStructureTier(t *testing.T) {
	t.Parallel()
	c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
		return `{"a":1}`, "", nil
	}, WithStructureModel(repairTierModel), WithStructureEffort(repairTierEffort))
	var v any
	parseErr := json.Unmarshal([]byte("{bad"), &v)
	if parseErr == nil {
		t.Fatal("expected {bad to fail to parse")
	}

	out, err := c.repairJSON("{bad", parseErr, "test", `{"type":"object"}`)
	if err != nil {
		t.Fatalf("repairJSON: %v", err)
	}
	if out != `{"a":1}` {
		t.Errorf("out = %q, want the session's text", out)
	}
	if len(*calls) != 1 {
		t.Fatalf("repairJSON ran %d sessions, want 1", len(*calls))
	}
	spec, prompt := (*calls)[0].spec, (*calls)[0].prompt
	if spec.label != "test-repair" {
		t.Errorf("label = %q, want test-repair", spec.label)
	}
	if spec.model != repairTierModel || spec.effort != repairTierEffort {
		t.Errorf("model/effort = %q/%q, want the structure tier %q/%q", spec.model, spec.effort, repairTierModel, repairTierEffort)
	}
	if !spec.noTools || !spec.readOnly {
		t.Errorf("noTools=%v readOnly=%v, want a tool-less read-only session", spec.noTools, spec.readOnly)
	}
	if spec.dir != "" {
		t.Errorf("dir = %q, want none: runSession resolves the structuring directory", spec.dir)
	}
	for _, want := range []string{parseErr.Error(), `{"type":"object"}`, "{bad"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("repair prompt does not contain %q:\n%s", want, prompt)
		}
	}
}

// TestRepairInvalidJSON_RunsOnTheStructureTier is the schema repair's
// counterpart: the same tier and spec, its own label suffix, and the
// validation error in the prompt.
func TestRepairInvalidJSON_RunsOnTheStructureTier(t *testing.T) {
	t.Parallel()
	c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
		return "", "", errors.New("claude unavailable")
	}, WithStructureModel(repairTierModel), WithStructureEffort(repairTierEffort))

	_, err := c.repairInvalidJSON(emptyTitleFindingJSON, errors.New("title is empty"), "structured review finding")
	if err == nil || !strings.Contains(err.Error(), "claude unavailable") {
		t.Errorf("err = %v, want the session error returned", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("repairInvalidJSON ran %d sessions, want 1", len(*calls))
	}
	spec, prompt := (*calls)[0].spec, (*calls)[0].prompt
	if spec.label != "structured review finding-schema-repair" {
		t.Errorf("label = %q, want structured review finding-schema-repair", spec.label)
	}
	if spec.model != repairTierModel || spec.effort != repairTierEffort {
		t.Errorf("model/effort = %q/%q, want the structure tier %q/%q", spec.model, spec.effort, repairTierModel, repairTierEffort)
	}
	if !spec.noTools || !spec.readOnly {
		t.Errorf("noTools=%v readOnly=%v, want a tool-less read-only session", spec.noTools, spec.readOnly)
	}
	for _, want := range []string{"title is empty", emptyTitleFindingJSON} {
		if !strings.Contains(prompt, want) {
			t.Errorf("schema-repair prompt does not contain %q:\n%s", want, prompt)
		}
	}
}

func TestRepairInvalidReview_BoundedRounds(t *testing.T) {
	t.Parallel()
	// A schema repair that never fixes the finding stops after maxRepairRounds.
	c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
		return emptyTitleFindingJSON, "", nil // empty title, still invalid
	})

	result := &report.ReviewResult{Findings: []report.Finding{{Title: "", Severity: report.SeverityWarning, Confidence: report.ConfidenceLikely}}}
	if err := c.repairInvalidReview(result, "review output"); err == nil {
		t.Error("expected failure after the schema-repair budget is exhausted")
	}
	if len(*calls) != maxRepairRounds {
		t.Errorf("expected %d schema-repair rounds, got %d", maxRepairRounds, len(*calls))
	}
}

func TestRepairInvalidReview_SucceedsWithinRounds(t *testing.T) {
	t.Parallel()
	c, calls := scriptedClient(t, func(call int, _ runSpec, _ string) (string, string, error) {
		if call == 1 {
			return emptyTitleFindingJSON, "", nil // still bad
		}
		return repairedFindingJSON, "", nil
	})

	result := &report.ReviewResult{Findings: []report.Finding{{Title: "", Severity: report.SeverityWarning, Confidence: report.ConfidenceLikely}}}
	if err := c.repairInvalidReview(result, "review output"); err != nil {
		t.Fatalf("expected success within the round budget, got %v", err)
	}
	if len(*calls) != 2 {
		t.Errorf("expected 2 rounds, got %d", len(*calls))
	}
	if result.Findings[0].Title != repairedFindingTitle {
		t.Errorf("result not updated to the repaired finding, got %q", result.Findings[0].Title)
	}
}

func TestPersistFailedOutput(t *testing.T) {
	raw := finderOutputJSON
	path := persistFailedOutput(raw)
	if path == "" {
		t.Fatal("expected a persisted-output path")
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	if !strings.HasSuffix(path, ".json") {
		t.Errorf("persisted output %s is not a .json file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading persisted output: %v", err)
	}
	if string(data) != raw {
		t.Errorf("persisted output = %q, want %q", data, raw)
	}
}

func TestWrapWithPersistedOutput(t *testing.T) {
	cause := errors.New("still invalid after 3 rounds")
	err := wrapWithPersistedOutput("raw output", cause)
	if !errors.Is(err, cause) {
		t.Error("the underlying cause must be wrapped")
	}
	if !strings.Contains(err.Error(), "raw output was saved to") {
		t.Errorf("error must name the saved raw output, got %v", err)
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
	t.Parallel()
	// A schema-valid review must not trigger a repair call.
	c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
		return "", "", errors.New("repair must not be called")
	})

	result := &report.ReviewResult{Findings: []report.Finding{validFinding()}}
	if err := c.repairInvalidReview(result, "review output"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("repair ran %d sessions for a valid review, want 0", len(*calls))
	}
}

func TestRepairInvalidReview_RepairsInvalid(t *testing.T) {
	t.Parallel()
	// An empty-title finding is repaired rather than normalized into a default.
	bad := validFinding()
	bad.Title = ""
	c := &Client{sessionFn: func(_ runSpec, prompt string) (string, string, error) {
		if verr := bad.Validate(); verr == nil || !strings.Contains(prompt, verr.Error()) {
			t.Errorf("repair prompt does not carry the validation error %v:\n%s", verr, prompt)
		}
		return `{"severity":"WARNING","title":"Repaired title","confidence":"verified","problem":"p","action":"a"}`, "", nil
	}}

	result := &report.ReviewResult{Findings: []report.Finding{bad}}
	if err := c.repairInvalidReview(result, "review output"); err != nil {
		t.Fatalf("expected repair to succeed, got: %v", err)
	}
	if len(result.Findings) != 1 || result.Findings[0].Title != "Repaired title" {
		t.Errorf("finding was not replaced by the repaired one: %+v", result.Findings)
	}
}

func TestRepairInvalidReview_StillInvalid(t *testing.T) {
	t.Parallel()
	// Bounded rounds: a repair that keeps returning still-invalid data fails loudly.
	c := &Client{sessionFn: func(runSpec, string) (string, string, error) {
		return `{"severity":"WARNING","title":"","confidence":"verified","problem":"p","action":"a"}`, "", nil
	}}

	bad := validFinding()
	bad.Title = ""
	result := &report.ReviewResult{Findings: []report.Finding{bad}}
	err := c.repairInvalidReview(result, "review output")
	if err == nil {
		t.Fatal("expected an error when the repaired finding is still invalid")
	}
	if !strings.Contains(err.Error(), "still invalid") {
		t.Errorf("error should describe the bounded-repair failure, got: %v", err)
	}
}

func TestRepairInvalidReview_RepairCallFails(t *testing.T) {
	t.Parallel()
	c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
		return "", "", errors.New("claude unavailable")
	})

	bad := validFinding()
	bad.Title = ""
	result := &report.ReviewResult{Findings: []report.Finding{bad}}
	err := c.repairInvalidReview(result, "review output")
	if len(*calls) != 1 {
		t.Errorf("repair ran %d sessions, want 1 — a failed session ends the repair", len(*calls))
	}
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
	t.Parallel()
	var sent []string
	c := &Client{sessionFn: func(_ runSpec, prompt string) (string, string, error) {
		sent = append(sent, fencedPayload(t, prompt, "invalid-finding"))
		return `{"title":"Repaired","severity":"CRITICAL","confidence":"likely","problem":"boom"}`, "", nil
	}}

	first := validFinding()
	first.Title = "first stays"
	offender := validFinding()
	offender.Title = ""
	offender.Problem = "boom"
	last := validFinding()
	last.Title = "last stays"
	result := &report.ReviewResult{Findings: []report.Finding{first, offender, last}}

	if err := c.repairInvalidReview(result, "review output"); err != nil {
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

// TestRepairInvalidReview_RetriesAnUnparseableRepair covers the other way a
// round can fail: the repair answer is not JSON at all. The parse error is fed
// back and the next round still lands, so one malformed answer does not spend
// the whole budget.
func TestRepairInvalidReview_RetriesAnUnparseableRepair(t *testing.T) {
	t.Parallel()
	c, calls := scriptedClient(t, func(call int, _ runSpec, _ string) (string, string, error) {
		if call == 1 {
			return "sorry, I could not do that", "", nil
		}
		return repairedFindingJSON, "", nil
	})

	result := &report.ReviewResult{Findings: []report.Finding{{Title: "", Severity: report.SeverityWarning, Confidence: report.ConfidenceLikely}}}
	if err := c.repairInvalidReview(result, "review output"); err != nil {
		t.Fatalf("expected the second round to succeed, got %v", err)
	}
	if result.Findings[0].Title != repairedFindingTitle {
		t.Errorf("finding was not replaced by the repaired one: %+v", result.Findings[0])
	}
	if len(*calls) != 2 {
		t.Fatalf("expected 2 rounds, got %d", len(*calls))
	}
	// Round one is told the schema violation; round two is told why its own
	// answer could not be read, which is the only way it can correct it.
	if first := (*calls)[0].prompt; !strings.Contains(first, "title is empty") {
		t.Errorf("first round was not given the validation error, got %q", first)
	}
	if second := (*calls)[1].prompt; !strings.Contains(second, "not valid JSON") {
		t.Errorf("second round was not given the parse failure, got %q", second)
	}
}

// fencedPayload returns the text a repair prompt carries between <tag> and
// </tag>, failing the test when the prompt has no such fence.
func fencedPayload(t *testing.T, prompt, tag string) string {
	t.Helper()
	open, closing := "<"+tag+">\n", "\n</"+tag+">"
	start := strings.Index(prompt, open)
	end := strings.LastIndex(prompt, closing)
	if start == -1 || end < start+len(open) {
		t.Fatalf("prompt has no <%s> fence:\n%s", tag, prompt)
	}
	return prompt[start+len(open) : end]
}

// emptyFinderOutputJSON is the smallest valid finder output: no findings, and
// an empty summary and recommendation.
const emptyFinderOutputJSON = `{"findings":[],"summary":"","recommendation":""}`

// finderOutputJSON is a valid finder output carrying one WARNING finding.
const finderOutputJSON = `{"findings":[{"severity":"WARNING","title":"t","file":"f.go","line":3,"actionability":"auto-fix","confidence":"likely","problem":"p","action":"a"}],"summary":"s","recommendation":""}`

// noSessionClient is a scriptedClient whose every session fails the test: the
// decode cases that must not reach the repair backstop run on it.
func noSessionClient(t *testing.T, opts ...Option) (*Client, *[]sessionCall) {
	t.Helper()
	return scriptedClient(t, func(_ int, spec runSpec, _ string) (string, string, error) {
		t.Errorf("unexpected %s session", spec.label)
		return "", "", errors.New("no session expected")
	}, opts...)
}

// savedRawOutput returns the file a finishReview error names as the saved raw
// output and removes it when the test ends.
func savedRawOutput(t *testing.T, err error) string {
	t.Helper()
	const marker = "raw output was saved to "
	msg := err.Error()
	i := strings.LastIndex(msg, marker)
	if i == -1 {
		t.Fatalf("error does not name the saved raw output: %v", err)
	}
	path := strings.TrimSpace(msg[i+len(marker):])
	t.Cleanup(func() { _ = os.Remove(path) })
	return path
}

// TestFinishReview_ValidObjectRunsNoSession covers the common case: the finder
// emitted a valid object, so it decodes without a repair session and leaves
// with its ID, its derived fix class, and the model that produced it.
func TestFinishReview_ValidObjectRunsNoSession(t *testing.T) {
	t.Parallel()
	c, calls := noSessionClient(t)

	result, err := c.finishReview(finderOutputJSON, testResolvedModel, "review output", "")
	if err != nil {
		t.Fatalf("finishReview: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("ran %d sessions for a valid object, want 0", len(*calls))
	}
	if len(result.Findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(result.Findings))
	}
	f := result.Findings[0]
	if f.ID != "W-001" || f.FixClass != report.FixClassAutoFix {
		t.Errorf("ID/FixClass = %q/%q, want W-001/%q", f.ID, f.FixClass, report.FixClassAutoFix)
	}
	if result.Model != testResolvedModel {
		t.Errorf("Model = %q, want %q", result.Model, testResolvedModel)
	}
}

// TestFinishReview_NullAndEmptyFindings covers a pass that reports nothing:
// findings encoded as null and as [] both decode to no findings without a
// session, and the summary survives.
func TestFinishReview_NullAndEmptyFindings(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ in, summary string }{
		"null":  {`{"findings":null,"summary":"clean","recommendation":"merge"}`, "clean"},
		"empty": {emptyFinderOutputJSON, ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c, calls := noSessionClient(t)
			result, err := c.finishReview(tc.in, testResolvedModel, "review output", "")
			if err != nil {
				t.Fatalf("finishReview: %v", err)
			}
			if len(*calls) != 0 || len(result.Findings) != 0 {
				t.Errorf("sessions=%d findings=%d, want 0 and 0", len(*calls), len(result.Findings))
			}
			if result.Summary != tc.summary {
				t.Errorf("Summary = %q, want %q", result.Summary, tc.summary)
			}
		})
	}
}

// TestFinishReview_WrappedPayloadRunsNoSession covers the wrappers a model adds
// to an object that is valid in itself: a markdown fence, a line of prose
// before it, and a line of prose after it. None of them costs a repair session.
func TestFinishReview_WrappedPayloadRunsNoSession(t *testing.T) {
	t.Parallel()
	for name, in := range map[string]string{
		"json fence":     "```json\n" + finderOutputJSON + "\n```",
		"prose preamble": "Here is the review output:\n" + finderOutputJSON,
		"trailing prose": finderOutputJSON + "\nThat is every finding.",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c, calls := noSessionClient(t)
			result, err := c.finishReview(in, testResolvedModel, "review output", "")
			if err != nil {
				t.Fatalf("finishReview: %v", err)
			}
			if len(*calls) != 0 || len(result.Findings) != 1 {
				t.Errorf("sessions=%d findings=%d, want 0 and 1", len(*calls), len(result.Findings))
			}
		})
	}
}

// TestFinishReview_RepairsTruncatedPayload covers an object cut short: one
// repair session on the structure tier, labeled after the pass, fixes it.
func TestFinishReview_RepairsTruncatedPayload(t *testing.T) {
	t.Parallel()
	c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
		return finderOutputJSON, "", nil
	}, WithStructureModel(repairTierModel))

	result, err := c.finishReview(strings.TrimSuffix(finderOutputJSON, "}"), testResolvedModel, "review output", "")
	if err != nil {
		t.Fatalf("finishReview: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("ran %d sessions, want 1 repair", len(*calls))
	}
	spec := (*calls)[0].spec
	if spec.label != "review output-repair" || !spec.noTools || spec.model != repairTierModel {
		t.Errorf("label=%q noTools=%v model=%q, want review output-repair on the tool-less structure tier %q", spec.label, spec.noTools, spec.model, repairTierModel)
	}
	if len(result.Findings) != 1 {
		t.Errorf("got %d findings, want the repaired one", len(result.Findings))
	}
}

// TestFinishReview_EmptyOutputFailsAfterRepairRounds covers a finder that
// returned nothing usable: the repair budget runs out, and the error names the
// pass and the file the raw output was saved to.
func TestFinishReview_EmptyOutputFailsAfterRepairRounds(t *testing.T) {
	t.Parallel()
	c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
		return "not json", "", nil
	})

	_, err := c.finishReview("", testResolvedModel, "review output", "")
	if err == nil {
		t.Fatal("expected an error for an empty output")
	}
	if len(*calls) != maxRepairRounds {
		t.Errorf("ran %d repair sessions, want %d", len(*calls), maxRepairRounds)
	}
	if !strings.Contains(err.Error(), "parsing review output as JSON") {
		t.Errorf("error does not name the pass: %v", err)
	}
	data, rerr := os.ReadFile(savedRawOutput(t, err))
	if rerr != nil {
		t.Fatalf("reading the saved raw output: %v", rerr)
	}
	if len(data) != 0 {
		t.Errorf("saved raw output = %q, want the empty payload", data)
	}
}

// TestFinishReview_RepairSessionErrorWrapsParseError covers a repair session
// that fails outright: the error reports the finder's own parse error, not the
// repair session's, and still names the saved raw output.
func TestFinishReview_RepairSessionErrorWrapsParseError(t *testing.T) {
	t.Parallel()
	c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
		return "", "", errors.New("claude unavailable")
	})

	_, err := c.finishReview(`{"findings":[`, testResolvedModel, "review output", "")
	if err == nil {
		t.Fatal("expected an error when the repair session fails")
	}
	if len(*calls) != 1 {
		t.Errorf("ran %d sessions, want 1", len(*calls))
	}
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Errorf("error does not wrap the original parse error: %v", err)
	}
	if strings.Contains(err.Error(), "claude unavailable") {
		t.Errorf("error reports the repair session's failure instead of the parse error: %v", err)
	}
	if !strings.Contains(err.Error(), "parsing review output as JSON") {
		t.Errorf("error does not name the pass: %v", err)
	}
	savedRawOutput(t, err)
}

// TestFinishReview_RepairsEmptyTitle covers the one finding rule a
// schema-valid output can still break: an empty title goes to the per-finding
// schema repair and comes back fixed.
func TestFinishReview_RepairsEmptyTitle(t *testing.T) {
	t.Parallel()
	c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
		return repairedFindingJSON, "", nil
	})

	result, err := c.finishReview(strings.Replace(finderOutputJSON, `"title":"t"`, `"title":""`, 1), testResolvedModel, "review output", "")
	if err != nil {
		t.Fatalf("finishReview: %v", err)
	}
	if len(*calls) != 1 || (*calls)[0].spec.label != "review output finding-schema-repair" {
		t.Fatalf("sessions = %+v, want one review output finding-schema-repair", *calls)
	}
	if len(result.Findings) != 1 || result.Findings[0].Title != repairedFindingTitle {
		t.Errorf("findings = %+v, want the repaired title", result.Findings)
	}
}

// TestFinishReview_RepairsUnusableLabel covers the output no CLI validation
// guards (the result fallback, and a JSON repair's answer): a finding whose
// severity is missing or off the enum, or whose confidence is off the enum,
// goes to the per-finding schema repair with the validator's error, and the
// repaired labels reach the result.
func TestFinishReview_RepairsUnusableLabel(t *testing.T) {
	t.Parallel()
	const repaired = `{"severity":"CRITICAL","title":"t","file":"f.go","line":3,"actionability":"auto-fix","confidence":"verified","problem":"p","action":"a"}`
	for name, tc := range map[string]struct{ from, to, wantErr string }{
		"severity missing":        {`"severity":"WARNING",`, ``, `severity "" is not one of`},
		"severity off the enum":   {`"severity":"WARNING"`, `"severity":"HIGH"`, `severity "HIGH" is not one of`},
		"confidence off the enum": {`"confidence":"likely"`, `"confidence":"certain"`, `confidence "certain" is not one of`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
				return repaired, "", nil
			})

			result, err := c.finishReview(strings.Replace(finderOutputJSON, tc.from, tc.to, 1), testResolvedModel, "review output", "")
			if err != nil {
				t.Fatalf("finishReview: %v", err)
			}
			if len(*calls) != 1 || (*calls)[0].spec.label != "review output finding-schema-repair" {
				t.Fatalf("sessions = %+v, want one review output finding-schema-repair", *calls)
			}
			if prompt := (*calls)[0].prompt; !strings.Contains(prompt, tc.wantErr) {
				t.Errorf("schema-repair prompt does not carry the validation error %q:\n%s", tc.wantErr, prompt)
			}
			if len(result.Findings) != 1 {
				t.Fatalf("got %d findings, want the repaired one", len(result.Findings))
			}
			if f := result.Findings[0]; f.Severity != report.SeverityCritical || f.Confidence != report.ConfidenceVerified {
				t.Errorf("labels = %q/%q, want the repaired %q/%q", f.Severity, f.Confidence, report.SeverityCritical, report.ConfidenceVerified)
			}
		})
	}
}

// TestFinishReview_StillInvalidFindingFails covers a schema repair that never
// lands, for an empty title and for an off-enum severity: the error names the
// pass and the exhausted budget, and the raw output is saved.
func TestFinishReview_StillInvalidFindingFails(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ from, to, stillInvalid string }{
		"empty title":           {`"title":"t"`, `"title":""`, emptyTitleFindingJSON},
		"severity off the enum": {`"severity":"WARNING"`, `"severity":"HIGH"`, `{"title":"t","severity":"HIGH","confidence":"likely"}`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
				return tc.stillInvalid, "", nil
			})

			_, err := c.finishReview(strings.Replace(finderOutputJSON, tc.from, tc.to, 1), testResolvedModel, "review output", "")
			if err == nil {
				t.Fatal("expected an error when the finding stays invalid")
			}
			if len(*calls) != maxRepairRounds {
				t.Errorf("ran %d schema-repair sessions, want %d", len(*calls), maxRepairRounds)
			}
			for _, want := range []string{"review output: ", "still invalid after 3 schema-repair rounds"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error does not contain %q: %v", want, err)
				}
			}
			savedRawOutput(t, err)
		})
	}
}

// TestFinishReview_TagsOnlyEmptyPatterns covers the pass's pattern tag: it
// fills a finding that names no pattern and leaves a named one alone.
func TestFinishReview_TagsOnlyEmptyPatterns(t *testing.T) {
	t.Parallel()
	c, _ := noSessionClient(t)
	in := `{"findings":[` +
		`{"severity":"WARNING","title":"untagged","file":"f.go","actionability":"auto-fix","confidence":"likely","problem":"p","action":"a"},` +
		`{"severity":"CRITICAL","title":"tagged","file":"f.go","pattern":"Injection","actionability":"needs-discussion","confidence":"verified","problem":"p","action":"a"}` +
		`],"summary":"","recommendation":""}`

	result, err := c.finishReview(in, testResolvedModel, "adversarial review", "adversarial-review")
	if err != nil {
		t.Fatalf("finishReview: %v", err)
	}
	if got := []string{result.Findings[0].Pattern, result.Findings[1].Pattern}; !reflect.DeepEqual(got, []string{"adversarial-review", "Injection"}) {
		t.Errorf("patterns = %q, want the tag only on the untagged finding", got)
	}
}

package claude

import (
	"encoding/json"
	"strings"
	"testing"
)

// testWorkerModel and testWorkerEffort are the values the tests configure as
// the worker tier. They are deliberately their own constants: the tests are
// about "the configured worker model", not about DefaultClaudeModel, which
// merely happens to share the value "opus".
const (
	testWorkerModel  = "opus"
	testWorkerEffort = "high"
)

// TestImplementAgentsJSON_EmptyWithoutWorkerModel pins the opt-in contract:
// without a worker model no subagent is defined, the runner omits the --agents
// flag entirely, and the implement invocation stays byte-for-byte what it was
// before orchestrator mode existed.
func TestImplementAgentsJSON_EmptyWithoutWorkerModel(t *testing.T) {
	got, err := implementAgentsJSON(goldenImplementContext())
	if err != nil {
		t.Fatalf("implementAgentsJSON returned %v, want nil", err)
	}
	if got != "" {
		t.Errorf("implementAgentsJSON without a worker model = %q, want empty", got)
	}
}

// TestImplementAgentsJSON_DefinesImplementerOnWorkerModel decodes the emitted
// --agents value and asserts the implementer definition carries the configured
// worker model and effort plus a non-empty prompt — the fields Claude Code
// reads to run the subagent on the worker tier.
func TestImplementAgentsJSON_DefinesImplementerOnWorkerModel(t *testing.T) {
	ctx := goldenImplementContext()
	ctx.WorkerModel = testWorkerModel
	ctx.WorkerEffort = testWorkerEffort

	raw, err := implementAgentsJSON(ctx)
	if err != nil {
		t.Fatalf("implementAgentsJSON returned %v, want nil", err)
	}
	var defs map[string]agentDefinition
	if err := json.Unmarshal([]byte(raw), &defs); err != nil {
		t.Fatalf("emitted --agents value is not valid JSON: %v\n%s", err, raw)
	}
	def, ok := defs[implementerAgentName]
	if !ok {
		t.Fatalf("emitted --agents value lacks the %q agent; got keys %v", implementerAgentName, raw)
	}
	if def.Model != testWorkerModel {
		t.Errorf("implementer model = %q, want the worker model %q", def.Model, testWorkerModel)
	}
	if def.Effort != testWorkerEffort {
		t.Errorf("implementer effort = %q, want the worker effort %q", def.Effort, testWorkerEffort)
	}
	if def.Description == "" {
		t.Error("implementer description is empty; the orchestrator matches agents by description")
	}
	if def.Prompt != buildImplementerAgentPrompt() {
		t.Error("implementer prompt differs from buildImplementerAgentPrompt()")
	}
}

// TestImplementAgentsJSON_DefaultsWorkerEffort pins the effort fallback: an
// empty WorkerEffort resolves to DefaultImplementWorkerEffort rather than
// emitting an empty field the CLI would have to guess about.
func TestImplementAgentsJSON_DefaultsWorkerEffort(t *testing.T) {
	ctx := goldenImplementContext()
	ctx.WorkerModel = testWorkerModel

	raw, err := implementAgentsJSON(ctx)
	if err != nil {
		t.Fatalf("implementAgentsJSON returned %v, want nil", err)
	}
	var defs map[string]agentDefinition
	if err := json.Unmarshal([]byte(raw), &defs); err != nil {
		t.Fatalf("emitted --agents value is not valid JSON: %v\n%s", err, raw)
	}
	if got := defs[implementerAgentName].Effort; got != DefaultImplementWorkerEffort {
		t.Errorf("implementer effort = %q, want default %q", got, DefaultImplementWorkerEffort)
	}
}

// TestImplementAttributionModel pins the attribution decision: in orchestrator
// mode the run's artifacts are attributed to the worker model — the model that
// wrote the code — while single-session mode keeps the resolved session model
// the envelope reported.
func TestImplementAttributionModel(t *testing.T) {
	orch := goldenImplementContext()
	orch.WorkerModel = testWorkerModel
	if got := implementAttributionModel(orch, "claude-fable-5"); got != testWorkerModel {
		t.Errorf("orchestrated attribution = %q, want the worker model %q", got, testWorkerModel)
	}
	if got := implementAttributionModel(goldenImplementContext(), "claude-fable-5"); got != "claude-fable-5" {
		t.Errorf("single-session attribution = %q, want the session model %q", got, "claude-fable-5")
	}
}

// TestBuildImplementerAgentPrompt_CarriesTheNonNegotiables asserts the worker
// prompt repeats everything the subagent must honor without seeing the
// orchestrator's context: the commit-trailer convention (so worker commits
// carry the worker's exact model id), the branch/push prohibitions (the
// orchestrator owns the branch and the delivery), and the scope discipline.
func TestBuildImplementerAgentPrompt_CarriesTheNonNegotiables(t *testing.T) {
	prompt := buildImplementerAgentPrompt()
	for _, want := range []string{
		"exactly ONE work package",
		"Assisted-by: Claude",
		"Signed-off-by",
		"NEVER create or switch branches",
		"NEVER push or open a pull request",
		"NEVER weaken, skip, or delete tests",
		"leave the working tree CLEAN",
		"FOREGROUND",
		"the commits you made (sha7 + subject)",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("implementer agent prompt is missing %q", want)
		}
	}
}

// TestBuildImplementPrompt_OrchestrationOnlyWithWorkerModel pins that the
// orchestration instructions appear exactly when a worker model is configured:
// the single-session prompt stays free of them, and the orchestrated prompt
// carries the section, the delegating workflow step, and the never-edit hard
// rule.
func TestBuildImplementPrompt_OrchestrationOnlyWithWorkerModel(t *testing.T) {
	plain := BuildImplementPrompt(goldenImplementContext())
	if strings.Contains(plain, "Orchestrated implementation") {
		t.Error("single-session implement prompt must not carry the orchestration section")
	}

	ctx := goldenImplementContext()
	ctx.WorkerModel = testWorkerModel
	orch := BuildImplementPrompt(ctx)
	for _, want := range []string{
		"## Orchestrated implementation",
		"Delegate ONE work package at a time",
		"SELF-CONTAINED",
		"5. IMPLEMENT the change set package by package",
		"- NEVER create or edit a file yourself in this orchestrated session",
	} {
		if !strings.Contains(orch, want) {
			t.Errorf("orchestrated implement prompt is missing %q", want)
		}
	}
}

// TestBuildImplementPrompt_OrchestratedKeepsReportContract guards the seam the
// Go orchestrator keys on: orchestrator mode must not change the report
// heading or the terminal STATUS contract, or implementReportStatus and the
// PARTIAL/BLOCKED guards in the implement package would stop recognizing the
// session's output.
func TestBuildImplementPrompt_OrchestratedKeepsReportContract(t *testing.T) {
	ctx := goldenImplementContext()
	ctx.WorkerModel = testWorkerModel
	prompt := BuildImplementPrompt(ctx)
	for _, want := range []string{
		"## Implementation Report (issue #42)",
		"STATUS: <DONE | DONE_WITH_CONCERNS | PARTIAL | BLOCKED | NEEDS_CONTEXT>",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("orchestrated implement prompt is missing the report contract piece %q", want)
		}
	}
}

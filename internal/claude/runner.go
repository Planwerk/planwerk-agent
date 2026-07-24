package claude

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/planwerk/planwerk-agent/internal/report"
)

const (
	// DefaultClaudeTimeout is the compiled-in default for the maximum time
	// allowed for a single Claude Code invocation. 60 minutes gives the
	// long-running sessions — audit, elaborate, and above all an implement
	// session that delegates its work packages to implementer subagents and
	// verifies each result — enough headroom to finish instead of being cut
	// off mid-branch. Override with WithTimeout (driven by the
	// --claude-timeout flag / PLANWERK_CLAUDE_TIMEOUT env var) when a run
	// needs even more.
	DefaultClaudeTimeout = 60 * time.Minute
	// DefaultClaudeModel is the compiled-in default model passed to Claude
	// Code via --model. The "opus" alias runs the latest Opus release
	// automatically, without re-pinning on each model bump; Opus follows
	// instructions more literally than smaller models, which matches the
	// strict MUST/NEVER style used throughout the review prompts. Override
	// with SetModel (driven by the --claude-model flag / PLANWERK_CLAUDE_MODEL
	// env var) to run reviews on a different model, e.g. "fable".
	DefaultClaudeModel = "opus"
	// DefaultPlanModel is the compiled-in default model for the implement
	// command's planning session. The "fable" alias runs the latest Claude
	// Fable release — Anthropic's most capable model. Planning is a single
	// read-only session whose output steers the entire implementation, so
	// the strongest reasoning pays off most there, while the implement
	// session itself stays on the cheaper DefaultClaudeModel. Override with
	// SetPlanModel (driven by the implement command's --plan-model flag /
	// PLANWERK_PLAN_MODEL env var).
	DefaultPlanModel = "fable"
	// DefaultClaudeEffort is the compiled-in default reasoning effort.
	// "xhigh" is Claude Code's own default and the recommended setting for
	// coding and agentic workloads; "max" buys little on top of it and
	// tends toward overthinking. Override with SetEffort (driven by the
	// --claude-effort flag / PLANWERK_CLAUDE_EFFORT env var), e.g. "max"
	// for the largest thinking budget on latency-tolerant one-off runs.
	DefaultClaudeEffort = "xhigh"
	// DefaultPlanEffort is the compiled-in default reasoning effort for the
	// implement command's planning session. "max" buys the largest thinking
	// budget: planning is a single read-only session whose output steers the
	// entire implementation, so the deepest reasoning pays off most there —
	// the same reasoning behind running it on the stronger DefaultPlanModel
	// while the implement session stays on the cheaper DefaultClaudeEffort.
	// The latency "max" adds is also tolerable here because planning is
	// one-shot and not on the implement session's critical loop. Override
	// with SetPlanEffort (driven by the implement command's --plan-effort
	// flag / PLANWERK_PLAN_EFFORT env var).
	DefaultPlanEffort = "max"
	// DefaultImplementWorkerEffort is the compiled-in default reasoning effort
	// for the implementer subagents an orchestrated implement session
	// delegates its work packages to (--implement-worker-model). "xhigh"
	// matches DefaultClaudeEffort: the workers do the actual coding, so they
	// get the same effort the single-session implement run would have spent.
	// There is no DefaultImplementWorkerModel — the worker tier is opt-in, and
	// an empty model keeps the historical single-session behavior. Override
	// with the --implement-worker-effort flag / PLANWERK_IMPLEMENT_WORKER_EFFORT
	// env var.
	DefaultImplementWorkerEffort = "xhigh"
	// DefaultStructureModel is the compiled-in default model for the mechanical
	// JSON-structuring passes — the secondary `claude -p` calls that cast an
	// upstream reasoning call's already-reasoned prose into the report schema
	// (review findings, proposals, elaborations, gap analyses, sync entries,
	// capture proposals, review-prepared). The "sonnet" alias runs the latest
	// Sonnet release: structuring is bounded extraction-to-schema, not
	// reasoning, so the heavy DefaultClaudeModel is wasted there. It is
	// deliberately independent of c.model — like DefaultPlanModel, this is a
	// dedicated cheap tier, not a derivation of the main model — and the
	// decodeJSONWithRepair backstop catches any malformed output. Override
	// with WithStructureModel (driven by the --structure-model flag /
	// PLANWERK_STRUCTURE_MODEL env var); pass "opus" to reproduce the former
	// behavior of structuring on the main model.
	DefaultStructureModel = "sonnet"
	// DefaultStructureEffort is the compiled-in default reasoning effort for the
	// structuring passes. "medium" is enough to transcribe already-reasoned
	// prose into JSON: the classification (severity / actionability /
	// confidence) was decided in the upstream reasoning call, so a near-max
	// thinking budget buys nothing here. The model swap is the primary cost
	// lever; this is the secondary tunable. Override with WithStructureEffort
	// (driven by the --structure-effort flag / PLANWERK_STRUCTURE_EFFORT env
	// var).
	DefaultStructureEffort = "medium"
	// claudeAutoPermissionMode is the --permission-mode value the implement
	// command passes to its orchestrated `claude -p` session so tool calls
	// run without an interactive confirmation. "auto" is Claude Code's auto
	// mode: a background classifier vets each action and blocks anything
	// irreversible, destructive, or aimed outside the repository (force
	// push, pushing to main, data exfiltration) while letting the routine
	// work of an implementation — edits, tests, commits, pushing a fresh
	// feature branch, opening a draft PR — proceed unattended. The implement
	// session is one-shot and non-interactive (no human to approve each
	// step), so auto mode is preferred over bypassPermissions precisely
	// because it keeps those safety checks. Read-only commands (review,
	// audit, …) keep the default mode by passing an empty permission mode
	// to runClaude. Requires Claude Code v2.1.83+; see
	// https://code.claude.com/docs/en/auto-mode-config.
	claudeAutoPermissionMode = "auto"
)

// claudeAllowedTools are pre-approved on every orchestrated `claude -p` session
// via --allowed-tools so the non-interactive sessions may use them without a
// permission prompt. WebSearch and WebFetch both require permission by default,
// and a non-interactive session has no human to grant it, so without this the
// read-only sessions (plan, draft, propose, elaborate, audit, …) would have
// every web call silently auto-denied. WebSearch finds sources; WebFetch reads
// the pages it surfaces — the pair lets a session verify, say, the current
// version or deprecation status of a dependency. The bare "WebFetch" entry
// carries no domain specifier, which pre-approves every domain (equivalent to
// WebFetch(domain:*)); a domain-scoped rule would instead prompt — and thus
// auto-deny — on every domain outside the list.
//
// --allowed-tools only ADDS these to the auto-approve list; it does NOT
// restrict the rest of the toolset. The auto-mode sessions (implement, fix,
// address, rebase) already get both from the auto classifier's read-only-HTTP
// allowance, so listing them here is redundant-but-harmless for them and
// load-bearing for the default-mode ones.
var claudeAllowedTools = []string{"WebSearch", "WebFetch"}

// withAllowedTools appends the --allowed-tools flag followed by every entry in
// claudeAllowedTools (a no-op when the list is empty). Both the JSON runner
// (runClaudeWithPermission) and the streaming runner (runClaudeStream) route
// their args through it so the two paths can never drift on which tools a
// session may use. The prompt is fed on stdin, never as a positional argument,
// so a trailing variadic flag is safe — there is no positional for the flag to
// swallow.
func withAllowedTools(args []string) []string {
	if len(claudeAllowedTools) == 0 {
		return args
	}
	args = append(args, "--allowed-tools")
	return append(args, claudeAllowedTools...)
}

// claudeReadOnlyDeniedTools are removed from the model's context on the
// read-only analysis passes (review, audit, propose, elaborate, the specialist
// and adversarial fan-out, and every structuring/repair call) via
// --disallowed-tools. A bare tool name removes the tool entirely — a
// harness-level guarantee, not a prompt-level request — so a pass whose
// contract is to analyze the checkout and never mutate it cannot edit a file
// even if the model is steered into trying. The mutating sessions (implement,
// fix, address, rebase, finalize) keep these tools and pass readOnly=false.
// NotebookEdit is denied for completeness even though the reviewed repos are
// Go: a future caller reusing this path on a notebook repo inherits the same
// guarantee for free.
var claudeReadOnlyDeniedTools = []string{"Edit", "Write", "NotebookEdit"}

// withReadOnlyDenied appends --disallowed-tools followed by every entry in
// claudeReadOnlyDeniedTools when readOnly is true (a no-op when readOnly is
// false or the list is empty). It must be appended before withAllowedTools so
// --allowed-tools stays the trailing variadic flag: --disallowed-tools is a
// variadic flag too, but the following --allowed-tools token terminates its
// value list, and the prompt is fed on stdin so no positional can be swallowed.
func withReadOnlyDenied(args []string, readOnly bool) []string {
	if !readOnly || len(claudeReadOnlyDeniedTools) == 0 {
		return args
	}
	args = append(args, "--disallowed-tools")
	return append(args, claudeReadOnlyDeniedTools...)
}

// runSpec bundles the per-invocation knobs behind one `claude -p` session so
// the buffered and streaming runners share one shape instead of ten parallel
// parameters. The runClaude* wrappers construct it; runClaudeWithPermission
// and runClaudeStream consume it.
type runSpec struct {
	dir            string
	label          string
	permissionMode string // --permission-mode when non-empty
	model          string
	effort         string
	readOnly       bool   // deny the write tools (withReadOnlyDenied)
	jsonSchema     string // --json-schema when non-empty
	agentsJSON     string // --agents when non-empty
	// sessionID, when non-empty, pins the CLI session's id via --session-id so
	// a later invocation can resume the very same session with its full
	// context. resume flips the invocation from starting a fresh session to
	// resuming sessionID via --resume — the completion nudge's follow-up turn.
	// Both stay zero for every ordinary one-shot call, leaving the historical
	// invocation unchanged.
	sessionID string
	resume    bool
}

// withSession appends the session-identity flags: --session-id <id> pins a
// fresh session's id so a follow-up turn can find it, --resume <id> continues
// that session in place of starting a fresh one. A no-op when no session id is
// set — the ordinary one-shot invocation. Both runner paths route their args
// through it so the buffered and streaming runners cannot drift on how a
// session is pinned or resumed.
func withSession(args []string, spec runSpec) []string {
	if spec.sessionID == "" {
		return args
	}
	if spec.resume {
		return append(args, "--resume", spec.sessionID)
	}
	return append(args, "--session-id", spec.sessionID)
}

// newSessionID returns a fresh RFC 4122 version-4 UUID for the CLI's
// --session-id flag (which requires a valid UUID). It returns "" when the
// system's entropy source fails — the caller then runs without a pinned
// session id, which merely disables the completion nudge for that call rather
// than failing the session.
func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// withAgents appends the --agents flag carrying the inline subagent
// definitions (a JSON object mapping agent names to their definition) when
// agentsJSON is non-empty, and is a no-op otherwise. Passing the definitions
// as a CLI flag — rather than writing .claude/agents/ files into the target
// checkout — keeps the orchestrated session hermetic: the flag outranks
// project settings and leaves the reviewed repository untouched. Only the
// implement session in orchestrator mode passes a non-empty value (see
// implementAgentsJSON); every other session runs without subagent
// definitions. Both runner paths route their args through it so the buffered
// and streaming runners cannot drift on how a subagent definition reaches the
// CLI.
func withAgents(args []string, agentsJSON string) []string {
	if agentsJSON == "" {
		return args
	}
	return append(args, "--agents", agentsJSON)
}

// cliJSONSchema prepares a schema document for the CLI's --json-schema flag by
// dropping its top-level "$schema" dialect declaration. The CLI validates the
// document with a draft-07 validator that has no 2020-12 meta-schema
// registered, so a document declaring that dialect is rejected outright —
// "--json-schema is not a valid JSON Schema: no schema with key or ref
// https://json-schema.org/draft/2020-12/schema" — and the session exits before
// the model is ever called, failing every structuring pass. Without the
// declaration the CLI applies its own dialect, and the keywords the embedded
// schemas use ($defs, $ref, type, enum, items, required, additionalProperties)
// mean the same thing under both drafts, so nothing is validated more loosely.
// The embedded documents stay authoritative 2020-12 for the schema subcommand
// and the contract tests; only this wire copy is adjusted. A document that does
// not parse as a JSON object — including the empty string the schema-less
// callers pass — travels through untouched, so the CLI reports the real defect
// instead of this helper masking it.
func cliJSONSchema(doc string) string {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(doc), &fields); err != nil {
		return doc
	}
	if _, ok := fields["$schema"]; !ok {
		return doc
	}
	delete(fields, "$schema")
	stripped, err := json.Marshal(fields)
	if err != nil {
		return doc
	}
	return string(stripped)
}

// hermeticArgs appends the flags that isolate an orchestrated `claude -p`
// session from the invoking user's global configuration so the same input
// yields the same output across machines and CI — the predictability the
// prompt-design doctrine treats as the root virtue. --setting-sources project
// drops the user-global ~/.claude/settings.json (and settings.local.json),
// keeping only the reviewed repo's committed .claude/settings.json, which
// travels with the repo and so is reproducible by construction; the CLI flags
// this runner passes (--model, --permission-mode, the tool flags) outrank
// project settings, so a reviewed repo cannot override them. --strict-mcp-config
// with no --mcp-config loads zero MCP servers, dropping any the user configured
// globally — none of the prompts need one. Both runner paths route through this
// helper so the JSON and streaming runners cannot drift on isolation.
//
// It deliberately does NOT suppress a user-global ~/.claude/CLAUDE.md: Claude
// Code loads memory independently of --setting-sources, and the only switch
// that drops it (--bare) also strips Read/Grep/Glob and the /review skill the
// analysis passes depend on. In CI — the primary use case — no user-global
// CLAUDE.md exists, so that residual is a local-run caveat (see design decision
// #45 and the configuration reference). WithInheritUserConfig(true) opts out of
// hermetic mode entirely for an environment whose claude authentication lives
// in user-global settings (e.g. apiKeyHelper).
func (c *Client) hermeticArgs(args []string) []string {
	if c.inheritUserConfig {
		return args
	}
	return append(args, "--setting-sources", "project", "--strict-mcp-config")
}

// Client runs Claude Code sessions with a fixed configuration. Each Client
// owns its own timeout, model, and effort settings, so independent runners can
// execute concurrently without sharing mutable state — the injectable
// counterpart to the package-level configuration this type replaces. Construct
// one with NewClient and thread it through the runners.
type Client struct {
	timeout time.Duration
	model   string
	// implementModel, when non-empty, overrides model for the implement
	// session only — the single code-writing `claude -p` call — leaving the
	// planning session on planModel and every other session (simplify/review
	// apply, finalize, fix, address, rebase) on model. Unlike planModel and
	// structureModel it has no compiled-in default: the empty zero value means
	// "inherit model", so --claude-model keeps steering the implement session
	// unless --implement-model is set. Set via WithImplementModel.
	implementModel  string
	planModel       string
	structureModel  string
	effort          string
	planEffort      string
	structureEffort string
	showOutput      bool

	// inheritUserConfig, when true, lets orchestrated `claude -p` sessions
	// load the invoking user's global ~/.claude settings and MCP servers. It
	// defaults to false: hermeticArgs isolates every session
	// (--setting-sources project --strict-mcp-config) so a review is
	// reproducible across machines and CI rather than varying with whoever's
	// ~/.claude happens to be present. Set via WithInheritUserConfig.
	inheritUserConfig bool

	// usageMu guards the per-Run usage accumulator. The review fan-out runs
	// several Claude calls concurrently on one shared Client (errgroup over
	// Review/AdversarialReview/CoverageMap/…), so addUsage must be safe to call
	// from multiple goroutines.
	usageMu sync.Mutex
	usage   report.Usage
}

// Option configures a Client. Pass any number of options to NewClient; later
// options win when two set the same field.
type Option func(*Client)

// NewClient returns a Client seeded with the compiled-in defaults
// (DefaultClaudeTimeout/Model/Effort and the planning defaults), then applies
// opts. With no options it behaves exactly as the historical package defaults.
func NewClient(opts ...Option) *Client {
	c := &Client{
		timeout:         DefaultClaudeTimeout,
		model:           DefaultClaudeModel,
		planModel:       DefaultPlanModel,
		structureModel:  DefaultStructureModel,
		effort:          DefaultClaudeEffort,
		planEffort:      DefaultPlanEffort,
		structureEffort: DefaultStructureEffort,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// WithTimeout sets the per-invocation Claude Code timeout. A non-positive d is
// ignored and the default is preserved — that keeps a misconfigured flag from
// silently disabling the timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// WithModel sets the model passed to Claude Code via --model. An empty m is
// ignored so a misconfigured flag cannot select an empty model.
func WithModel(m string) Option {
	return func(c *Client) {
		if m != "" {
			c.model = m
		}
	}
}

// WithImplementModel sets the model used by the implement session only (the
// code-writing phase); the surrounding sessions stay on the main model. An
// empty m is ignored, which leaves the zero value in place — the implement
// session then inherits the main model (see Client.implementModel).
func WithImplementModel(m string) Option {
	return func(c *Client) {
		if m != "" {
			c.implementModel = m
		}
	}
}

// WithPlanModel sets the model used by Plan sessions (the implement command's
// planning phase). An empty m is ignored.
func WithPlanModel(m string) Option {
	return func(c *Client) {
		if m != "" {
			c.planModel = m
		}
	}
}

// WithStructureModel sets the model used by the JSON-structuring passes (the
// secondary `claude -p` calls that cast reasoned prose into the report schema).
// An empty m is ignored so a misconfigured flag cannot select an empty model.
func WithStructureModel(m string) Option {
	return func(c *Client) {
		if m != "" {
			c.structureModel = m
		}
	}
}

// WithEffort sets the reasoning effort passed to Claude Code via --effort. An
// empty e is ignored so a misconfigured flag cannot select an empty effort.
func WithEffort(e string) Option {
	return func(c *Client) {
		if e != "" {
			c.effort = e
		}
	}
}

// WithPlanEffort sets the reasoning effort used by Plan sessions (the implement
// command's planning phase). An empty e is ignored.
func WithPlanEffort(e string) Option {
	return func(c *Client) {
		if e != "" {
			c.planEffort = e
		}
	}
}

// WithStructureEffort sets the reasoning effort used by the JSON-structuring
// passes. An empty e is ignored so a misconfigured flag cannot select an empty
// effort.
func WithStructureEffort(e string) Option {
	return func(c *Client) {
		if e != "" {
			c.structureEffort = e
		}
	}
}

// WithShowOutput toggles live streaming of Claude Code output. When false (the
// default), runClaude buffers the result via --output-format json. When true,
// runClaude delegates to runClaudeStream which uses
// --output-format stream-json --verbose and surfaces assistant text and tool
// activity to a streamSink as it arrives.
func WithShowOutput(b bool) Option {
	return func(c *Client) { c.showOutput = b }
}

// WithInheritUserConfig controls whether orchestrated `claude -p` sessions
// inherit the invoking user's global ~/.claude settings and MCP servers. The
// default (false) runs every session hermetically — see hermeticArgs — so the
// same input yields the same output across machines and CI. Pass true only when
// claude's authentication depends on user-global configuration that hermetic
// mode would drop (e.g. an apiKeyHelper defined in ~/.claude/settings.json).
func WithInheritUserConfig(b bool) Option {
	return func(c *Client) { c.inheritUserConfig = b }
}

// runClaude invokes claude in the given directory on its default permission
// mode and returns the extracted text response along with the resolved model
// id the session reported. Use it for the read-only analysis steps (review,
// audit, …) that do not mutate the checkout; the JSON-structuring passes —
// and their repair recovery — use runClaudeStructure for the dedicated cheap
// tier instead.
func (c *Client) runClaude(dir, prompt, label string) (text, model string, err error) {
	return c.runClaudeWithPermission(runSpec{dir: dir, label: label, model: c.model, effort: c.effort, readOnly: true}, prompt)
}

// runClaudePlan is runClaude on the dedicated planning model (planModel,
// default "fable") at the dedicated planning effort (planEffort, default
// "max"). The implement command's planning session uses it: the session only
// reads the checkout and emits the implementation plan as text, so it keeps
// the default (read-only) permission mode while the strongest-reasoning model
// thinks at the largest budget — the one session where that depth steers the
// whole implementation.
func (c *Client) runClaudePlan(dir, prompt, label string) (text, model string, err error) {
	return c.runClaudeWithPermission(runSpec{dir: dir, label: label, model: c.planModel, effort: c.planEffort, readOnly: true}, prompt)
}

// runClaudeStructure is runClaude on the dedicated structuring tier
// (structureModel/structureEffort, defaults "sonnet"/"medium"). The JSON
// structuring passes use it: a structuring call only reads upstream prose and
// transcribes it into the report schema (it passes dir="" — no checkout — and
// keeps the read-only permission mode), so it runs on the cheap mechanical tier
// rather than the heavy reasoning model the upstream call used. The
// decodeJSONWithRepair backstop guards malformed output.
func (c *Client) runClaudeStructure(prompt, label string) (text, model string, err error) {
	return c.runClaudeStructureWithSchema(prompt, label, "")
}

// runClaudeStructureWithSchema is runClaudeStructure that additionally passes a
// JSON Schema to the CLI via --json-schema, constraining the structured output
// to that shape. Only the review structuring pass uses it (with
// schema.StructuredReview); every other structuring caller uses the schema-less
// runClaudeStructure, since the CLI's schema enforcement is a hard constraint we
// only want where a stable wire contract exists. decodeJSONWithRepair still
// backstops the decode either way.
func (c *Client) runClaudeStructureWithSchema(prompt, label, jsonSchema string) (text, model string, err error) {
	return c.runClaudeWithPermission(runSpec{label: label, model: c.structureModel, effort: c.structureEffort, readOnly: true, jsonSchema: jsonSchema}, prompt)
}

// runClaudeAuto is runClaude with claudeAutoPermissionMode, letting the
// session edit files and run git/gh/test commands without an interactive
// approval — the orchestrated, one-shot `claude -p` sessions run unattended
// inside a checkout and must commit and push without a human confirming each
// step, while the auto-mode classifier still vets every action. The sessions
// without a terminal-report contract (verify-implementation, address, the
// rebase sessions) call it directly; the report-bearing mutating sessions go
// through runClaudeAutoReport, which adds the completion nudge on top of the
// same autoSpec.
func (c *Client) runClaudeAuto(dir, prompt, label string) (text, model string, err error) {
	return c.runClaudeWithPermission(c.autoSpec(dir, label), prompt)
}

// autoSpec is the runSpec every auto-mode mutating session starts from: the
// shared main model and effort, claudeAutoPermissionMode, and the write tools
// kept (readOnly false). runClaudeAuto and the completion-gated variants build
// on it so the auto-mode invocation is defined once.
func (c *Client) autoSpec(dir, label string) runSpec {
	return runSpec{dir: dir, label: label, permissionMode: claudeAutoPermissionMode, model: c.model, effort: c.effort}
}

// runClaudeImplement is runClaudeAuto on the implement session's model
// override (implementModel) when one is set, and on the shared main model
// otherwise. Only the Implement call uses it: --implement-model swaps the
// model of the single code-writing session without recoloring the auto-mode
// sessions around it (simplify/review apply, finalize, fix, address, rebase),
// which stay on runClaudeAuto and the main model. agentsJSON, when non-empty,
// carries the inline implementer-subagent definition the orchestrated mode
// passes via --agents (see implementAgentsJSON); an empty value runs the
// session without subagent definitions, exactly as before orchestrator mode
// existed. The session runs under the completion nudge: when it ends without
// its implementation report (heading + terminal STATUS), the same session is
// resumed to finish and report instead of the run aborting outright.
func (c *Client) runClaudeImplement(dir, prompt, label, agentsJSON string) (text, model string, err error) {
	spec := c.autoSpec(dir, label)
	spec.model = c.implementSessionModel()
	spec.agentsJSON = agentsJSON
	return c.runWithCompletionNudge(spec, prompt, implementReportHeading, implementReportStatusChoices)
}

// implementSessionModel resolves the model for the implement session: the
// dedicated implementModel override when set, the shared main model otherwise —
// the "--implement-model defaults to --claude-model" contract.
func (c *Client) implementSessionModel() string {
	if c.implementModel != "" {
		return c.implementModel
	}
	return c.model
}

// runClaudeWithPermission is the shared implementation behind runClaude,
// runClaudePlan, runClaudeAuto, and runClaudeImplement. The spec carries the
// per-invocation knobs: permissionMode, when non-empty, is passed to claude as
// --permission-mode (an empty value leaves claude on its default mode); model
// and effort are the --model/--effort values the caller selected; readOnly is
// true for the analysis passes (runClaude, runClaudePlan) and denies the write
// tools via withReadOnlyDenied so the session cannot mutate the checkout;
// agentsJSON, when non-empty, is passed via --agents so the session can
// delegate to inline-defined subagents (only the orchestrated implement
// session uses it — see withAgents); sessionID/resume pin or resume the CLI
// session for the completion nudge (see withSession). Every invocation is also
// isolated from user-global config via hermeticArgs and pre-approves
// claudeAllowedTools via --allowed-tools (see withAllowedTools). The label
// tags elapsed-time progress updates (or per-line stream prefixes when
// streaming is enabled).
//
// When c.showOutput is false it uses --output-format json and captures the
// full result via cmd.Output(). When c.showOutput is true it delegates to
// runClaudeStream, which uses --output-format stream-json --verbose and
// surfaces output incrementally; the periodic heartbeat is skipped in that
// mode because the stream itself is the heartbeat.
func (c *Client) runClaudeWithPermission(spec runSpec, prompt string) (string, string, error) {
	// Normalize once, above the fork, so neither runner path can pass the CLI a
	// dialect declaration it cannot resolve.
	spec.jsonSchema = cliJSONSchema(spec.jsonSchema)
	if c.showOutput {
		return c.runClaudeStream(spec, prompt)
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	stopProgress := startProgress(spec.label)
	defer stopProgress()

	args := []string{
		"-p",
		"--model", spec.model,
		"--effort", spec.effort,
		"--output-format", "json",
	}
	if spec.permissionMode != "" {
		args = append(args, "--permission-mode", spec.permissionMode)
	}
	if spec.jsonSchema != "" {
		args = append(args, "--json-schema", spec.jsonSchema)
	}
	args = withSession(args, spec)
	args = withAgents(args, spec.agentsJSON)
	args = c.hermeticArgs(args)
	args = withReadOnlyDenied(args, spec.readOnly)
	args = withAllowedTools(args)
	cmd := exec.CommandContext(ctx, "claude", args...)
	if spec.dir != "" {
		cmd.Dir = spec.dir
	}
	cmd.Stdin = strings.NewReader(prompt)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return "", "", fmt.Errorf("claude (model %s): %w", spec.model, err)
		}
		return "", "", claudeRunError(err, spec.model, out, exitErr.Stderr)
	}
	// The returned model is the exact id the envelope reports (e.g.
	// "claude-opus-5"); the caller threads it per-run into the artifact
	// footers instead of a package-level global, mirroring runClaudeStream.
	text, resolvedModel, usage, cost, err := extractText(out)
	if err != nil {
		return "", "", err
	}
	c.addUsage(usage, cost)
	return text, resolvedModel, nil
}

type claudeResponse struct {
	Result string `json:"result"`
	// StructuredOutput carries the schema-validated object the CLI produces when
	// invoked with --json-schema. It is preferred over Result when present so a
	// structuring pass reads the constrained output directly; when the flag was
	// not passed (or the CLI carries the object in Result instead) it stays nil
	// and extractText falls back to Result. Captured raw and stringified in
	// extractText, so an envelope that omits it costs nothing.
	StructuredOutput json.RawMessage `json:"structured_output,omitempty"`
	// Model is the resolved model id the CLI reports in the JSON envelope
	// (e.g. "claude-opus-5"). It is the non-streaming counterpart of the
	// streamEvent init model and feeds the attribution footers.
	Model string `json:"model,omitempty"`
	// Usage and TotalCostUSD carry the per-call cumulative token counts and the
	// CLI's own estimated cost; they feed the per-Run usage accumulator. Both are
	// captured raw and decoded best-effort in extractText, decoupled from the
	// result/model decode above: a usage-schema change — a reshaped usage object,
	// a stringified cost, a token count past int64 — must degrade these figures to
	// zero, not fail the envelope and discard a result the call carried fine.
	Usage        json.RawMessage `json:"usage"`
	TotalCostUSD json.RawMessage `json:"total_cost_usd"`
	// IsError marks an envelope the CLI emitted for a FAILED turn. Claude Code
	// exits non-zero whenever it sets this — for an upstream API error and for
	// an exhausted turn budget alike — so only the error path reads it; a
	// zero-exit call never sees it set. Subtype and APIErrorStatus qualify the
	// failure; envelopeFailure turns the three into one diagnostic line.
	IsError bool `json:"is_error,omitempty"`
	// Subtype names a failure that carries no result text at all
	// ("error_max_turns"). A successful turn reports "success" here, which
	// envelopeFailure must therefore never quote back as a reason.
	Subtype string `json:"subtype,omitempty"`
	// APIErrorStatus is the upstream HTTP status behind an API-level failure
	// (429 once a model's rate limit is hit). Captured raw and stringified so a
	// status the CLI one day emits as a string cannot make the whole envelope
	// unparseable — the same wire-drift tolerance Usage and TotalCostUSD get.
	APIErrorStatus json.RawMessage `json:"api_error_status,omitempty"`
}

// claudeRunError renders a failed `claude -p` invocation into an error that
// names what actually went wrong. Claude Code reports an API-level failure — a
// hit rate limit, an exhausted turn budget — by writing its result envelope to
// STDOUT and exiting non-zero, leaving STDERR empty. A runner that reports only
// stderr therefore prints "claude: exit status 1\nstderr: " and drops the one
// sentence that explains the failure ("You've reached your Fable 5 limit."),
// which reads exactly like a crashed binary. stdout is the buffered envelope —
// or, on the streaming path, the raw `result` event, which repeats the same
// fields; stderr carries the failures that never reach the API, such as an
// unknown flag. The model alias is named either way: the envelope talks about
// "Fable 5" while the operator only ever typed `implement`, so which session hit
// the limit is the actionable part.
func claudeRunError(err error, model string, stdout, stderr []byte) error {
	if failure := envelopeFailure(stdout); failure != "" {
		return fmt.Errorf("claude (model %s): %w: %s", model, err, failure)
	}
	if msg := bytes.TrimSpace(stderr); len(msg) > 0 {
		return fmt.Errorf("claude (model %s): %w\nstderr: %s", model, err, msg)
	}
	if msg := bytes.TrimSpace(stdout); len(msg) > 0 {
		return fmt.Errorf("claude (model %s): %w\nstdout: %s", model, err, head(msg, 200))
	}
	return fmt.Errorf("claude (model %s): %w (no failure envelope on stdout and no stderr output)", model, err)
}

// envelopeFailure extracts the human-readable reason from a `claude -p` result
// envelope that reports a failed turn, and "" when raw is not an envelope or the
// turn succeeded. Both observed failure shapes are covered: an API error carries
// the reason in result and the upstream status in api_error_status (is_error
// true, api_error_status 429, result "You've reached your Fable 5 limit."),
// while a harness-level abort carries no result at all and names itself only in
// subtype (is_error true, subtype "error_max_turns"). It reads the streaming
// path's raw `result` event just as well, since that event repeats these fields
// verbatim — so the two runners cannot drift on how a failure is diagnosed.
func envelopeFailure(raw []byte) string {
	var resp claudeResponse
	if err := json.Unmarshal(raw, &resp); err != nil || !resp.IsError {
		return ""
	}
	reason := strings.TrimSpace(resp.Result)
	if reason == "" && resp.Subtype != "success" {
		reason = resp.Subtype
	}
	status := strings.TrimSpace(string(resp.APIErrorStatus))
	if status == "" || status == "null" {
		return reason
	}
	if reason == "" {
		return "api error " + status
	}
	return fmt.Sprintf("api error %s: %s", status, reason)
}

// extractText extracts the response text, the resolved model id, and the
// per-call token usage and estimated cost from Claude's JSON output envelope.
// When the output is not the expected envelope it returns an error wrapping the
// parse failure and a truncated copy of the raw output. Failing loudly keeps a
// changed CLI wire format (schema rename, error envelope, OAuth challenge) from
// being silently treated as the assistant's reply, which would otherwise
// produce nonsense findings or empty reports.
func extractText(raw []byte) (text, model string, usage tokenUsage, cost float64, err error) {
	var resp claudeResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", "", tokenUsage{}, 0, fmt.Errorf("claude: parse output envelope: %w; first 200 bytes: %q", err, head(raw, 200))
	}
	// Decode usage and cost best-effort, independently of the result/model decode
	// above. A malformed, reshaped, or absent block leaves the figures at zero
	// instead of failing the whole call — usage-schema drift must never cost a
	// result the envelope carried fine.
	_ = json.Unmarshal(resp.Usage, &usage)
	_ = json.Unmarshal(resp.TotalCostUSD, &cost)
	return preferStructuredOutput(resp.StructuredOutput, resp.Result), resp.Model, usage, cost, nil
}

// preferStructuredOutput returns the CLI's schema-validated structured_output as
// text when it is present, falling back to result otherwise. It is defensive
// against either envelope variant: --json-schema may surface the object in
// structured_output, or the CLI may leave it in result. A literal JSON null in
// structured_output is treated as absent.
func preferStructuredOutput(structured json.RawMessage, result string) string {
	if s := strings.TrimSpace(string(structured)); s != "" && s != "null" {
		return s
	}
	return result
}

// head returns the first n bytes of b, or all of b when it is shorter. It
// bounds the raw output embedded in parse-failure errors so logs stay readable.
func head(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}

package claude

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/planwerk/planwerk-agent/internal/claudeplugin"
	"github.com/planwerk/planwerk-agent/internal/impeccable"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
	"github.com/planwerk/planwerk-agent/internal/report/schema"
	"github.com/planwerk/planwerk-agent/internal/search"
)

const (
	// DefaultClaudeTimeout is the compiled-in maximum time for a single Claude
	// Code invocation; override it with WithTimeout (--claude-timeout /
	// PLANWERK_CLAUDE_TIMEOUT) (decision 74).
	DefaultClaudeTimeout = 60 * time.Minute
	// DefaultClaudeModel is the compiled-in model passed via --model. The "opus"
	// alias runs the latest Opus release without re-pinning; override it with
	// WithModel (--claude-model / PLANWERK_CLAUDE_MODEL) (decision 101).
	DefaultClaudeModel = "opus"
	// DefaultPlanModel is the compiled-in model for the implement command's
	// planning session; override it with WithPlanModel (--plan-model /
	// PLANWERK_PLAN_MODEL) (decision 101).
	DefaultPlanModel = "opus"
	// DefaultClaudeEffort is the compiled-in reasoning effort passed via
	// --effort; override it with WithEffort (--claude-effort /
	// PLANWERK_CLAUDE_EFFORT) (decision 101).
	DefaultClaudeEffort = "xhigh"
	// DefaultPlanEffort is the compiled-in reasoning effort for the implement
	// command's planning session; override it with WithPlanEffort (--plan-effort
	// / PLANWERK_PLAN_EFFORT) (decision 101).
	DefaultPlanEffort = "xhigh"
	// DefaultImplementWorkerEffort is the compiled-in reasoning effort for the
	// implementer subagents of an orchestrated implement session; override it
	// with --implement-worker-effort / PLANWERK_IMPLEMENT_WORKER_EFFORT. There is
	// no worker-model default; an empty worker model keeps the single-session
	// run (decision 74).
	DefaultImplementWorkerEffort = "xhigh"
	// DefaultFinderEffort is the compiled-in reasoning effort for the read-only
	// finder passes (see finderSpec). Empty inherits the main tier;
	// override it with --finder-effort / PLANWERK_FINDER_EFFORT (decision 79).
	DefaultFinderEffort = ""
	// DefaultFinderModel is the compiled-in model for the finder passes. Empty
	// inherits the main tier; override it with --finder-model /
	// PLANWERK_FINDER_MODEL (decision 79).
	DefaultFinderModel = ""
	// DefaultFinderTools is the compiled-in --finder-tools list. Empty passes no
	// --tools, so the seven passes that emit findings load Claude Code's default
	// tool set; override it with WithFinderTools (--finder-tools /
	// PLANWERK_FINDER_TOOLS) (decision 152).
	DefaultFinderTools = ""
	// DefaultStructureModel is the compiled-in model for the structuring tier:
	// the secondary `claude -p` calls that transcribe an upstream call's
	// already-reasoned prose into its artifact's JSON schema. It is independent
	// of the main model; override it with WithStructureModel
	// (--structure-model / PLANWERK_STRUCTURE_MODEL) (decisions 56 and 101).
	DefaultStructureModel = "sonnet"
	// DefaultStructureEffort is the compiled-in reasoning effort for the
	// structuring tier; override it with WithStructureEffort (--structure-effort
	// / PLANWERK_STRUCTURE_EFFORT) (decisions 56 and 101).
	DefaultStructureEffort = "xhigh"
	// DefaultBrainAnalysisModel is the compiled-in model for the analysis of
	// `brain bootstrap`, the session that proposes the pages of one unit;
	// override it with WithBrainAnalysisModel (--analysis-model /
	// PLANWERK_BRAIN_ANALYSIS_MODEL) (decisions 112 and 120).
	DefaultBrainAnalysisModel = "haiku"
	// DefaultBrainAnalysisEffort is the compiled-in reasoning effort for the
	// analysis of `brain bootstrap`; override it with WithBrainAnalysisEffort
	// (--analysis-effort / PLANWERK_BRAIN_ANALYSIS_EFFORT) (decisions 112 and
	// 120).
	DefaultBrainAnalysisEffort = "xhigh"
	// DefaultBrainStructureModel is the compiled-in structuring model of `brain
	// bootstrap`: the command passes it to WithStructureModel in place of
	// DefaultStructureModel, so the structuring of its analysis and review runs
	// on it unless --structure-model or PLANWERK_STRUCTURE_MODEL names another
	// (decision 120). The structuring effort keeps DefaultStructureEffort.
	DefaultBrainStructureModel = "haiku"
	// DefaultBrainReviewModel is the compiled-in model for the page review of
	// `brain bootstrap`, the second session that judges every page the
	// analysis proposes; override it with WithBrainReviewModel (--review-model
	// / PLANWERK_BRAIN_REVIEW_MODEL) (decisions 112 and 120).
	DefaultBrainReviewModel = "haiku"
	// DefaultBrainReviewEffort is the compiled-in reasoning effort for the page
	// review of `brain bootstrap`; override it with WithBrainReviewEffort
	// (--review-effort / PLANWERK_BRAIN_REVIEW_EFFORT) (decisions 112 and 120).
	DefaultBrainReviewEffort = "xhigh"
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
	// audit, …) pass an empty permission mode to runClaude and run in
	// claudeReadOnlyPermissionMode. Requires Claude Code v2.1.306+; see
	// https://code.claude.com/docs/en/auto-mode-config.
	claudeAutoPermissionMode = "auto"
	// claudeReadOnlyPermissionMode is the --permission-mode claudeArgs passes
	// to every read-only and structuring session that names no mode of its
	// own. "dontAsk" denies every tool call that is not pre-approved, so a
	// checkout's permissions.defaultMode cannot start a read-only pass in a
	// mode that approves edits or commands (decision 123).
	claudeReadOnlyPermissionMode = "dontAsk"
	// claudeWaitDelay bounds how long a timed-out invocation may still block
	// after its deadline. exec.CommandContext kills only the `claude` process
	// itself; a test run, build, or subagent it started inherits the stdout
	// pipe and keeps it open, and Output/Wait block until that grandchild ends
	// — so a 60-minute deadline could be followed by an unbounded wait on a
	// process nobody is reading any more. WaitDelay closes the pipes this long
	// after the deadline instead. Two seconds leaves a killed session room to
	// flush a final envelope without making the failure path drag.
	claudeWaitDelay = 2 * time.Second
)

// efforts is the closed set of reasoning-effort levels Claude Code accepts.
var efforts = []string{"low", "medium", "high", "xhigh", "max"}

// ValidEffort reports whether e is one of the reasoning-effort levels Claude
// Code accepts (EffortLevels). The root command checks the structuring and
// finder efforts with it, and the eval every effort it reads, before a session
// runs.
func ValidEffort(e string) bool {
	return slices.Contains(efforts, e)
}

// EffortLevels lists the levels ValidEffort accepts as "low, medium, high,
// xhigh, max", for the error that rejects any other effort.
func EffortLevels() string {
	return strings.Join(efforts, ", ")
}

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
// required for the read-only ones, which run in
// claudeReadOnlyPermissionMode.
var claudeAllowedTools = []string{"WebSearch", "WebFetch"}

// withAllowedTools appends the --allowed-tools flag followed by every entry in
// claudeAllowedTools and then every rule in rules, the permission rules of
// this one session. The prompt is fed on stdin, never as a positional
// argument, so a trailing variadic flag is safe — there is no positional for
// the flag to swallow.
func withAllowedTools(args []string, rules ...string) []string {
	args = append(args, "--allowed-tools")
	args = append(args, claudeAllowedTools...)
	return append(args, rules...)
}

// claudeReadOnlyDeniedTools are the write tools --disallowed-tools removes from
// the read-only passes, so a pass that must not mutate the checkout cannot edit
// a file even if the model is steered into trying (decision 46).
var claudeReadOnlyDeniedTools = []string{"Edit", "Write", "NotebookEdit"}

// withReadOnlyDenied appends --disallowed-tools followed by every entry in
// claudeReadOnlyDeniedTools when readOnly is true (a no-op otherwise). It must
// be appended before withAllowedTools so --allowed-tools stays the trailing
// variadic flag: --disallowed-tools is a variadic flag too, but the following
// --allowed-tools token terminates its value list, and the prompt is fed on
// stdin so no positional can be swallowed.
func withReadOnlyDenied(args []string, readOnly bool) []string {
	if !readOnly {
		return args
	}
	args = append(args, "--disallowed-tools")
	return append(args, claudeReadOnlyDeniedTools...)
}

// withDenyRules appends rules to the --disallowed-tools list: it continues the
// list withReadOnlyDenied opened for a read-only session, and opens one for a
// mutating session. A nil rules is a no-op. Like withReadOnlyDenied it must
// precede withAllowedTools, whose flag terminates the variadic list.
func withDenyRules(args []string, readOnly bool, rules []string) []string {
	if len(rules) == 0 {
		return args
	}
	if !readOnly {
		args = append(args, "--disallowed-tools")
	}
	return append(args, rules...)
}

// skillDenyRules returns one Skill deny rule per name, in the parameter form
// Skill(skill:<name>), which Claude Code matches whichever of its names the
// model calls the skill by (verified 2026-10-07: a session under
// --permission-mode auto had the invocation refused and recorded as a
// permission denial, while an unruled session ran the skill).
func skillDenyRules(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	rules := make([]string, 0, len(names))
	for _, n := range names {
		rules = append(rules, "Skill(skill:"+n+")")
	}
	return rules
}

// readOnlyHookSettings is the --settings value that switches every hook off for
// a read-only session, so a hook in the reviewed checkout's
// .claude/settings.json never runs as the operator (decision 95).
const readOnlyHookSettings = `{"disableAllHooks":true}`

// withHooksDisabled appends --settings readOnlyHookSettings when readOnly is
// true and nothing otherwise; --settings takes exactly one value, so it may sit
// anywhere before the trailing variadic tool flags (decision 95).
func withHooksDisabled(args []string, readOnly bool) []string {
	if !readOnly {
		return args
	}
	return append(args, "--settings", readOnlyHookSettings)
}

// withNoTools appends --tools followed by a single empty value, which loads
// none of Claude Code's built-in tools, in place of withReadOnlyDenied and
// withAllowedTools. Like --allowed-tools it is variadic, so it is appended
// last, where no following token can be mistaken for one of its values
// (decision 91).
func withNoTools(args []string) []string {
	return append(args, "--tools", "")
}

// withTools appends --tools followed by names joined by commas, the form the
// CLI's help documents ("Bash,Edit,Read"), when names is non-empty, and is a
// no-op otherwise. The one value keeps the variadic flag's list closed by the
// --disallowed-tools or --allowed-tools token claudeArgs appends after it
// (decision 152).
func withTools(args []string, names []string) []string {
	if len(names) == 0 {
		return args
	}
	return append(args, "--tools", strings.Join(names, ","))
}

// runSpec bundles the per-invocation knobs of one `claude -p` session. The
// runClaude* wrappers construct it; runSession and runClaudeStream consume it.
type runSpec struct {
	dir            string
	label          string
	permissionMode string // --permission-mode when non-empty; a read-only spec defaults to claudeReadOnlyPermissionMode
	model          string
	effort         string
	readOnly       bool // deny the write tools (withReadOnlyDenied)
	// noTools removes every built-in tool (withNoTools) and supersedes readOnly:
	// the spec emits neither --disallowed-tools nor --allowed-tools. It also runs
	// the session in structureWorkDir, which runSession resolves
	// before any process starts. Only runClaudeStructure sets it.
	noTools bool
	// jsonSchema is passed via --json-schema when non-empty. The runners of the
	// passes that emit findings, runClaudeFindings and runClaudeFinderFindings,
	// set it (decision 109); runClaudeSchema sets it for the rebase analysis,
	// and Address for an address session given no --claude-plugin plugin
	// (decision 160).
	jsonSchema string
	agentsJSON string // --agents when non-empty
	// appendSystemPrompt is passed as --append-system-prompt when non-empty;
	// only the implement session sets it (decision 108).
	appendSystemPrompt string
	// addDirs holds the directories the session may read besides its working
	// directory: the on-disk pattern catalog directory (patterns.Materialize),
	// which runClaudeMemory, runClaudeFindings, runClaudeSchema, runClaudePlan,
	// and autoSpec add; the project memory directory
	// (patterns.MaterializeMemory), which
	// runClaudeMemory, runClaudeFindings, runClaudePlan, runClaudeBrainReview,
	// and autoMemorySpec add; and the directory of the diff a finder reads
	// (diffscope.Materialize), which runClaudeFindings and
	// runClaudeFinderFindings add. The non-empty entries follow one --add-dir,
	// in order. A noTools
	// spec never honors it, because a structuring session runs in
	// structureWorkDir with no tools and must not be handed any other
	// directory.
	addDirs []string
	// searchRule is the permission rule that pre-approves `brain search` for
	// the repository of the run (search.Surface.AllowRule), "" for a session
	// without the search. It follows claudeAllowedTools, and only a readOnly
	// spec honors it: the sessions that edit, commit, and push get no search.
	// runClaudeMemory, runClaudeFindings, and runClaudePlan set it.
	searchRule string
	// denyRules are the permission deny rules of this one session, emitted
	// under --disallowed-tools after the read-only list (withDenyRules). The
	// fix and address sessions deny the Skill tool the skills the pull request
	// adds or changes (skillDenyRules), because Claude Code loads a skill's
	// body from the checkout they run in.
	denyRules []string
	// tools names the built-in tools the session loads, passed as --tools with
	// the names joined by commas when non-empty (withTools); nil loads Claude
	// Code's default set. The two runners of the passes that emit findings set
	// it from --finder-tools. A noTools spec ignores it (decision 152).
	tools []string
	// sessionID pins the CLI session's id (--session-id) and resume continues
	// that session (--resume). runSession pins a fresh id on every session that
	// names none; the completion nudge sets both to resume a session it pinned
	// (decisions 78 and 152).
	sessionID string
	resume    bool
}

// withSession appends the session-identity flags: --session-id <id> pins a
// fresh session's id so a follow-up turn can find it and the run's log and
// report can name it, --resume <id> continues that session in place of
// starting a fresh one. The id names a transcript only when persistsSession
// holds. A no-op when no session id is set, which runSession leaves only when
// newSessionID failed (decisions 152 and 161).
func withSession(args []string, spec runSpec) []string {
	if spec.sessionID == "" {
		return args
	}
	if spec.resume {
		return append(args, "--resume", spec.sessionID)
	}
	return append(args, "--session-id", spec.sessionID)
}

// persistsSession reports whether Claude Code saves spec's transcript: a
// session that edits always does, a read-only or structuring one only under
// WithKeepTranscripts(true) (decision 161).
func (c *Client) persistsSession(spec runSpec) bool {
	return c.keepTranscripts || (!spec.readOnly && !spec.noTools)
}

// withNoSessionPersistence appends --no-session-persistence when persist is
// false and is a no-op otherwise. The flag takes no value, so it may sit
// anywhere before the trailing variadic tool flags (decision 161).
func withNoSessionPersistence(args []string, persist bool) []string {
	if persist {
		return args
	}
	return append(args, "--no-session-persistence")
}

// newSessionID returns a fresh RFC 4122 version-4 UUID for the CLI's
// --session-id flag (which requires a valid UUID). It returns "" when the
// system's entropy source fails — that session then just runs unpinned, which
// disables the completion nudge for the call and leaves its id to the stream,
// rather than failing the session (decision 152).
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
// agentsJSON is non-empty, and is a no-op otherwise. The definitions travel as
// a flag, not files, so the checkout stays untouched. Only the implement
// session in orchestrator mode passes a value (see implementAgentsJSON)
// (decision 74).
func withAgents(args []string, agentsJSON string) []string {
	if agentsJSON == "" {
		return args
	}
	return append(args, "--agents", agentsJSON)
}

// withAppendSystemPrompt appends --append-system-prompt carrying text when
// text is non-empty, and is a no-op otherwise. The text is one argv element
// and never passes through a shell, so it may contain newlines.
func withAppendSystemPrompt(args []string, text string) []string {
	if text == "" {
		return args
	}
	return append(args, "--append-system-prompt", text)
}

// nonEmptyDirs returns the non-empty entries of dirs, in order, in a slice of
// its own. A session without a pattern catalog or without a project memory
// passes the empty string for that directory.
func nonEmptyDirs(dirs []string) []string {
	return slices.DeleteFunc(slices.Clone(dirs), func(d string) bool { return d == "" })
}

// cliJSONSchema prepares a schema document for the CLI's --json-schema flag by
// dropping its top-level "$schema" dialect declaration. The CLI validates the
// document with a draft-07 validator that has no 2020-12 meta-schema
// registered, so a document declaring that dialect is rejected outright —
// "--json-schema is not a valid JSON Schema: no schema with key or ref
// https://json-schema.org/draft/2020-12/schema" — and the session exits before
// the model is ever called, failing every finder pass. Without the
// declaration the CLI applies its own dialect, and the keywords the embedded
// schemas use ($defs, $ref, type, enum, items, required, additionalProperties)
// mean the same thing under both drafts, so nothing is validated more loosely.
// The embedded documents stay authoritative 2020-12 for the schema subcommand
// and the contract tests; only this wire copy is adjusted. Only the top-level
// object is re-encoded, with its keys sorted; every nested value passes through
// as written, so the order of a finding's properties under $defs, which the
// session fills in that order, survives (decision 136). A decode into
// map[string]any would sort them alphabetically. A document that does
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

// hermeticArgs appends the flags that isolate a session from the invoking
// user's global configuration. For a mutating session, --setting-sources
// project drops the user-global ~/.claude/settings.json (and
// settings.local.json) and keeps the checkout's committed
// .claude/settings.json; --strict-mcp-config with no --mcp-config loads no MCP
// servers. A read-only session gets --setting-sources= in one element, an
// empty source list, so it loads no settings file and the checkout's
// permission rules never reach it (decision 123). WithInheritUserConfig(true)
// opts out for an environment whose claude authentication lives in
// user-global settings (e.g. apiKeyHelper): a mutating session then loads
// every source, a read-only one the user's alone (decision 45).
func (c *Client) hermeticArgs(args []string, readOnly bool) []string {
	switch {
	case c.inheritUserConfig && readOnly:
		return append(args, "--setting-sources", "user")
	case c.inheritUserConfig:
		return args
	case readOnly:
		return append(args, "--setting-sources=", "--strict-mcp-config")
	}
	return append(args, "--setting-sources", "project", "--strict-mcp-config")
}

// Client runs Claude Code sessions with a fixed configuration. Each Client
// owns its own timeout, model, and effort settings, so independent runners can
// execute concurrently without sharing mutable state. Construct one with
// NewClient and thread it through the runners.
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
	implementModel string
	planModel      string
	structureModel string
	// finderModel/finderEffort select the tier of the read-only finder passes
	// (see FinderTier). NewClient seeds them from DefaultFinderModel and
	// DefaultFinderEffort; an empty value inherits the main tier (decision 79).
	finderModel  string
	finderEffort string
	// finderTools is the --tools list of the seven passes that emit findings,
	// set via WithFinderTools; nil, the default (DefaultFinderTools), passes no
	// --tools (decision 152).
	finderTools     []string
	effort          string
	planEffort      string
	structureEffort string
	// brainAnalysisModel/brainAnalysisEffort select the tier of the analysis
	// of `brain bootstrap` (see BrainAnalysisTier), brainReviewModel/
	// brainReviewEffort the tier of its page review (see BrainReviewTier).
	// NewClient seeds them from DefaultBrainAnalysisModel,
	// DefaultBrainAnalysisEffort, DefaultBrainReviewModel, and
	// DefaultBrainReviewEffort (decisions 112 and 120).
	brainAnalysisModel  string
	brainAnalysisEffort string
	brainReviewModel    string
	brainReviewEffort   string
	// showOutput prints every session's events as they arrive (see
	// streamSinkFor); false, the default, keeps them quiet behind the
	// heartbeat. Either way runClaudeStream reads them. Set via WithShowOutput.
	showOutput bool

	// inheritUserConfig, when true, lets sessions load the invoking user's
	// global ~/.claude settings and MCP servers instead of running hermetically
	// (see hermeticArgs). Set via WithInheritUserConfig (decision 45).
	inheritUserConfig bool

	// keepTranscripts, when true, lets Claude Code save the transcript of
	// every session, the read-only and structuring ones included; false, the
	// default, saves only the transcripts of the sessions that edit (see
	// persistsSession). Set via WithKeepTranscripts (decision 161).
	keepTranscripts bool

	// diffExclusions leaves lock, vendored and generated files out of the diff
	// the diff-scoped finders read (diffscope.Classify). Set via
	// WithDiffExclusions; off by default (decision 137).
	diffExclusions bool

	// plugins are the Claude Code plugins the mutating sessions load
	// (sessionPlugins), and designKit the impeccable skill among them, which
	// hasDesignKit says was found. Set via WithPlugins (decisions 153 and
	// 155).
	plugins      []claudeplugin.Plugin
	designKit    impeccable.Kit
	hasDesignKit bool

	// sessionFn, when set, runs every session in place of the claude CLI. Only tests set it.
	sessionFn func(spec runSpec, prompt string) (text, model string, err error)

	// usageMu guards the per-Run usage accumulators. The review fan-out runs
	// several Claude calls concurrently on one shared Client (errgroup over
	// Review/AdversarialReview/CoverageMap/…), so addUsage must be safe to call
	// from multiple goroutines.
	usageMu sync.Mutex
	usage   report.Usage
	// usageByPass accumulates the same counts keyed by the runner label of the
	// invocation that spent them, so a run's cost can be attributed to the pass
	// it belongs to. Lazily created by addUsage; nil until the first call.
	usageByPass map[string]report.PassUsage
}

// Option configures a Client. Pass any number of options to NewClient; later
// options win when two set the same field.
type Option func(*Client)

// NewClient returns a Client seeded with the compiled-in defaults
// (DefaultClaudeTimeout/Model/Effort and the planning, finder, structuring,
// and brain analysis and review defaults), then applies opts.
func NewClient(opts ...Option) *Client {
	c := &Client{
		timeout:             DefaultClaudeTimeout,
		model:               DefaultClaudeModel,
		planModel:           DefaultPlanModel,
		structureModel:      DefaultStructureModel,
		finderModel:         DefaultFinderModel,
		finderEffort:        DefaultFinderEffort,
		effort:              DefaultClaudeEffort,
		planEffort:          DefaultPlanEffort,
		structureEffort:     DefaultStructureEffort,
		brainAnalysisModel:  DefaultBrainAnalysisModel,
		brainAnalysisEffort: DefaultBrainAnalysisEffort,
		brainReviewModel:    DefaultBrainReviewModel,
		brainReviewEffort:   DefaultBrainReviewEffort,
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

// WithFinderModel sets the model used by the read-only finder passes (the
// adversarial pass, the domain specialists, the coverage map, the compliance
// check, the simplify finder, the implementation verifier, and claim
// verification); every other session stays on the main model. An empty m is
// ignored, which leaves the seeded default in place (DefaultFinderModel; empty
// inherits the main model, see FinderTier).
func WithFinderModel(m string) Option {
	return func(c *Client) {
		if m != "" {
			c.finderModel = m
		}
	}
}

// WithFinderEffort sets the reasoning effort used by the finder passes. An empty
// e is ignored, which leaves the seeded default in place (DefaultFinderEffort;
// empty inherits the main effort, see FinderTier).
func WithFinderEffort(e string) Option {
	return func(c *Client) {
		if e != "" {
			c.finderEffort = e
		}
	}
}

// WithFinderTools sets the built-in tools the seven passes that emit findings
// load, passed to each as --tools: the review, the audit, the adversarial pass,
// each domain specialist, the feature-compliance check, the simplify finder and
// the implementation verifier. The coverage map, claim verification and every
// other session keep Claude Code's default set. An empty names is ignored,
// which leaves the earlier value in place (DefaultFinderTools passes no
// --tools); a later option wins, and names is cloned. ParseToolList produces
// the list the root command and the eval pass in (decision 152).
func WithFinderTools(names []string) Option {
	return func(c *Client) {
		if len(names) > 0 {
			c.finderTools = slices.Clone(names)
		}
	}
}

// FinderTools returns a copy of the --tools list the passes that emit findings
// run with, nil when they load Claude Code's default set. cmd/planwerk-eval's
// tests read it to prove buildClient wires the list.
func (c *Client) FinderTools() []string {
	return slices.Clone(c.finderTools)
}

// toolName is the shape ParseToolList admits for a built-in tool name: a
// capital letter, then letters and digits.
var toolName = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)

// ParseToolList parses a --finder-tools value: names separated by commas, each
// trimmed. It returns the names sorted with duplicates dropped, nil for a blank
// s, and an error quoting the first entry that is empty or not a capitalized
// tool name, so a typo such as "read", a permission rule such as "Bash(git:*)"
// or the CLI's "default" keyword fails before any session runs. It checks the
// syntax only; a well-formed name Claude Code does not know is caught by the
// warning against the session's init event (warnUnloadedTools). Write-tool
// names are accepted, since claudeReadOnlyDeniedTools still denies them on the
// read-only passes (decision 152).
func ParseToolList(s string) ([]string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var names []string
	for _, entry := range strings.Split(s, ",") {
		name := strings.TrimSpace(entry)
		if name == "" {
			return nil, fmt.Errorf("empty tool name in %q", s)
		}
		if !toolName.MatchString(name) {
			return nil, fmt.Errorf("tool %q is not a built-in tool name: want a capital letter, then letters and digits", name)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return slices.Compact(names), nil
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

// WithStructureModel sets the model used by the JSON-structuring passes: the
// tier that casts an analysis session's prose into its JSON schema (propose,
// elaborate, gap-analysis, sync, capture, review-prepared) and runs the JSON
// repair and dedup calls. An empty m is ignored so a misconfigured flag cannot
// select an empty model.
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

// WithBrainAnalysisModel sets the model used by the analysis of `brain
// bootstrap`. An empty m is ignored, which leaves the seeded default in place
// (DefaultBrainAnalysisModel).
func WithBrainAnalysisModel(m string) Option {
	return func(c *Client) {
		if m != "" {
			c.brainAnalysisModel = m
		}
	}
}

// WithBrainAnalysisEffort sets the reasoning effort used by the analysis of
// `brain bootstrap`. An empty e is ignored, which leaves the seeded default in
// place (DefaultBrainAnalysisEffort).
func WithBrainAnalysisEffort(e string) Option {
	return func(c *Client) {
		if e != "" {
			c.brainAnalysisEffort = e
		}
	}
}

// WithBrainReviewModel sets the model used by the page review of `brain
// bootstrap`. An empty m is ignored, which leaves the seeded default in place
// (DefaultBrainReviewModel).
func WithBrainReviewModel(m string) Option {
	return func(c *Client) {
		if m != "" {
			c.brainReviewModel = m
		}
	}
}

// WithBrainReviewEffort sets the reasoning effort used by the page review of
// `brain bootstrap`. An empty e is ignored, which leaves the seeded default in
// place (DefaultBrainReviewEffort).
func WithBrainReviewEffort(e string) Option {
	return func(c *Client) {
		if e != "" {
			c.brainReviewEffort = e
		}
	}
}

// WithShowOutput toggles printing Claude Code's live output. Every session runs
// with --output-format stream-json --verbose and is read by runClaudeStream
// either way; true prints the assistant text and tool activity as they arrive
// (newDefaultStreamSink), false (the default) keeps them quiet behind the
// elapsed-time heartbeat (decision 152).
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

// WithKeepTranscripts controls whether Claude Code saves the transcript of the
// read-only and structuring sessions under ~/.claude/projects. The default
// (false) runs them with --no-session-persistence, since nothing resumes or
// reads them; the sessions that edit keep their transcript either way. Pass
// true to inspect a read-only session after the run (decision 161).
func WithKeepTranscripts(b bool) Option {
	return func(c *Client) { c.keepTranscripts = b }
}

// WithDiffExclusions controls whether the diff each diff-scoped finder pass
// reads leaves out the changed lock files, vendored directories and generated
// files, which the pass's result then lists in ExcludedFiles. The default
// (false) diffs every changed file (decision 137).
func WithDiffExclusions(b bool) Option {
	return func(c *Client) { c.diffExclusions = b }
}

// DiffExclusions reports whether WithDiffExclusions turned the exclusions on.
// The review runner reads it to write the review session's diff with the same
// setting the Client's other finder passes use.
func (c *Client) DiffExclusions() bool { return c.diffExclusions }

// KeepTranscripts reports whether WithKeepTranscripts kept the transcripts of
// the read-only and structuring sessions. The root command's and
// cmd/planwerk-eval's tests read it to prove the flag and the environment
// variable reach the Client.
func (c *Client) KeepTranscripts() bool { return c.keepTranscripts }

// runClaude invokes claude in the given directory in
// claudeReadOnlyPermissionMode and returns the extracted text response along
// with the resolved model id the session reported. Use it for the read-only
// analysis steps (propose, elaborate, sync, capture, …) that do not mutate the
// checkout; the review and the audit use runClaudeFindings, and the
// JSON-structuring passes — and their repair recovery — use runClaudeStructure
// for the dedicated cheap tier instead. cat is the pattern catalog whose
// directory the session may read (--add-dir), noCatalog for a session without
// one.
func (c *Client) runClaude(dir, prompt, label string, cat patterns.Catalog) (text, model string, err error) {
	return c.runClaudeMemory(dir, prompt, label, cat, patterns.MemoryCatalog{}, search.Surface{})
}

// runClaudeMemory is runClaude for a session whose prompt carries the project
// memory: mem is the memory catalog whose directory the session may read
// beside the pattern catalog's, the zero MemoryCatalog for a run without one.
// surface pre-approves `brain search` for the session, the zero Surface for a
// session without the search. The propose analysis and the elaboration call
// it.
func (c *Client) runClaudeMemory(dir, prompt, label string, cat patterns.Catalog, mem patterns.MemoryCatalog, surface search.Surface) (text, model string, err error) {
	return c.runSession(runSpec{dir: dir, label: label, model: c.model, effort: c.effort, readOnly: true, addDirs: []string{cat.Dir, mem.Dir}, searchRule: surface.AllowRule()}, prompt)
}

// runClaudeFindings is runClaude, on the main --claude-model tier, that also
// passes schema.FinderOutput to the CLI via --json-schema, so the session emits
// its findings as JSON itself. The review and the audit call it; the other five
// passes that emit findings run on the finder tier through
// runClaudeFinderFindings (decision 109). All seven load the --finder-tools
// list (decision 152). cat is the pattern catalog whose directory the session
// may read (--add-dir); the audit, and a review without --scope-patterns, pass
// noCatalog, because their prompts carry the bodies (decision 141). mem is the
// project memory catalog whose directory the session may read, the zero
// MemoryCatalog for a run without one. surface pre-approves `brain search` for
// the session, the zero Surface for a session without the search. diffDir is
// the directory of the diff the session reads (diffscope.Diff.Dir), "" for
// none; the audit has no diff (decision 137).
func (c *Client) runClaudeFindings(dir, prompt, label string, cat patterns.Catalog, mem patterns.MemoryCatalog, surface search.Surface, diffDir string) (text, model string, err error) {
	return c.runSession(runSpec{dir: dir, label: label, model: c.model, effort: c.effort, readOnly: true, jsonSchema: string(schema.FinderOutput), addDirs: []string{cat.Dir, mem.Dir, diffDir}, searchRule: surface.AllowRule(), tools: c.finderTools}, prompt)
}

// runClaudeSchema is runClaude that also passes jsonSchema to the CLI via
// --json-schema, so the session emits its result as JSON the CLI validates.
// The rebase analysis calls it with schema.RebaseAnalysis (decision 160). cat
// is the pattern catalog whose directory the session may read (--add-dir),
// noCatalog for a session without one.
func (c *Client) runClaudeSchema(dir, prompt, label string, cat patterns.Catalog, jsonSchema []byte) (text, model string, err error) {
	return c.runSession(runSpec{dir: dir, label: label, model: c.model, effort: c.effort, readOnly: true, jsonSchema: string(jsonSchema), addDirs: []string{cat.Dir}}, prompt)
}

// noCatalog is what a session without a pattern catalog passes to its runner:
// the zero Catalog, which opens no directory.
var noCatalog patterns.Catalog

// FinderTier returns the resolved model and effort the finder passes run on:
// the finder tier's own value where set, the main tier's otherwise (decision
// 79). finderSpec runs on it, and cmd/planwerk-eval records it as the finder
// tier a report ran on.
func (c *Client) FinderTier() (model, effort string) {
	return firstNonEmpty(c.finderModel, c.model), firstNonEmpty(c.finderEffort, c.effort)
}

// finderSpec is the read-only session spec on the finder tier (FinderTier),
// which both finder runners build on so their tier resolution cannot drift
// apart.
func (c *Client) finderSpec(dir, label string) runSpec {
	model, effort := c.FinderTier()
	return runSpec{
		dir:      dir,
		label:    label,
		model:    model,
		effort:   effort,
		readOnly: true,
	}
}

// runClaudeFinder is runClaude on the finder tier (finderSpec), for the
// read-only finder passes that return prose or their own JSON rather than
// schema.FinderOutput: the coverage map and claim verification. The finder-tier
// passes that emit findings use runClaudeFinderFindings.
func (c *Client) runClaudeFinder(dir, prompt, label string) (text, model string, err error) {
	return c.runSession(c.finderSpec(dir, label), prompt)
}

// runClaudeFinderFindings is runClaudeFinder, on the --finder-model tier, that
// also passes schema.FinderOutput to the CLI via --json-schema. Five of the
// seven passes that emit findings call it: the adversarial pass, each domain
// specialist, the feature-compliance check, the simplify finder, and the
// implementation verifier. The review and the audit run on --claude-model
// through runClaudeFindings (decision 109). All seven load the --finder-tools
// list, which finderSpec leaves out, so the coverage map and claim
// verification keep the default set (decision 152). diffDir is the directory
// of the diff the session reads (diffscope.Diff.Dir), "" for none (decision
// 137). readDirs are further directories the session may read, after the
// diff's: the design skill's reference documents for the ui specialist
// (decision 155).
func (c *Client) runClaudeFinderFindings(dir, prompt, label, diffDir string, readDirs ...string) (text, model string, err error) {
	spec := c.finderSpec(dir, label)
	spec.jsonSchema = string(schema.FinderOutput)
	spec.addDirs = append([]string{diffDir}, readDirs...)
	spec.tools = c.finderTools
	return c.runSession(spec, prompt)
}

// firstNonEmpty returns override when it is set and fallback otherwise — the
// "a tier with no compiled-in default inherits the main one" rule, in one place
// so the model and effort halves cannot drift apart.
func firstNonEmpty(override, fallback string) string {
	if override != "" {
		return override
	}
	return fallback
}

// runClaudePlan is runClaude on the dedicated planning tier (planModel,
// planEffort) for the implement command's read-only planning session
// (decision 101). cat is the pattern catalog and mem the project memory
// catalog whose directories the session may read (--add-dir), noCatalog and
// the zero MemoryCatalog for a session without one. surface pre-approves
// `brain search` for the session, the zero Surface for a session without the
// search.
func (c *Client) runClaudePlan(dir, prompt, label string, cat patterns.Catalog, mem patterns.MemoryCatalog, surface search.Surface) (text, model string, err error) {
	return c.runSession(runSpec{dir: dir, label: label, model: c.planModel, effort: c.planEffort, readOnly: true, addDirs: []string{cat.Dir, mem.Dir}, searchRule: surface.AllowRule()}, prompt)
}

// runClaudeBrainAnalysis is runClaude on the brain analysis tier
// (BrainAnalysisTier) for the analysis of `brain bootstrap` (decision 120).
// mem is the memory catalog whose directory the session may read (--add-dir):
// the pages of the working set the analysis deduplicates against and corrects.
func (c *Client) runClaudeBrainAnalysis(dir, prompt, label string, mem patterns.MemoryCatalog) (text, model string, err error) {
	return c.runSession(runSpec{dir: dir, label: label, model: c.brainAnalysisModel, effort: c.brainAnalysisEffort, readOnly: true, addDirs: []string{mem.Dir}}, prompt)
}

// BrainAnalysisTier returns the model and effort runClaudeBrainAnalysis runs
// the analysis of `brain bootstrap` on.
func (c *Client) BrainAnalysisTier() (model, effort string) {
	return c.brainAnalysisModel, c.brainAnalysisEffort
}

// runClaudeBrainReview is runClaude on the brain review tier (BrainReviewTier)
// for the page review of `brain bootstrap` (decision 112). mem is the memory
// catalog whose directory the session may read (--add-dir): the pages of the
// working set the review checks a proposal against.
func (c *Client) runClaudeBrainReview(dir, prompt, label string, mem patterns.MemoryCatalog) (text, model string, err error) {
	return c.runSession(runSpec{dir: dir, label: label, model: c.brainReviewModel, effort: c.brainReviewEffort, readOnly: true, addDirs: []string{mem.Dir}}, prompt)
}

// BrainReviewTier returns the model and effort runClaudeBrainReview runs the
// page review of `brain bootstrap` on.
func (c *Client) BrainReviewTier() (model, effort string) {
	return c.brainReviewModel, c.brainReviewEffort
}

// runClaudeStructure is runClaude on the dedicated structuring tier
// (structureModel/structureEffort, defaults "sonnet"/"xhigh"). The JSON
// structuring passes use it: a structuring call only reads an analysis
// session's prose and casts it into that artifact's JSON schema, so it runs on
// the cheap mechanical tier rather than the heavy reasoning model the analysis
// used. The JSON repair and the dedup fallback run here too. The
// decodeJSONWithRepair backstop guards malformed output. Every structuring and
// repair call reaches the CLI through here, so this is where the tier's
// isolation is set: the spec sets noTools and names no dir, and runSession runs
// it in structureWorkDir (decisions 56 and 91).
func (c *Client) runClaudeStructure(prompt, label string) (text, model string, err error) {
	return c.runSession(runSpec{label: label, model: c.structureModel, effort: c.structureEffort, readOnly: true, noTools: true}, prompt)
}

// StructureTier returns the model and effort runClaudeStructure runs the
// structuring passes on. cmd/planwerk-eval's tests read it to prove buildClient
// wires the structure tier.
func (c *Client) StructureTier() (model, effort string) {
	return c.structureModel, c.structureEffort
}

// structureWorkDir returns the directory the structuring sessions run in: one
// stable, empty directory this tool owns, structure-workdir under the result
// cache's root (cache.defaultCacheDir), or planwerk-agent-structure-workdir in
// the temp dir when the OS reports no user cache directory (decision 91).
func structureWorkDir() (string, error) {
	dir := filepath.Join(os.TempDir(), "planwerk-agent-structure-workdir")
	if base, err := os.UserCacheDir(); err == nil {
		dir = filepath.Join(base, "planwerk-agent", "structure-workdir")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating the structuring working directory %s: %w", dir, err)
	}
	return dir, nil
}

// runClaudeAuto is runClaude with claudeAutoPermissionMode and the write tools
// kept, for the one-shot sessions that edit, commit, and push unattended. The
// rebase sessions, which have no terminal-report contract, call it directly;
// the report-bearing ones go through runClaudeAutoReport (decision 78), and
// fix and address build their spec with autoMemorySpec. cat is the pattern
// catalog whose directory the session may read (--add-dir), noCatalog for a
// session without one.
func (c *Client) runClaudeAuto(dir, prompt, label string, cat patterns.Catalog) (text, model string, err error) {
	return c.runSession(c.autoSpec(dir, label, cat), prompt)
}

// autoSpec is the runSpec every auto-mode mutating session starts from: the
// shared main model and effort, claudeAutoPermissionMode, and the write tools
// kept (readOnly false). runClaudeAuto and the completion-gated variants build
// on it so the auto-mode invocation is defined once. cat is the pattern catalog
// whose directory the session may read (--add-dir), noCatalog for a session
// without one.
func (c *Client) autoSpec(dir, label string, cat patterns.Catalog) runSpec {
	return runSpec{dir: dir, label: label, permissionMode: claudeAutoPermissionMode, model: c.model, effort: c.effort, addDirs: []string{cat.Dir}}
}

// autoMemorySpec is autoSpec for a mutating session whose prompt carries the
// project memory: the session may also read mem.Dir. fix and address use it.
func (c *Client) autoMemorySpec(dir, label string, cat patterns.Catalog, mem patterns.MemoryCatalog) runSpec {
	spec := c.autoSpec(dir, label, cat)
	spec.addDirs = append(spec.addDirs, mem.Dir)
	return spec
}

// runClaudeImplement runs the implement session: runClaudeAuto on
// implementModel when one is set and on the main model otherwise, with
// agentsJSON passed via --agents when non-empty (orchestrator mode), under the
// completion nudge (decisions 74 and 78). ImplementSystemPrompt is passed via
// --append-system-prompt; the nudge's resumed turn carries it too (decision
// 108). cat is the pattern catalog whose directory the session may read
// (--add-dir), noCatalog for a session without one.
func (c *Client) runClaudeImplement(dir, prompt, label, agentsJSON string, cat patterns.Catalog) (text, model string, err error) {
	spec := c.autoSpec(dir, label, cat)
	spec.model = c.implementSessionModel()
	spec.agentsJSON = agentsJSON
	spec.appendSystemPrompt = ImplementSystemPrompt()
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

// claudeArgs assembles the claude CLI argv for spec. Every session runs with
// --output-format stream-json --verbose, the event stream runClaudeStream
// reads (decision 152). Every flag an isolation-, tool-, agent- or
// session-level decision produces is emitted here and only here.
func (c *Client) claudeArgs(spec runSpec) []string {
	args := []string{
		"-p",
		"--model", spec.model,
		"--effort", spec.effort,
		"--output-format", "stream-json", "--verbose",
	}
	readOnly := spec.readOnly || spec.noTools
	plugins := c.sessionPlugins(readOnly)
	args = withAppendSystemPrompt(args, joinPrompts(spec.appendSystemPrompt, pluginSessionPrompt(plugins)))
	mode := spec.permissionMode
	if mode == "" && readOnly {
		mode = claudeReadOnlyPermissionMode
	}
	if mode != "" {
		args = append(args, "--permission-mode", mode)
	}
	if dirs := nonEmptyDirs(spec.addDirs); len(dirs) > 0 && !spec.noTools {
		args = append(append(args, "--add-dir"), dirs...)
	}
	if spec.jsonSchema != "" {
		args = append(args, "--json-schema", spec.jsonSchema)
	}
	args = withSession(args, spec)
	args = withNoSessionPersistence(args, c.persistsSession(spec))
	args = withAgents(args, spec.agentsJSON)
	args = c.hermeticArgs(args, readOnly)
	args = withHooksDisabled(args, readOnly)
	args = withPluginDirs(args, plugins)
	if spec.noTools {
		return withNoTools(args)
	}
	args = withTools(args, spec.tools)
	args = withReadOnlyDenied(args, spec.readOnly)
	args = withDenyRules(args, spec.readOnly, spec.denyRules)
	var rules []string
	if spec.readOnly && spec.searchRule != "" {
		rules = append(rules, spec.searchRule)
	}
	return withAllowedTools(args, rules...)
}

// claudeCommand builds the claude subprocess for spec: claudeArgs for the
// argv, the wait delay, the working directory when spec names one, then the
// environment of a session that loads plugins (withSessionEnv), and the
// prompt on stdin.
func (c *Client) claudeCommand(ctx context.Context, spec runSpec, prompt string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "claude", c.claudeArgs(spec)...)
	cmd.WaitDelay = claudeWaitDelay
	if spec.dir != "" {
		cmd.Dir = spec.dir
	}
	withSessionEnv(cmd, c.sessionPlugins(spec.readOnly || spec.noTools))
	cmd.Stdin = strings.NewReader(prompt)
	return cmd
}

// runSession runs one session: through sessionFn when a test set it, through
// the claude CLI otherwise. On the CLI path it first resolves structureWorkDir
// as a noTools spec's working directory, before any process starts, and drops
// jsonSchema's dialect declaration (cliJSONSchema). It then pins a fresh
// --session-id on a spec that names no id, logs the id and whether the
// session's transcript is saved (persistsSession) at debug level, and runs the
// session through runClaudeStream. A completion nudge's resumed spec already
// carries the id it resumes, so --resume is untouched (decisions 78, 152 and
// 161).
func (c *Client) runSession(spec runSpec, prompt string) (string, string, error) {
	if c.sessionFn != nil {
		return c.sessionFn(spec, prompt)
	}
	if spec.noTools {
		dir, err := structureWorkDir()
		if err != nil {
			return "", "", err
		}
		spec.dir = dir
	}
	spec.jsonSchema = cliJSONSchema(spec.jsonSchema)
	if spec.sessionID == "" {
		spec.sessionID = newSessionID()
	}
	slog.Debug("claude session", "label", spec.label, "session_id", spec.sessionID, "resume", spec.resume, "persisted", c.persistsSession(spec))
	return c.runClaudeStream(spec, prompt)
}

// claudeResponse is the view of a `result` event that a failed turn needs:
// envelopeFailure reads its reason.
type claudeResponse struct {
	Result string `json:"result"`
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
	// unparseable.
	APIErrorStatus json.RawMessage `json:"api_error_status,omitempty"`
}

// timeoutError reports the deadline this Client imposed, or nil when ctx was
// not the reason the invocation ended. exec.CommandContext kills the child on
// deadline, and the kill surfaces as an *exec.ExitError ("signal: killed") that
// does not wrap context.DeadlineExceeded, so only ctx still knows. Any stderr
// the child produced before the kill is kept: it is the last thing the session
// said.
func (c *Client) timeoutError(ctx context.Context, model string, stderr []byte) error {
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil
	}
	if msg := bytes.TrimSpace(stderr); len(msg) > 0 {
		return fmt.Errorf("claude (model %s): timed out after %s (--claude-timeout)\nstderr: %s", model, c.timeout, head(msg, 500))
	}
	return fmt.Errorf("claude (model %s): timed out after %s (--claude-timeout)", model, c.timeout)
}

// addFailureUsage counts the tokens a FAILED or timed-out invocation spent.
// Claude Code reports an exhausted turn budget or an upstream API error by
// writing its `result` event — usage block included — to stdout and exiting
// non-zero; u and cost are the stream reducer's figures over every `result`
// event the invocation wrote, so the turns a woken session finished before
// the failure count as well (decision 163). An invocation that wrote no usage
// at all records nothing rather than an empty call.
func (c *Client) addFailureUsage(label string, u tokenUsage, cost float64) {
	if u == (tokenUsage{}) && cost == 0 {
		return
	}
	c.addUsage(label, u, cost)
}

// claudeRunError renders a failed `claude -p` invocation into an error that
// names what went wrong. Claude Code reports an API-level failure (a hit rate
// limit, an exhausted turn budget) by writing its `result` event to STDOUT and
// exiting non-zero with STDERR empty. stdout is that raw event, as
// runClaudeStream kept it; stderr carries the failures that never reach the
// API, such as an unknown flag. The model alias is named either way, since
// which session hit the limit is the actionable part.
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
// subtype (is_error true, subtype "error_max_turns"). raw is the stream's
// `result` event, which carries these fields verbatim, the same ones
// `--output-format json` writes as its envelope.
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
	if status == "" || status == jsonNull {
		return reason
	}
	if reason == "" {
		return "api error " + status
	}
	return fmt.Sprintf("api error %s: %s", status, reason)
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

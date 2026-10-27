package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/attribution"
	"github.com/planwerk/planwerk-agent/internal/mdfence"
)

// emDashPlaceholder is rendered in cells/fields that have no value.
const emDashPlaceholder = "—"

type Renderer struct {
	w io.Writer
}

func NewRenderer(w io.Writer) *Renderer {
	return &Renderer{w: w}
}

func (r *Renderer) RenderJSON(result ReviewResult, minSeverity Severity, minConfidence Confidence) error {
	cf := Categorize(result.Findings, minSeverity, minConfidence)
	filtered := ReviewResult{
		Findings:       cf.All(),
		Summary:        result.Summary,
		Recommendation: result.Recommendation,
	}
	// Carry the run-level gate records so --format json tells the same story the
	// posted data block does; the counts describe the whole run, not the filtered
	// list. The pass records, the excluded files and the partitions describe the
	// run too.
	filtered.Gates = result.Gates
	filtered.Passes = result.Passes
	filtered.ExcludedFiles = result.ExcludedFiles
	filtered.Partitions = result.Partitions

	enc := json.NewEncoder(r.w)
	enc.SetIndent("", "  ")
	return enc.Encode(filtered)
}

func (r *Renderer) RenderMarkdown(result ReviewResult, pr PRInfo, minSeverity Severity, minConfidence Confidence, version string) {
	cf := Categorize(result.Findings, minSeverity, minConfidence)

	_, _ = fmt.Fprintf(r.w, "# Review: %s/%s#%d\n\n", pr.Owner, pr.Repo, pr.Number)
	_, _ = fmt.Fprintf(r.w, "> *%s*  \n", pr.Title)
	_, _ = fmt.Fprintf(r.w, "> Reviewed by %s %s\n", attribution.ToolWithVersion(version), attribution.AssistantWith(result.Model))
	RenderWikiProvenance(r.w, result.WikiRepo, result.WikiCommit)
	_, _ = fmt.Fprintln(r.w)

	// Machine-readable summary for tooling (Claude Code, CI scripts, etc.)
	_, _ = fmt.Fprintf(r.w, "<!-- planwerk-agent: blocking=%d critical=%d warning=%d info=%d unverified=%d recommendation=%s -->\n\n",
		len(cf.Blocking), len(cf.Critical), len(cf.Warning), len(cf.Info), len(cf.Unverified),
		r.recommendationKey(cf, result.Recommendation))

	r.renderFailedPasses(result.FailedPasses())

	r.renderSection("BLOCKING", cf.Blocking)
	r.renderSection("CRITICAL", cf.Critical)
	r.renderSection("WARNING", cf.Warning)
	r.renderSection("INFO", cf.Info)
	r.renderUnverifiedSection(cf.Unverified)
	r.renderExcludedFiles(result.ExcludedFiles)
	r.renderFilesNotReviewed(result.Gates)

	r.renderSummary(cf, result)
	r.renderRecommendation(cf, result.Recommendation)
}

// renderFailedPasses writes the Incomplete review alert above the findings,
// naming each requested pass that failed with its error, so the report does not
// read like one in which those passes found nothing. It writes nothing when
// failed is empty (decision 135).
func (r *Renderer) renderFailedPasses(failed []PassStatus) {
	if len(failed) == 0 {
		return
	}
	_, _ = fmt.Fprintf(r.w, "> [!WARNING]\n> **Incomplete review** — %d requested pass(es) failed and contributed nothing to this report:\n", len(failed))
	for _, p := range failed {
		_, _ = fmt.Fprintf(r.w, "> - `%s`: %s\n", p.Pass, p.Error)
	}
	_, _ = fmt.Fprint(r.w, ">\n> A review with a failed pass is never cached, so running it again retries the pass.\n\n")
}

// renderExcludedFiles writes the note that lists what --diff-exclusions left
// out of the diff the review read, so an exclusion is never silent: one line
// per entry up to ExcludedFilesListLimit, then a count of the rest. Each path
// is written by changedFilePath. It writes nothing when excluded is empty
// (decision 137).
func (r *Renderer) renderExcludedFiles(excluded []ExcludedFile) {
	if len(excluded) == 0 {
		return
	}
	_, _ = fmt.Fprintf(r.w, "> [!NOTE]\n> %d changed file(s) were left out of the diff the review read (--diff-exclusions): lock files, vendored code, and generated code. Tests outside vendored directories are never left out.\n", ExcludedFileCount(excluded))
	shown := excluded[:min(len(excluded), ExcludedFilesListLimit)]
	for _, e := range shown {
		_, _ = fmt.Fprintf(r.w, "> - %s (%s)\n", changedFilePath(e.Path), e.Label())
	}
	if rest := len(excluded) - len(shown); rest > 0 {
		_, _ = fmt.Fprintf(r.w, "> - and %d more (the --format json output and the data block list every one)\n", rest)
	}
	_, _ = fmt.Fprintln(r.w)
}

// renderFilesNotReviewed writes the Files Not Reviewed section, which names
// each changed file the review prompt listed and the review session did not
// report reading: skipped, with the session's reason when it gave one, or not
// reported. The record is the session's own account, so the section claims no
// more than that. Each path is written by changedFilePath. It writes nothing
// when g records no missing file (decision 138).
func (r *Renderer) renderFilesNotReviewed(g *GateStats) {
	if g == nil || g.Files == nil || len(g.Files.Missing) == 0 {
		return
	}
	_, _ = fmt.Fprintf(r.w, "## Files Not Reviewed (%d of %d)\n\n", len(g.Files.Missing), g.Files.Changed)
	_, _ = fmt.Fprint(r.w, "The review did not report reading these changed files, so the findings above may not cover them.\n\n")
	for _, m := range g.Files.Missing {
		file := changedFilePath(m.File)
		switch reason := strings.Join(strings.Fields(m.Reason), " "); {
		case m.Status == FileUnreported:
			_, _ = fmt.Fprintf(r.w, "- %s: not reported\n", file)
		case reason != "":
			_, _ = fmt.Fprintf(r.w, "- %s: skipped (%s)\n", file, reason)
		default:
			_, _ = fmt.Fprintf(r.w, "- %s: skipped\n", file)
		}
	}
	_, _ = fmt.Fprint(r.w, "\n---\n\n")
}

// changedFilePath writes a changed file's path for the Files Not Reviewed
// section and the excluded-files note. The pull request's author picks the
// path, so it goes in a code span no backtick in it can close (mdfence.Span),
// and each "<" in it is written as "&lt;", which the span shows as it stands:
// the posted comment then holds no "<!--" the path put there, and a path
// cannot forge the data block ParseDataBlock reads or any other marker
// (decision 138).
func changedFilePath(p string) string {
	return mdfence.Span(strings.ReplaceAll(p, "<", "&lt;"))
}

// RenderWikiProvenance emits the "> Wiki: <repo>.wiki @ <short-sha>" header line
// identifying the target repo's GitHub Wiki commit a result was pinned to. It is
// shared by the review, audit, and proposal renderers and writes nothing when
// commit is empty, leaving wiki-less output unchanged.
func RenderWikiProvenance(w io.Writer, repo, commit string) {
	if commit == "" {
		return
	}
	if repo != "" {
		_, _ = fmt.Fprintf(w, "> Wiki: %s.wiki @ %s\n", repo, ShortSHA(commit))
		return
	}
	_, _ = fmt.Fprintf(w, "> Wiki: %s\n", ShortSHA(commit))
}

// recommendationKey returns a short machine-readable verdict for the HTML comment.
func (r *Renderer) recommendationKey(cf CategorizedFindings, custom string) string {
	if custom != "" {
		return "CUSTOM"
	}
	if cf.HasBlockersOrCritical() {
		return "HOLD"
	}
	if len(cf.Warning) > 0 {
		return "REVIEW"
	}
	return "MERGE"
}

func (r *Renderer) renderSection(label string, findings []Finding) {
	if len(findings) == 0 {
		return // skip empty sections — no noise for tooling or readers
	}
	_, _ = fmt.Fprintf(r.w, "## %s (%d)\n\n", label, len(findings))
	for i, f := range findings {
		r.renderFinding(f, i == len(findings)-1)
	}
	_, _ = fmt.Fprint(r.w, "---\n\n")
}

// renderUnverifiedSection renders findings that were pulled out of their
// severity bucket because they are low-confidence (uncertain WARNING/INFO) or
// were demoted by the snippet-verification gate. A short note explains why
// they are separated so readers do not mistake them for fully verified issues.
func (r *Renderer) renderUnverifiedSection(findings []Finding) {
	if len(findings) == 0 {
		return
	}
	_, _ = fmt.Fprintf(r.w, "## Unverified / Low-Confidence (%d)\n\n", len(findings))
	_, _ = fmt.Fprint(r.w, "These findings could not be verified with high confidence — treat them as leads to check, not confirmed issues.\n\n")
	for i, f := range findings {
		r.renderFinding(f, i == len(findings)-1)
	}
	_, _ = fmt.Fprint(r.w, "---\n\n")
}

// renderFinding writes a single finding block. last suppresses the trailing
// horizontal rule between findings (the section adds its own closing rule).
func (r *Renderer) renderFinding(f Finding, last bool) {
	_, _ = fmt.Fprintf(r.w, "### %s: %s\n", f.ID, f.Title)

	// Compact single-line metadata: File — Fix — Confidence — Pattern
	meta := fmt.Sprintf("**File**: `%s`", fileRef(f))
	if f.FixClass != "" {
		meta += fmt.Sprintf(" — **Fix**: %s", f.FixClass)
	}
	if f.Confidence != "" {
		meta += fmt.Sprintf(" — **Confidence**: %s", f.Confidence)
	}
	if len(f.ConfirmedBy) >= 2 {
		meta += fmt.Sprintf(" — **Confirmed by**: %s", strings.Join(f.ConfirmedBy, " + "))
	}
	if f.Pattern != "" {
		meta += fmt.Sprintf(" — **Pattern**: %s", f.Pattern)
	}
	_, _ = fmt.Fprintln(r.w, meta)
	_, _ = fmt.Fprintln(r.w)
	if f.VerificationNote != "" {
		_, _ = fmt.Fprintf(r.w, "**Claim check**: %s\n\n", f.VerificationNote)
	}
	if f.SnippetCheck != "" && f.SnippetCheck != SnippetCheckPassed {
		_, _ = fmt.Fprintf(r.w, "**Snippet check**: %s\n\n", f.SnippetCheck)
	}
	_, _ = fmt.Fprintf(r.w, "**Problem**: %s\n\n", f.Problem)
	if f.CodeSnippet != "" {
		_, _ = fmt.Fprintf(r.w, "**Code**:\n%s\n\n", mdfence.Wrap(f.CodeSnippet, ""))
	}
	_, _ = fmt.Fprintf(r.w, "**Action Required**: %s\n\n", f.Action)
	if f.SuggestedFix != "" {
		_, _ = fmt.Fprintf(r.w, "**Suggested Fix**:\n%s\n\n", mdfence.Wrap(f.SuggestedFix, ""))
	}
	renderFixOptions(r.w, f)
	if len(f.RelatedTo) > 0 {
		_, _ = fmt.Fprintf(r.w, "**Related**: %s\n\n", strings.Join(f.RelatedTo, ", "))
	}

	if !last {
		_, _ = fmt.Fprint(r.w, "---\n\n")
	}
}

// renderFixOptions writes a Markdown table of alternative fix approaches plus
// the recommended option. It emits nothing when the finding carries no options
// (e.g. auto-fix findings) so the report stays clean.
func renderFixOptions(w io.Writer, f Finding) {
	if len(f.FixOptions) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "**Fix Options**:")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "| Option | Approach | Pros | Cons | Effort | Risk if skipped |")
	_, _ = fmt.Fprintln(w, "|--------|----------|------|------|--------|-----------------|")
	for _, opt := range f.FixOptions {
		_, _ = fmt.Fprintf(w, "| %s | %s | %s | %s | %s | %s |\n",
			cellEscape(opt.ID),
			cellEscape(opt.Approach),
			cellEscape(opt.Pros),
			cellEscape(opt.Cons),
			cellEscape(opt.Effort),
			cellEscape(opt.RiskIfSkipped),
		)
	}
	_, _ = fmt.Fprintln(w)
	if f.RecommendedOption != "" {
		if f.RecommendationReasoning != "" {
			_, _ = fmt.Fprintf(w, "**Recommended**: %s — %s\n\n", f.RecommendedOption, f.RecommendationReasoning)
		} else {
			_, _ = fmt.Fprintf(w, "**Recommended**: %s\n\n", f.RecommendedOption)
		}
	}
}

// cellEscape sanitizes a value for use inside a Markdown table cell:
// pipes are escaped and newlines are replaced with `<br>` so the row stays on
// one line.
func cellEscape(s string) string {
	if s == "" {
		return emDashPlaceholder
	}
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", "<br>")
	return s
}

// fileRef returns "file:line" when a line number is known, otherwise just "file".
func fileRef(f Finding) string {
	if f.Line > 0 && f.LineEnd > 0 && f.LineEnd != f.Line {
		return fmt.Sprintf("%s:%d-%d", f.File, f.Line, f.LineEnd)
	}
	if f.Line > 0 {
		return fmt.Sprintf("%s:%d", f.File, f.Line)
	}
	return f.File
}

// renderSummary writes the Summary section: the result's summary, the Passes
// line, the Partitions line, and the severity counts. It takes the whole result
// so a run-level record can add its own line here.
func (r *Renderer) renderSummary(cf CategorizedFindings, result ReviewResult) {
	_, _ = fmt.Fprint(r.w, "## Summary\n\n")
	if result.Summary != "" {
		_, _ = fmt.Fprintf(r.w, "%s\n\n", result.Summary)
	}
	if line := passesLine(result.Passes); line != "" {
		_, _ = fmt.Fprintf(r.w, "%s\n\n", line)
	}
	if line := partitionsLine(result.Partitions); line != "" {
		_, _ = fmt.Fprintf(r.w, "%s\n\n", line)
	}
	_, _ = fmt.Fprintln(r.w, "| Severity | Count |")
	_, _ = fmt.Fprintln(r.w, "|----------|-------|")
	_, _ = fmt.Fprintf(r.w, "| BLOCKING | %d |\n", len(cf.Blocking))
	_, _ = fmt.Fprintf(r.w, "| CRITICAL | %d |\n", len(cf.Critical))
	_, _ = fmt.Fprintf(r.w, "| WARNING  | %d |\n", len(cf.Warning))
	_, _ = fmt.Fprintf(r.w, "| INFO     | %d |\n", len(cf.Info))
	if len(cf.Unverified) > 0 {
		_, _ = fmt.Fprintf(r.w, "| UNVERIFIED | %d |\n", len(cf.Unverified))
	}
	_, _ = fmt.Fprintln(r.w)
}

// passesLine renders the `**Passes**:` Summary line, grouping the passes as
// ran, gated out, then failed, each group in recorded order. An empty group is
// left out, and the line is empty when no pass was recorded (decision 135).
func passesLine(passes []PassStatus) string {
	var groups []string
	for _, g := range []struct{ status, label string }{
		{PassStatusRan, "ran"},
		{PassStatusGatedOut, "gated out"},
		{PassStatusFailed, "failed"},
	} {
		var names []string
		for _, p := range passes {
			if p.Status == g.status {
				names = append(names, p.Pass)
			}
		}
		if len(names) > 0 {
			groups = append(groups, g.label+" "+strings.Join(names, ", "))
		}
	}
	if len(groups) == 0 {
		return ""
	}
	return "**Passes**: " + strings.Join(groups, "; ")
}

// partitionsLine renders the `**Partitions**:` Summary line of a partitioned
// review: how many review sessions it ran and the file count of the largest
// partition, which --partition-files bounds. It is empty when the review ran as
// one session (decision 142).
func partitionsLine(partitions []ReviewPartition) string {
	if len(partitions) == 0 {
		return ""
	}
	largest := 0
	for _, p := range partitions {
		largest = max(largest, len(p.Files))
	}
	files := "files"
	if largest == 1 {
		files = "file"
	}
	return fmt.Sprintf("**Partitions**: %d review sessions of at most %d %s", len(partitions), largest, files)
}

func (r *Renderer) renderRecommendation(cf CategorizedFindings, custom string) {
	if custom != "" {
		_, _ = fmt.Fprintf(r.w, "> [!IMPORTANT]\n> **Recommendation**: %s\n", custom)
		return
	}
	var parts []string
	if len(cf.Blocking) > 0 {
		parts = append(parts, fmt.Sprintf("%d BLOCKING", len(cf.Blocking)))
	}
	if len(cf.Critical) > 0 {
		parts = append(parts, fmt.Sprintf("%d CRITICAL", len(cf.Critical)))
	}
	switch {
	case cf.HasBlockersOrCritical():
		_, _ = fmt.Fprintf(r.w, "> [!CAUTION]\n> **Do not merge** — %s findings must be resolved first.\n",
			strings.Join(parts, " and "))
	case len(cf.Warning) > 0:
		_, _ = fmt.Fprintf(r.w, "> [!WARNING]\n> **Review before merging** — %d warning(s) should be addressed.\n", len(cf.Warning))
	default:
		_, _ = fmt.Fprint(r.w, "> [!TIP]\n> **Ready to merge** — no blocking or critical findings.\n")
	}
}

type PRInfo struct {
	Owner  string
	Repo   string
	Number int
	Title  string
}

// RepoInfo identifies a repository audited as a whole (no PR context).
type RepoInfo struct {
	Owner string
	Name  string
}

// RenderAuditMarkdown writes a full-codebase audit result as Markdown.
// The format mirrors RenderMarkdown but uses an "Audit" header and an
// audit-specific verdict line (no merge decision).
func (r *Renderer) RenderAuditMarkdown(result ReviewResult, repo RepoInfo, minSeverity Severity, minConfidence Confidence, version string) {
	cf := Categorize(result.Findings, minSeverity, minConfidence)

	_, _ = fmt.Fprintf(r.w, "# Audit: %s/%s\n\n", repo.Owner, repo.Name)
	_, _ = fmt.Fprintf(r.w, "> Audited by %s %s\n", attribution.ToolWithVersion(version), attribution.AssistantWith(result.Model))
	RenderWikiProvenance(r.w, result.WikiRepo, result.WikiCommit)
	_, _ = fmt.Fprintln(r.w)

	_, _ = fmt.Fprintf(r.w, "<!-- planwerk-audit: blocking=%d critical=%d warning=%d info=%d unverified=%d verdict=%s -->\n\n",
		len(cf.Blocking), len(cf.Critical), len(cf.Warning), len(cf.Info), len(cf.Unverified),
		r.auditVerdictKey(cf))

	r.renderSection("BLOCKING", cf.Blocking)
	r.renderSection("CRITICAL", cf.Critical)
	r.renderSection("WARNING", cf.Warning)
	r.renderSection("INFO", cf.Info)
	r.renderUnverifiedSection(cf.Unverified)

	r.renderSummary(cf, result)
	r.renderAuditVerdict(cf)
}

// auditVerdictKey returns a short machine-readable verdict for the HTML comment.
func (r *Renderer) auditVerdictKey(cf CategorizedFindings) string {
	if cf.HasBlockersOrCritical() {
		return "ACTION-REQUIRED"
	}
	if len(cf.Warning) > 0 {
		return "IMPROVEMENTS-SUGGESTED"
	}
	return "HEALTHY"
}

func (r *Renderer) renderAuditVerdict(cf CategorizedFindings) {
	var parts []string
	if len(cf.Blocking) > 0 {
		parts = append(parts, fmt.Sprintf("%d BLOCKING", len(cf.Blocking)))
	}
	if len(cf.Critical) > 0 {
		parts = append(parts, fmt.Sprintf("%d CRITICAL", len(cf.Critical)))
	}
	switch {
	case cf.HasBlockersOrCritical():
		_, _ = fmt.Fprintf(r.w, "> [!CAUTION]\n> **Action required** — %s finding(s) must be addressed.\n",
			strings.Join(parts, " and "))
	case len(cf.Warning) > 0:
		_, _ = fmt.Fprintf(r.w, "> [!WARNING]\n> **Improvements suggested** — %d warning(s) should be addressed.\n", len(cf.Warning))
	default:
		_, _ = fmt.Fprint(r.w, "> [!TIP]\n> **Codebase healthy** — no blocking or critical findings.\n")
	}
}

// Usage is the aggregated Claude Code token usage and estimated cost for a
// single Run. It is surfaced two ways: a one-line stderr summary on completion
// and, embedded in the report data block, a machine-readable totals object for
// CI extraction. CostUSD is the sum of Claude Code's own per-call
// total_cost_usd, the literal estimate (not a recomputed tokens×price figure).
//
// Passes breaks the same totals down by the pass that spent them. A run fans
// out over a dozen sessions — a review runs the primary pass, the adversarial
// pass, and one session per domain specialist — so the totals alone cannot say
// which pass a cost belongs to, and a change meant to make one pass cheaper
// cannot be shown to have worked. It is omitted
// from the wire form when empty, so a payload written before this existed (or
// by a caller that tracks no passes) is unchanged.
type Usage struct {
	Calls               int         `json:"calls"`
	InputTokens         int64       `json:"input_tokens"`
	OutputTokens        int64       `json:"output_tokens"`
	CacheReadTokens     int64       `json:"cache_read_input_tokens"`
	CacheCreationTokens int64       `json:"cache_creation_input_tokens"`
	CostUSD             float64     `json:"est_cost_usd"`
	Passes              []PassUsage `json:"passes,omitempty"`
}

// Add returns the totals of u plus those of o. The result carries no per-pass
// breakdown.
func (u Usage) Add(o Usage) Usage {
	return Usage{
		Calls:               u.Calls + o.Calls,
		InputTokens:         u.InputTokens + o.InputTokens,
		OutputTokens:        u.OutputTokens + o.OutputTokens,
		CacheReadTokens:     u.CacheReadTokens + o.CacheReadTokens,
		CacheCreationTokens: u.CacheCreationTokens + o.CacheCreationTokens,
		CostUSD:             u.CostUSD + o.CostUSD,
	}
}

// Sub returns the totals of u minus those of o: what was spent between the
// snapshot o and the later snapshot u. The result carries no per-pass
// breakdown.
func (u Usage) Sub(o Usage) Usage {
	return Usage{
		Calls:               u.Calls - o.Calls,
		InputTokens:         u.InputTokens - o.InputTokens,
		OutputTokens:        u.OutputTokens - o.OutputTokens,
		CacheReadTokens:     u.CacheReadTokens - o.CacheReadTokens,
		CacheCreationTokens: u.CacheCreationTokens - o.CacheCreationTokens,
		CostUSD:             u.CostUSD - o.CostUSD,
	}
}

// PassUsage is one pass's share of a Run's Usage, named by the label the runner
// tags its `claude -p` invocation with ("review", "adversarial",
// "specialist-security", "elaborate-structure", …). The counters mean exactly
// what their Usage counterparts do; only the scope is narrower.
//
// The last four fields record how the pass's sessions ran. NumTurns is the sum
// of each call's num_turns, as its `result` event reports it.
// PermissionDenials is the number of tool calls Claude Code's permission check
// refused. ToolUses counts every tool call of the pass's sessions by tool name,
// a subagent's included. SessionIDs holds each session's id once, in the order
// the sessions ended; the id names the transcript Claude Code wrote for the
// session only for a session that edits, or for any session under
// --keep-transcripts (decision 161). Each is omitted when empty, so a payload
// written before they existed is unchanged, and encoding/json sorts the
// ToolUses keys, so the wire form stays deterministic (decision 152).
type PassUsage struct {
	Pass                string         `json:"pass"`
	Calls               int            `json:"calls"`
	InputTokens         int64          `json:"input_tokens"`
	OutputTokens        int64          `json:"output_tokens"`
	CacheReadTokens     int64          `json:"cache_read_input_tokens"`
	CacheCreationTokens int64          `json:"cache_creation_input_tokens"`
	CostUSD             float64        `json:"est_cost_usd"`
	NumTurns            int            `json:"num_turns,omitempty"`
	PermissionDenials   int            `json:"permission_denials,omitempty"`
	ToolUses            map[string]int `json:"tool_uses,omitempty"`
	SessionIDs          []string       `json:"session_ids,omitempty"`
}

// dataBlockPayload is the JSON structure embedded in the HTML comment for machine consumption.
type dataBlockPayload struct {
	CommitSHA     string         `json:"commit_sha"`
	WikiCommit    string         `json:"wiki_commit,omitempty"`
	Findings      []Finding      `json:"findings"`
	Gates         *GateStats     `json:"gates,omitempty"`
	Passes        []PassStatus   `json:"passes,omitempty"`
	ExcludedFiles []ExcludedFile `json:"excluded_files,omitempty"`
	Usage         Usage          `json:"usage"`
}

// RenderDataBlock returns an HTML comment containing the JSON-encoded findings,
// the per-Run Claude token usage totals, and metadata for machine consumption
// by tools like Claude Code and CI scripts. It leaves out result.Partitions:
// their file lists grow with the pull request, a summary comment over GitHub's
// size cap is cut from its end, where the block sits, and nothing reads them
// back from the block. --format json carries them (decision 142).
func RenderDataBlock(result ReviewResult, commitSHA string, usage Usage) string {
	payload := dataBlockPayload{
		CommitSHA:     commitSHA,
		WikiCommit:    result.WikiCommit,
		Findings:      result.Findings,
		Gates:         result.Gates,
		Passes:        result.Passes,
		ExcludedFiles: result.ExcludedFiles,
		Usage:         usage,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("\n<!-- planwerk-agent-data\n%s\n-->\n", string(data))
}

package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// emDashPlaceholder is rendered in cells/fields that have no value.
const emDashPlaceholder = "—"

type Renderer struct {
	w io.Writer
}

func NewRenderer(w io.Writer) *Renderer {
	return &Renderer{w: w}
}

func (r *Renderer) RenderJSON(result ReviewResult, minSeverity Severity) error {
	cf := Categorize(result.Findings, minSeverity)
	filtered := ReviewResult{
		Summary:        result.Summary,
		Recommendation: result.Recommendation,
	}
	filtered.Findings = append(filtered.Findings, cf.Blocking...)
	filtered.Findings = append(filtered.Findings, cf.Critical...)
	filtered.Findings = append(filtered.Findings, cf.Warning...)
	filtered.Findings = append(filtered.Findings, cf.Info...)

	enc := json.NewEncoder(r.w)
	enc.SetIndent("", "  ")
	return enc.Encode(filtered)
}

func (r *Renderer) RenderMarkdown(result ReviewResult, pr PRInfo, minSeverity Severity, version string) {
	cf := Categorize(result.Findings, minSeverity)

	_, _ = fmt.Fprintf(r.w, "# Review: %s/%s#%d\n\n", pr.Owner, pr.Repo, pr.Number)
	_, _ = fmt.Fprintf(r.w, "> *%s*  \n", pr.Title)
	_, _ = fmt.Fprintf(r.w, "> Reviewed by planwerk-review %s with Claude Code\n\n", version)

	// Machine-readable summary for tooling (Claude Code, CI scripts, etc.)
	_, _ = fmt.Fprintf(r.w, "<!-- planwerk-review: blocking=%d critical=%d warning=%d info=%d recommendation=%s -->\n\n",
		len(cf.Blocking), len(cf.Critical), len(cf.Warning), len(cf.Info),
		r.recommendationKey(cf, result.Recommendation))

	r.renderSection("BLOCKING", cf.Blocking)
	r.renderSection("CRITICAL", cf.Critical)
	r.renderSection("WARNING", cf.Warning)
	r.renderSection("INFO", cf.Info)

	r.renderSummary(cf, result.Summary)
	r.renderRecommendation(cf, result.Recommendation)
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
		_, _ = fmt.Fprintf(r.w, "### %s: %s\n", f.ID, f.Title)

		// Compact single-line metadata: File — Fix — Confidence — Pattern
		meta := fmt.Sprintf("**File**: `%s`", fileRef(f))
		if f.FixClass != "" {
			meta += fmt.Sprintf(" — **Fix**: %s", f.FixClass)
		}
		if f.Confidence != "" {
			meta += fmt.Sprintf(" — **Confidence**: %s", f.Confidence)
		}
		if f.Pattern != "" {
			meta += fmt.Sprintf(" — **Pattern**: %s", f.Pattern)
		}
		_, _ = fmt.Fprintln(r.w, meta)
		_, _ = fmt.Fprintln(r.w)
		_, _ = fmt.Fprintf(r.w, "**Problem**: %s\n\n", f.Problem)
		if f.CodeSnippet != "" {
			_, _ = fmt.Fprintf(r.w, "**Code**:\n```\n%s\n```\n\n", f.CodeSnippet)
		}
		_, _ = fmt.Fprintf(r.w, "**Action Required**: %s\n\n", f.Action)
		if f.SuggestedFix != "" {
			_, _ = fmt.Fprintf(r.w, "**Suggested Fix**:\n```\n%s\n```\n\n", f.SuggestedFix)
		}
		renderFixOptions(r.w, f)
		if len(f.RelatedTo) > 0 {
			_, _ = fmt.Fprintf(r.w, "**Related**: %s\n\n", strings.Join(f.RelatedTo, ", "))
		}

		if i < len(findings)-1 {
			_, _ = fmt.Fprint(r.w, "---\n\n")
		}
	}
	_, _ = fmt.Fprint(r.w, "---\n\n")
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

func (r *Renderer) renderSummary(cf CategorizedFindings, summary string) {
	_, _ = fmt.Fprint(r.w, "## Summary\n\n")
	if summary != "" {
		_, _ = fmt.Fprintf(r.w, "%s\n\n", summary)
	}
	_, _ = fmt.Fprintln(r.w, "| Severity | Count |")
	_, _ = fmt.Fprintln(r.w, "|----------|-------|")
	_, _ = fmt.Fprintf(r.w, "| BLOCKING | %d |\n", len(cf.Blocking))
	_, _ = fmt.Fprintf(r.w, "| CRITICAL | %d |\n", len(cf.Critical))
	_, _ = fmt.Fprintf(r.w, "| WARNING  | %d |\n", len(cf.Warning))
	_, _ = fmt.Fprintf(r.w, "| INFO     | %d |\n\n", len(cf.Info))
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
func (r *Renderer) RenderAuditMarkdown(result ReviewResult, repo RepoInfo, minSeverity Severity, version string) {
	cf := Categorize(result.Findings, minSeverity)

	_, _ = fmt.Fprintf(r.w, "# Audit: %s/%s\n\n", repo.Owner, repo.Name)
	_, _ = fmt.Fprintf(r.w, "> Audited by planwerk-review %s with Claude Code\n\n", version)

	_, _ = fmt.Fprintf(r.w, "<!-- planwerk-audit: blocking=%d critical=%d warning=%d info=%d verdict=%s -->\n\n",
		len(cf.Blocking), len(cf.Critical), len(cf.Warning), len(cf.Info),
		r.auditVerdictKey(cf))

	r.renderSection("BLOCKING", cf.Blocking)
	r.renderSection("CRITICAL", cf.Critical)
	r.renderSection("WARNING", cf.Warning)
	r.renderSection("INFO", cf.Info)

	r.renderSummary(cf, result.Summary)
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

// dataBlockPayload is the JSON structure embedded in the HTML comment for machine consumption.
type dataBlockPayload struct {
	CommitSHA string    `json:"commit_sha"`
	Findings  []Finding `json:"findings"`
}

// RenderDataBlock returns an HTML comment containing the JSON-encoded findings
// and metadata for machine consumption by tools like Claude Code.
func RenderDataBlock(result ReviewResult, commitSHA string) string {
	payload := dataBlockPayload{
		CommitSHA: commitSHA,
		Findings:  result.Findings,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("\n<!-- planwerk-review-data\n%s\n-->\n", string(data))
}

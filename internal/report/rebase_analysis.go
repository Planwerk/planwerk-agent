package report

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/attribution"
)

// RebaseAnalysis is the structured result of the post-rebase pass: for each
// rebased commit, whether the upstream changes that entered the base since the
// PR forked require an adjustment — even where git produced no textual
// conflict. It is the --format json contract described by
// schema/rebase-analysis.schema.json.
type RebaseAnalysis struct {
	Commits        []CommitAnalysis `json:"commits"`
	Summary        string           `json:"summary"`
	Recommendation string           `json:"recommendation"`
	// Model is the resolved Claude model id (e.g. "claude-opus-5") that
	// produced this analysis. It is threaded per-run to the attribution footer
	// and excluded from the serialized payload.
	Model string `json:"-"`
}

// CommitAnalysis carries the per-commit verdict: the rebased commit's SHA and
// subject plus any adjustments the upstream range implies for it. An empty
// Adjustments slice means the commit's assumptions still hold.
type CommitAnalysis struct {
	SHA         string       `json:"sha"`
	Subject     string       `json:"subject"`
	Adjustments []Adjustment `json:"adjustments"`
}

// Adjustment is one concrete change an upstream commit implies for a rebased
// commit. Kind classifies the upstream change that invalidated an assumption;
// UpstreamRef and Confidence are optional context.
type Adjustment struct {
	// Kind is one of: renamed-symbol, changed-signature, removed-helper,
	// lint-rule, semantic-change.
	Kind        string `json:"kind"`
	File        string `json:"file"`
	Detail      string `json:"detail"`
	Action      string `json:"action"`
	UpstreamRef string `json:"upstream_ref,omitempty"`
	Confidence  string `json:"confidence,omitempty"`
}

// RenderRebaseAnalysisJSON writes the analysis as indented JSON, matching the
// review/propose JSON renderers.
func (r *Renderer) RenderRebaseAnalysisJSON(result RebaseAnalysis) error {
	enc := json.NewEncoder(r.w)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

// RenderRebaseAnalysisMarkdown writes the post-rebase analysis as Markdown: a
// per-commit breakdown of the adjustments the upstream range implies, followed
// by an overall summary and recommendation. The same body is posted back to
// the PR as a comment.
func (r *Renderer) RenderRebaseAnalysisMarkdown(result RebaseAnalysis, repoFullName string, prNumber int, onto, version string) {
	_, _ = fmt.Fprintf(r.w, "# Rebase analysis: %s#%d\n\n", repoFullName, prNumber)
	_, _ = fmt.Fprintf(r.w, "> Rebased onto `%s`  \n", onto)
	_, _ = fmt.Fprintf(r.w, "> Analyzed by %s %s\n\n", attribution.ToolWithVersion(version), attribution.AssistantWith(result.Model))

	_, _ = fmt.Fprintf(r.w, "<!-- planwerk-rebase: commits=%d adjustments=%d -->\n\n",
		len(result.Commits), countAdjustments(result.Commits))

	for _, c := range result.Commits {
		r.renderCommitAnalysis(c)
	}

	_, _ = fmt.Fprint(r.w, "## Summary\n\n")
	if result.Summary != "" {
		_, _ = fmt.Fprintf(r.w, "%s\n\n", result.Summary)
	}
	if result.Recommendation != "" {
		_, _ = fmt.Fprintf(r.w, "> [!IMPORTANT]\n> **Recommendation**: %s\n", result.Recommendation)
	}
}

func (r *Renderer) renderCommitAnalysis(c CommitAnalysis) {
	_, _ = fmt.Fprintf(r.w, "## %s %s\n\n", ShortSHA(c.SHA), c.Subject)
	if len(c.Adjustments) == 0 {
		_, _ = fmt.Fprint(r.w, "No adjustments needed — the upstream range does not invalidate this commit.\n\n")
		return
	}
	for _, a := range c.Adjustments {
		_, _ = fmt.Fprintf(r.w, "- **%s** in `%s` — %s\n", a.Kind, a.File, a.Detail)
		_, _ = fmt.Fprintf(r.w, "  - Action: %s\n", a.Action)
		if a.UpstreamRef != "" {
			_, _ = fmt.Fprintf(r.w, "  - Upstream: %s\n", a.UpstreamRef)
		}
		if a.Confidence != "" {
			_, _ = fmt.Fprintf(r.w, "  - Confidence: %s\n", a.Confidence)
		}
	}
	_, _ = fmt.Fprintln(r.w)
}

// countAdjustments totals the adjustments across every commit, for the
// machine-readable summary comment.
func countAdjustments(commits []CommitAnalysis) int {
	n := 0
	for _, c := range commits {
		n += len(c.Adjustments)
	}
	return n
}

// ShortSHA abbreviates a commit SHA to its first seven characters for
// display, leaving shorter strings untouched.
func ShortSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}

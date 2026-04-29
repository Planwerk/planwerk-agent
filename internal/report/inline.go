package report

import (
	"fmt"
	"strings"
)

// FormatInlineComment formats a Finding as a GitHub inline review comment body.
// For auto-fix findings with a SuggestedFix, it includes a GitHub suggestion block.
func FormatInlineComment(f Finding) string {
	var sb strings.Builder

	// Header: ID, title, severity, fix class
	header := fmt.Sprintf("**%s: %s** | %s", f.ID, f.Title, f.Severity)
	if f.FixClass != "" {
		header += fmt.Sprintf(" | %s", f.FixClass)
	}
	sb.WriteString(header)
	sb.WriteString("\n\n")

	// Problem
	sb.WriteString(f.Problem)
	sb.WriteString("\n")

	// For auto-fix findings with a suggested fix, use GitHub's suggestion syntax
	if f.Actionability == ActionabilityAutoFix && f.SuggestedFix != "" {
		sb.WriteString("\n```suggestion\n")
		sb.WriteString(f.SuggestedFix)
		sb.WriteString("\n```\n")
	} else if f.Action != "" {
		sb.WriteString("\n**Action**: ")
		sb.WriteString(f.Action)
		sb.WriteString("\n")
	}

	// Surface the recommended option in the visible body and fold the full
	// option matrix into a <details> block so inline diff comments stay short.
	if len(f.FixOptions) > 0 {
		if f.RecommendedOption != "" {
			sb.WriteString("\n**Recommended fix**: ")
			sb.WriteString(f.RecommendedOption)
			if f.RecommendationReasoning != "" {
				sb.WriteString(" — ")
				sb.WriteString(f.RecommendationReasoning)
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n<details><summary>Fix options</summary>\n\n")
		sb.WriteString("| Option | Approach | Pros | Cons | Effort | Risk if skipped |\n")
		sb.WriteString("|--------|----------|------|------|--------|-----------------|\n")
		for _, opt := range f.FixOptions {
			fmt.Fprintf(&sb, "| %s | %s | %s | %s | %s | %s |\n",
				cellEscape(opt.ID),
				cellEscape(opt.Approach),
				cellEscape(opt.Pros),
				cellEscape(opt.Cons),
				cellEscape(opt.Effort),
				cellEscape(opt.RiskIfSkipped),
			)
		}
		sb.WriteString("\n</details>\n")
	}

	return sb.String()
}

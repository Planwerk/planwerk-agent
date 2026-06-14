package claude

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-review/internal/report"
)

// structureReview calls Claude to convert unstructured review text into JSON.
// If the first attempt produces invalid JSON, decodeJSONWithRepair retries once
// with the parse error included so Claude can correct the output.
func structureReview(rawReview string) (*report.ReviewResult, error) {
	text, err := runClaude("", buildStructurePrompt(rawReview), "structure")
	if err != nil {
		return nil, err
	}
	var result report.ReviewResult
	if err := decodeJSONWithRepair(text, "structured review", &result); err != nil {
		return nil, err
	}
	if err := repairInvalidReview(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

// repairInvalidReview validates result against the finding schema. When a
// finding has an empty title, an off-enum severity, or an off-enum confidence,
// it asks Claude once to repair the offending fields instead of letting
// assignIDs normalize the bad data into placeholder defaults. The repair is
// bounded to a single round: if the repair call fails, or the repaired output
// is unparseable or still invalid, it returns a descriptive error that wraps
// the validation failure.
func repairInvalidReview(result *report.ReviewResult) error {
	verr := result.Validate()
	if verr == nil {
		return nil
	}
	current, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshaling structured review for schema repair: %w", err)
	}
	repaired, err := repairInvalidJSON(string(current), verr, "structured review")
	if err != nil {
		return fmt.Errorf("repairing schema-invalid structured review: %w (validation error: %w)", err, verr)
	}
	repaired = stripMarkdownFences(repaired)
	var fixed report.ReviewResult
	if err := json.Unmarshal([]byte(repaired), &fixed); err != nil {
		return fmt.Errorf("parsing schema-repaired structured review as JSON: %w\nraw output:\n%s", err, repaired)
	}
	if err := fixed.Validate(); err != nil {
		return fmt.Errorf("structured review still invalid after schema repair: %w\nraw output:\n%s", err, repaired)
	}
	*result = fixed
	return nil
}

func buildStructurePrompt(rawReview string) string {
	return `Convert the following code review output into structured JSON. Extract every finding mentioned.

Output ONLY valid JSON matching this exact schema (no markdown fences, no surrounding text):

{
  "findings": [
    {
      "id": "",
      "severity": "BLOCKING|CRITICAL|WARNING|INFO",
      "title": "Short title",
      "file": "path/to/file.go",
      "line": 42,
      "line_end": 45,
      "pattern": "Pattern name if triggered by a review pattern, otherwise omit",
      "actionability": "auto-fix|needs-discussion|architectural",
      "confidence": "verified|likely|uncertain",
      "problem": "Description of the problem",
      "action": "What should be done to fix it",
      "code_snippet": "The exact problematic lines from the diff, preserving indentation",
      "suggested_fix": "The exact replacement code for auto-fix findings (no markdown fences, no comments, correct indentation), or a concrete description for others",
      "fix_options": [
        {
          "id": "A",
          "approach": "One-sentence summary of the fix approach",
          "pros": "Short list of benefits",
          "cons": "Short list of drawbacks",
          "effort": "LOW|MED|HIGH",
          "risk_if_skipped": "What goes wrong if this option is NOT chosen"
        }
      ],
      "recommended_option": "A",
      "recommendation_reasoning": "1-2 sentences citing codebase pattern, review-pattern source, or project constraint",
      "related_to": ["titles of related findings from this review"]
    }
  ],
  "summary": "Overall summary: what was done well, key issues found, and overall quality assessment (2-4 sentences, balanced and constructive)",
  "recommendation": "Whether the PR should be merged and under what conditions"
}

Severity levels:
- BLOCKING: Fundamental architecture or security issues — PR must not be merged
- CRITICAL: Bugs, security vulnerabilities, severe problems — must be fixed before merge
- WARNING: Code quality issues, potential problems — should be fixed
- INFO: Style suggestions, minor improvements — optional

Actionability classification (determines fix approach):
- auto-fix: A senior engineer would apply this fix without discussion (dead code removal, N+1 query fixes, stale comment cleanup, magic number extraction, missing error wrapping, simple nil checks). These will be marked as AUTO-FIX — an agent should apply them directly.
- needs-discussion: Requires team input before fixing (security fixes, race condition resolutions, API/design changes, anything changing observable behavior). These will be marked as ASK — requires human confirmation.
- architectural: Fundamental design issue that needs a broader conversation (wrong abstraction, missing layer, significant refactor needed). These will be marked as ASK.

Confidence levels:
- verified: The issue is directly visible in the code with certainty
- likely: Strong evidence but depends on context outside the diff
- uncertain: Potential issue that requires further investigation

Field rules:
- Leave the "id" field as an empty string — it will be assigned automatically.
- "code_snippet": REQUIRED for every finding. Quote the exact lines from the diff.
- "suggested_fix": REQUIRED for auto-fix findings. Must contain ONLY the replacement code — no markdown fences, no inline comments explaining the fix, correct indentation from the original file. For other findings, provide a concrete description of what to change.
- "line_end": Include when the finding spans multiple lines. Omit if it is a single-line issue.
- "confidence": REQUIRED for every finding.
- "fix_options": REQUIRED (2-3 entries) for findings with actionability "needs-discussion" or "architectural". MUST be omitted (or an empty array) for "auto-fix" findings. Each entry: id ("A","B","C",…), approach, pros, cons, effort (LOW|MED|HIGH), risk_if_skipped.
- "recommended_option": REQUIRED when fix_options is set. Must equal one of the fix_options ids. Omit when fix_options is empty.
- "recommendation_reasoning": REQUIRED when recommended_option is set; 1-2 sentences. Omit otherwise.
- "related_to": Include titles of other findings in this review that are related. Use an empty array if none.
- Extract ONLY findings actually present in the review output below. Do NOT invent new findings, and do NOT re-introduce any issue the review text explicitly suppressed or chose not to flag.
- If there are no findings, return an empty findings array.

<review-output>
` + rawReview + `
</review-output>`
}

func assignIDs(result *report.ReviewResult) {
	counters := map[report.Severity]int{
		report.SeverityBlocking: 0,
		report.SeverityCritical: 0,
		report.SeverityWarning:  0,
		report.SeverityInfo:     0,
	}
	prefixes := map[report.Severity]string{
		report.SeverityBlocking: "B",
		report.SeverityCritical: "C",
		report.SeverityWarning:  "W",
		report.SeverityInfo:     "I",
	}

	for i := range result.Findings {
		sev := report.Severity(strings.ToUpper(string(result.Findings[i].Severity)))
		result.Findings[i].Severity = sev
		result.Findings[i].Actionability = report.NormalizeActionability(string(result.Findings[i].Actionability))
		result.Findings[i].FixClass = report.DeriveFixClass(result.Findings[i].Actionability)
		result.Findings[i].Confidence = report.NormalizeConfidence(string(result.Findings[i].Confidence))
		// Auto-fix findings carry a single SuggestedFix, never an option set.
		// Strip stray options so consumers don't render a confusing table next
		// to a copy-paste-ready replacement.
		if result.Findings[i].Actionability == report.ActionabilityAutoFix {
			result.Findings[i].FixOptions = nil
			result.Findings[i].RecommendedOption = ""
			result.Findings[i].RecommendationReasoning = ""
		} else if result.Findings[i].RecommendedOption != "" {
			// Drop a recommended_option that doesn't match any option ID —
			// otherwise the renderer would point at a non-existent row.
			rec := strings.TrimSpace(result.Findings[i].RecommendedOption)
			match := false
			for _, opt := range result.Findings[i].FixOptions {
				if strings.EqualFold(strings.TrimSpace(opt.ID), rec) {
					match = true
					break
				}
			}
			if !match {
				result.Findings[i].RecommendedOption = ""
				result.Findings[i].RecommendationReasoning = ""
			}
		}
		counters[sev]++
		prefix := prefixes[sev]
		if prefix == "" {
			prefix = "X"
		}
		result.Findings[i].ID = fmt.Sprintf("%s-%03d", prefix, counters[sev])
	}

	// Resolve related_to references: map titles to assigned IDs
	titleToID := make(map[string]string)
	for _, f := range result.Findings {
		titleToID[strings.ToLower(strings.TrimSpace(f.Title))] = f.ID
	}
	for i := range result.Findings {
		for j, ref := range result.Findings[i].RelatedTo {
			if id, ok := titleToID[strings.ToLower(strings.TrimSpace(ref))]; ok {
				result.Findings[i].RelatedTo[j] = id
			}
		}
	}
}

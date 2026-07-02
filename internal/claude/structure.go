package claude

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/report"
	"github.com/planwerk/planwerk-agent/internal/report/schema"
)

// structuredReview is the structuring pass's decode target: the report schema
// plus source_finding_count, the structuring model's own count of distinct
// findings it read in the source review. The count drives warnOnDroppedFindings
// and is not part of the report.ReviewResult payload itself.
type structuredReview struct {
	report.ReviewResult
	SourceFindingCount int `json:"source_finding_count"`
}

// structureReview calls Claude to convert unstructured review text into JSON.
// If the first attempt produces invalid JSON, decodeJSONWithRepair retries once
// with the parse error included so Claude can correct the output.
func (c *Client) structureReview(rawReview string) (*report.ReviewResult, error) {
	// The structuring pass runs on the dedicated structure tier
	// (structureModel/structureEffort), independent of the upstream review
	// model, so the discarded model return is not the attribution model. It
	// passes the wire schema via --json-schema so the transcribe-only tier is
	// constrained to the report shape at the CLI level; decodeJSONWithRepair
	// remains the backstop.
	text, _, err := c.runClaudeStructureWithSchema(buildStructurePrompt(rawReview), "structure", string(schema.StructuredReview))
	if err != nil {
		return nil, err
	}
	var sr structuredReview
	if err := c.decodeJSONWithRepair(text, "structured review", &sr); err != nil {
		return nil, err
	}
	result := sr.ReviewResult
	// Structuring is transcribe-only, so a finding whose source review stated no
	// severity/confidence label arrives here with an empty one. Fill those
	// deterministically in Go before validation instead of spending a repair
	// round on it.
	normalizeTranscribedLabels(&result)
	if err := c.repairInvalidReview(&result); err != nil {
		return nil, err
	}
	warnOnDroppedFindings(sr.SourceFindingCount, len(result.Findings))
	return &result, nil
}

// normalizeTranscribedLabels fills the two labels the transcribe-only structure
// tier may leave empty when the source review stated none: an empty Severity
// becomes INFO and an empty Confidence becomes uncertain, each logged with the
// finding's title. Defaulting conservatively in Go — rather than by a model
// repair round — lands an unlabeled finding in the Unverified section instead of
// being silently dropped by Categorize, which skips unknown-severity findings.
func normalizeTranscribedLabels(result *report.ReviewResult) {
	for i := range result.Findings {
		f := &result.Findings[i]
		if strings.TrimSpace(string(f.Severity)) == "" {
			slogWarnFn("structuring left a finding without a severity label; defaulting to INFO", "title", f.Title)
			f.Severity = report.SeverityInfo
		}
		if strings.TrimSpace(string(f.Confidence)) == "" {
			slogWarnFn("structuring left a finding without a confidence label; defaulting to uncertain", "title", f.Title)
			f.Confidence = report.ConfidenceUncertain
		}
	}
}

// slogWarnFn is the warn-logging seam (mirrors progress.go's slogInfoFn) so the
// reconciliation guard can be asserted in tests without parsing global slog
// output.
var slogWarnFn = slog.Warn

// warnOnDroppedFindings surfaces a likely silent finding drop by the structuring
// pass. That pass now defaults to a cheaper model than the upstream reasoning
// call (DefaultStructureModel), so a long transcription can omit a finding under
// token pressure — and a dropped finding never reaches the PR comment at all,
// unlike a still-present severity downgrade. When the model's own
// source_finding_count exceeds the findings it actually emitted, log a warning
// rather than fail: re-running would discard the expensive upstream reasoning
// for an unprovable gain. A non-positive sourceCount means the model reported
// none, so there is nothing to reconcile.
func warnOnDroppedFindings(sourceCount, emitted int) {
	if sourceCount > 0 && emitted < sourceCount {
		slogWarnFn("structuring emitted fewer findings than the source review reported; a finding may have been dropped in transcription",
			"source_finding_count", sourceCount, "structured_findings", emitted)
	}
}

// repairInvalidReview validates result against the finding schema. When a
// finding has an empty title, an off-enum severity, or an off-enum confidence,
// it asks Claude once to repair the offending fields instead of letting
// assignIDs normalize the bad data into placeholder defaults. The repair is
// bounded to a single round: if the repair call fails, or the repaired output
// is unparseable or still invalid, it returns a descriptive error that wraps
// the validation failure.
func (c *Client) repairInvalidReview(result *report.ReviewResult) error {
	verr := result.Validate()
	if verr == nil {
		return nil
	}
	current, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshaling structured review for schema repair: %w", err)
	}
	repaired, err := repairInvalidJSON(c, string(current), verr, "structured review")
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
	return `Transcribe the following code review output into structured JSON. This is a TRANSCRIPTION pass, not an analysis pass: extract every finding the review states and copy what it already decided — never re-classify, re-judge, or add anything the review did not provide.

` + jsonSchemaOnlyLine() + `

{
  "findings": [
    {
      "id": "",
      "severity": "copy the finding's stated Severity label (BLOCKING|CRITICAL|WARNING|INFO); empty string if it states none",
      "title": "Short title",
      "file": "path/to/file.go",
      "line": 42,
      "line_end": 45,
      "pattern": "Pattern name if the review names one, otherwise omit",
      "actionability": "copy the finding's stated Actionability label (auto-fix|needs-discussion|architectural); empty string if it states none",
      "confidence": "copy the finding's stated Confidence label (verified|likely|uncertain); empty string if it states none",
      "problem": "Description of the problem",
      "action": "What should be done to fix it",
      "code_snippet": "The exact lines the review quoted, preserving indentation; omit if the review quoted none",
      "suggested_fix": "The replacement code or fix description the review gave; omit if it gave none",
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
      "recommendation_reasoning": "1-2 sentences, copied from the review",
      "related_to": ["titles of related findings from this review"]
    }
  ],
  "summary": "Copy the review's overall summary",
  "recommendation": "Copy the review's merge recommendation",
  "source_finding_count": 0
}

Field rules — transcribe, do not analyze:
- ` + emptyIDLine() + `
- "severity", "actionability", "confidence": copy the label the finding STATES, verbatim. When the finding states no such label, leave the field an empty string ("") — NEVER infer, guess, or default one. Classification was decided upstream where the code was read; assigning it here is wrong.
- "code_snippet", "suggested_fix", "fix_options", "recommended_option", "recommendation_reasoning": copy them ONLY when the review states them. Do NOT invent a snippet, a fix, or an option set the review did not provide.
- "line_end": include only when the review gives a line range. Omit for a single-line issue.
- "related_to": include titles of other findings in this review that the review connects. Use an empty array if none.
- Extract ONLY findings actually present in the review output below. Do NOT invent new findings, and do NOT re-introduce any issue the review text explicitly suppressed or chose not to flag.
- If there are no findings, return an empty findings array.
- "source_finding_count": count the distinct findings in the <review-output> below from your reading of the source, and report that integer here. The "findings" array MUST then contain exactly that many entries — if it ends up shorter you dropped a finding during transcription, so recount the source and add the missing one back.

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

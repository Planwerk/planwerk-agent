package claude

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
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
	wireSchema := string(schema.StructuredReview)
	text, _, err := c.runClaudeStructureWithSchema(buildStructurePrompt(rawReview), "structure", wireSchema)
	if err != nil {
		return nil, err
	}
	var sr structuredReview
	if err := c.decodeJSONWithRepairSchema(text, "structured review", wireSchema, &sr); err != nil {
		return nil, wrapWithPersistedAnalysis(rawReview, err)
	}
	result := sr.ReviewResult
	// Structuring is transcribe-only, so a finding whose source review stated no
	// severity/confidence label arrives here with an empty one. Fill those
	// deterministically in Go before validation instead of spending a repair
	// round on it.
	normalizeTranscribedLabels(&result)
	if err := c.repairInvalidReview(&result); err != nil {
		return nil, wrapWithPersistedAnalysis(rawReview, err)
	}
	warnOnDroppedFindings(sr.SourceFindingCount, len(result.Findings))
	return &result, nil
}

// persistFailedAnalysis writes the raw upstream analysis to a temp file so an
// expensive reasoning call is not discarded when structuring finally fails. It
// returns the path, or "" if the file could not be written.
func persistFailedAnalysis(raw string) string {
	f, err := os.CreateTemp("", "planwerk-analysis-*.md")
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(raw); err != nil {
		return ""
	}
	return f.Name()
}

// wrapWithPersistedAnalysis persists the raw analysis and wraps cause with the
// saved path and a re-structure-only retry hint, so a final structuring failure
// does not throw away the expensive analysis. When the file cannot be written it
// returns cause unchanged.
func wrapWithPersistedAnalysis(raw string, cause error) error {
	path := persistFailedAnalysis(raw)
	if path == "" {
		return cause
	}
	return fmt.Errorf("%w\nthe raw analysis was saved to %s — re-run structuring only against it to retry without repeating the analysis", cause, path)
}

// normalizeTranscribedLabels settles the two labels the transcribe-only
// structure tier can leave unusable: one the source review never stated, which
// arrives empty, and one it stated in words the enum does not have. Both become
// INFO and uncertain here, logged with the finding's title and the label that
// was rejected.
//
// Deciding them in Go rather than by a model repair round matters twice over. An
// unlabeled finding would otherwise be dropped silently by Categorize, which
// skips unknown severities; and these are the two of the three schema rules a
// parser can settle alone, which leaves the empty title as the only violation
// that still needs a model to look at it.
func normalizeTranscribedLabels(result *report.ReviewResult) {
	for i := range result.Findings {
		f := &result.Findings[i]
		if sev, err := report.ParseSeverity(string(f.Severity)); err == nil {
			f.Severity = sev
		} else {
			slogWarnFn("structuring left a finding without a usable severity label; defaulting to INFO",
				"title", f.Title, "severity", f.Severity)
			f.Severity = report.SeverityInfo
		}
		if conf, err := report.ParseConfidence(string(f.Confidence)); err == nil {
			f.Confidence = conf
		} else {
			slogWarnFn("structuring left a finding without a usable confidence label; defaulting to uncertain",
				"title", f.Title, "confidence", f.Confidence)
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

// repairInvalidReview validates every finding against the finding schema and
// asks Claude to repair the ones that fail, rather than letting assignIDs
// normalize bad data into placeholder defaults. After
// normalizeTranscribedLabels only an empty title can still fail, and a title is
// a property of one finding, so each offender is repaired on its own: a review
// of twenty findings with one bad title sends that finding, not the other
// nineteen along with it. A review whose findings all validate makes no call.
func (c *Client) repairInvalidReview(result *report.ReviewResult) error {
	for i := range result.Findings {
		verr := result.Findings[i].Validate()
		if verr == nil {
			continue
		}
		fixed, err := c.repairFinding(i, result.Findings[i], verr)
		if err != nil {
			return err
		}
		result.Findings[i] = fixed
	}
	return nil
}

// repairFinding asks Claude to repair one schema-invalid finding, bounded to
// maxRepairRounds. Each round feeds back the latest failure — a parse error when
// the answer is not JSON at all, the validation error when it parses but still
// violates the schema — so a repair that misses once can still land. i and the
// finding's title identify it in every error, since the caller repairs findings
// by index and a bare "still invalid" would not say which one. On failure the
// original finding is returned unchanged alongside the error.
func (c *Client) repairFinding(i int, f report.Finding, verr error) (report.Finding, error) {
	current, err := json.Marshal(f)
	if err != nil {
		return f, fmt.Errorf("marshaling finding %d (%q) for schema repair: %w", i, f.Title, err)
	}
	payload := string(current)
	for round := 0; round < maxRepairRounds; round++ {
		repaired, err := repairInvalidJSON(c, payload, verr, "structured review finding")
		if err != nil {
			return f, fmt.Errorf("repairing schema-invalid finding %d (%q): %w (validation error: %w)", i, f.Title, err, verr)
		}
		repaired = stripMarkdownFences(repaired)
		var fixed report.Finding
		if perr := unmarshalJSON(repaired, &fixed); perr != nil {
			// The repaired output does not even parse; feed that back next round.
			payload, verr = repaired, fmt.Errorf("output is not valid JSON: %w", perr)
			continue
		}
		if verr = fixed.Validate(); verr == nil {
			return fixed, nil
		}
		payload = repaired
	}
	return f, fmt.Errorf("finding %d (%q) still invalid after %d schema-repair rounds: %w", i, f.Title, maxRepairRounds, verr)
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

// structure runs the structuring pass for a free-form analysis and decodes its
// JSON into a T. The pass runs on the dedicated structure tier
// (structureModel/structureEffort), independent of the upstream analysis
// model, so the model it reports is discarded rather than threaded into the
// artifact's attribution. runLabel names the pass in logs and usage;
// decodeLabel names the decoded artifact in a repair error.
func structure[T any](c *Client, prompt, runLabel, decodeLabel string) (*T, error) {
	text, _, err := c.runClaudeStructure(prompt, runLabel)
	if err != nil {
		return nil, err
	}
	var result T
	if err := c.decodeJSONWithRepair(text, decodeLabel, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// finishReview turns a finder pass's raw transcript into its findings: the
// structuring pass, the pass's pattern tag on every finding that carries none
// (skipped when tag is empty), stable IDs, and the model that produced the
// pass. pass names the pass in the structuring error.
func (c *Client) finishReview(raw, model, pass, tag string) (*report.ReviewResult, error) {
	result, err := c.structureReview(raw)
	if err != nil {
		return nil, fmt.Errorf("structuring %s: %w", pass, err)
	}
	if tag != "" {
		for i := range result.Findings {
			if result.Findings[i].Pattern == "" {
				result.Findings[i].Pattern = tag
			}
		}
	}
	assignIDs(result)
	result.Model = model
	return result, nil
}

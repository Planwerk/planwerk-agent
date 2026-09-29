package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/report"
	"github.com/planwerk/planwerk-agent/internal/report/schema"
)

// persistFailedOutput writes a pass's raw output to a temp file so an
// expensive reasoning call is not discarded when its decode finally fails. It
// returns the path, or "" if the file could not be written.
func persistFailedOutput(raw string) string {
	f, err := os.CreateTemp("", "planwerk-finder-output-*.json")
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(raw); err != nil {
		return ""
	}
	return f.Name()
}

// wrapWithPersistedOutput persists a pass's raw output and wraps cause with
// the saved path, so a final decode failure does not throw away the expensive
// session that produced it. When the file cannot be written it returns cause
// unchanged.
func wrapWithPersistedOutput(raw string, cause error) error {
	path := persistFailedOutput(raw)
	if path == "" {
		return cause
	}
	return fmt.Errorf("%w\nthe pass's raw output was saved to %s", cause, path)
}

// repairInvalidReview validates every finding against the finding schema and
// asks Claude to repair the ones that fail, rather than letting assignIDs
// normalize bad data into placeholder defaults. With the labels enforced by
// schema.FinderOutput at the wire, an empty title is the only rule a validated
// output can still break; the result fallback (an envelope without
// structured_output) is not validated by the CLI at all. A title is a property
// of one finding, so each offender is repaired on its own: a review of twenty
// findings with one bad title sends that finding, not the other nineteen along
// with it. A review whose findings all validate makes no call. pass names the
// pass whose output is repaired in each repair session's label.
func (c *Client) repairInvalidReview(result *report.ReviewResult, pass string) error {
	for i := range result.Findings {
		verr := result.Findings[i].Validate()
		if verr == nil {
			continue
		}
		fixed, err := c.repairFinding(i, result.Findings[i], verr, pass)
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
func (c *Client) repairFinding(i int, f report.Finding, verr error, pass string) (report.Finding, error) {
	current, err := json.Marshal(f)
	if err != nil {
		return f, fmt.Errorf("marshaling finding %d (%q) for schema repair: %w", i, f.Title, err)
	}
	payload := string(current)
	for round := 0; round < maxRepairRounds; round++ {
		repaired, err := c.repairInvalidJSON(payload, verr, pass+" finding")
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

// finishReview decodes a finder pass's schema-constrained output into its
// findings (decision 109): the JSON decode with the structure tier's repair
// backstop, the schema repair of any finding that still fails validation, the
// pass's pattern tag on every finding that carries none (skipped when tag is
// empty), stable IDs, and the model that produced the pass. pass names the pass
// in the decode and repair errors; a final failure persists text and names the
// saved file.
func (c *Client) finishReview(text, model, pass, tag string) (*report.ReviewResult, error) {
	var result report.ReviewResult
	if err := c.decodeJSONWithRepairSchema(text, pass, string(schema.FinderOutput), &result); err != nil {
		return nil, wrapWithPersistedOutput(text, err)
	}
	if err := c.repairInvalidReview(&result, pass); err != nil {
		return nil, fmt.Errorf("%s: %w", pass, wrapWithPersistedOutput(text, err))
	}
	if tag != "" {
		for i := range result.Findings {
			if result.Findings[i].Pattern == "" {
				result.Findings[i].Pattern = tag
			}
		}
	}
	assignIDs(&result)
	result.Model = model
	return &result, nil
}

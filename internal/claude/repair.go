package claude

import (
	"encoding/json"
	"fmt"
	"strings"
)

// repairJSON asks Claude to fix malformed JSON, feeding the parse error back so
// the model can correct it. It is a package variable so tests can substitute a
// deterministic repair without invoking the claude CLI; the *Client carries the
// session configuration the repair call runs under.
var repairJSON = func(c *Client, malformed string, parseErr error, label string) (string, error) {
	return c.runClaude("", buildRepairPrompt(malformed, parseErr), label+"-repair")
}

// repairInvalidJSON asks Claude to fix JSON that parsed cleanly but failed
// schema validation, feeding the validation error back so the model can correct
// the offending fields. It is a package variable so tests can substitute a
// deterministic repair without invoking the claude CLI; the *Client carries the
// session configuration the repair call runs under.
var repairInvalidJSON = func(c *Client, invalid string, validationErr error, label string) (string, error) {
	return c.runClaude("", buildValidationRepairPrompt(invalid, validationErr), label+"-schema-repair")
}

// decodeJSONWithRepair strips markdown fences from text and unmarshals it into
// v. On a parse error it asks Claude once to repair the JSON, then retries.
// Every structuring step shares this so the repair behavior — and the one-shot
// fallback that keeps a one-character JSON glitch from failing the whole run —
// stays identical across review, audit, elaborate, propose, gap-analysis, and
// review-prepared. The common case (valid JSON) never triggers a repair call.
func (c *Client) decodeJSONWithRepair(text, label string, v any) error {
	text = stripMarkdownFences(text)
	err := json.Unmarshal([]byte(text), v)
	if err == nil {
		return nil
	}
	retry, retryErr := repairJSON(c, text, err, label)
	if retryErr != nil {
		return fmt.Errorf("parsing %s as JSON: %w\nraw output:\n%s", label, err, text)
	}
	retry = stripMarkdownFences(retry)
	if err2 := json.Unmarshal([]byte(retry), v); err2 != nil {
		return fmt.Errorf("parsing %s as JSON (after retry): %w\nraw output:\n%s", label, err2, retry)
	}
	return nil
}

// buildRepairPrompt asks Claude to fix malformed JSON using the parse error.
func buildRepairPrompt(malformedJSON string, parseErr error) string {
	return `The following JSON is malformed. The Go JSON parser reported this error:

` + parseErr.Error() + `

Fix the JSON so it is valid. Output ONLY the corrected JSON, nothing else.

<malformed-json>
` + malformedJSON + `
</malformed-json>`
}

// buildValidationRepairPrompt asks Claude to fix JSON that is well-formed but
// violates the finding schema, using the validation error to pinpoint the fix.
func buildValidationRepairPrompt(invalidJSON string, validationErr error) string {
	return `The following JSON is valid JSON but violates the finding schema. The validator reported this error:

` + validationErr.Error() + `

Fix the offending finding so every finding satisfies these rules:
- "title" must be a non-empty string.
- "severity" must be one of BLOCKING, CRITICAL, WARNING, INFO.
- "confidence" must be one of verified, likely, uncertain.

Do not invent new findings or drop existing ones. Output ONLY the corrected JSON, nothing else.

<invalid-json>
` + invalidJSON + `
</invalid-json>`
}

// stripMarkdownFences removes ```json ... ``` wrapping that LLMs frequently add.
func stripMarkdownFences(s string) string {
	s = strings.TrimSpace(s)
	// Only strip when the block is well-formed: opening fence, newline,
	// and a matching closing fence. Otherwise leave the input untouched
	// so downstream parsers see the original content.
	if !strings.HasPrefix(s, "```") || !strings.HasSuffix(s, "```") {
		return s
	}
	idx := strings.Index(s, "\n")
	if idx == -1 {
		return s
	}
	s = s[idx+1:]
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

// sanitizeReport normalizes a Claude session's raw final output into the report
// artifact that is echoed to stdout and posted onto GitHub as a comment. Every
// report prompt (plan, implement, fix) instructs the session to output ONLY the
// report, but models routinely wrap it in a markdown fence or prepend a line of
// commentary ("The branch is published. Final report:"). That preamble must
// never reach the issue or PR comment, so we strip a wrapping fence and then
// drop everything before the first line whose trimmed text starts with heading.
//
// When the heading is absent (unexpected output) the de-fenced text is returned
// trimmed but otherwise intact, rather than risk discarding a legitimate report:
// a bare escalation that omits the heading still survives, and its "STATUS: ..."
// line — which the orchestrator parses to stop the loop — is preserved because
// it always appears after the heading anchor.
func sanitizeReport(out, heading string) string {
	out = stripMarkdownFences(out)
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), heading) {
			return strings.TrimSpace(strings.Join(lines[i:], "\n"))
		}
	}
	return strings.TrimSpace(out)
}

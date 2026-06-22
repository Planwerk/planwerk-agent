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
	text, _, err := c.runClaude("", buildRepairPrompt(malformed, parseErr), label+"-repair")
	return text, err
}

// repairInvalidJSON asks Claude to fix JSON that parsed cleanly but failed
// schema validation, feeding the validation error back so the model can correct
// the offending fields. It is a package variable so tests can substitute a
// deterministic repair without invoking the claude CLI; the *Client carries the
// session configuration the repair call runs under.
var repairInvalidJSON = func(c *Client, invalid string, validationErr error, label string) (string, error) {
	text, _, err := c.runClaude("", buildValidationRepairPrompt(invalid, validationErr), label+"-schema-repair")
	return text, err
}

// decodeJSONWithRepair strips markdown fences from text and unmarshals it into
// v. On a parse error it asks Claude once to repair the JSON, then retries.
// Every structuring step shares this so the repair behavior — and the one-shot
// fallback that keeps a one-character JSON glitch from failing the whole run —
// stays identical across review, audit, elaborate, propose, gap-analysis, and
// review-prepared. The common case (valid JSON) never triggers a repair call.
func (c *Client) decodeJSONWithRepair(text, label string, v any) error {
	text = stripMarkdownFences(text)
	err := unmarshalJSON(text, v)
	if err == nil {
		return nil
	}
	retry, retryErr := repairJSON(c, text, err, label)
	if retryErr != nil {
		return fmt.Errorf("parsing %s as JSON: %w\nraw output:\n%s", label, err, text)
	}
	retry = stripMarkdownFences(retry)
	if err2 := unmarshalJSON(retry, v); err2 != nil {
		return fmt.Errorf("parsing %s as JSON (after retry): %w\nraw output:\n%s", label, err2, retry)
	}
	return nil
}

// unmarshalJSON unmarshals text into v, first as-is and — only if that fails —
// after recovering the JSON value embedded in any surrounding prose. Models
// occasionally prepend a sentence before the object ("Removing the preamble
// yields valid JSON:") or trail commentary after it; stripMarkdownFences only
// handles a fence wrapping the whole string, so otherwise the preamble reaches
// the parser as 'T...' and fails with "invalid character 'T'". Valid JSON takes
// the fast path and never pays for extraction.
func unmarshalJSON(text string, v any) error {
	err := json.Unmarshal([]byte(text), v)
	if err == nil {
		return nil
	}
	if extracted := extractJSONValue(text); extracted != text {
		if err2 := json.Unmarshal([]byte(extracted), v); err2 == nil {
			return nil
		}
	}
	// Report the original error: it describes the payload the caller actually
	// produced and is the most useful message to feed back to the repair call.
	return err
}

// extractJSONValue returns the first balanced JSON object or array embedded in s,
// scanning past a prose preamble (and ignoring trailing commentary, including a
// closing markdown fence) that a model wrapped around it. It is string- and
// escape-aware so braces inside string literals never throw off the depth count.
// When s holds no balanced value it is returned unchanged, so a genuinely
// malformed payload still reaches the repair path rather than being altered.
func extractJSONValue(s string) string {
	start := strings.IndexAny(s, "{[")
	if start == -1 {
		return s
	}
	opener := s[start]
	closer := byte('}')
	if opener == '[' {
		closer = ']'
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		switch ch := s[i]; {
		case escaped:
			escaped = false
		case inString && ch == '\\':
			escaped = true
		case ch == '"':
			inString = !inString
		case inString:
			// Other characters inside a string literal are not delimiters.
		case ch == opener:
			depth++
		case ch == closer:
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return s
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

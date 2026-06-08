package claude

import (
	"encoding/json"
	"fmt"
	"strings"
)

// repairJSON asks Claude to fix malformed JSON, feeding the parse error back so
// the model can correct it. It is a package variable so tests can substitute a
// deterministic repair without invoking the claude CLI.
var repairJSON = func(malformed string, parseErr error, label string) (string, error) {
	return runClaude("", buildRepairPrompt(malformed, parseErr), label+"-repair")
}

// decodeJSONWithRepair strips markdown fences from text and unmarshals it into
// v. On a parse error it asks Claude once to repair the JSON, then retries.
// Every structuring step shares this so the repair behavior — and the one-shot
// fallback that keeps a one-character JSON glitch from failing the whole run —
// stays identical across review, audit, elaborate, propose, gap-analysis, and
// review-prepared. The common case (valid JSON) never triggers a repair call.
func decodeJSONWithRepair(text, label string, v any) error {
	text = stripMarkdownFences(text)
	err := json.Unmarshal([]byte(text), v)
	if err == nil {
		return nil
	}
	retry, retryErr := repairJSON(text, err, label)
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

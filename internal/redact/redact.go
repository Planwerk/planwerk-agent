// Package redact scrubs common secret patterns from untrusted PR content
// before it is forwarded to Claude.
//
// The reviewer pipeline assembles the prompt from PR title, body, and commit
// log — any of which may accidentally contain AWS keys, GitHub PATs, private
// key material, or provider-specific tokens. Redact replaces recognized
// patterns with a marker like "[REDACTED:<name>]" and returns a count per
// pattern so callers can warn when scrubbing occurs.
package redact

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Result is the output of Redact: the scrubbed text plus per-pattern counts.
type Result struct {
	Text   string
	Counts map[string]int
}

// Total returns the sum of redaction counts across all patterns.
func (r Result) Total() int {
	total := 0
	for _, c := range r.Counts {
		total += c
	}
	return total
}

// Names returns the pattern names that matched, sorted for deterministic
// logging.
func (r Result) Names() []string {
	names := make([]string, 0, len(r.Counts))
	for n := range r.Counts {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Redact scrubs known secret patterns from text.
func Redact(text string) Result {
	counts := make(map[string]int)
	for _, p := range patterns {
		text = p.apply(text, counts)
	}
	return Result{Text: text, Counts: counts}
}

// behaviorRevision is raised with every change to a filter, to apply, or to
// the marker: none of them shows in a pattern's fields.
const behaviorRevision = 1

// Fingerprint identifies the redaction rules: the hex SHA-256 over
// behaviorRevision and every pattern's name, expression, value group, and
// whether it has a filter, in order. A reader that stores redacted text
// compares it to know when the rules changed.
func Fingerprint() string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%d\n", behaviorRevision)
	for _, p := range patterns {
		// A NUL ends a field and a line feed an entry. A name holds neither,
		// and an expression is written with escapes.
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00%d\x00%t\n", p.name, p.re.String(), p.valueGroup, p.filter != nil)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// pattern describes a single redaction rule.
type pattern struct {
	name string
	re   *regexp.Regexp
	// valueGroup, when > 0, identifies a capture group whose span is the
	// secret value to replace. Remaining submatch text (e.g. the
	// "api_key=" prefix) is preserved around the redaction marker.
	// When 0, the entire match is replaced.
	valueGroup int
	// filter, when non-nil, is passed the candidate secret (the
	// valueGroup capture if set, else the full match). Returning true
	// skips the match — used for placeholder/low-entropy filtering.
	filter func(value string) bool
}

func (p pattern) apply(text string, counts map[string]int) string {
	marker := "[REDACTED:" + p.name + "]"
	if p.valueGroup == 0 {
		return p.re.ReplaceAllStringFunc(text, func(m string) string {
			if p.filter != nil && p.filter(m) {
				return m
			}
			counts[p.name]++
			return marker
		})
	}
	var out strings.Builder
	last := 0
	for _, idx := range p.re.FindAllStringSubmatchIndex(text, -1) {
		vStart, vEnd := idx[2*p.valueGroup], idx[2*p.valueGroup+1]
		if vStart < 0 {
			continue
		}
		value := text[vStart:vEnd]
		if p.filter != nil && p.filter(value) {
			continue
		}
		out.WriteString(text[last:vStart])
		out.WriteString(marker)
		last = vEnd
		counts[p.name]++
	}
	out.WriteString(text[last:])
	return out.String()
}

// Patterns are applied in order. PEM blocks are scrubbed first so that the
// base64 body is not double-processed by later patterns.
var patterns = []pattern{
	{
		name: "private-key-pem",
		// Multi-line: dotall to span newlines inside the key body.
		re: regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----.*?-----END [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----`),
	},
	{
		name: "aws-access-key-id",
		// 20-char AWS key IDs: AKIA/ASIA/etc. prefix + 16 uppercase alphanumerics.
		re: regexp.MustCompile(`\b(?:AKIA|ASIA|AIDA|AGPA|AROA|AIPA|ANPA|ANVA|ASCA)[0-9A-Z]{16}\b`),
	},
	{
		name: "github-token",
		// Classic and OAuth-derived GitHub tokens: ghp_/gho_/ghu_/ghs_/ghr_ + 36-255 chars.
		re: regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,255}\b`),
	},
	{
		name: "github-fine-grained-pat",
		re:   regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{80,255}\b`),
	},
	{
		name: "slack-token",
		re:   regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`),
	},
	{
		name: "google-api-key",
		re:   regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}\b`),
	},
	{
		name: "stripe-secret-key",
		re:   regexp.MustCompile(`\b(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{20,}\b`),
	},
	{
		name: "openai-style-secret-key",
		// Covers sk-..., sk-proj-..., sk-ant-... (OpenAI, Anthropic, and
		// other providers that adopted the convention). Distinct from the
		// stripe rule above which requires sk_live_/sk_test_.
		re: regexp.MustCompile(`\bsk-(?:proj-|ant-)?[A-Za-z0-9_\-]{40,}\b`),
	},
	{
		name: "jwt",
		re:   regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{10,}\.eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\b`),
	},
	{
		name: "assignment-secret",
		// No leading word boundary: the keyword may be embedded in a
		// SCREAMING_SNAKE identifier (GITHUB_TOKEN, MY_API_KEY). Trailing
		// \b still requires the keyword to end at a non-alphanumeric
		// character so "passwordless" does not trigger on "password".
		re: regexp.MustCompile(`(?i)(?:api[_-]?key|access[_-]?key|secret[_-]?key|secret|passwd|password|token|auth[_-]?token|bearer)\b\s*[:=]\s*["']?([A-Za-z0-9_\-+/=]{20,})["']?`),
		valueGroup: 1,
		filter:     isPlaceholderOrLowEntropy,
	},
}

// isPlaceholderOrLowEntropy returns true when the captured value looks like
// documentation filler rather than a real secret. The filter is intentionally
// conservative — on a tie we prefer to scrub.
func isPlaceholderOrLowEntropy(v string) bool {
	lower := strings.ToLower(v)
	placeholders := []string{
		"xxxxx", "your-", "your_", "-here", "_here",
		"example", "placeholder", "changeme", "redacted",
		"todo", "fixme", "abcdef", "123456",
	}
	for _, p := range placeholders {
		if strings.Contains(lower, p) {
			return true
		}
	}
	// Reject runs of a single repeated character.
	if strings.Count(v, string(v[0])) == len(v) {
		return true
	}
	// Require at least two character classes out of {lower, upper, digit,
	// symbol}. A 20-char all-lowercase word is usually prose, not a secret.
	var hasLower, hasUpper, hasDigit, hasSymbol bool
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z':
			hasLower = true
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= '0' && r <= '9':
			hasDigit = true
		default:
			hasSymbol = true
		}
	}
	classes := 0
	for _, b := range []bool{hasLower, hasUpper, hasDigit, hasSymbol} {
		if b {
			classes++
		}
	}
	return classes < 2
}

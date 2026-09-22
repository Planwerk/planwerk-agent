package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/hygiene"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// claimVerdicts is the decode target for the claim-verification output. The
// verdict type itself lives in internal/hygiene, the shared finding-hygiene
// package both review and implement drive it from.
type claimVerdicts struct {
	Verdicts []hygiene.ClaimVerdict `json:"verdicts"`
}

// VerifyFindingClaims re-checks each finding's CLAIM (not merely its quoted
// snippet) against the checkout. It runs on the main tier because it must read
// the cited code — read-only is harness-enforced (design decision #46) — and
// returns one verdict per finding it judged, keyed by index into findings. The
// caller demotes refuted findings rather than dropping them; decodeJSONWithRepair
// backstops malformed output. An empty batch needs no call.
func (c *Client) VerifyFindingClaims(dir string, findings []report.Finding) ([]hygiene.ClaimVerdict, error) {
	if len(findings) == 0 {
		return nil, nil
	}
	text, _, err := c.runClaudeFinder(dir, buildClaimVerificationPrompt(findings), "verify-claims")
	if err != nil {
		return nil, err
	}
	var v claimVerdicts
	if err := c.decodeJSONWithRepair(text, "claim verification", &v); err != nil {
		return nil, err
	}
	return v.Verdicts, nil
}

// buildClaimVerificationPrompt renders the numbered finding list the verifier
// judges against the checkout. It frames the task as testing each finding's
// CLAIM (not its quote) and asks for one of three verdicts: confirmed (the code
// that makes it true, quoted), refuted (the code that makes it false, or a named
// search that shows a symbol it depends on is absent), or unverifiable (the
// checkout cannot settle it). Unverifiable exists so that a claim nobody checked
// is not recorded as a confirmation: the gate's refuted/sent ratio is the pass's
// only evidence that it verifies anything.
func buildClaimVerificationPrompt(findings []report.Finding) string {
	var b strings.Builder
	b.WriteString("You are verifying the highest-severity findings from a code review against the actual checkout. Your job is to confirm or refute each finding's CLAIM — not merely whether it quoted a real line.\n\n")
	b.WriteString(outputLanguageBlock())
	b.WriteString("## Task\n\n")
	b.WriteString("For each finding below, test its claim, not its wording: open the code it cites and follow callers and callees as far as the claim depends on them. Then return exactly one verdict:\n")
	b.WriteString("   - \"confirmed\": you found the code that makes the claimed problem real; the evidence quotes that file:line.\n")
	b.WriteString("   - \"refuted\": you found code that makes the claim false (a guard, a caller that already validates, a type that rules the case out), or a symbol or behavior the claim depends on does not exist. The evidence quotes the disproving file:line or, for something absent, names the search you ran and its empty result (e.g. `grep -rn 'func parseToken' .` found nothing).\n")
	b.WriteString("   - \"unverifiable\": the checkout cannot settle it, because the claim depends on runtime configuration, an external service, or code that is not here; the reason names what is missing.\n\n")
	b.WriteString("Only a refutation demotes a finding, so refute only on evidence you can quote or a search you can name. Never refute on a hunch, and never confirm a claim you did not check: say unverifiable instead.\n\n")
	b.WriteString("This is a read-only check: do NOT edit any file.\n\n")
	b.WriteString(jsonSchemaOnlyLine())
	b.WriteString("\n\n{\n  \"verdicts\": [\n    {\n      \"index\": 0,\n      \"verdict\": \"confirmed|refuted|unverifiable\",\n      \"evidence\": \"path/to/file.go:42 — the exact line you grounded the verdict in\",\n      \"reason\": \"One sentence. REQUIRED for refuted and unverifiable; may be empty for confirmed.\"\n    }\n  ]\n}\n\n")
	b.WriteString("Return exactly one verdict per finding, keyed by its index. Do NOT invent findings.\n\n")
	b.WriteString("<findings>\n")
	for i, f := range findings {
		loc := f.File
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", loc, f.Line)
		}
		fmt.Fprintf(&b, "%d. [%s] %s — %s\n   Problem: %s\n", i, f.Severity, f.Title, loc, f.Problem)
		if f.CodeSnippet != "" {
			fmt.Fprintf(&b, "   Quoted code:\n%s\n", fenceSnippet(f.CodeSnippet))
		}
	}
	b.WriteString("</findings>")
	return b.String()
}

// fenceSnippet wraps s in a backtick fence sized longer than the longest run of
// backticks inside it, so the snippet cannot terminate the fence early. The
// snippet is a verbatim quote of attacker-controlled PR diff lines; a fixed ```
// fence lets a diff containing a raw triple-backtick close it and inject
// free-standing prompt text that instructs this suppression-only verifier to
// refute a genuine finding.
func fenceSnippet(s string) string {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			if run++; run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	ticks := strings.Repeat("`", longest+3)
	return ticks + "\n" + s + "\n" + ticks
}

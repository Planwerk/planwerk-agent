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
	text, _, err := c.runClaude(dir, buildClaimVerificationPrompt(findings), "verify-claims")
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
// judges against the checkout. It frames the task as confirming or refuting each
// finding's CLAIM (not its quote), demands concrete quoted counter-evidence
// before a refutation, and defaults to confirming when none is found.
func buildClaimVerificationPrompt(findings []report.Finding) string {
	var b strings.Builder
	b.WriteString("You are verifying the highest-severity findings from a code review against the actual checkout. Your job is to confirm or refute each finding's CLAIM — not merely whether it quoted a real line.\n\n")
	b.WriteString(outputLanguageBlock())
	b.WriteString("## Task\n\n")
	b.WriteString("For each finding below:\n")
	b.WriteString("1. Open the file it cites in the checkout and read the surrounding code.\n")
	b.WriteString("2. Decide whether the finding's claimed problem is actually true in the code as it stands.\n")
	b.WriteString("3. Return a verdict:\n")
	b.WriteString("   - \"confirmed\": the claimed problem is real in the code.\n")
	b.WriteString("   - \"refuted\": the claimed problem does NOT hold — the code already handles it, the quoted line does not mean what the finding says, or the cited symbol/behavior is not there.\n\n")
	b.WriteString("Refute ONLY when you can point to concrete counter-evidence: quote the file:line that disproves the claim. When you cannot find such evidence, confirm — the default is to trust the finding. Never refute on a hunch.\n\n")
	b.WriteString("This is a read-only check: do NOT edit any file.\n\n")
	b.WriteString(jsonSchemaOnlyLine())
	b.WriteString("\n\n{\n  \"verdicts\": [\n    {\n      \"index\": 0,\n      \"verdict\": \"confirmed|refuted\",\n      \"evidence\": \"path/to/file.go:42 — the exact line you grounded the verdict in\",\n      \"reason\": \"One sentence. REQUIRED for a refuted verdict; may be empty for confirmed.\"\n    }\n  ]\n}\n\n")
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

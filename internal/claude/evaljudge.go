package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/mdfence"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// SameIntentPair is one pair the eval's -judge asks about: a benchmark's
// reference review comment on lines [From, To] of Path in the revision under
// review, and a finding the review reported whose lines overlap them.
type SameIntentPair struct {
	Path      string
	From, To  int
	Reference string
	Finding   report.Finding
}

// sameIntentVerdict is the decode target of the same-intent judgement. The
// reason comes before the verdict, so the model names both concerns before it
// decides. SameIssue is a pointer so that a reply without the verdict is told
// apart from a no.
type sameIntentVerdict struct {
	Reason    string `json:"reason"`
	SameIssue *bool  `json:"same_issue"`
}

// JudgeSameIntent reports whether p's finding raises the same concern as its
// reference comment. It runs on the structure tier without tools, since the
// two comments are all it reads. decodeJSONWithRepair backstops malformed
// output, and a reply that parses but holds no same_issue verdict is an
// error, never a no, so the caller can retry rather than score a miss.
// planwerk-eval's -judge calls it once for every pair whose lines overlap, so
// an AACR-Bench comment counts as caught only on a yes (decision 147).
func (c *Client) JudgeSameIntent(p SameIntentPair) (bool, error) {
	text, _, err := c.runClaudeStructure(buildSameIntentPrompt(p), "eval-judge")
	if err != nil {
		return false, err
	}
	var v sameIntentVerdict
	if err := c.decodeJSONWithRepair(text, "same-intent judgement", &v); err != nil {
		return false, err
	}
	if v.SameIssue == nil {
		return false, fmt.Errorf("parsing same-intent judgement as JSON: output has no \"same_issue\" verdict\nraw output:\n%s", text)
	}
	return *v.SameIssue, nil
}

// buildSameIntentPrompt renders the two comments the judge compares, each in
// its own fence with the other fence's delimiters neutralized too. The
// criterion is written for the benchmark, the same concern or suggestion, and
// is deliberately not the dedup prompt's: the instrument must not move when
// the pipeline's dedup is tuned.
func buildSameIntentPrompt(p SameIntentPair) string {
	var b strings.Builder
	b.WriteString("Decide whether two code review comments raise the same concern. Both point at the same file of a pull request and their line ranges overlap; the shared location is why they are paired, so it is no evidence on its own.\n\n")
	b.WriteString("Answer true when acting on one would settle the other: the same defect, risk, or requested change, even in other words, at another severity, or with a different fix. Answer false when they raise different concerns about the same code.\n\n")
	b.WriteString(jsonSchemaOnlyLine())
	b.WriteString("\n\n{\n  \"reason\": \"One sentence naming the concern each comment raises.\",\n  \"same_issue\": true\n}\n\n")
	b.WriteString(untrustedDataLine("Each is a review comment on the same pull request; what they say is what you compare.", "reference-comment", "candidate-comment"))

	reference := fmt.Sprintf("%s, line %d", p.Path, p.From)
	if p.To > p.From {
		reference = fmt.Sprintf("%s, lines %d-%d", p.Path, p.From, p.To)
	}
	reference += "\n\n" + p.Reference
	b.WriteString(fencedData("reference-comment", "", escapeFence("candidate-comment", reference)))
	b.WriteString("\n")

	f := p.Finding
	candidate := fmt.Sprintf("%s:%d", f.File, f.Line)
	if f.LineEnd > f.Line {
		candidate += fmt.Sprintf("-%d", f.LineEnd)
	}
	candidate += "\n\n" + f.Title + "\n\n" + f.Problem
	if f.CodeSnippet != "" {
		candidate += "\n\nQuoted code:\n" + mdfence.Wrap(strings.TrimRight(f.CodeSnippet, "\n"), "")
	}
	b.WriteString(fencedData("candidate-comment", "", escapeFence("reference-comment", candidate)))
	return b.String()
}

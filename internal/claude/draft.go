package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-review/internal/draft"
)

// DraftQuestions asks Claude for a short list of clarifying questions that
// sharpen a one-line feature idea into a fileable issue. It is the first of the
// draft command's two Claude calls.
func DraftQuestions(seed string) ([]string, error) {
	text, err := runClaude("", buildDraftQuestionsPrompt(seed), "draft-questions")
	if err != nil {
		return nil, fmt.Errorf("generating draft questions: %w", err)
	}
	var payload struct {
		Questions []string `json:"questions"`
	}
	if err := decodeJSONWithRepair(text, "draft questions", &payload); err != nil {
		return nil, err
	}
	return payload.Questions, nil
}

// Draft turns the seed idea plus the clarifying answers into a structured issue
// draft (title, description, motivation, rough scope). It runs one Claude call
// with no checkout — draft describes the idea, it does not plan against the
// repository.
func Draft(ctx draft.Context) (*draft.Result, error) {
	text, err := runClaude("", BuildDraftPrompt(ctx), "draft")
	if err != nil {
		return nil, fmt.Errorf("running draft: %w", err)
	}
	var result draft.Result
	if err := decodeJSONWithRepair(text, "drafted issue", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// buildDraftQuestionsPrompt builds the clarifying-questions prompt. It asks for
// a small, fixed number of questions that probe the description, never the
// implementation.
func buildDraftQuestionsPrompt(seed string) string {
	return `A user wants to file a GitHub issue from this one-line feature idea:

<idea>
` + strings.TrimSpace(seed) + `
</idea>

Ask the few clarifying questions you need to write a strong issue description: the problem behind the idea, who benefits, rough scope, and any hard constraints. Ask only what genuinely sharpens the description. Do NOT ask about implementation details, file layout, or a step-by-step plan — that is a separate, later step.

Output between 3 and 5 questions, each a single short sentence.

Output ONLY valid JSON (no markdown fences, no surrounding text):

{
  "questions": ["First question?", "Second question?"]
}`
}

// BuildDraftPrompt assembles the issue-drafting prompt from the seed and the
// collected Q&A. It enforces the house issue format and the non-goals: the
// draft DESCRIBES the idea and must not slide into elaboration. Exported so the
// draft subcommand can render the prompt without invoking Claude
// (--print-prompt mode).
func BuildDraftPrompt(ctx draft.Context) string {
	var sb strings.Builder

	sb.WriteString(`You are a product-minded engineer turning a rough, one-line feature idea into a clear, ready-to-file GitHub issue.

Your job is to DESCRIBE the idea well, not to plan its implementation. This is the front of the pipeline: a later, separate elaborate step turns this description into an engineering plan. Keep this draft deliberately shallow.

## Hard non-goals — do NOT do any of these
- No file-level affected-areas breakdown.
- No step-by-step implementation design.
- No acceptance criteria grounded in concrete files, symbols, or functions.
- No naming of specific source files or functions, and no codebase analysis for a plan.

If you catch yourself writing an "Affected Areas" list, "Acceptance Criteria", or implementation steps, stop — that belongs to the separate elaborate step, not here. Any mention of scope is a rough sizing, never a design.

`)

	fmt.Fprintf(&sb, "## The idea\n\n<idea>\n%s\n</idea>\n\n", strings.TrimSpace(ctx.Seed))

	if len(ctx.Answers) > 0 {
		sb.WriteString("## Clarifications (from a short Q&A with the author)\n\n")
		for _, qa := range ctx.Answers {
			q := strings.TrimSpace(qa.Question)
			a := strings.TrimSpace(qa.Answer)
			if q == "" {
				continue
			}
			if a == "" {
				a = "(no answer given)"
			}
			fmt.Fprintf(&sb, "- Q: %s\n  A: %s\n", q, a)
		}
		sb.WriteString("\n")
	}

	sb.WriteString(`## What to write

- A descriptive, specific title — imperative mood, no severity or priority prefix.
- A Description: a few short paragraphs framing the problem and what the feature does, in plain terms a maintainer can act on.
- A Motivation: why this matters — who benefits and what is worse without it.
- A rough Scope: exactly one of Small, Medium, or Large.

`)

	sb.WriteString(proseStyleBlock())

	sb.WriteString(`## Output

Output ONLY valid JSON (no markdown fences, no surrounding text):

{
  "title": "Descriptive issue title",
  "description": "Markdown prose for the Description section",
  "motivation": "Markdown prose for the Motivation section",
  "scope": "Small|Medium|Large"
}

- Do NOT invent fields beyond the schema.
- "scope" MUST be exactly one of Small, Medium, or Large.
`)

	return sb.String()
}

// BuildBareDraftPrompt assembles a portable, self-contained draft prompt that a
// user can paste into a manual Claude Code session. It carries no dependency on
// planwerk-review, GitHub, or the pattern catalog: the session runs the short
// Q&A itself and drafts the issue in the house format. Exported for the
// --print-bare-prompt mode.
func BuildBareDraftPrompt(seed string) string {
	var sb strings.Builder

	sb.WriteString(`You are turning a rough, one-line feature idea into a ready-to-file GitHub issue through a short conversation. Describe the idea well; do NOT plan its implementation — no affected-areas breakdown, no step-by-step design, no acceptance criteria tied to concrete files or symbols. That is a separate elaborate step.

`)

	fmt.Fprintf(&sb, "## The idea\n\n%s\n\n", strings.TrimSpace(seed))

	sb.WriteString(`## How to proceed

1. Ask the author 3-5 short clarifying questions — the problem, who benefits, rough scope, and any hard constraints. Ask only what sharpens the description; do not ask about implementation details or a step-by-step plan.
2. Wait for the answers.
3. Draft the issue in this exact house format:

   **Category**: feature | **Scope**: <Small|Medium|Large>

   ## Description

   <a few short paragraphs framing the problem and what the feature does>

   ## Motivation

   <why it matters: who benefits, what is worse without it>

4. Give the issue a descriptive, specific title — imperative mood, no severity or priority prefix.
5. Offer to file it with ` + "`gh issue create`" + ` once the author approves. Do not file it without approval.

`)

	sb.WriteString(proseStyleBlock())

	return sb.String()
}

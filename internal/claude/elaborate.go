package claude

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/elaborate"
	"github.com/planwerk/planwerk-agent/internal/patterns"
)

// Elaborate turns a high-level GitHub issue (typically the output of
// propose/audit) into a deeply detailed engineering plan grounded in the
// actual repository state. It runs two Claude calls:
//  1. Read the issue + walk the repo, producing a freeform elaboration.
//  2. Structure the elaboration into JSON matching elaborate.Result.
func (c *Client) Elaborate(dir string, ctx elaborate.Context) (*elaborate.Result, error) {
	rawElaboration, model, err := c.runClaude(dir, buildElaboratePrompt(ctx), "elaborate")
	if err != nil {
		return nil, fmt.Errorf("running elaboration: %w", err)
	}

	result, err := c.structureElaboration(rawElaboration, ctx)
	if err != nil {
		return nil, fmt.Errorf("structuring elaboration: %w", err)
	}
	result.Model = model
	return result, nil
}

func buildElaboratePrompt(ctx elaborate.Context) string {
	var sb strings.Builder

	sb.WriteString(`You are a Staff Engineer turning a high-level GitHub issue into a deeply detailed engineering plan.

Calibrate the detail to the reader: write for an engineer who is competent with the language but has ZERO context for this codebase and questionable taste — assume they know almost nothing about the problem domain and tend to skip tests. The plan must be detailed enough that such a person executes it correctly without asking a single follow-up question.

Apply these thinking patterns:
- "What already exists in the codebase that this story builds on?" — Cite concrete files, symbols, and migration numbers.
- "What is the smallest concrete change that satisfies the issue?" — No speculative scope.
- "What's the blast radius?" — Which existing tests, configs, or contracts shift when this lands?
- "Where do tests, docs, and migrations live?" — Mirror the repo's existing conventions.
- "What does done look like?" — Acceptance criteria are observable and testable.

`)

	if ctx.RepoName != "" {
		fmt.Fprintf(&sb, "Repository: %s\n\n", ctx.RepoName)
	}

	if ctx.Issue != nil {
		sb.WriteString("## Source Issue\n\n")
		fmt.Fprintf(&sb, "**Issue #%d**: %s\n", ctx.Issue.Number, ctx.Issue.Title)
		if ctx.Issue.URL != "" {
			fmt.Fprintf(&sb, "**URL**: %s\n", ctx.Issue.URL)
		}
		sb.WriteString("\n<issue-body>\n")
		sb.WriteString(strings.TrimSpace(ctx.Issue.Body))
		sb.WriteString("\n</issue-body>\n\n")
	}

	renderIssueRelations(&sb, ctx.MetaIssue, ctx.SiblingIssues, ctx.ChildIssues)

	if len(ctx.Patterns) > 0 {
		sb.WriteString("## Review Patterns to Ground the Elaboration In\n\n")
		sb.WriteString("These patterns are the catalog the project's review/audit/propose tools share. When the elaboration touches an area covered by a pattern, reference the pattern by name in the description or motivation so reviewers can trace the rationale.\n\n")
		sb.WriteString("<review-patterns>\n")
		sb.WriteString(patterns.FormatGroupedForPrompt(ctx.Patterns, ctx.MaxPatterns))
		sb.WriteString("</review-patterns>\n\n")
	}

	if ctx.PriorDraft != "" {
		sb.WriteString("## Revising a Prior Draft\n\n")
		sb.WriteString("A previous draft of this elaboration was reviewed and found to have gaps. Produce a REVISED elaboration that keeps everything already correct and closes every gap below. Do not start from scratch and do not regress sections that were fine.\n\n")
		if len(ctx.ReviewGaps) > 0 {
			sb.WriteString("Gaps to close:\n")
			for _, g := range ctx.ReviewGaps {
				fmt.Fprintf(&sb, "- %s\n", strings.TrimSpace(g))
			}
			sb.WriteString("\n")
		}
		if t := strings.TrimSpace(ctx.ReviewTarget); t != "" {
			fmt.Fprintf(&sb, "What a 10/10 plan would look like: %s\n\n", t)
		}
		sb.WriteString("<prior-draft>\n")
		sb.WriteString(strings.TrimSpace(ctx.PriorDraft))
		sb.WriteString("\n</prior-draft>\n\n")
	}

	sb.WriteString(`## Methodology

1. **Walk the repository first.** Open README, top-level layout, the package(s) the issue mentions, the migration directory if present, the test conventions (unit / integration / E2E), the documentation structure. Do NOT skip this — the elaboration must be grounded in concrete files and symbols, not generic advice.
2. **Identify what already exists.** For every claim like "the X service is in place", cite the exact file path. Distinguish "already exists" from "this issue adds" with concrete boundaries.
3. **Plan the smallest change that satisfies the issue.** Do not invent scope. If the issue is ambiguous, list the ambiguity in Non-Goals or as a clarifying note in Description.
4. **Enumerate every affected area.** Source files, test files, docs, schema/migrations, generated artifacts, CI configuration. Be exhaustive — surprise files in a PR are a process smell.
5. **Write acceptance criteria as observable behavior.** Each item should be a check a reviewer can run (a test passes, a CLI invocation produces X, a doc page exists, an invariant test still passes).
6. **Call out what is explicitly out of scope.** Non-Goals is what stops the issue from accidentally absorbing adjacent work.
7. **List references.** Existing files/sections of the README the elaboration relies on, related issues, external specs.

## Output Sections (in this order)

- **Description**: Multi-paragraph prose. Include numbered "concrete boundaries" subsections that pair "already exists" facts (with file path citations) against "this issue adds". This is the densest section — the example issue we model after had ~9 numbered concrete-boundaries items. Do not be afraid of length when each line carries information.
- **Motivation**: 2-4 paragraphs. Open on the concrete problem and its impact — never on background ("Background: the system has many components…" is throat-clearing; cut it). Structure it as a short arc: the current state → the gap this issue addresses (the "however") → what this change does about it. Why does this matter NOW? What downstream work depends on it? What goes wrong if we skip it?
- **User Stories** (optional): Group the acceptance criteria under user stories, each written as "As a {role}, I want {want}, so that {so_that}" followed by the acceptance criteria that serve it, so the work is anchored to who benefits and why. Proportionality is the rule: generate exactly as many stories as the issue REQUIRES — never pad with redundant stories to reach a minimum, and never omit a needed one. This is a developer CLI, not an end-user product: for purely mechanical or infrastructure work (dependency bumps, formatter sweeps, CI fixes, rebases, refactors) emit ZERO stories and omit the section entirely. Never invent a synthetic persona to fill it — "As a developer, I want clean code" is noise, not a story.
- **Affected Areas**: Bullet list of every file, package, or directory that will be touched, with a parenthetical describing what changes there.
- **Acceptance Criteria**: Bullet checklist (each item starts with a verb and describes an observable check).
- **Non-Goals**: Bullet list of explicitly-out-of-scope items, each with one sentence explaining why.
- **References**: Bullet list of READMEs, existing files, related issues, external specs.

## Plan Quality Rules

The plan must be executable, not merely readable. These are plan failures — never emit them:
- Placeholders: "TBD", "TODO", "to be determined", "fill in later", "(details to follow)".
- Vague hand-waves: "add error handling", "handle edge cases", "add appropriate validation", "etc." — name the SPECIFIC errors, edge cases, and validations instead.
- Cross-references in place of content: "similar to the X section", "same as above", "see Task N" — repeat the actual content; the engineer may read sections out of order.
- A reference to a type, function, file, flag, or migration that no section of the plan defines or cites by its real name.

Every Acceptance Criterion must map to a concrete, named change somewhere in Description or Affected Areas.

## Edge-Case Enumeration

A data-flow acceptance criterion is any criterion about a function, handler, or pipeline that consumes input and produces output. For EVERY such criterion, do not stop at the happy path — spell out its shadow paths as SEPARATE acceptance-criterion entries (one observable check each, since each renders as its own checkbox):
- Empty or zero-length input: the empty string, the empty slice or map, a zero count. State the expected behavior (e.g. return an empty result, not an error).
- Nil or absent input: a nil pointer or interface, a missing optional field, an absent config key. State the expected behavior.
- Upstream error: the dependency this code calls returns an error. Name the CONCRETE error and how it is handled — for example io.EOF, sql.ErrNoRows, context.DeadlineExceeded, or a wrapped fmt.Errorf("doing X: %w", err) propagated to the caller.

Name the actual error or exception value, never a vague "handle the error case".

## Anti-Hallucination Rules

These are MANDATORY:

- Every file path you cite MUST exist in the repository. If you are not sure, walk the directory before naming the file.
- Every line-number citation must be verifiable. Prefer file-only citations when you cannot verify the line.
- NEVER invent symbol names, function signatures, or migration numbers — open the file and read them.
- If the issue references something the repo does not yet have ("S006", "S009", "PX-0011"), preserve the reference exactly as written but mark it as "per the issue" so reviewers know it is an assumption.

## Communication Style

Be direct. State what IS, not what "could be considered". Match the density and precision of a senior engineer's design doc, not a marketing brief.
`)

	sb.WriteString("\n")
	sb.WriteString(proseStyleBlock())
	sb.WriteString(outputLanguageBlock())
	sb.WriteString(domainGlossaryBlock(ctx.Glossary))

	sb.WriteString(`## Self-Review (run before emitting the plan)

Before you output the elaborated issue, review your own draft and fix what you find:
1. Spec coverage — every Acceptance Criterion maps to a concrete change named in Description or Affected Areas. List any criterion that does not, then close the gap.
2. Placeholder scan — no "TBD" / "add error handling" / "see Task N"-style placeholders remain (see Plan Quality Rules).
3. Name consistency — each symbol is named identically everywhere. A function called clearLayers() in one section and clearFullLayers() in another is a bug; reconcile it.

`)

	sb.WriteString("\nNow walk the repository, then produce the elaborated issue. Use the section headings above (**Description**, **Motivation**, **User Stories**, **Affected Areas**, **Acceptance Criteria**, **Non-Goals**, **References**) so the structuring step can extract them reliably.\n")

	return sb.String()
}

func (c *Client) structureElaboration(rawElaboration string, ctx elaborate.Context) (*elaborate.Result, error) {
	// The structuring pass runs on the dedicated structure tier
	// (structureModel/structureEffort), independent of the upstream elaboration
	// model, so the discarded model return is not the attribution model.
	text, _, err := c.runClaudeStructure(buildElaborateStructurePrompt(rawElaboration, ctx), "elaborate-structure")
	if err != nil {
		return nil, err
	}
	var result elaborate.Result
	if err := c.decodeJSONWithRepair(text, "structured elaboration", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func buildElaborateStructurePrompt(rawElaboration string, ctx elaborate.Context) string {
	title := ""
	if ctx.Issue != nil {
		title = ctx.Issue.Title
	}
	return `Convert the following elaborated issue plan into structured JSON.

` + jsonSchemaOnlyLine() + `

{
  "title": "Issue title — keep the source title verbatim unless the elaboration explicitly proposes a sharper one",
  "description": "Full Description section as Markdown (preserve formatting, lists, numbered subsections, code references)",
  "motivation": "Full Motivation section as Markdown",
  "user_stories": [{"role": "who benefits", "want": "the capability", "so_that": "the benefit", "criteria": ["Acceptance criterion serving this story"]}],
  "affected_areas": ["path/to/file.go (what changes)", "package/name (what changes)"],
  "acceptance_criteria": ["Observable check 1", "Observable check 2"],
  "non_goals": ["Out-of-scope item 1 with one-sentence reason", "Out-of-scope item 2 with reason"],
  "references": ["README section name (path/file.md:line)", "Related issue #N", "External spec URL"]
}

Field rules:
- "title": If the elaboration does not change the title, copy this exact source title: ` + jsonString(title) + `.
- "description" and "motivation": Preserve the Markdown structure (bold subheadings, bullet lists, numbered items, inline code) so the issue body renders the same way the example does.
- "user_stories": Populate ONLY if the elaboration emitted a User Stories section; each entry needs role/want/so_that and at least one criterion. Emit [] when the elaboration has none (purely mechanical or infrastructure work) — never synthesize a story the elaboration did not write.
- "affected_areas", "acceptance_criteria", "non_goals", "references": Plain strings, one per array entry. No leading bullets — the renderer adds them.
- Do NOT invent fields beyond the schema.

<elaboration-output>
` + rawElaboration + `
</elaboration-output>`
}

// ReviewElaboration runs the optional reviewer gate: it scores a rendered
// elaboration draft for executability against the repository (0-10) and returns
// the score, the concrete gaps the next revision must close, and what a 10/10
// plan would look like. It is a single structured Claude call with the same
// malformed-JSON repair fallback as the other structurers.
func (c *Client) ReviewElaboration(dir string, ctx elaborate.Context, draftBody string) (*elaborate.ReviewResult, error) {
	// The reviewer gate's score is internal and renders no footer, so the
	// resolved model is not threaded out.
	text, _, err := c.runClaude(dir, buildElaborateReviewPrompt(ctx, draftBody), "elaborate-review")
	if err != nil {
		return nil, fmt.Errorf("running elaboration review: %w", err)
	}
	var rr elaborate.ReviewResult
	if err := c.decodeJSONWithRepair(text, "elaboration review", &rr); err != nil {
		return nil, err
	}
	// Guard against an out-of-range score from the model so the gate and the
	// rendered "N/10" stay sane.
	rr.Score = clampScore(rr.Score)
	return &rr, nil
}

// clampScore bounds an executability score to the valid 0-10 range.
func clampScore(n int) int {
	if n < 0 {
		return 0
	}
	if n > 10 {
		return 10
	}
	return n
}

func buildElaborateReviewPrompt(ctx elaborate.Context, draftBody string) string {
	var sb strings.Builder

	sb.WriteString(`You are a Senior Engineer reviewing a draft engineering plan BEFORE it is handed to an implementer. Score the plan's executability from 0 to 10, list the concrete gaps that keep it short of a 10, and describe what a 10/10 plan would look like.

Do NOT rewrite the plan. Do NOT assume it is correct because it looks thorough — verify its claims against the repository.

`)

	if ctx.Issue != nil {
		fmt.Fprintf(&sb, "## Source Issue #%d: %s\n\n", ctx.Issue.Number, ctx.Issue.Title)
		if body := strings.TrimSpace(ctx.Issue.Body); body != "" {
			sb.WriteString("<issue-body>\n")
			sb.WriteString(body)
			sb.WriteString("\n</issue-body>\n\n")
		}
	}

	sb.WriteString("<draft-plan>\n")
	sb.WriteString(strings.TrimSpace(draftBody))
	sb.WriteString("\n</draft-plan>\n\n")

	sb.WriteString(`## What to check

1. Spec coverage — every Acceptance Criterion maps to a concrete, named change in Description or Affected Areas. A criterion with no corresponding change is a gap.
2. No placeholders — flag "TBD", "add error handling", "handle edge cases", "see Task N", or any reference to a type/function/file/migration the plan never defines or cites by its real name.
3. Ground truth — every cited file path, symbol, or migration must exist in the repository. Walk the repo to confirm; a citation that does not exist is a gap unless the plan explicitly marks it as an assumption.
4. Name consistency — a symbol must be named identically throughout. Two names for one thing is a gap.
5. Executable acceptance criteria — each criterion is an observable check, not a vague goal.
6. Edge-case coverage — every data-flow acceptance criterion enumerates its empty/zero-length, nil/absent, and upstream-error shadow paths as separate entries, each naming the concrete error (e.g. io.EOF, sql.ErrNoRows, a wrapped fmt.Errorf). A data-flow criterion that covers only the happy path is a gap.

## Scoring rubric

Score executability on this scale — the number is what the refine loop optimizes against, so calibrate it honestly:
- 10: an implementer with zero context executes the plan correctly without asking a single question.
- 8-9: solid; at most cosmetic gaps that would not change what gets built.
- 4-7: real gaps that would make the implementer build the wrong thing or get stuck on a decision the plan should have made.
- 0-3: not executable — missing coverage, placeholders, or citations that do not exist.

Only count gaps that would make an implementer build the wrong thing or get stuck. Minor wording and stylistic preferences are NOT gaps.

## Output

Output ONLY valid JSON (no markdown fences, no surrounding text):

{
  "score": 8,
  "gaps": [],
  "to_reach_ten": ""
}

- "score": integer 0-10 from the rubric above.
- "gaps": each entry is one concrete, actionable problem keeping the plan short of a 10, referencing the exact section or criterion. Empty array only for a 10.
- "to_reach_ten": one or two sentences describing what a 10/10 version of THIS plan would add or fix. Empty string only for a 10.
`)

	return sb.String()
}

// jsonString quotes s as a JSON string literal so it can be embedded inline
// in the structuring prompt without breaking the surrounding JSON example.
func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

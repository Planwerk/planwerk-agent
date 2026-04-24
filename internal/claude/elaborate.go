package claude

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-review/internal/elaborate"
	"github.com/planwerk/planwerk-review/internal/patterns"
)

// Elaborate turns a high-level GitHub issue (typically the output of
// propose/audit) into a deeply detailed engineering plan grounded in the
// actual repository state. It runs two Claude calls:
//  1. Read the issue + walk the repo, producing a freeform elaboration.
//  2. Structure the elaboration into JSON matching elaborate.Result.
func Elaborate(dir string, ctx elaborate.Context) (*elaborate.Result, error) {
	rawElaboration, err := runClaude(dir, buildElaboratePrompt(ctx), "elaborate")
	if err != nil {
		return nil, fmt.Errorf("running elaboration: %w", err)
	}

	result, err := structureElaboration(rawElaboration, ctx)
	if err != nil {
		return nil, fmt.Errorf("structuring elaboration: %w", err)
	}
	return result, nil
}

func buildElaboratePrompt(ctx elaborate.Context) string {
	var sb strings.Builder

	sb.WriteString(`You are a Staff Engineer turning a high-level GitHub issue into a deeply detailed engineering plan that another senior engineer can pick up and execute without further clarification.

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

	if len(ctx.Patterns) > 0 {
		sb.WriteString("## Review Patterns to Ground the Elaboration In\n\n")
		sb.WriteString("These patterns are the catalog the project's review/audit/propose tools share. When the elaboration touches an area covered by a pattern, reference the pattern by name in the description or motivation so reviewers can trace the rationale.\n\n")
		sb.WriteString("<review-patterns>\n")
		sb.WriteString(patterns.FormatGroupedForPrompt(ctx.Patterns, ctx.MaxPatterns))
		sb.WriteString("</review-patterns>\n\n")
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
- **Motivation**: 2-4 paragraphs. Why does this matter NOW? What downstream work depends on it? What goes wrong if we skip it?
- **Affected Areas**: Bullet list of every file, package, or directory that will be touched, with a parenthetical describing what changes there.
- **Acceptance Criteria**: Bullet checklist (each item starts with a verb and describes an observable check).
- **Non-Goals**: Bullet list of explicitly-out-of-scope items, each with one sentence explaining why.
- **References**: Bullet list of READMEs, existing files, related issues, external specs.

## Anti-Hallucination Rules

These are MANDATORY:

- Every file path you cite MUST exist in the repository. If you are not sure, walk the directory before naming the file.
- Every line-number citation must be verifiable. Prefer file-only citations when you cannot verify the line.
- NEVER invent symbol names, function signatures, or migration numbers — open the file and read them.
- If the issue references something the repo does not yet have ("S006", "S009", "PX-0011"), preserve the reference exactly as written but mark it as "per the issue" so reviewers know it is an assumption.

## Communication Style

Be direct. State what IS, not what "could be considered". Match the density and precision of a senior engineer's design doc, not a marketing brief.
`)

	sb.WriteString("\nNow walk the repository, then produce the elaborated issue. Use the section headings above (**Description**, **Motivation**, **Affected Areas**, **Acceptance Criteria**, **Non-Goals**, **References**) so the structuring step can extract them reliably.\n")

	return sb.String()
}

func structureElaboration(rawElaboration string, ctx elaborate.Context) (*elaborate.Result, error) {
	text, err := runClaude("", buildElaborateStructurePrompt(rawElaboration, ctx), "elaborate-structure")
	if err != nil {
		return nil, err
	}
	text = stripMarkdownFences(text)

	var result elaborate.Result
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		// Retry once with the parse error fed back to Claude, mirroring the
		// review structurer's recovery path.
		retry, retryErr := runClaude("", buildRepairPrompt(text, err), "elaborate-repair")
		if retryErr != nil {
			return nil, fmt.Errorf("parsing structured elaboration as JSON: %w\nraw output:\n%s", err, text)
		}
		retry = stripMarkdownFences(retry)
		if err2 := json.Unmarshal([]byte(retry), &result); err2 != nil {
			return nil, fmt.Errorf("parsing structured elaboration as JSON (after retry): %w\nraw output:\n%s", err2, retry)
		}
	}
	return &result, nil
}

func buildElaborateStructurePrompt(rawElaboration string, ctx elaborate.Context) string {
	title := ""
	if ctx.Issue != nil {
		title = ctx.Issue.Title
	}
	return `Convert the following elaborated issue plan into structured JSON.

Output ONLY valid JSON matching this exact schema (no markdown fences, no surrounding text):

{
  "title": "Issue title — keep the source title verbatim unless the elaboration explicitly proposes a sharper one",
  "description": "Full Description section as Markdown (preserve formatting, lists, numbered subsections, code references)",
  "motivation": "Full Motivation section as Markdown",
  "affected_areas": ["path/to/file.go (what changes)", "package/name (what changes)"],
  "acceptance_criteria": ["Observable check 1", "Observable check 2"],
  "non_goals": ["Out-of-scope item 1 with one-sentence reason", "Out-of-scope item 2 with reason"],
  "references": ["README section name (path/file.md:line)", "Related issue #N", "External spec URL"]
}

Field rules:
- "title": If the elaboration does not change the title, copy this exact source title: ` + jsonString(title) + `.
- "description" and "motivation": Preserve the Markdown structure (bold subheadings, bullet lists, numbered items, inline code) so the issue body renders the same way the example does.
- "affected_areas", "acceptance_criteria", "non_goals", "references": Plain strings, one per array entry. No leading bullets — the renderer adds them.
- Do NOT invent fields beyond the schema.

<elaboration-output>
` + rawElaboration + `
</elaboration-output>`
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

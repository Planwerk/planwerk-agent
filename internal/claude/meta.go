package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-review/internal/meta"
)

// Meta carves a Meta Issue into the fewest sensible draft-depth Sub Issues. It
// runs one Claude call with no checkout — meta reads the issue and decides the
// breakdown, it does not plan against the repository.
func (c *Client) Meta(ctx meta.Context) (*meta.Result, error) {
	text, model, err := c.runClaude("", BuildMetaPrompt(ctx), "meta")
	if err != nil {
		return nil, fmt.Errorf("running meta split: %w", err)
	}
	var result meta.Result
	if err := c.decodeJSONWithRepair(text, "meta split", &result); err != nil {
		return nil, err
	}
	result.Model = model
	return &result, nil
}

// BuildMetaPrompt assembles the meta-split prompt from the Meta Issue. It
// enforces the autonomy, fewest-packages, and preserve-the-implied-structure
// rules, reuses draft's hard non-goals so a Sub Issue cannot slide into a
// file-level plan, and specifies the {{sub:KEY}} placeholder contract the
// runner substitutes deterministically. Exported so the meta subcommand and the
// prompt golden test can render it without invoking Claude.
func BuildMetaPrompt(ctx meta.Context) string {
	var sb strings.Builder

	sb.WriteString(`You are a Staff Engineer breaking a Meta Issue into the fewest self-contained Sub Issues.

A Meta Issue frames a larger body of work as several self-contained work packages. Read it and decide — on your own — which Sub Issues are worth carving out, then describe each at DRAFT depth: enough context to stand on its own and be picked up later, never a file-level plan.

Make the breakdown autonomously. Do NOT ask the author what to split or how — read the Meta Issue and decide. Keep the split deliberately small: group the work into the fewest sensible packages rather than many tiny ones, and never let the breakdown sprawl. Where the Meta Issue already implies an order or dependencies — a foundation package others build on, numbered tiers, lettered workstreams — preserve that structure rather than inventing your own, and key the Sub Issues so that order is clear.

## Hard non-goals — do NOT do any of these
- No file-level affected-areas breakdown.
- No step-by-step implementation design.
- No acceptance criteria grounded in concrete files, symbols, or functions.
- No naming of specific source files or functions, and no codebase analysis for a plan.

Each Sub Issue is a draft, not a plan. Turning a Sub Issue into a file-level engineering plan is the job of the separate elaborate and implement commands, run per Sub Issue when the author is ready. If you catch yourself writing acceptance criteria, an affected-areas list, or implementation steps, stop — that belongs to a later step.

Describe each Sub Issue by the behavior and interfaces it changes — what users and callers see — because a Sub Issue sits in the tracker and may be carved off and picked up long after the surrounding code has moved; a behavioral brief outlives one pinned to today's file layout.

`)

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

	sb.WriteString(`## What to write for each Sub Issue

Carve each Sub Issue as a vertical slice: it cuts end-to-end through every layer it touches and is demoable on its own, rather than a horizontal layer (all the types here, all the wiring there) that delivers nothing until a sibling lands.

- A short, stable key that encodes any implied order — "a", "b", "c" for lettered workstreams; "tier-1", "tier-2" for numbered tiers; "foundation" for a package others build on. Keys are lowercase, hyphenated, and unique.
- A descriptive, specific title — imperative mood, no severity or priority prefix.
- A Description: a few short paragraphs framing this work package and what it delivers, in plain terms a maintainer can pick up. State an honest "Blocked by" ordering here — name the sibling packages this one must wait on by their keys, or say it is unblocked — so siblings with no real dependency stay grabbable in parallel. Draft depth only.
- A Motivation: why this package matters and what depends on it.
- A rough Scope: exactly one of Small, Medium, or Large.

## Syncing the Meta Issue body

Return the Meta Issue body in "metaBody", edited ONLY to mark where each Sub Issue reference belongs:
- Reproduce the body VERBATIM. Change nothing except inserting placeholder tokens.
- Where the body carries a work-package list or section — a checkbox list, numbered tiers, lettered workstreams — insert the token {{sub:KEY}} on the existing line for that package, using the matching Sub Issue key. The runner replaces each token with the created issue's #number so the prose and the sub-issue list agree.
- Insert ONE token per work-package line, and only on lines that already describe a package. Do not add, remove, reorder, or reword any line.
- If the body carries no such list, return it UNCHANGED with no tokens.
- Every {{sub:KEY}} you insert MUST use a key you declared in "subIssues".

`)

	sb.WriteString(proseStyleBlock())
	sb.WriteString(outputLanguageBlock())

	sb.WriteString(`## Output

Output ONLY valid JSON (no markdown fences, no surrounding text):

{
  "subIssues": [
    {
      "key": "a",
      "title": "Descriptive Sub Issue title",
      "description": "Markdown prose for the Description section",
      "motivation": "Markdown prose for the Motivation section",
      "scope": "Small|Medium|Large"
    }
  ],
  "metaBody": "the Meta Issue body, verbatim, with {{sub:KEY}} tokens inserted on work-package lines"
}

- Do NOT invent fields beyond the schema.
- "scope" MUST be exactly one of Small, Medium, or Large.
- Prefer the fewest Sub Issues that cover the work; do not split into many tiny packages.
`)

	return sb.String()
}

package claude

import (
	"strings"

	"github.com/planwerk/planwerk-review/internal/attribution"
)

// This file holds prompt building blocks that are shared across more than one
// prompt builder. Keeping them in one place stops the copies from drifting
// (the failure mode that motivated extracting them): before this, the audit
// prompt carried a shortened Suppressions list and the adversarial and
// compliance prompts had no anti-hedging discipline at all.
//
// Blocks that carry intentional, builder-specific variation (the Staff
// Engineer persona, Verification of Claims, and Finding Enrichment differ
// between a diff review and a whole-codebase audit) are deliberately NOT
// shared here — forcing them into one shape would inject diff-only wording
// into the codebase audit and vice versa.

// promptScope distinguishes a diff-scoped review (a PR or branch comparison)
// from a whole-codebase audit. It selects the scope-specific suppression
// bullets so a single source can serve both without leaking diff-only wording
// into the codebase audit.
type promptScope int

const (
	// scopeDiff is a review that only considers added/modified lines relative
	// to a base branch (review, adversarial, compliance).
	scopeDiff promptScope = iota
	// scopeCodebase is a review of the entire current repository state (audit).
	scopeCodebase
)

// suppressionsBlock returns the "## Suppressions — DO NOT flag these" section.
// The common bullets apply to every review type; the two diff-only bullets
// (already-addressed-in-the-same-diff, and only-review-changed-lines) are
// emitted only for scopeDiff, where a diff actually exists.
//
// For scopeDiff this reproduces the canonical review suppression list verbatim.
func suppressionsBlock(scope promptScope) string {
	bullets := []string{
		`TODO/FIXME comments that reference an issue tracker (e.g. TODO(#123))`,
		`Missing tests for trivial getters/setters, simple delegation methods, or configuration constants — this does NOT suppress missing tests for functions with logic or branching`,
		`Import ordering or formatting differences (these are handled by formatters)`,
		`Variable naming that follows the project's existing conventions, even if you'd prefer different names`,
		`Missing documentation on unexported/private functions or internal implementation details — this does NOT suppress missing documentation for new public APIs, CLI flags, or user-facing behavior changes`,
		`Minor style preferences that don't affect correctness or readability`,
		`"X is redundant with Y" when the redundancy is harmless and aids readability (defense in depth)`,
		`Threshold or constant comments that would rot faster than the code they describe`,
		`Assertions that already cover the behavior being tested (e.g. "this assertion could be tighter")`,
		`Consistency-only suggestions ("use X style everywhere") with no correctness impact`,
	}
	if scope == scopeDiff {
		bullets = append(bullets, `Issues that are already addressed elsewhere in the same diff — read the FULL diff before commenting`)
	}
	bullets = append(bullets,
		`Suggestions to "add logging" when the error path already returns a descriptive error`,
		`"Consider using X library" when the current approach works correctly — this does NOT suppress flagging deprecated, unmaintained, or severely outdated versions of NEWLY INTRODUCED dependencies`,
	)
	if scope == scopeDiff {
		bullets = append(bullets, `Code that was not changed in this diff — only review and comment on added or modified lines, never on unchanged surrounding context`)
	}

	var b strings.Builder
	b.WriteString("## Suppressions — DO NOT flag these\n\n")
	for _, bl := range bullets {
		b.WriteString("- ")
		b.WriteString(bl)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
}

// proseStyleBlock returns the "## Prose Style" section applied to builders that
// generate narrative text a human reads — elaborate, propose, gap analysis,
// review-prepared. The rules raise writing quality (lead-first, concrete,
// active voice, no AI-slop vocabulary) and are adapted from the
// econ-writing-skill reference.
//
// The concreteness rule is deliberately subordinated to accuracy: a model told
// only to "be concrete" will fabricate file paths and numbers to sound
// specific. The block states that genuine unknowns are marked as assumptions,
// never invented — so it cooperates with, rather than fights, each builder's
// anti-hallucination rules.
func proseStyleBlock() string {
	return `## Prose Style

Apply these rules to all prose you write (descriptions, motivations, summaries, issue bodies):

- Lead with the most important information; never bury it. State the one core point in the first sentence.
- Be concrete: name the actual behavior, component, file, or change — not "improve the system" or "various aspects". This rule is subordinate to accuracy: NEVER invent a specific (a file path, symbol, or number) just to sound concrete. When a specific is genuinely unknown, mark it as an assumption rather than fabricating it.
- Active voice, present tense. Short, common words ("use", not "utilize"). One idea per paragraph, topic sentence first.
- Cut ruthlessly. Delete throat-clearing openers ("It should be noted that", "It is worth noting that", "In other words", "This contributes by"). If a sentence adds nothing, remove it.
- ` + bannedVocabularyLine() + `
- Vary sentence length. Do not dress up your own work with adjectives ("critical fix", "powerful feature"). Write "This change…", not a bare "This…".

`
}

// outputLanguageBlock returns the "## Output Language" section that pins every
// generated artifact — implementation plan, fix report, implementation report,
// review, audit, analysis, drafted issue, … — to English, whatever language the
// input is written in. The maintainers routinely write issues, seeds, and code
// comments in German; without this pin the model mirrors that language into the
// artifact. The one deliberate exception lives outside this block: the draft
// command asks its clarifying questions in the author's own language (see
// buildDraftQuestionsPrompt and BuildBareDraftPrompt) — only the questions, not
// the drafted issue, which stays English like every other artifact.
func outputLanguageBlock() string {
	return `## Output Language

Write your entire output in English, whatever language the input is written in — the issue, diff, seed idea, code comments, or Q&A answers may be in another language. Read non-English input faithfully, but never mirror its language back: the artifact you produce is always English. Quote identifiers, code, paths, and command output verbatim; translate the surrounding prose.

`
}

// bannedVocabularyLine returns the shared AI-slop vocabulary ban, used by both
// the prose-style block (narrative builders) and the communication-style block
// (review findings) so the list has a single source. It combines the gstack
// and econ-writing ban lists; qualifiers ("leverage" only as a verb, "robust"
// only outside statistics) keep the constraint from over-triggering on
// legitimate technical usage.
func bannedVocabularyLine() string {
	return `Never use AI-slop vocabulary: delve, landscape, multifaceted, notably, crucial, comprehensive, nuanced, furthermore, underscore, foster, showcase, leverage (as a verb), robust (outside its statistical sense), pivotal, groundbreaking, shed light on, pave the way.`
}

// communicationStyleBlock returns the anti-sycophancy "## Communication Style"
// section. Directness is universal across every review type, so the same
// block is shared verbatim by review, audit, adversarial, and compliance.
func communicationStyleBlock() string {
	return `## Communication Style

Be direct and decisive in your findings. Do NOT hedge:
- Do NOT write "you might want to consider..." — state what IS wrong
- Do NOT write "this could potentially cause..." — state what WILL happen
- Do NOT write "it might be worth looking into..." — state the specific problem
- Take a clear position on every finding. If something is wrong, say it is wrong.
- If something is fine, do not mention it at all.
- ` + bannedVocabularyLine() + `

`
}

// commitTrailerBlock returns the "## Commit trailers" section shared by every
// prompt whose session creates commits (implement, fix, address, and their
// bare variants). It pins the trailer convention the maintainers require on
// EVERY commit: an Assisted-by trailer naming the assistant, a Signed-off-by
// added via `git commit -s` as the final line, and never a Co-authored-by
// trailer.
//
// Ordering is load-bearing. The Signed-off-by MUST be the last line, so the
// Assisted-by line is passed as the final `-m` paragraph — `git commit -s`
// folds its Signed-off-by into that same trailer block, landing it last.
// Passing Assisted-by via `--trailer` instead would place it AFTER the
// sign-off, breaking the order. The Assisted-by format follows the osism
// promptcraft commit skill: the agent's own name, optionally suffixed with its
// exact model id.
func commitTrailerBlock() string {
	return `## Commit trailers

EVERY commit you create MUST end with exactly these two trailers, in this order:

    Assisted-by: Claude
    Signed-off-by: <committer name> <committer email>

- Pass ` + "`-s`" + ` to ` + "`git commit`" + ` so git appends the ` + "`Signed-off-by`" + ` line from the committer identity. It MUST be the very last line of the message.
- Add an ` + "`Assisted-by: Claude`" + ` trailer naming yourself as the assistant. Append your exact model id when your runtime context provides it (e.g. ` + "`Assisted-by: Claude:claude-opus-4-8`" + `); otherwise emit ` + "`Assisted-by: Claude`" + ` alone — never guess the id. Pass it as the final ` + "`-m`" + ` paragraph, NOT via ` + "`--trailer`" + ` (git places ` + "`--trailer`" + ` values after the sign-off), so it lands directly above ` + "`Signed-off-by`" + `.
- NEVER add a ` + "`Co-authored-by`" + ` trailer — not for Claude, not for planwerk-review, not for anyone.

`
}

// attributionFooterBlock returns the "## Attribution footer" section shared by
// every prompt whose session authors a GitHub artifact a human reads — a pull
// request description, an issue or PR comment, a review-thread reply. It is the
// prose-side companion to commitTrailerBlock: where that pins the Assisted-by
// commit trailer, this pins the self-attribution footer that signs the artifact
// and names the exact model that wrote it.
//
// The artifacts planwerk-review renders itself carry the same wording from the
// internal/attribution package; this block governs the artifacts the agent
// writes directly, where the orchestrator only ever passed a model alias and
// only the agent knows its exact model id at runtime — the same reason the
// model id lives in the prompt rather than the Go renderer.
//
// verb names the action in the footer's lead so it matches the command the
// session runs ("Implemented by" for implement, "Addressed by" for address)
// instead of a generic word the agent would otherwise copy verbatim. It mirrors
// the verb the Go renderers use for the same command's artifacts.
func attributionFooterBlock(verb string) string {
	return `## Attribution footer

End every GitHub artifact you author yourself — the pull request description, any issue or PR comment, any review-thread reply — with this attribution footer as its final line, after a "---" separator:

    ---

    _` + verb + ` ` + attribution.Tool() + ` with Claude:<your model id>_

- Append your exact model id when your runtime context provides it (e.g. ` + "`with Claude:claude-opus-4-8`" + `); otherwise write a bare ` + "`with Claude`" + ` — never guess the id. This mirrors the Assisted-by commit trailer.
- Keep the ` + "`[planwerk-review]`" + ` link intact so the artifact points back at the tool that produced it.
- Add the footer once, as the last line of the artifact — do NOT repeat it per section.

`
}

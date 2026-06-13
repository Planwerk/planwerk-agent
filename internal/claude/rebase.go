package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/rebase"
	"github.com/planwerk/planwerk-review/internal/report"
)

// ResolveRebaseConflict runs a fresh Claude Code session inside the checkout to
// resolve the conflict on one stopped rebase commit. The session inspects both
// sides, honors the replayed commit's intent, produces a semantically correct
// resolution, and stages it with `git add` — but does NOT run
// `git rebase --continue` or push; the orchestrator owns those. It runs in
// auto mode so the session can edit and `git add` without confirmation.
func ResolveRebaseConflict(dir string, ctx rebase.ConflictContext) (string, error) {
	out, err := runClaudeAuto(dir, BuildRebaseConflictPrompt(ctx), "rebase-conflict")
	if err != nil {
		return "", fmt.Errorf("resolving rebase conflict: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// AnalyzeRebasedCommits runs a read-only Claude session that judges each
// rebased commit against the upstream range and returns the structured
// per-commit adjustments. The decode shares decodeJSONWithRepair so a
// one-character JSON glitch does not fail the run.
func AnalyzeRebasedCommits(dir string, ctx rebase.AnalysisContext) (*report.RebaseAnalysis, error) {
	text, err := runClaude(dir, BuildRebaseAnalysisPrompt(ctx), "rebase-analysis")
	if err != nil {
		return nil, fmt.Errorf("analyzing rebased commits: %w", err)
	}
	var result report.RebaseAnalysis
	if err := decodeJSONWithRepair(text, "structured rebase-analysis", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ApplyRebaseAdjustments runs a fresh auto-mode Claude session that applies the
// post-rebase analysis as fixup commits folded into the commits they belong to
// (git commit --fixup + git rebase --autosquash), reusing the fix --local
// recipe. It does NOT push — the orchestrator force-pushes under --push.
func ApplyRebaseAdjustments(dir string, ctx rebase.ApplyContext) (string, error) {
	out, err := runClaudeAuto(dir, BuildRebaseApplyPrompt(ctx), "rebase-apply")
	if err != nil {
		return "", fmt.Errorf("applying rebase adjustments: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// BuildRebaseConflictPrompt assembles the prompt for resolving a single
// conflicting commit during the rebase. Exported so the rebase subcommand and
// tests can render it without invoking Claude.
func BuildRebaseConflictPrompt(ctx rebase.ConflictContext) string {
	var sb strings.Builder

	sb.WriteString(`You are a Staff Engineer resolving a git rebase conflict on a GitHub pull request.

`)
	sb.WriteString(baselineBehavioralPrinciples)
	sb.WriteString(`Apply these task-specific thinking patterns on top of the baseline above:
- "Resolve semantically, never a blind side-pick." — Do NOT just take "ours" or "theirs". Produce the resolution that keeps BOTH the replayed commit's intent and the upstream change correct.
- "Honor the replayed commit's intent." — The commit being applied has a purpose (its subject/message). The resolved file must still serve that purpose after absorbing the upstream change.
- "Inspect both sides before editing." — Read the conflict markers, then use git to see each side: ` + "`git log`, `git show`, `git diff`" + ` on the conflicted paths. Understand what upstream changed and why before resolving.
- "Touch only the conflicted files." — Resolve exactly the files git marked as unmerged. Do not refactor, reformat, or change unrelated code while resolving.
- "Leave no markers." — The resolved files must contain no conflict markers (` + "`<<<<<<<`, `=======`, `>>>>>>>`" + `) and must compile / parse.

`)

	fmt.Fprintf(&sb, "## Conflict\n\n- Repository: %s\n- PR #%d\n- Rebasing onto: origin/%s (freshly fetched)\n- Head branch: %s\n- Replayed commit: %s — %s\n\n",
		ctx.RepoFullName, ctx.PRNumber, ctx.Onto, ctx.HeadBranch, shortCommit(ctx.Commit.SHA), ctx.Commit.Subject)

	if len(ctx.ConflictedFiles) > 0 {
		sb.WriteString("Conflicted files (git marked these unmerged):\n\n")
		for _, f := range ctx.ConflictedFiles {
			fmt.Fprintf(&sb, "- %s\n", f)
		}
		sb.WriteString("\n")
	}

	writePatternSection(&sb, ctx.Patterns, ctx.MaxPatterns,
		"These patterns are the catalog the project's review/audit tools share. Your conflict resolution MUST stay consistent with them: do not resolve a conflict in a way that would itself be flagged by a pattern below.")

	sb.WriteString(`## What to do

1. For each conflicted file, open it and read the conflict markers. Use git to inspect both sides and the upstream commit that changed them.
2. Produce a resolution that keeps the replayed commit's intent AND the upstream change. Reconcile renamed symbols, changed signatures, removed helpers, and reformatted code — do not regress either side.
3. Verify the resolved files parse / compile where you can run the toolchain locally.
4. Stage every resolved file:

   git add -- <each resolved file>

5. Output a one-paragraph summary of how you reconciled each file.

## Hard rules

- Resolve ONLY the conflicted files listed above. Do NOT touch other files.
- Do NOT run ` + "`git rebase --continue`" + ` — the orchestrator runs it after this session, once the files are staged.
- Do NOT run ` + "`git rebase --abort`, `git commit`, `git push`" + `, or any force-push. Your job ends at ` + "`git add`" + `.
- Leave NO conflict markers in any file.
- NEVER pick one side blindly to make the conflict "go away" — that silently drops a change. If you genuinely cannot reconcile the two sides, STOP and explain rather than guessing.
- NEVER skip pre-commit / CI hooks.
`)

	return sb.String()
}

// BuildRebaseAnalysisPrompt assembles the read-only analysis prompt. It pairs
// the rebased commits with the upstream range that entered the base since the
// PR forked and asks Claude to report, per commit, whether the upstream changes
// invalidate any assumptions — even absent a textual conflict — as structured
// JSON. Exported so --print-prompt can render it without invoking Claude.
func BuildRebaseAnalysisPrompt(ctx rebase.AnalysisContext) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, `You are a Staff Engineer analyzing a freshly rebased pull request branch.

The PR's commits (below) were replayed onto origin/%s. Even where git produced
no textual conflict, an upstream change that entered the base since the PR
forked can silently invalidate a rebased commit: a renamed symbol it still
references, a changed function signature it still calls the old way, a helper it
relies on that was removed, a new lint/format rule it now violates, or a
semantic behavior change it assumed away.

`, ctx.Onto)

	fmt.Fprintf(&sb, "## Repository\n\n- Repository: %s\n- PR #%d\n- Rebased onto: origin/%s\n\n", ctx.RepoFullName, ctx.PRNumber, ctx.Onto)

	sb.WriteString("## Rebased commits (analyze each one, in order)\n\n")
	sb.WriteString(formatAnalysisCommits(ctx.RebasedCommits))
	sb.WriteString("\n## Upstream commits that entered the base since the PR forked\n\n")
	sb.WriteString(formatAnalysisCommits(ctx.UpstreamCommits))
	sb.WriteString("\n")

	writePatternSection(&sb, ctx.Patterns, ctx.MaxPatterns,
		"Ground your analysis in these patterns: a rebased commit that now violates one because of an upstream change is exactly the kind of adjustment to report.")

	sb.WriteString(`## What to do

For EACH rebased commit above:
1. Read what the commit changed (` + "`git show <sha>`" + `) and which symbols, signatures, helpers, and files it depends on.
2. Compare against the upstream commits. Decide whether any upstream change invalidates an assumption in this commit — even with no textual conflict.
3. Report each concrete adjustment the commit needs. If the commit's assumptions still hold, return an empty adjustments array for it.

Open the actual files; do not guess. Report only adjustments you can ground in the diff.

Output ONLY valid JSON matching this exact schema (no markdown fences, no surrounding text):

{
  "commits": [
    {
      "sha": "full SHA of the rebased commit",
      "subject": "the commit subject",
      "adjustments": [
        {
          "kind": "renamed-symbol|changed-signature|removed-helper|lint-rule|semantic-change",
          "file": "path/to/file.go",
          "detail": "what upstream changed and why it affects this commit",
          "action": "the concrete adjustment to make in this commit",
          "upstream_ref": "the upstream commit responsible (sha + subject), or omit",
          "confidence": "verified|likely|uncertain"
        }
      ]
    }
  ],
  "summary": "Overall: how cleanly the rebase landed and the key adjustments needed (2-4 sentences)",
  "recommendation": "Whether the rebased branch is safe to push as-is or needs the adjustments first"
}

Field rules:
- "kind": one of renamed-symbol, changed-signature, removed-helper, lint-rule, semantic-change.
- "upstream_ref" and "confidence" are optional; omit "upstream_ref" when no single upstream commit is responsible.
- Include one "commits" entry for EVERY rebased commit, in order, even when its adjustments array is empty.
- Do NOT invent adjustments. If the upstream range does not affect a commit, its adjustments array is empty.
`)

	return sb.String()
}

// BuildRebaseApplyPrompt assembles the auto-mode prompt that applies the
// post-rebase analysis as fixup commits folded into the commits they belong to.
// It does NOT push. Exported for symmetry and testing.
func BuildRebaseApplyPrompt(ctx rebase.ApplyContext) string {
	var sb strings.Builder

	sb.WriteString(`You are a Staff Engineer applying post-rebase adjustments to a GitHub pull request branch.

`)
	sb.WriteString(baselineBehavioralPrinciples)
	sb.WriteString(`Apply these task-specific thinking patterns on top of the baseline above:
- "Apply only the reported adjustments." — Make exactly the changes the analysis below lists. No drive-by refactors, no unrelated cleanups.
- "Fold each change into the commit it belongs to." — A fix for code a specific commit introduced belongs IN that commit, not in a new commit stacked on top.
- "Verify before folding." — Where you can run the toolchain, confirm the adjusted code compiles / passes before recording the fixup.

`)

	fmt.Fprintf(&sb, "## Pull Request\n\n- Repository: %s\n- PR #%d\n- Rebased onto: origin/%s\n- Head branch: %s\n\n", ctx.RepoFullName, ctx.PRNumber, ctx.Onto, ctx.HeadBranch)

	sb.WriteString("## Adjustments to apply\n\n")
	sb.WriteString(formatApplyAdjustments(ctx.Analysis))
	sb.WriteString("\n")

	writePatternSection(&sb, ctx.Patterns, ctx.MaxPatterns,
		"Keep the adjustments consistent with these patterns.")

	fmt.Fprintf(&sb, `## What to do

1. For each adjustment, open the named file and apply exactly the described change.
2. Verify locally where you can run the toolchain.
3. Fold each change into the commit it belongs to (the branch's own commits are the range origin/%[1]s..HEAD):

   a. List the branch's commits (oldest first):

      git log --oneline --reverse origin/%[1]s..HEAD

   b. For each change, find the commit that introduced the code you are adjusting
      (git blame / git log -p / git log -S<symbol>), stage ONLY that change, and
      record it as a fixup of its target commit:

      git add -- <files for this change>
      git commit --fixup=<target-sha>

   c. Once every change is recorded as a fixup, fold them in non-interactively:

      GIT_SEQUENCE_EDITOR=true git rebase -i --autosquash origin/%[1]s

4. Output a short report listing, per adjustment, which commit it was folded into.

## Hard rules

- Apply ONLY the adjustments listed above.
- Do NOT push. Do NOT force-push. The orchestrator publishes the branch separately, only when --push is given.
- NEVER rebase, reorder, drop, or rewrite commits that already exist on origin/%[1]s — only this branch's own commits (origin/%[1]s..HEAD) may be folded.
- If there is nothing to apply, do NOT create an empty commit; say so and stop.
- NEVER skip pre-commit / CI hooks.
`, ctx.Onto)

	return sb.String()
}

// BuildBareRebasePrompt assembles a portable, self-contained rebase prompt that
// is copy-pasted into a manual Claude session already running inside a checkout
// of the PR head. The session performs the rebase, resolves conflicts
// semantically, and analyzes the rebased commits itself. The pattern catalog is
// inlined so the session needs no access to planwerk-review.
func BuildBareRebasePrompt(ctx rebase.BareContext) string {
	var sb strings.Builder

	sb.WriteString(`You are a Staff Engineer rebasing a GitHub pull request onto its base branch, resolving conflicts semantically, then analyzing the rebased commits.

`)
	sb.WriteString(baselineBehavioralPrinciples)
	sb.WriteString(`Apply these task-specific thinking patterns on top of the baseline above:
- "Resolve conflicts semantically, never a blind side-pick." — Keep both the replayed commit's intent and the upstream change correct.
- "Preserve individual commits." — Replay each commit; do NOT squash. The per-commit analysis depends on it.
- "Analyze even without conflicts." — An upstream change can invalidate a rebased commit (renamed symbol, changed signature, removed helper, new lint rule, semantic change) with no textual conflict. Inspect each rebased commit against the upstream range.

`)

	fmt.Fprintf(&sb, "## Pull Request\n\n- Repository: %s\n- PR #%d\n- Rebase onto: %s\n\n", ctx.RepoFullName, ctx.PRNumber, ctx.Onto)

	if len(ctx.TechTags) > 0 {
		fmt.Fprintf(&sb, "Detected technologies in the target repo (used to filter the pattern catalog below): %s\n\n",
			strings.Join(ctx.TechTags, ", "))
	}

	sb.WriteString("You are already running inside a checkout of this PR's head branch. Do NOT re-checkout, do NOT clone. Operate on the working tree you have.\n\n")

	sb.WriteString(renderBareCatalog(ctx.PatternCatalog, ctx.HasRepoLocalRefs))

	fmt.Fprintf(&sb, `## What to do

1. Fetch the base and replay this branch's commits onto it, preserving individual commits:

   git fetch origin %[1]s
   git rebase origin/%[1]s

2. On each conflict, resolve it semantically (keep both the replayed commit's intent and the upstream change), `+"`git add`"+` the resolved files, then:

   git rebase --continue

   Repeat until the rebase completes. If a conflict cannot be reconciled, STOP and explain — never blind-pick a side.

3. After a clean rebase, analyze each rebased commit (origin/%[1]s..HEAD) against the upstream range (the commits that entered origin/%[1]s since this branch forked — `+"`git log <merge-base>..origin/%[1]s`"+`). For each rebased commit, report whether an upstream change invalidates an assumption, even with no textual conflict.

4. Output the analysis as a per-commit report (commit, then the concrete adjustments it needs, or "no adjustments").

## Hard rules

- Preserve individual commits — do NOT squash.
- Do NOT force-push unless explicitly asked. A rebase rewrites SHAs, so publishing needs `+"`git push --force-with-lease`"+` — only run it if the human asked you to publish.
- NEVER blind-pick a conflict side to make it go away.
- NEVER skip pre-commit / CI hooks.
`, ctx.Onto)

	return sb.String()
}

// writePatternSection appends the shared "Project Review Patterns to Honor"
// block when patterns are present, with a task-specific lead-in.
func writePatternSection(sb *strings.Builder, pats []patterns.Pattern, maxPatterns int, leadIn string) {
	if len(pats) == 0 {
		return
	}
	sb.WriteString("## Project Review Patterns to Honor\n\n")
	sb.WriteString(leadIn)
	sb.WriteString("\n\n<review-patterns>\n")
	sb.WriteString(patterns.FormatGroupedForPrompt(pats, maxPatterns))
	sb.WriteString("</review-patterns>\n\n")
}

// formatAnalysisCommits renders a commit list for the analysis prompt, full SHA
// first so Claude can reference and inspect each one exactly.
func formatAnalysisCommits(commits []github.Commit) string {
	if len(commits) == 0 {
		return "(none)\n"
	}
	var sb strings.Builder
	for _, c := range commits {
		fmt.Fprintf(&sb, "- %s %s\n", c.SHA, c.Subject)
	}
	return sb.String()
}

// formatApplyAdjustments renders the analysis as a per-commit checklist the
// apply session works through.
func formatApplyAdjustments(a report.RebaseAnalysis) string {
	var sb strings.Builder
	any := false
	for _, c := range a.Commits {
		if len(c.Adjustments) == 0 {
			continue
		}
		any = true
		fmt.Fprintf(&sb, "### %s %s\n\n", shortCommit(c.SHA), c.Subject)
		for _, adj := range c.Adjustments {
			fmt.Fprintf(&sb, "- **%s** in `%s` — %s\n  - Action: %s\n", adj.Kind, adj.File, adj.Detail, adj.Action)
			if adj.UpstreamRef != "" {
				fmt.Fprintf(&sb, "  - Upstream: %s\n", adj.UpstreamRef)
			}
		}
		sb.WriteString("\n")
	}
	if !any {
		return "(The analysis reported no adjustments.)\n"
	}
	return sb.String()
}

// shortCommit abbreviates a commit SHA to 7 characters for prompt display.
func shortCommit(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}

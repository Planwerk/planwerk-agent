package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/patterns"
)

// buildReviewPrompt constructs the diff-review prompt: scope, persona, patterns,
// checks, and the JSON output contract the review session emits.
func buildReviewPrompt(ctx ReviewContext) string {
	var sb strings.Builder

	baseBranch := ctx.BaseBranch
	if baseBranch == "" {
		baseBranch = DefaultBaseBranch
	}

	// Review scope: pin the review to the cumulative PR diff so multi-commit PRs
	// are reviewed as a whole instead of just the latest (or first) commit. A
	// diff file the runner wrote replaces the git diff the session would run;
	// the zero ctx.Diff keeps the command (decision 137).
	fmt.Fprintf(&sb, `## Review Scope

Review the FULL pull request diff — every commit between origin/%s and HEAD must be considered together as one cumulative change set.

`, baseBranch)
	gitLog := "`git log origin/" + baseBranch + "..HEAD --oneline`"
	if ctx.Diff.File == "" {
		sb.WriteString("- Run `git diff origin/" + baseBranch + "...HEAD` to see the cumulative diff and " + gitLog + " to enumerate every commit on the branch.\n")
	} else {
		sb.WriteString("- " + diffFileSentence(ctx.Diff) + " Run " + gitLog + " to enumerate every commit on the branch.\n")
		if len(ctx.Diff.Excluded) > 0 {
			sb.WriteString("- " + excludedFilesSentence(ctx.Diff) + "\n")
		}
	}
	sb.WriteString(`- Every added/modified line in any commit on this branch is in scope, regardless of which commit introduced it.
- Do NOT restrict the review to HEAD alone, to the most recent commit, to the first commit, or to the working-tree diff. All commits on the branch are part of this PR.
- When a later commit fixes or supersedes something an earlier commit introduced, judge the final state — do not flag the intermediate state.
- Ignore changes under .planwerk/: they are planning artifacts that are always expected in the diff, so create no findings for them. Two exceptions change how this repository is reviewed and worked on, and are findings in their own right:
  - A change to .planwerk/checklist.md or .planwerk/review_patterns/ changes the rules pull requests are reviewed against. Report it as a needs-discussion WARNING titled "Review Configuration Changed: <file>" and say which rule it adds, drops, or weakens.
  - A change to .claude/ (settings, hooks, skills, commands, agents) changes what AI agent sessions in this repository do. Report it as a needs-discussion WARNING titled "Agent Configuration Changed: <file>" and say what the change makes a session do.

`)
	// The files the diff left out, which the scope bullet above names, are
	// fenced after the list rather than inside it (decision 137).
	if ctx.Diff.File != "" && len(ctx.Diff.Excluded) > 0 {
		sb.WriteString(excludedFilesFence(ctx.Diff))
	}

	// Changed files: the list the session accounts for in files_reviewed,
	// right under the scope it narrows (decision 138).
	sb.WriteString(changedFilesBlock(ctx.ChangedFiles))

	// Staff Engineer persona
	sb.WriteString(`You are a Staff Engineer performing a code review. Apply these thinking patterns:
- "What happens at 10x scale?" — Consider load, data volume, and concurrent users
- "What's the blast radius?" — If this code fails, what else breaks?
- "What happens at 3am?" — Is the error path clear? Will oncall understand the logs?
- "Would a new team member understand this?" — Is the intent clear from the code?
- "Where are the tests?" — Does every new behavior have a test? Can I see the test fail if the code is wrong?
- "Would I find this in the docs?" — If I were a new user/developer, could I discover this feature or API from the documentation?

`)

	// Review patterns (grouped by category: technology, design-principle, project)
	sb.WriteString(reviewPatternsBlock(ctx))

	// Verification of claims — anti-hallucination rules
	sb.WriteString(`## Verification of Claims

These rules exist because a reader acts on what the review claims, and a claim the code does not back misleads them.

- QUOTE-OR-DEMOTE: every finding MUST quote the exact triggering line(s) verbatim from the diff in its code snippet; the Confidence label below says what to do when you cannot. Unverifiable findings are downgraded automatically; fabricating a snippet defeats the gate.
- NEVER say "this is probably tested" — name the specific test file and test function, or flag as "test coverage unknown"
- NEVER say "this is handled elsewhere" — cite the exact file and line that handles it, or say "not verified"
- NEVER say "the caller validates this" — name the caller and the validation, or say "unverified assumption"
- NEVER assume error handling exists unless you can see it in the diff or trace it in the codebase
- If you are not sure an issue is real, report it anyway: state the claim plainly, prefix its problem with "UNVERIFIED:", and set its Confidence to uncertain. An uncertain warning or nit is filed in a separate section and an uncertain blocker goes to a claim check, so reporting one costs the reader little, while dropping a real bug costs a lot.
- When referencing code outside the diff, always prefix with the file path (e.g. "In cmd/main.go:42, ...")

`)

	// Scope Drift Detection
	if ctx.PRTitle != "" || ctx.PRBody != "" || ctx.CommitLog != "" {
		sb.WriteString("## Scope Analysis\n\n")
		var fences []string
		if ctx.PRTitle != "" {
			sb.WriteString(fencedData("pr-title", "", ctx.PRTitle))
			fences = append(fences, "pr-title")
		}
		if ctx.PRBody != "" {
			sb.WriteString(fencedData("pr-body", "", ctx.PRBody))
			fences = append(fences, "pr-body")
		}
		if ctx.CommitLog != "" {
			sb.WriteString(fencedData("commit-log", "", ctx.CommitLog))
			fences = append(fences, "commit-log")
		}
		sb.WriteString("\n")
		sb.WriteString(untrustedDataLine("Use it to judge what the change claims to do and why.", fences...))
		sb.WriteString(`Check:
1. SCOPE CREEP: Are there files changed that seem unrelated to the PR title/description? Also cross-reference with commit messages — do any commits address unrelated concerns? Flag each as WARNING with title "Scope Creep: <file or area>"
2. MISSING REQUIREMENTS: Are there requirements mentioned in the PR description that are NOT addressed in the diff? Flag each as WARNING with title "Missing Requirement: <requirement>"
3. COMMIT COHERENCE: Do the commit messages tell a coherent story? Are there commits that seem to belong to a different PR? Flag as INFO with title "Commit Coherence: <observation>"

`)
	}

	// Review checklist (always provided — checklist.Load() returns embedded default as fallback)
	if ctx.Checklist != "" {
		sb.WriteString(ctx.Checklist)
		sb.WriteString("\n\n")
	}

	// Test & Documentation Verification
	sb.WriteString(`## Test & Documentation Verification

Check also:

### Test Completeness
For every NEW or SIGNIFICANTLY MODIFIED function/method/class in the diff:
1. Identify ALL testing conventions used in this project. Check for:
   - Unit tests: _test.go, test_*.py, *.test.ts, *.spec.ts, __tests__/
   - Integration tests: tests/integration/, test/integration/
   - E2E tests: e2e/, tests/e2e/, chainsaw/, .chainsaw/, chainsaw-test.yaml
   - Kubernetes/Infrastructure E2E: Chainsaw test manifests (chainsaw-test.yaml with apiVersion: chainsaw.kyverno.io), Helm chart tests (tests/), kuttl tests
2. Check whether the PR includes corresponding test additions or modifications FOR EACH test type the project uses
3. If the project already has unit tests, integration tests, or E2E tests, new code must include matching test types — as comprehensively as the project already does.
4. Actively search for test directories: look for chainsaw/, e2e/, tests/, test/ directories in the repository. If they exist and contain tests for similar features, flag missing tests for the new code.
5. If no test exists for new code:
   - If the project has tests elsewhere: flag as WARNING with title "Missing Tests: <function/file>"
   - If the project has E2E tests elsewhere but none for new code: flag as WARNING with title "Missing E2E Tests: <feature/component>"
   - If the project has no test convention at all: flag as INFO with title "No Test Convention Detected"

### Documentation Completeness
For every NEW public API, CLI flag, configuration option, or user-facing behavior change:
1. Check whether the PR modifies documentation files (README, CHANGELOG, doc comments)
2. If new public API is exported but not documented: flag as WARNING with title "Missing Documentation: <item>"
3. If a CLI flag or config option is added without being documented: flag as WARNING with title "Undocumented Flag/Config: <name>"
4. If existing documentation references changed behavior but was not updated: flag as WARNING with title "Stale Documentation: <file>"

`)

	sb.WriteString("### Documentation Structure & Quality\n")
	sb.WriteString("When the diff touches any documentation-like path (`*.md`, `*.rst`, `*.adoc`, `docs/**`, `README*`, `CHANGELOG*`) OR consists of comment-only changes (godoc, docstrings, JSDoc, rustdoc), apply the `Documentation Structure (Diátaxis)` review pattern in addition to the completeness checks above:\n\n")
	sb.WriteString("1. Identify each changed page's intended Diátaxis mode (Tutorial / How-To / Reference / Explanation) from its location (`docs/tutorials/`, `docs/how-to/`, `docs/reference/`, `docs/explanation/`), title, or content. Flag any section that drifts into a different mode as WARNING with title \"Diátaxis Drift: <page> mixes <claimed-mode> with <actual-mode>\".\n")
	sb.WriteString("2. Comment-only diffs are in scope. A doc-comment that paraphrases the code (WHAT instead of WHY) is a finding even when no code line moved — flag as INFO with title \"Comment Restates Code: <file>:<line>\".\n")
	sb.WriteString("3. For fenced code blocks in docs, verify they still match the current API/CLI/config. Flag mismatches as CRITICAL with title \"Stale Doc Example: <file>:<approx line>\".\n")
	sb.WriteString("4. Removed or renamed public APIs, CLI flags, or config keys MUST carry an explicit deprecation block with a migration path. Flag missing deprecation as WARNING with title \"Missing Deprecation Notice: <name>\".\n")
	sb.WriteString("5. Flag terminology drift (one concept named two ways across the docs touched in this PR) as INFO with title \"Terminology Drift: <term-A>/<term-B>\".\n\n")

	// Dependency Freshness & Maintenance Verification
	sb.WriteString(`## Dependency Freshness & Maintenance Verification

When the diff introduces a new dependency, check its freshness and maintenance status. Look each fact up with WebSearch or WebFetch (the package registry, the project's releases page, or its repository) instead of answering from memory: your training data is months behind the current releases, and a stale "latest version" is a false finding. Put the URL you checked in the finding; if the lookup fails, say so and set its Confidence to uncertain.

### What counts as a new dependency
- A new entry in go.mod, requirements.txt, pyproject.toml, package.json, Cargo.toml, pom.xml, build.gradle, Gemfile
- A new GitHub Action (uses: owner/action@version) in workflow YAML files
- A new container image (FROM image:tag or image references in Kubernetes manifests, docker-compose, Helm values)
- A new Helm chart dependency in Chart.yaml
- Any other external dependency introduced for the first time

### What to check for each new dependency
1. **Version Currency**: A newer patch or minor release is not a finding; a newer major release is. If the diff pins an old major version (e.g. v1.2 when v3.1 is current), flag it.
2. **Active Maintenance**: Does the project show signs of active maintenance (recent commits, releases within the last 12 months, responsive issue tracker)? If the repository is archived, abandoned, or has had no activity for over a year, flag it.
3. **Deprecation Status**: Has the project been officially deprecated or superseded by a replacement? Check for deprecation notices in the repository README, GitHub archive status, or well-known replacements (e.g. actions/create-release is deprecated in favor of softprops/action-gh-release).

### Severity guidance
These apply the Severity Ladder to dependencies, and the ladder governs where they differ: a deprecated or unmaintained dependency is CRITICAL because it receives no fixes, so a defect in it is one the project cannot patch; an outdated version is WARNING because the fixes it lacks sit on paths the project may not use.
- Using a deprecated dependency: flag as CRITICAL with title "Deprecated Dependency: <name>"
- Using an unmaintained dependency (archived/abandoned): flag as CRITICAL with title "Unmaintained Dependency: <name>"
- Using a significantly outdated version when a current version exists: flag as WARNING with title "Outdated Dependency: <name> uses <version>, latest is <latest>"
- Include the recommended replacement or current version in the suggested fix.

`)

	// New feature documentation hints
	if len(ctx.NewFeatures) > 0 {
		sb.WriteString("## New Feature Documentation Hints\n\n")
		sb.WriteString("The following new files were added in this PR and may need documentation:\n\n")
		for _, nf := range ctx.NewFeatures {
			fmt.Fprintf(&sb, "- %s (%s)\n", nf.File, nf.Description)
		}
		sb.WriteString("\nCheck whether these additions are reflected in README or other documentation. Flag as WARNING with title \"Missing Documentation: <file>\" if documentation is missing for user-facing additions.\n\n")
	}

	// TODO cross-reference
	if ctx.TodoContent != "" {
		sb.WriteString("## TODO Cross-Reference\n\n")
		sb.WriteString("The project has a TODOS.md file. Review the PR against these open items:\n\n")
		sb.WriteString(fencedData("todos-content", "", ctx.TodoContent))
		sb.WriteString("\n")
		sb.WriteString("Check:\n")
		sb.WriteString("1. Does this PR complete any TODO items? If so, flag as INFO with title \"TODO Completed: <item>\"\n")
		sb.WriteString("2. Does this PR introduce work that should be tracked as a TODO? If so, flag as INFO with title \"New TODO Needed: <description>\"\n\n")
	}

	// Documentation staleness hints
	if len(ctx.StaleDocs) > 0 {
		sb.WriteString("## Documentation Staleness Hints\n\n")
		sb.WriteString("The following documentation files may need updating based on code changes in this PR:\n\n")
		for _, doc := range ctx.StaleDocs {
			fmt.Fprintf(&sb, "- %s references code in %s which was modified\n", doc.DocFile, strings.Join(doc.RelatedDirs, ", "))
		}
		sb.WriteString("\nFlag a hint whose documentation is actually outdated as WARNING with title \"Stale Documentation: <file>\", as the Documentation Completeness rule above says. A hint whose documentation turns out to be current gets no finding.\n\n")
	}

	// False-positive suppressions (shared with audit via suppressionsBlock)
	sb.WriteString(suppressionsBlock(scopeDiff))

	// Anti-sycophancy rules (communicationStyleBlock, shared with every finder)
	sb.WriteString(communicationStyleBlock())
	sb.WriteString(outputLanguageBlock())

	// Domain glossary (no-op when the repo commits none)
	sb.WriteString(domainGlossaryBlock(ctx.Glossary))

	// Project memory from the repo's GitHub Wiki (no-op when the wiki carries
	// no memory pages)
	sb.WriteString(projectMemoryBlock(ctx.Memory))
	sb.WriteString(brainSearchBlock(ctx.Brain))

	// Finding enrichment for machine processing
	sb.WriteString(`## Finding Enrichment

Every finding you report includes:

1. **Code Snippet**: Quote the exact 3-5 lines of problematic code, verbatim and without line-number prefixes (line numbers belong in the location and the Line Range). If the issue spans multiple lines, include all affected lines. For a missing test or missing documentation, quote the added declaration that needs it.

2. **Suggested Fix**: For auto-fix findings, provide the EXACT replacement code that resolves the issue. The fix code MUST:
   - Use the exact indentation from the original file
   - Contain NO markdown fences, NO comments, NO explanations — pure replacement code only
   - Be directly copy-paste ready with no placeholders or "..." elisions
   For needs-discussion and architectural findings, describe the fix approach concretely.

3. **Line Range**: When a finding affects multiple lines, specify both the start line and end line.

4. **Related Findings**: If two or more findings are connected (e.g., a missing nil check and a missing test for that nil check), note the relationship by referencing the other finding's title.

5. **Fix Options** (REQUIRED for needs-discussion and architectural findings; OMIT for auto-fix):
   Provide 2-3 alternative approaches (label them A, B, C). For each option supply: ` + "`approach`" + ` (one sentence), ` + "`pros`" + `, ` + "`cons`" + `, ` + "`effort`" + ` (LOW | MED | HIGH), and ` + "`risk_if_skipped`" + ` (what happens if this option is NOT chosen).
   Then weigh the options in ` + "`recommendation_reasoning`" + ` (1-2 sentences referencing codebase patterns, the relevant review-pattern source, or project constraints) and name the ONE option it favors in ` + "`recommended_option`" + ` (the chosen option's id).

`)

	sb.WriteString(severityLadderBlock(scopeDiff))
	sb.WriteString(findingLabelsBlock())

	// Finding limit
	sb.WriteString(findingBudgetBlock(ctx.MaxFindings))

	// Review Summary instructions
	sb.WriteString(`## Review Summary

In the ` + "`summary`" + ` field, write a brief overall summary (2-4 sentences) that:
1. Highlights the most important issues found
2. Gives an overall assessment of the PR quality
Be direct about problems and say nothing about what is fine.

In the ` + "`recommendation`" + ` field, write ONE recommendation in exactly this form, with no ` + "`Recommendation:`" + ` prefix:

<merge | merge after fixes | do not merge> because <name the single most important finding by its title and state the specific reason it drives the decision>.

The reason MUST name a specific finding and what it breaks. Generic justifications — "because it's safer", "to improve quality", "follows best practice", "because it's cleaner" — are not acceptable; if no finding must be fixed before merge, recommend merge.

A zero-finding review is a valid outcome: when the diff is clean after the full checklist, set ` + "`findings`" + ` to ` + "`[]`" + `, say the PR is clean in the summary, and recommend ` + "`merge because the review found no issues`" + `, the one form of the recommendation that names no finding; do not invent one to appear productive.

`)

	sb.WriteString(findingsOutputBlock())

	return sb.String()
}

// reviewPatternsLeadIn opens the review prompt's pattern section; the caller
// adds the colon, or a sentence and then the colon.
const reviewPatternsLeadIn = "Apply these review patterns grouped by category. Flag violations in your review, and name the pattern (its \"Pattern:\" title) in any finding that applies one"

// reviewPatternsBlock renders the review prompt's pattern section, "" when
// ctx.Patterns is empty. With a ctx.Scope (review --scope-patterns) it carries
// the bodies of Scope.Inline, with `<patterns for="…">` blocks for the patterns
// a changed file matched, which the lead-in introduces only when the
// --max-patterns truncation keeps one, and lists Scope.Rest as the index of
// ctx.Catalog.Only(Scope.Rest), whose files the session reads from
// ctx.Catalog.Dir. Without a Scope, and when Scope.Rest has no index line
// because no catalog was written (a failed write, a printed prompt), it
// carries every body of ctx.Patterns, the section an unscoped review carries
// (decision 141).
func reviewPatternsBlock(ctx ReviewContext) string {
	if len(ctx.Patterns) == 0 {
		return ""
	}
	s := ctx.Scope
	var index string
	if s != nil && len(s.Rest) > 0 {
		index = patterns.FormatCatalogIndex(ctx.Catalog.Only(s.Rest), ctx.MaxPatterns)
	}
	if s == nil || (len(s.Rest) > 0 && index == "") {
		return reviewPatternsLeadIn + ":\n\n<review-patterns>\n" +
			patterns.FormatGroupedForPrompt(ctx.Patterns, ctx.MaxPatterns) +
			"</review-patterns>\n\n"
	}
	var sb strings.Builder
	if len(s.Inline) > 0 {
		bodies, fileBlocks := patterns.FormatScopedForPrompt(*s, ctx.MaxPatterns)
		sb.WriteString(reviewPatternsLeadIn)
		if fileBlocks {
			sb.WriteString(". A <patterns for=\"…\"> block holds the patterns chosen by technology or path for the changed files it lists: apply them to those files. A pattern outside such a block applies to every changed file")
		}
		sb.WriteString(":\n\n<review-patterns>\n" + bodies + "</review-patterns>\n\n")
	}
	if index != "" {
		sb.WriteString("The patterns indexed below match no changed file by technology or path, so this prompt lists them instead of carrying their bodies. " +
			patternCatalogDirLine(ctx.Catalog.Dir, catalogFinder) +
			"\n\n<review-patterns-index>\n" + index + "</review-patterns-index>\n\n")
	}
	return sb.String()
}

// changedFilesBlock renders the review prompt's Changed Files section: one line
// per file in a <changed-files> fence (changedFileLine), the line framing the
// paths as data, and the completion criterion that every listed file gets its
// own pass and one files_reviewed entry, reviewed or skipped with a reason. It
// returns "" for no files. The list is the denominator hygiene.CheckFileLedger
// checks the session's account against, and it is handed to the session
// rather than left for it to look up because the check needs the session to
// answer for exactly the files Go counts (decision 138). Only the review lists
// files, so the block lives here rather than in components.go.
func changedFilesBlock(files []github.FileChange) string {
	if len(files) == 0 {
		return ""
	}
	lines := make([]string, len(files))
	for i, f := range files {
		lines[i] = changedFileLine(f)
	}
	return "## Changed Files\n\n" +
		fencedData("changed-files", "", strings.Join(lines, "\n")) + "\n" +
		untrustedDataLine("Use it as the list of files this review accounts for.", "changed-files") +
		"These are the files this pull request changes, each with how it changed and its added and removed line counts. Give every listed file its own pass over its diff, whatever its size or role: a test, a document, a configuration file, a deleted file, and the smaller half of a pair (an interface beside its implementation, a schema beside the code that reads it) each get one, because a defect there ships as surely as one in the main file. Account for each listed file once in `files_reviewed`, by its path exactly as listed: `reviewed` once you have read its diff, or `skipped` with the reason you did not read it. The report names every file you skip or leave out of `files_reviewed` as not reviewed, with your reason.\n\n"
}

// changedFileLine renders one file of the Changed Files list as
// "<path> (<how>, +<added> -<deleted>)", where how is its status, or "renamed
// from <old>" or "copied from <old>"; a binary file shows "binary" in place
// of the counts.
func changedFileLine(f github.FileChange) string {
	how := f.Status
	switch f.Status {
	case github.FileRenamed, github.FileCopied:
		how = f.Status + " from " + f.OldPath
	}
	if f.Binary {
		return fmt.Sprintf("%s (%s, binary)", f.Path, how)
	}
	return fmt.Sprintf("%s (%s, +%d -%d)", f.Path, how, f.Added, f.Deleted)
}

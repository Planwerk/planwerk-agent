package claude

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/reviewprepared"
)

// ReviewPrepared analyzes a batch of prepared Planwerk feature specs and
// returns structured findings + (optionally) a rewritten JSON per file.
//
// It runs two Claude calls:
//  1. Free-form review of every prepared spec, with quality criteria.
//  2. Structuring pass that converts the review into the JSON shape the
//     reviewprepared.Result expects, INCLUDING the rewritten feature JSON
//     when ctx.IncludeImproved is set.
func ReviewPrepared(dir string, ctx reviewprepared.AnalysisContext) (*reviewprepared.Result, error) {
	rawAnalysis, err := runClaude(dir, buildReviewPreparedPrompt(ctx), "review-prepared")
	if err != nil {
		return nil, fmt.Errorf("running review-prepared analysis: %w", err)
	}

	result, err := structureReviewPreparedResult(rawAnalysis, ctx)
	if err != nil {
		return nil, fmt.Errorf("structuring review-prepared output: %w", err)
	}
	return result, nil
}

// buildReviewPreparedPrompt asks Claude to review every prepared spec for
// completeness, traceability, and concreteness — and, when requested, to
// emit a rewritten JSON for each file.
func buildReviewPreparedPrompt(ctx reviewprepared.AnalysisContext) string {
	var sb strings.Builder

	sb.WriteString(`You are a Staff Engineer reviewing Planwerk feature specifications that are about to enter implementation. Your ONLY job is to improve the SPEC TEXT itself — clarity, verifiability, completeness, internal traceability. You are NOT performing a gap analysis: do NOT compare the spec to the codebase, do NOT grep for missing implementations, do NOT flag anything that is "not yet built". Implementation has not started; the code is irrelevant to this review except as background context for choosing realistic file paths and existing patterns.

Apply these thinking patterns:
- "Could a new engineer build this from the spec alone?" — if the answer is no, the spec needs improvement
- "Every SHALL must have a TestSpecification declared in the spec" — judged purely against the spec text
- "Every story criterion must be verifiable as written" — vague verbs (handle, support, work) are findings
- "Implementation_notes is a contract, not prose" — concrete file paths, function names, pitfalls; quality is judged by the spec, not by checking whether they exist yet
- "Internal consistency over external truth" — every cross-reference (REQ-IDs in stories/tasks/tests) must resolve INSIDE this spec; do not look outside

`)

	if ctx.RepoName != "" {
		fmt.Fprintf(&sb, "Repository: %s\n\n", ctx.RepoName)
	}

	if len(ctx.Patterns) > 0 {
		sb.WriteString("## Review Patterns (context, not the focus)\n\n")
		sb.WriteString("These are the project's review patterns. Use them as a sanity lens to judge whether the SPEC anticipates them — for example, if a pattern says 'every public API needs docs' and the spec lacks a docs task, that's a finding. Do NOT check whether the patterns are followed in the existing codebase; that is not this command's scope.\n\n")
		sb.WriteString("<review-patterns>\n")
		sb.WriteString(patterns.FormatGroupedForPrompt(ctx.Patterns, ctx.MaxPatterns))
		sb.WriteString("</review-patterns>\n\n")
	}

	sb.WriteString("## Prepared Feature Specifications to Review\n\n")
	sb.WriteString(`Each block below is a feature whose Planwerk file declares status="prepared". The team has finished drafting it; the next step is implementation. Your job is to find ANY weakness in the spec TEXT that would make implementation slower, more ambiguous, or more error-prone.

This review covers ONLY the spec content. Whether the described behaviour is implemented in the repository is irrelevant — for "prepared" status it almost never is, and that's expected. Findings like "this endpoint does not exist in the code" or "no test file matches yet" are out of scope and must not be reported.

For each feature you receive BOTH a structured rendering (the same prompt format the implementer would see) AND the raw JSON. Use the raw JSON to identify exact JSON pointers for findings.

`)

	for _, pf := range ctx.Features {
		base := filepath.Base(pf.Feature.FilePath)
		fmt.Fprintf(&sb, "<feature id=%q file=%q>\n", pf.Feature.FeatureID, base)
		sb.WriteString(pf.Feature.FormatForPrompt())
		sb.WriteString("\n<raw-json>\n")
		sb.Write(pf.Raw)
		sb.WriteString("\n</raw-json>\n")
		fmt.Fprintf(&sb, "</feature>\n\n")
	}

	sb.WriteString(`## What to look for

Walk EVERY feature through these seven categories. Each category produces one finding per issue — never merge unrelated weaknesses into a single finding. EVERY judgment is made against the SPEC TEXT, never against the codebase.

1. **stories** — Every story has a clear role, want, so_that. Each acceptance criterion is verifiable as written (asserts a concrete output, status code, side-effect, or measurable property). Vague verbs ("handle", "support", "work correctly") without a measurable assertion are findings. Missing edge cases the spec itself implies (failure modes, authorisation, idempotency) are findings.
2. **requirements** — Each requirement has an ID, priority, rationale, and at least one Scenario. Priority must map to a known severity vocabulary (must/should/could). Scenarios use When/Then/AndThen with concrete inputs and outputs. Requirements that are not referenced by any story criterion are findings (this is internal traceability, judged inside the spec).
3. **tasks** — Tasks are ordered, sized for a single PR, and each carries a list of Requirements they fulfil. A task whose description is too coarse ("Implement endpoint") is a finding. Tasks must cover every requirement; missing tasks for documented behaviour are findings.
4. **tests** — Every requirement has at least one TestSpecification declared in the spec. Every TestSpecification cites a concrete test_file and test_function and explains the expected assertion. Do NOT verify whether those test files actually exist on disk — that is gap-analysis territory; judge purely whether the spec is internally complete and specific.
5. **review_criteria** — review_criteria covers every SHALL requirement and every error path the spec mentions. Missing review criteria for documented invariants are findings.
6. **implementation_notes** — implementation_notes carries concrete file paths, package layouts, and at least one pattern reference per major architectural decision. Hand-wavy phrases ("similar to existing code", "TBD") are findings. Concreteness is judged from the spec wording; do NOT verify whether the referenced files exist.
7. **other** — Cross-cutting issues that don't fit a single category: empty summary, ambiguous slug, contradictions between description and stories, broken internal cross-references (a story criterion that cites REQ-XYZ which is not in requirements[]; a TestSpecification.requirement_id missing from requirements[]; a task.requirements naming a missing requirement ID).

For each finding, include:
- "category" from the list above
- "severity" — CRITICAL for blocking ambiguity, WARNING for issues that will slow implementation, INFO for polish; never BLOCKING (a prepared spec is not a runtime artifact).
- "title" — short, no severity prefix, no [LEVEL] tags
- "description" — what's wrong and why it matters
- "suggestion" — concrete, copy-paste-ready edit text. If you propose new criterion text, write the exact wording.
- "spec_pointer" — JSON-pointer-ish path inside the file, e.g. "stories[2].criteria[1]" or "requirements[REQ-005].scenarios[0]". This MUST be precise enough that a maintainer can find the exact node.
- "confidence" — verified|likely|uncertain. "verified" means you read the exact spec text that supports the finding.

## Out of scope (do NOT report)
- "This requirement is not yet implemented in the code" — expected for prepared specs; this command does not check implementation.
- "The referenced file does not exist on disk" — gap-analysis concern, not ours.
- "No test function matches this TestSpecification yet" — expected; judge spec completeness only.
- "Pattern XYZ is violated by the existing code" — review-patterns are used here only to judge whether the spec anticipates them.
- Code-quality findings (smells, formatting, complexity) — there is no code to review.

## Severity guidance
- CRITICAL: a SHALL with no TestSpecification declared; a story criterion that cannot be verified at all; a broken internal cross-reference (REQ-ID, task.requirements) that would derail implementation.
- WARNING: a vague criterion; a missing edge case; a thin implementation_notes block.
- INFO: a polish issue (typo, missing summary, ambiguous priority text).

## Verification rules (mandatory)
- Every finding MUST cite the exact spec_pointer and a 1-2 sentence description.
- The spec is the only source of truth — do NOT speculate about what the codebase does.
- If a feature is in great shape, emit it with an empty findings array and a positive 1-sentence summary.

`)

	if ctx.IncludeImproved {
		sb.WriteString(`## Improved JSON (REQUIRED for every feature)

For EVERY feature you receive, emit a complete rewritten feature JSON that incorporates ALL findings of severity WARNING or higher (incorporate INFO findings when they don't conflict).

Rules for the rewrite:
- The rewrite MUST be valid JSON and MUST keep the existing top-level keys (feature_id, title, slug, status, phase, summary, description, stories, requirements, tasks, test_specifications, affected_files, similar_patterns, review_criteria, implementation_notes, status_history, execution_history). Preserve any field whose value you are not improving.
- Do NOT change feature_id, slug, status, status_history, execution_history. These are lifecycle metadata.
- Preserve every story/requirement/task/test_specification that is fundamentally sound; rewrite only the parts that produced findings. Keep IDs stable so existing references survive.
- When you ADD a new acceptance criterion, scenario, task, or TestSpecification, give it the same shape as the existing entries.
- Do NOT invent file paths, function names, or feature IDs that are not already in the spec. (Reuse existing references; do not introduce new ones from outside the spec.)
- Do NOT add new tasks or TestSpecifications that imply implementation work outside what the spec already declares — your job is to clarify and complete the spec, not to expand its scope.
- The rewritten JSON must round-trip through json.Unmarshal into the project's planwerk.Feature struct without errors.

`)
	}

	sb.WriteString(`## Output format

When you are done, emit a comprehensive review grouped by feature_id. For every feature provide:
- Per-finding entries with the fields described above.
- A 1-3 sentence "summary" of the spec's overall state.
`)
	if ctx.IncludeImproved {
		sb.WriteString("- The full rewritten feature JSON.\n")
	}
	sb.WriteString("\nNow perform the review.\n")

	return sb.String()
}

// structureReviewPreparedResult turns the free-form review into the strict
// JSON shape that reviewprepared.Result expects.
func structureReviewPreparedResult(rawAnalysis string, ctx reviewprepared.AnalysisContext) (*reviewprepared.Result, error) {
	text, err := runClaude("", buildReviewPreparedStructurePrompt(rawAnalysis, ctx.IncludeImproved), "review-prepared-structure")
	if err != nil {
		return nil, err
	}
	text = stripMarkdownFences(text)

	var result reviewprepared.Result
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		repair, repairErr := runClaude("", buildRepairPrompt(text, err), "review-prepared-repair")
		if repairErr != nil {
			return nil, fmt.Errorf("parsing structured review-prepared as JSON: %w\nraw output:\n%s", err, text)
		}
		repair = stripMarkdownFences(repair)
		if err := json.Unmarshal([]byte(repair), &result); err != nil {
			return nil, fmt.Errorf("parsing structured review-prepared after repair: %w\nraw output:\n%s", err, repair)
		}
	}

	reconcileReviewFeatures(&result, ctx.Features)
	return &result, nil
}

// reconcileReviewFeatures fills in feature_file/title on every finding and
// ensures the result contains an entry for every input feature even when the
// model dropped a clean one. Mirrors gapanalysis.reconcileFeatures.
func reconcileReviewFeatures(result *reviewprepared.Result, features []reviewprepared.PreparedFeature) {
	byID := make(map[string]reviewprepared.PreparedFeature, len(features))
	for _, pf := range features {
		byID[strings.ToUpper(pf.Feature.FeatureID)] = pf
	}

	seen := make(map[string]bool, len(result.Features))
	for fi := range result.Features {
		fr := &result.Features[fi]
		key := strings.ToUpper(fr.FeatureID)
		seen[key] = true
		if pf, ok := byID[key]; ok {
			if fr.FeatureFile == "" {
				fr.FeatureFile = filepath.Base(pf.Feature.FilePath)
			}
			if fr.Title == "" {
				fr.Title = pf.Feature.Title
			}
		}
		for ji := range fr.Findings {
			f := &fr.Findings[ji]
			if f.FeatureID == "" {
				f.FeatureID = fr.FeatureID
			}
			if f.FeatureFile == "" {
				f.FeatureFile = fr.FeatureFile
			}
		}
	}

	for _, pf := range features {
		if seen[strings.ToUpper(pf.Feature.FeatureID)] {
			continue
		}
		result.Features = append(result.Features, reviewprepared.FeatureReview{
			FeatureID:   pf.Feature.FeatureID,
			FeatureFile: filepath.Base(pf.Feature.FilePath),
			Title:       pf.Feature.Title,
			Summary:     "No findings reported by the model. Spec appears ready for implementation.",
		})
	}
}

func buildReviewPreparedStructurePrompt(rawAnalysis string, includeImproved bool) string {
	improvedField := ""
	if includeImproved {
		improvedField = `
      "improved_json": { "...": "the full rewritten feature JSON content as a JSON OBJECT (not a string). Required when present in the analysis. Preserve every top-level key from the original." },`
	}
	return `Convert the following review-prepared report into structured JSON. Extract every finding, grouped by feature_id.

Output ONLY valid JSON matching this exact schema (no markdown fences, no surrounding text):

{
  "repo": "owner/name",
  "overview": "2-4 sentence overall summary: how many features were reviewed, headline themes, and the 1-2 most important findings.",
  "features": [
    {
      "feature_id": "PX-0028",
      "feature_file": "PX-0028-...json",
      "title": "Feature title from the spec",
      "summary": "1-3 sentence verdict for this specific feature.",` + improvedField + `
      "findings": [
        {
          "id": "",
          "feature_id": "PX-0028",
          "feature_file": "PX-0028-...json",
          "category": "stories|requirements|tasks|tests|review_criteria|implementation_notes|other",
          "severity": "CRITICAL|WARNING|INFO",
          "title": "Short, specific finding title (no severity prefix, no brackets)",
          "description": "What is wrong and why it matters. 1-3 sentences.",
          "suggestion": "Concrete edit text. Copy-paste-ready when possible.",
          "spec_pointer": "JSON-pointer-ish path inside the spec, e.g. stories[2].criteria[1] or requirements[REQ-005].scenarios[0]",
          "confidence": "verified|likely|uncertain"
        }
      ]
    }
  ]
}

Field rules:
- "id": leave as empty string — it is assigned automatically.
- "category": exactly one of the values above.
- "severity": uppercase, never BLOCKING.
- "feature_id" / "feature_file": copy from the surrounding feature block.
- "spec_pointer": MUST point to a real path in the original JSON. If the finding is about a missing field, point to the parent (e.g. "stories[1]" for a story missing criteria).
- "suggestion": describe the change as a concrete edit. When proposing new text, write the exact wording.
- If a feature has no findings, include it with an empty findings array and a positive summary.` +
		func() string {
			if includeImproved {
				return `
- "improved_json": ALWAYS include for every feature — emit the full rewritten feature JSON as a nested JSON object (NOT a string). Preserve feature_id, slug, status, status_history, execution_history exactly as the input.`
			}
			return `
- Do NOT include an "improved_json" field — it is not requested for this run.`
		}() + `

<review-report>
` + rawAnalysis + `
</review-report>`
}

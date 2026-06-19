package claude

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/planwerk/planwerk-review/internal/gapanalysis"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/planwerk"
)

// GapAnalysis compares completed Planwerk feature files against the cloned
// repo and emits a structured Result with one bucket per feature.
//
// It runs two Claude calls:
//  1. Analyze the spec vs. the code and produce a free-form gap report.
//  2. Structure that report into JSON matching gapanalysis.Result.
func (c *Client) GapAnalysis(dir string, ctx gapanalysis.AnalysisContext) (*gapanalysis.Result, error) {
	rawAnalysis, err := c.runClaude(dir, buildGapAnalysisPrompt(ctx), "gap-analysis")
	if err != nil {
		return nil, fmt.Errorf("running gap analysis: %w", err)
	}

	result, err := c.structureGapResult(rawAnalysis, ctx)
	if err != nil {
		return nil, fmt.Errorf("structuring gap-analysis output: %w", err)
	}
	return result, nil
}

// buildGapAnalysisPrompt is the prompt that asks Claude to compare the spec
// to the code. It mirrors the audit prompt's persona and verification rules
// so the output style stays consistent across commands.
func buildGapAnalysisPrompt(ctx gapanalysis.AnalysisContext) string {
	var sb strings.Builder

	sb.WriteString(`You are a Staff Engineer performing a gap analysis between a project's Planwerk feature specifications and the actual repository state. Apply these thinking patterns:
- "Trust nothing without grep" — verify every claim against the real code
- "Spec said done, code says what?" — your job is to surface discrepancies
- "Where are the tests?" — a feature without tests is partially done
- "If a behavior is missing from the docs, users won't find it" — completeness includes documentation

`)

	if ctx.RepoName != "" {
		fmt.Fprintf(&sb, "Repository: %s\n\n", ctx.RepoName)
	}

	if len(ctx.Patterns) > 0 {
		sb.WriteString("## Review Patterns (context, not the focus)\n\n")
		sb.WriteString("These are the project's review patterns. Use them as a sanity lens, but the PRIMARY input is the feature specs below. A gap is a spec-vs-code discrepancy, not a generic pattern violation.\n\n")
		sb.WriteString("<review-patterns>\n")
		sb.WriteString(patterns.FormatGroupedForPrompt(ctx.Patterns, ctx.MaxPatterns))
		sb.WriteString("</review-patterns>\n\n")
	}

	// Spec injection — every feature is included verbatim. Each block carries
	// the feature_id, the feature_file basename (the JSON file under
	// .planwerk/completed/) and the rendered Planwerk prompt block. The
	// model emits gaps grouped by feature_id.
	sb.WriteString("## Completed Feature Specifications to Audit\n\n")
	sb.WriteString(`Each block below is a feature whose Planwerk file declares status="completed". The team considers these features done. Your job is to find ANY part of each spec that is NOT actually implemented in the current codebase.

`)
	for _, f := range ctx.Features {
		base := filepath.Base(f.FilePath)
		fmt.Fprintf(&sb, "<feature id=%q file=%q>\n", f.FeatureID, base)
		sb.WriteString(f.FormatForPrompt())
		fmt.Fprintf(&sb, "</feature>\n\n")
	}

	sb.WriteString(`## What counts as a gap

For EACH feature, walk these four checks. Every check produces a separate gap_type — never merge them.

1. **missing_criterion** — for every story acceptance criterion, search the codebase for the behavior it describes. If you cannot find code that implements it, file a gap. Cite the criterion verbatim in "source".
2. **missing_scenario** — for every requirement scenario (When / Then / And then), check whether the described behavior exists. If the production path or the matching test is absent, file a gap. Cite the scenario in "source".
3. **missing_test** — for every TestSpecification, run a literal grep for the test_function in the test_file. If the test does not exist (or exists but does not assert what "expected" describes), file a gap. Cite test_file/test_function in "source".
4. **missing_task** — for every task with status="completed", verify the task description is reflected in the code (a file/function/migration/CLI flag named in the task). If the task description has no visible counterpart, file a gap. Cite task ID and title in "source".

## Severity mapping

- A gap inherits the requirement's priority when one is mapped:
  - critical / must / blocker → CRITICAL
  - high / should → WARNING
  - medium / could → WARNING
  - low / nice-to-have → INFO
- Default to WARNING when no priority is stated.
- A missing TEST for a critical requirement is at most WARNING — it is a documentation/coverage hole, not a runtime bug.
- A missing CRITERION or SCENARIO that implies missing PRODUCTION code can escalate to CRITICAL when a critical priority is mapped.
- Never use BLOCKING — a gap on already-merged "completed" work is by definition not blocking new merges.

## Verification rules (mandatory)

- Every gap MUST cite concrete evidence (a path you grepped, a function you searched for, a config you read). If you cannot cite evidence, do NOT report the gap.
- "I assume X is implemented elsewhere" is NOT acceptable — set confidence: "uncertain" and prefix the description with "UNVERIFIED:" in those cases.
- A test that calls a function but does not assert the expected behavior counts as missing_test, not missing_criterion. Pick the gap_type that matches what is missing.
- If a feature is FULLY implemented, emit it in the result with an empty gaps array and a one-sentence summary confirming completeness.
- Do not invent new types of gaps beyond the four listed above.

## Suggested issue

For EVERY gap, propose a GitHub issue:

- "suggested_issue.title": short, specific, no severity prefix, no [LEVEL] tags. Example: "Implement <criterion> for CC-0042" or "Add missing test <test_function> for CC-0042".
- "suggested_issue.body": Markdown that a maintainer could post unchanged. Include the spec source, what's missing, the evidence you collected, and a concrete next step.

Severity must NEVER appear in the title or as a label — keep it inside the body only (this matches the project's existing convention).

`)

	sb.WriteString(proseStyleBlock())
	sb.WriteString(outputLanguageBlock())

	sb.WriteString(`## Output format

When you are done, emit a comprehensive gap report grouped by feature_id, with each gap's gap_type, severity, title, source (the verbatim spec snippet), description (what is missing), evidence (where you looked), confidence, and a suggested_issue with title and body.

Now perform the gap analysis.
`)

	return sb.String()
}

// structureGapResult takes the free-form gap report and asks Claude to emit a
// strict JSON shape matching gapanalysis.Result. The features array is then
// reconciled against the input feature list so the result has one entry per
// audited feature even if the model dropped a fully-implemented feature.
func (c *Client) structureGapResult(rawAnalysis string, ctx gapanalysis.AnalysisContext) (*gapanalysis.Result, error) {
	text, err := c.runClaude("", buildGapStructurePrompt(rawAnalysis), "gap-structure")
	if err != nil {
		return nil, err
	}
	var result gapanalysis.Result
	if err := c.decodeJSONWithRepair(text, "structured gap-analysis", &result); err != nil {
		return nil, err
	}
	reconcileFeatures(&result, ctx.Features)
	return &result, nil
}

// reconcileFeatures fills in the file basename and feature title on every
// gap so renderers and dedupe always have a stable handle, and ensures the
// result contains an entry for every input feature (even fully-implemented
// ones, so the report shows what was checked).
func reconcileFeatures(result *gapanalysis.Result, features []*planwerk.Feature) {
	byID := make(map[string]*planwerk.Feature, len(features))
	for _, f := range features {
		byID[strings.ToUpper(f.FeatureID)] = f
	}

	seen := make(map[string]bool, len(result.Features))
	for fi := range result.Features {
		fg := &result.Features[fi]
		key := strings.ToUpper(fg.FeatureID)
		seen[key] = true
		spec := byID[key]
		if spec != nil {
			if fg.FeatureFile == "" {
				fg.FeatureFile = filepath.Base(spec.FilePath)
			}
			if fg.Title == "" {
				fg.Title = spec.Title
			}
		}
		for gi := range fg.Gaps {
			g := &fg.Gaps[gi]
			if g.FeatureID == "" {
				g.FeatureID = fg.FeatureID
			}
			if g.FeatureFile == "" {
				g.FeatureFile = fg.FeatureFile
			}
		}
	}

	// Append a "no gaps" entry for any feature the model omitted entirely —
	// silence is not the same as completeness, so we surface what was
	// checked even when the verdict is positive.
	for _, f := range features {
		if seen[strings.ToUpper(f.FeatureID)] {
			continue
		}
		result.Features = append(result.Features, gapanalysis.FeatureGaps{
			FeatureID:   f.FeatureID,
			FeatureFile: filepath.Base(f.FilePath),
			Title:       f.Title,
			Summary:     "No gaps reported by the model. Feature appears fully implemented.",
		})
	}
}

func buildGapStructurePrompt(rawAnalysis string) string {
	return `Convert the following gap-analysis report into structured JSON. Extract every gap mentioned, grouped by feature_id.

Output ONLY valid JSON matching this exact schema (no markdown fences, no surrounding text):

{
  "repo": "owner/name",
  "overview": "2-4 sentence overall summary of the gap analysis: how many features were checked, headline themes, and the 1-2 most important gaps to fix first.",
  "features": [
    {
      "feature_id": "CC-0042",
      "feature_file": "CC-0042-...json",
      "title": "Feature title from the spec",
      "summary": "1-3 sentence verdict for this specific feature.",
      "gaps": [
        {
          "id": "",
          "feature_id": "CC-0042",
          "feature_file": "CC-0042-...json",
          "type": "missing_criterion|missing_scenario|missing_test|missing_task",
          "severity": "CRITICAL|WARNING|INFO",
          "title": "Short, specific gap title (no severity prefix, no brackets)",
          "description": "What is missing and why it matters. 1-3 sentences.",
          "evidence": "Concrete file paths, grep results, or 'searched X, found nothing'.",
          "source": "The verbatim spec snippet this gap maps to (criterion text, scenario When/Then, task title, or test_function name).",
          "confidence": "verified|likely|uncertain",
          "suggested_issue": {
            "title": "Issue title — no severity prefix, no [LEVEL] tag.",
            "body": "Markdown body suitable to post as a GitHub issue."
          }
        }
      ]
    }
  ]
}

Field rules:
- "id": leave as empty string — it is assigned automatically.
- "type": one of the four values above. Pick the type that matches WHAT IS MISSING.
- "severity": uppercase, never BLOCKING (gap analysis runs on already-merged work).
- "feature_id" / "feature_file": copy from the surrounding feature block.
- "source": include the verbatim text from the spec — criterion, scenario, task, or test name.
- "evidence": cite paths or grep results. "checked X, did not find Y" is acceptable.
- "suggested_issue.title": never start with "[CRITICAL]", "Severity:", or any other level marker.
- If a feature has no gaps, still include it with an empty "gaps" array and a positive summary.

<gap-analysis-report>
` + rawAnalysis + `
</gap-analysis-report>`
}

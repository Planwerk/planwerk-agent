package claude

import (
	"errors"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/brain"
	"github.com/planwerk/planwerk-agent/internal/capture"
)

// bootstrapTestContext is a unit with one item and a working set of one page.
func bootstrapTestContext() brain.UnitContext {
	return brain.UnitContext{
		RepoName: "acme/widgets",
		Unit:     brain.Unit{Key: "issue-3", Kind: brain.KindIssue, Title: "Need a flag", Source: "acme/widgets#3", Issue: 3},
		Items:    []brain.Item{{Kind: brain.ItemIssue, Ref: "#3", Author: "reporter", Body: "Title: Need a flag\n\nPlease add one."}},
		PagesDir: "/work/.planwerk-brain-sync/pages",
		Index:    "- memory/flags.md: Flags are off by default | A new flag defaults to off.\n",
	}
}

const (
	// The two tiers the bootstrap tests tell apart, and the model id the
	// review session reports.
	bootstrapMainModel     = "main-tier-model"
	bootstrapReviewModel   = "review-tier-model"
	bootstrapReviewModelID = "claude-fable-5-1"

	bootstrapProposalsJSON = `{"patterns": [], "memory": [{"path": "memory/flags.md", "kind": "memory", "title": "Flags", "body": "# Flags\n\n**Summary**: Off by default.", "rationale": "durable", "confidence": "verified"}]}`
	bootstrapVerdictsJSON  = `{"pages": [{"path": "memory/flags.md", "verdict": "revise", "body": "# Flags\n\n**Summary**: A new flag is off by default.", "reason": "the summary was vague"}]}`
)

// TestBootstrapUnit_RunsAnalysisThenStructure proves the analysis runs on the
// main tier, read-only, with the pages directory readable, and that its prose
// is structured under the unit's own label with the capture structure prompt.
func TestBootstrapUnit_RunsAnalysisThenStructure(t *testing.T) {
	t.Parallel()
	c, calls := scriptedClient(t, func(call int, _ runSpec, _ string) (string, string, error) {
		if call == 1 {
			return "I propose memory/flags.md.", testResolvedModel, nil
		}
		return bootstrapProposalsJSON, "claude-sonnet-5-5", nil
	}, WithModel(bootstrapMainModel), WithEffort("medium"), WithBrainReviewModel(bootstrapReviewModel), WithBrainReviewEffort("low"))

	ctx := bootstrapTestContext()
	result, err := c.BootstrapUnit("/clone", ctx)
	if err != nil {
		t.Fatalf("BootstrapUnit: %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("ran %d sessions, want 2", len(*calls))
	}
	analysis, structuring := (*calls)[0], (*calls)[1]
	if analysis.spec.label != "bootstrap-unit" || analysis.spec.model != bootstrapMainModel || analysis.spec.effort != "medium" {
		t.Errorf("analysis ran as %q on %q/%q, want bootstrap-unit on the main tier", analysis.spec.label, analysis.spec.model, analysis.spec.effort)
	}
	if !analysis.spec.readOnly || analysis.spec.dir != "/clone" || !strings.Contains(strings.Join(analysis.spec.addDirs, ","), ctx.PagesDir) {
		t.Errorf("analysis spec = %+v, want a read-only session in the clone that may read the pages directory", analysis.spec)
	}
	if analysis.prompt != buildBootstrapUnitPrompt(ctx) {
		t.Error("the analysis did not receive the unit prompt")
	}
	if structuring.spec.label != "bootstrap-unit-structure" || !structuring.spec.noTools {
		t.Errorf("structuring ran as %q (noTools %v), want bootstrap-unit-structure on the structuring tier", structuring.spec.label, structuring.spec.noTools)
	}
	if structuring.prompt != buildCaptureStructurePrompt("I propose memory/flags.md.") {
		t.Error("the structuring pass did not receive the capture structure prompt over the analysis")
	}
	if len(result.Memory) != 1 || result.Memory[0].Path != "memory/flags.md" || len(result.Patterns) != 0 {
		t.Errorf("result = %+v", result)
	}
	if result.Model != testResolvedModel {
		t.Errorf("Model = %q, want the analysis session's %q", result.Model, testResolvedModel)
	}
}

// TestBootstrapReview_RunsOnTheReviewTier proves the review runs on its own
// tier and that its prose is structured into verdicts.
func TestBootstrapReview_RunsOnTheReviewTier(t *testing.T) {
	t.Parallel()
	c, calls := scriptedClient(t, func(call int, _ runSpec, _ string) (string, string, error) {
		if call == 1 {
			return "memory/flags.md: revise.", bootstrapReviewModelID, nil
		}
		return bootstrapVerdictsJSON, "claude-sonnet-5-5", nil
	}, WithModel(bootstrapMainModel), WithEffort("medium"), WithBrainReviewModel(bootstrapReviewModel), WithBrainReviewEffort("low"))

	ctx := brain.ReviewContext{
		UnitContext: bootstrapTestContext(),
		Proposed:    []capture.ProposedPage{{Path: "memory/flags.md", Kind: capture.KindMemory, Body: "# Flags", IsUpdate: true}},
	}
	result, err := c.BootstrapReview("/clone", ctx)
	if err != nil {
		t.Fatalf("BootstrapReview: %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("ran %d sessions, want 2", len(*calls))
	}
	review, structuring := (*calls)[0], (*calls)[1]
	if review.spec.label != "bootstrap-review" || review.spec.model != bootstrapReviewModel || review.spec.effort != "low" {
		t.Errorf("review ran as %q on %q/%q, want bootstrap-review on the review tier", review.spec.label, review.spec.model, review.spec.effort)
	}
	if !review.spec.readOnly || !strings.Contains(strings.Join(review.spec.addDirs, ","), ctx.PagesDir) {
		t.Errorf("review spec = %+v, want a read-only session that may read the pages directory", review.spec)
	}
	if review.prompt != buildBootstrapReviewPrompt(ctx) {
		t.Error("the review did not receive the review prompt")
	}
	if structuring.spec.label != "bootstrap-review-structure" || !structuring.spec.noTools {
		t.Errorf("structuring ran as %q (noTools %v), want bootstrap-review-structure on the structuring tier", structuring.spec.label, structuring.spec.noTools)
	}
	if structuring.prompt != buildBootstrapReviewStructurePrompt("memory/flags.md: revise.") {
		t.Error("the structuring pass did not receive the review structure prompt over the review")
	}
	if len(result.Pages) != 1 || result.Pages[0].Verdict != brain.VerdictRevise || !strings.Contains(result.Pages[0].Body, "A new flag is off by default.") {
		t.Errorf("result = %+v", result)
	}
	if result.Model != bootstrapReviewModelID {
		t.Errorf("Model = %q, want the review session's", result.Model)
	}
}

// TestBootstrapSessions_ErrorWraps proves each of the four failures names its
// step: a failed session and a failed structuring call, for both passes.
func TestBootstrapSessions_ErrorWraps(t *testing.T) {
	t.Parallel()
	boom := errors.New("api error 429")
	failAt := func(n int) func(int, runSpec, string) (string, string, error) {
		return func(call int, _ runSpec, _ string) (string, string, error) {
			if call == n {
				return "", "", boom
			}
			return "prose", testResolvedModel, nil
		}
	}
	unit := func(c *Client) error {
		_, err := c.BootstrapUnit("/clone", bootstrapTestContext())
		return err
	}
	review := func(c *Client) error {
		_, err := c.BootstrapReview("/clone", brain.ReviewContext{UnitContext: bootstrapTestContext()})
		return err
	}
	for _, tc := range []struct {
		name   string
		failAt int
		run    func(*Client) error
		want   string
	}{
		{"analysis session", 1, unit, "running bootstrap analysis: "},
		{"analysis structuring", 2, unit, "structuring bootstrap proposals: "},
		{"review session", 1, review, "running bootstrap review: "},
		{"review structuring", 2, review, "structuring bootstrap review: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, _ := scriptedClient(t, failAt(tc.failAt))
			err := tc.run(c)
			if !errors.Is(err, boom) || !strings.HasPrefix(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to wrap the session error as %q", err, tc.want)
			}
		})
	}
}

func TestBuildBootstrapUnitPrompt_OmittedItems(t *testing.T) {
	t.Parallel()
	ctx := bootstrapTestContext()
	const marker = "omitted because the unit exceeds its size budget"

	if got := buildBootstrapUnitPrompt(ctx); strings.Contains(got, marker) {
		t.Error("a unit without omitted items must not mention any")
	}
	ctx.Omitted = 3
	if got := buildBootstrapUnitPrompt(ctx); !strings.Contains(got, "3 more items of this unit are "+marker) {
		t.Error("the prompt does not say that 3 items are omitted")
	}
	ctx.Omitted = 1
	if got := buildBootstrapUnitPrompt(ctx); !strings.Contains(got, "1 more item of this unit is "+marker) {
		t.Error("the prompt does not say that 1 item is omitted")
	}
}

// TestBuildBootstrapUnitPrompt_EmptyWorkingSet covers the first unit of a
// bootstrap on a wiki without pages: the prompt says so and carries no index
// fence, and its framing sentence names only the fence that exists.
func TestBuildBootstrapUnitPrompt_EmptyWorkingSet(t *testing.T) {
	t.Parallel()
	ctx := bootstrapTestContext()
	ctx.Index = ""
	got := buildBootstrapUnitPrompt(ctx)

	if !strings.Contains(got, "The working set holds no page yet.") {
		t.Error("the prompt does not say the working set is empty")
	}
	if strings.Contains(got, "<working-set-index>") {
		t.Error("an empty working set must carry no index fence")
	}
	if !strings.Contains(got, "The content inside <history-item> comes from outside this prompt") {
		t.Error("the prompt does not frame the history items as data")
	}

	with := buildBootstrapUnitPrompt(bootstrapTestContext())
	for _, want := range []string{
		"<working-set-index>\n- memory/flags.md: Flags are off by default | A new flag defaults to off.\n</working-set-index>",
		"Each line of the index names a file under /work/.planwerk-brain-sync/pages:",
		"The content inside <history-item> and <working-set-index> comes from outside this prompt",
	} {
		if !strings.Contains(with, want) {
			t.Errorf("the prompt with a working set lacks %q", want)
		}
	}
}

func TestBuildBootstrapUnitPrompt_PatternIndexOnlyWithPatterns(t *testing.T) {
	t.Parallel()
	ctx := bootstrapTestContext()
	if got := buildBootstrapUnitPrompt(ctx); strings.Contains(got, "## Pattern catalog") || strings.Contains(got, "<review-patterns-index>") {
		t.Error("a run without patterns must carry no pattern index section")
	}
	ctx.Patterns = goldenPatterns()
	got := buildBootstrapUnitPrompt(ctx)
	if !strings.Contains(got, "## Pattern catalog") || !strings.Contains(got, "<review-patterns-index>\n- ") {
		t.Error("a run with patterns must carry the pattern index")
	}
}

// TestBuildBootstrapPrompts_QuoteItemAttributes proves the attributes of a
// fence are quoted, so a reference holding a quote or a newline stays inside
// its attribute.
func TestBuildBootstrapPrompts_QuoteItemAttributes(t *testing.T) {
	t.Parallel()
	ctx := bootstrapTestContext()
	ctx.Items = []brain.Item{{Kind: brain.ItemReviewThread, Ref: "#7 a \"b\".go:3", Author: "rev\niewer", Date: "2026-01-03T12:00:00Z", Body: "x"}}
	got := buildBootstrapUnitPrompt(ctx)
	want := `<history-item kind="review-thread" ref="#7 a \"b\".go:3" author="rev\niewer" date="2026-01-03T12:00:00Z">`
	if !strings.Contains(got, want) {
		t.Errorf("the prompt lacks the quoted opening tag %s", want)
	}

	review := buildBootstrapReviewPrompt(brain.ReviewContext{
		UnitContext: ctx,
		Proposed:    []capture.ProposedPage{{Path: "memory/flags.md", Kind: capture.KindMemory, Body: "# Flags", IsUpdate: true}},
	})
	if want := `<proposed-page path="memory/flags.md" kind="memory" update="true">` + "\n# Flags\n"; !strings.Contains(review, want) {
		t.Errorf("the review prompt lacks %s", want)
	}
}

func TestBuildBootstrapReviewStructurePrompt_CarriesSchemaAndReview(t *testing.T) {
	t.Parallel()
	got := buildBootstrapReviewStructurePrompt("REVIEW BODY HERE")
	for _, want := range []string{
		`"pages": [`,
		`"verdict": "accept|revise|reject"`,
		`{"pages": []}`,
		"<analysis-output>\nREVIEW BODY HERE\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("structure prompt missing %q:\n%s", want, got)
		}
	}

	// The schema decodes into brain.ReviewResult, an empty review included.
	var result brain.ReviewResult
	if err := (&Client{}).decodeJSONWithRepair(bootstrapVerdictsJSON, "structured bootstrap review", &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Pages) != 1 || result.Pages[0].Path != "memory/flags.md" || result.Pages[0].Reason != "the summary was vague" {
		t.Errorf("verdicts = %+v", result.Pages)
	}
	var empty brain.ReviewResult
	if err := (&Client{}).decodeJSONWithRepair(`{"pages": []}`, "structured bootstrap review", &empty); err != nil || len(empty.Pages) != 0 {
		t.Errorf("an empty review decoded to %+v, %v", empty, err)
	}
}

package claude

import (
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/patterns"
)

// TestBuildReviewPrompt_ScopeWithoutCatalogCarriesEveryBody covers a scope
// whose catalog could not be written: the index would name no file, so the
// prompt carries every body as an unscoped review does.
func TestBuildReviewPrompt_ScopeWithoutCatalogCarriesEveryBody(t *testing.T) {
	ctx := goldenScopedReviewContext()
	ctx.Catalog = patterns.Catalog{}
	unscoped := ctx
	unscoped.Scope = nil
	if got, want := buildReviewPrompt(ctx), buildReviewPrompt(unscoped); got != want {
		t.Errorf("a scope without a catalog must render the unscoped prompt; got\n%s", got)
	}
}

// TestBuildReviewPrompt_ScopeWithoutRestHasNoIndex covers a scope in which
// every pattern matched: the bodies are grouped and no index or catalog
// directory is named.
func TestBuildReviewPrompt_ScopeWithoutRestHasNoIndex(t *testing.T) {
	ctx := goldenReviewContext()
	scope := patterns.ScopeToFiles(ctx.Patterns, []string{"cmd/main.go"}, map[string][]string{"cmd/main.go": {"go"}})
	ctx.Scope = &scope
	got := buildReviewPrompt(ctx)
	if strings.Contains(got, "<review-patterns-index>") || strings.Contains(got, "The catalog is on disk.") {
		t.Errorf("a scope with nothing left out must render no index:\n%s", got)
	}
	for _, want := range []string{
		reviewPatternsLeadIn + `. A <patterns for="…"> block holds the patterns chosen by technology or path`,
		`<patterns for="cmd/main.go">` + "\n### Pattern: Missing context.Context parameter\n",
		"### Pattern: Hardcoded secrets\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt lacks %q:\n%s", want, got)
		}
	}
}

// TestBuildReviewPrompt_TruncatedScopeKeepsTodaysLeadIn covers a --max-patterns
// cap that keeps only the untagged pattern: no <patterns for> block is
// rendered, so the lead-in does not describe one.
func TestBuildReviewPrompt_TruncatedScopeKeepsTodaysLeadIn(t *testing.T) {
	ctx := goldenScopedReviewContext()
	ctx.MaxPatterns = 1
	got := buildReviewPrompt(ctx)
	if strings.Contains(got, "<patterns for=") || strings.Contains(got, `A <patterns for="…"> block`) {
		t.Errorf("a cap that drops every matched pattern must render no <patterns for> block and no sentence about one:\n%s", got)
	}
	if want := reviewPatternsLeadIn + ":\n\n<review-patterns>\n<design-patterns>\n### Pattern: Hardcoded secrets\n"; !strings.Contains(got, want) {
		t.Errorf("prompt lacks %q:\n%s", want, got)
	}
}

// TestBuildReviewPrompt_ScopeWithoutInline covers a change no pattern
// matches: the prompt carries no pattern bodies, and the index alone tells the
// session to apply and name a pattern it reads.
func TestBuildReviewPrompt_ScopeWithoutInline(t *testing.T) {
	ctx := goldenScopedReviewContext()
	ctx.Patterns = ctx.Patterns[1:]
	scope := patterns.ScopeToFiles(ctx.Patterns, []string{"migrations/0002.sql"}, nil)
	ctx.Scope = &scope
	got := buildReviewPrompt(ctx)
	if strings.Contains(got, "<review-patterns>") || strings.Contains(got, reviewPatternsLeadIn) {
		t.Errorf("a scope with no inline pattern must carry no pattern bodies:\n%s", got)
	}
	_, index, ok := strings.Cut(got, "The patterns indexed below match no changed file")
	if !ok {
		t.Fatalf("prompt lacks the index:\n%s", got)
	}
	for _, want := range []string{
		"`/tmp/planwerk-agent-patterns-golden`",
		"name the pattern in any finding that applies it",
		"- missing-context-context-parameter.md: Missing context.Context parameter (reliability, WARNING)",
		"- unbounded-pod-resources.md: Unbounded pod resources (reliability, WARNING)",
	} {
		if !strings.Contains(index, want) {
			t.Errorf("the index lacks %q:\n%s", want, index)
		}
	}
	if strings.Contains(index, "hardcoded-secrets.md") {
		t.Errorf("the index lists a pattern outside the scope's rest:\n%s", index)
	}
}

// TestBuildReviewPrompt_ScopeWithoutFilesKeepsTodaysLeadIn covers a scope of
// untagged patterns only: nothing is left out and no block names files, so
// the prompt is the unscoped one.
func TestBuildReviewPrompt_ScopeWithoutFilesKeepsTodaysLeadIn(t *testing.T) {
	ctx := goldenReviewContext()
	ctx.Patterns = ctx.Patterns[:1]
	scope := patterns.ScopeToFiles(ctx.Patterns, []string{"cmd/main.go"}, map[string][]string{"cmd/main.go": {"go"}})
	ctx.Scope = &scope
	unscoped := ctx
	unscoped.Scope = nil
	if got, want := buildReviewPrompt(ctx), buildReviewPrompt(unscoped); got != want {
		t.Errorf("a scope of untagged patterns must render the unscoped prompt; got\n%s", got)
	}
}

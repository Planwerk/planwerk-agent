package claude

import (
	"fmt"
	"io"

	"github.com/planwerk/planwerk-review/internal/doccheck"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/report"
)

// DefaultBaseBranch is the fallback base branch name when none is specified.
const DefaultBaseBranch = "main"

// ReviewContext holds all context needed to build the review prompt.
type ReviewContext struct {
	Patterns    []patterns.Pattern
	MaxPatterns int    // max patterns to inject; <= 0 disables truncation
	MaxFindings int    // cap on findings Claude returns; <= 0 disables cap
	BaseBranch  string // PR base branch; empty falls back to DefaultBaseBranch
	PRTitle     string
	PRBody      string
	Checklist   string                    // external checklist content (empty = use built-in)
	CommitLog   string                    // git log output for scope drift detection
	StaleDocs   []doccheck.StaleDocHint   // documentation files that may need updating
	NewFeatures []doccheck.NewFeatureHint // new files that may need documentation
	TodoContent string                    // content of TODOS.md if present
	Glossary    string                    // repo domain glossary from CONTEXT.md / .planwerk/context.md; empty when absent
}

// Review invokes `claude /review` in the given directory and returns structured findings.
// It runs two Claude calls:
//  1. `claude /review` to get the unstructured review output
//  2. `claude -p` to structure the output into JSON
func (c *Client) Review(dir string, ctx ReviewContext) (*report.ReviewResult, error) {
	// Step 1: Run /review
	rawReview, model, err := c.runReview(dir, ctx)
	if err != nil {
		return nil, fmt.Errorf("running /review: %w", err)
	}

	// Step 2: Structure the output into JSON
	result, err := c.structureReview(rawReview)
	if err != nil {
		return nil, fmt.Errorf("structuring review output: %w", err)
	}

	assignIDs(result)
	result.Model = model
	return result, nil
}

// runReview invokes `claude -p` with a prompt that includes patterns and the
// /review command, returning the raw review text and the resolved model id.
func (c *Client) runReview(dir string, rctx ReviewContext) (text, model string, err error) {
	return c.runClaude(dir, buildReviewPrompt(rctx), "review")
}

// tokenUsage is a tolerant view over the per-call token counts Claude Code
// reports on its JSON envelope (`--output-format json`) and on the streaming
// `result` event. Absent fields decode to zero, so an older or newer CLI that
// omits any of them degrades to a partial count rather than a parse error. The
// counts are cumulative for the call, so they are summed directly across calls.
type tokenUsage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
}

// addUsage folds one Claude call's token counts and estimated cost into the
// Client's per-Run accumulator and increments the call counter. It is safe to
// call concurrently: the review fan-out runs several calls on one shared
// Client. costUSD is the call's own total_cost_usd as Claude Code reports it;
// the summed value is the "estimated cost" surfaced on completion, which avoids
// a drift-prone per-model pricing table.
func (c *Client) addUsage(u tokenUsage, costUSD float64) {
	c.usageMu.Lock()
	defer c.usageMu.Unlock()
	c.usage.Calls++
	c.usage.InputTokens += u.InputTokens
	c.usage.OutputTokens += u.OutputTokens
	c.usage.CacheReadTokens += u.CacheReadTokens
	c.usage.CacheCreationTokens += u.CacheCreationTokens
	c.usage.CostUSD += costUSD
}

// UsageTotals returns a snapshot of the per-Run token usage and estimated cost
// accumulated so far. The returned value is a copy, safe to read without the
// lock.
func (c *Client) UsageTotals() report.Usage {
	c.usageMu.Lock()
	defer c.usageMu.Unlock()
	return c.usage
}

// LogUsageSummary writes a one-line totals summary of the Run's Claude token
// usage and estimated cost to w (typically os.Stderr), e.g. "claude usage:
// 13.4k in / 4.2k out across 6 calls, est. $0.42". It writes nothing when no
// Claude call was made, so commands that exit early (--help, dry runs,
// print-prompt) stay silent.
func (c *Client) LogUsageSummary(w io.Writer) {
	u := c.UsageTotals()
	if u.Calls == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "claude usage: %s in / %s out across %d calls, est. $%.2f\n",
		humanizeTokens(u.InputTokens), humanizeTokens(u.OutputTokens), u.Calls, u.CostUSD)
}

// humanizeTokens renders a token count compactly: "1.2M" for millions, "13.4k"
// for thousands, and the bare integer below 1000, matching the totals-summary
// format in the issue.
func humanizeTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

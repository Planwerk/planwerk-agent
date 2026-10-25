package claude

import (
	"fmt"
	"io"
	"sort"

	"github.com/planwerk/planwerk-agent/internal/diffscope"
	"github.com/planwerk/planwerk-agent/internal/doccheck"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
	"github.com/planwerk/planwerk-agent/internal/search"
)

// DefaultBaseBranch is the fallback base branch name when none is specified.
const DefaultBaseBranch = "main"

// Session labels of the review's passes. Each is the label a pass's `claude -p`
// session runs under: the run records the session's usage under it
// (report.PassUsage) and the pass's outcome too (report.PassStatus), so a failed
// pass's cost and its status read under one name. A specialist's is
// Specialist.PassLabel. They are not the provenance labels findings carry in
// ConfirmedBy, which package hygiene owns (decision 135).
const (
	PassLabelReview      = "review"
	PassLabelAdversarial = "adversarial"
	PassLabelCoverage    = "coverage"
	PassLabelCompliance  = "compliance"
)

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
	Glossary    string                    // repo domain glossary (glossary.LoadBody); empty when absent
	Memory      patterns.MemoryCatalog    // wiki project memory: the pages and the directory a session reads them from; zero when absent
	Brain       search.Surface            // the search of the local mirror the session may run; zero means no search
	// Diff is the diff the session reads, which the caller writes
	// (diffscope.MaterializeOrWarn) and removes once Review returns. The zero
	// Diff keeps the git command in the prompt (decision 137).
	Diff diffscope.Diff
	// ChangedFiles are the files the Changed Files section lists and the
	// session accounts for in files_reviewed, which the caller checks with
	// hygiene.CheckFileLedger; empty renders no section (decision 138).
	ChangedFiles []github.FileChange
	// Scope is the split of Patterns by the changed files under review
	// --scope-patterns (patterns.ScopeToFiles): the prompt carries the bodies
	// of Scope.Inline and lists Scope.Rest as an index of Catalog. nil renders
	// every body (decision 141).
	Scope *patterns.FileScope
	// Catalog is the on-disk catalog whose entries the index of Scope.Rest
	// names; the review session opens its directory with --add-dir. It is zero
	// without a Scope or with an empty Scope.Rest (decision 141).
	Catalog patterns.Catalog
	// Partition is the partition of a partitioned review this session reviews
	// (--partition-files); the zero value reviews the whole pull request. The
	// caller narrows Diff and ChangedFiles to the partition's files and keeps
	// every other field the sessions share (decision 142).
	Partition PartitionScope
}

// PartitionScope narrows a review session to one partition of a partitioned
// review: the prompt's Partition section names the session's files and those
// of the sessions running beside it. The zero value (Count < 2) is no
// partition, and the prompt carries no such section (decision 142).
type PartitionScope struct {
	Index  int      // 1-based number of this session's partition
	Count  int      // number of partitions in the review
	Files  []string // the changed files this session reviews
	Others []string // the changed files the other partitions review, in partition order
}

// Review runs the diff-review session in the given directory and returns its
// findings. The session emits them as JSON under --json-schema with
// schema.FinderOutput, and finishReview decodes that output (decision 109).
// The session reads ctx.Diff and is handed its directory; Review writes no
// diff of its own, and the result's ExcludedFiles is ctx.Diff.Excluded,
// whatever the session emitted (decision 137).
func (c *Client) Review(dir string, ctx ReviewContext) (*report.ReviewResult, error) {
	rawReview, model, err := c.runReview(dir, ctx)
	if err != nil {
		return nil, fmt.Errorf("running review: %w", err)
	}

	res, err := c.finishReview(rawReview, model, "review output", "")
	if err != nil {
		return nil, err
	}
	res.ExcludedFiles = ctx.Diff.Excluded
	return res, nil
}

// runReview invokes `claude -p` with the review prompt, returning the review's
// JSON output and the resolved model id.
func (c *Client) runReview(dir string, rctx ReviewContext) (text, model string, err error) {
	return c.runClaudeFindings(dir, buildReviewPrompt(rctx), PassLabelReview, rctx.Catalog, rctx.Memory, rctx.Brain, rctx.Diff.Dir)
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
//
// label is the runner's own name for the invocation ("review", "adversarial",
// "specialist-security", "elaborate-structure", …). The same counts are folded
// a second time into the per-pass accumulator under that label, so a run's cost
// can be attributed to the pass that spent it rather than only to the run as a
// whole. Calls that share a label (the finder rounds of the review loop)
// accumulate together, which is the intent: the question the breakdown answers
// is what a *pass* costs, not what one invocation of it did.
func (c *Client) addUsage(label string, u tokenUsage, costUSD float64) {
	c.usageMu.Lock()
	defer c.usageMu.Unlock()
	c.usage.Calls++
	c.usage.InputTokens += u.InputTokens
	c.usage.OutputTokens += u.OutputTokens
	c.usage.CacheReadTokens += u.CacheReadTokens
	c.usage.CacheCreationTokens += u.CacheCreationTokens
	c.usage.CostUSD += costUSD

	if c.usageByPass == nil {
		c.usageByPass = make(map[string]report.PassUsage)
	}
	p := c.usageByPass[label]
	p.Pass = label
	p.Calls++
	p.InputTokens += u.InputTokens
	p.OutputTokens += u.OutputTokens
	p.CacheReadTokens += u.CacheReadTokens
	p.CacheCreationTokens += u.CacheCreationTokens
	p.CostUSD += costUSD
	c.usageByPass[label] = p
}

// UsageTotals returns a snapshot of the per-Run token usage and estimated cost
// accumulated so far, with the per-pass breakdown attached. The returned value
// is a copy — including the Passes slice — so it is safe to read and to embed
// in a report without the lock.
func (c *Client) UsageTotals() report.Usage {
	c.usageMu.Lock()
	defer c.usageMu.Unlock()
	u := c.usage
	u.Passes = c.snapshotPasses()
	return u
}

// snapshotPasses copies the per-pass accumulator into a slice ordered by
// estimated cost (descending), then by total tokens, then by name — the order a
// reader wants, most expensive first, and deterministic even when every entry
// reports a zero cost (as the stub-driven tests and an older CLI both do).
// Callers must hold usageMu.
func (c *Client) snapshotPasses() []report.PassUsage {
	if len(c.usageByPass) == 0 {
		return nil
	}
	passes := make([]report.PassUsage, 0, len(c.usageByPass))
	for _, p := range c.usageByPass {
		passes = append(passes, p)
	}
	sort.Slice(passes, func(i, j int) bool {
		if passes[i].CostUSD != passes[j].CostUSD {
			return passes[i].CostUSD > passes[j].CostUSD
		}
		ti := passes[i].InputTokens + passes[i].OutputTokens
		tj := passes[j].InputTokens + passes[j].OutputTokens
		if ti != tj {
			return ti > tj
		}
		return passes[i].Pass < passes[j].Pass
	})
	return passes
}

// maxSummaryPasses bounds the per-pass breakdown so a long run (implement fans
// out over a dozen labels) does not bury its own totals line. What falls outside
// the bound is summed into a trailing "further passes" line rather than dropped:
// a breakdown that silently truncates reads as if it accounted for everything.
const maxSummaryPasses = 8

// LogUsageSummary writes a totals summary of the Run's Claude token usage and
// estimated cost to w (typically os.Stderr), e.g. "claude usage: 13.4k in /
// 4.2k out across 6 calls, est. $0.42", followed by the same figures broken
// down per pass, most expensive first. It writes nothing when no Claude call was
// made, so commands that exit early (--help, dry runs, print-prompt) stay
// silent, and it omits the breakdown when a single pass accounts for the whole
// run — there the totals line already says everything the breakdown would.
func (c *Client) LogUsageSummary(w io.Writer) {
	u := c.UsageTotals()
	if u.Calls == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "claude usage: %s in / %s out across %d calls, est. $%.2f\n",
		humanizeTokens(u.InputTokens), humanizeTokens(u.OutputTokens), u.Calls, u.CostUSD)
	if len(u.Passes) < 2 {
		return
	}
	shown := u.Passes
	var rest []report.PassUsage
	if len(shown) > maxSummaryPasses {
		shown, rest = shown[:maxSummaryPasses], shown[maxSummaryPasses:]
	}
	for _, p := range shown {
		_, _ = fmt.Fprintf(w, "  %-24s %2d %s  %s in / %s out, est. $%.2f\n",
			p.Pass, p.Calls, pluralCalls(p.Calls), humanizeTokens(p.InputTokens), humanizeTokens(p.OutputTokens), p.CostUSD)
	}
	if len(rest) > 0 {
		var agg report.PassUsage
		for _, p := range rest {
			agg.Calls += p.Calls
			agg.InputTokens += p.InputTokens
			agg.OutputTokens += p.OutputTokens
			agg.CostUSD += p.CostUSD
		}
		_, _ = fmt.Fprintf(w, "  %-24s %2d %s  %s in / %s out, est. $%.2f\n",
			fmt.Sprintf("+%d further passes", len(rest)), agg.Calls, pluralCalls(agg.Calls),
			humanizeTokens(agg.InputTokens), humanizeTokens(agg.OutputTokens), agg.CostUSD)
	}
}

// pluralCalls returns the noun that follows a call count, so a single-call pass
// reads "1 call" rather than "1 calls".
func pluralCalls(n int) string {
	if n == 1 {
		return "call "
	}
	return "calls"
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

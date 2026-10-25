package eval

import (
	"cmp"
	"maps"
	"slices"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// promptTokens counts every prompt token a run sent: input, cache reads and
// cache creation. The token levers shrink prompts, and caching moves tokens
// between those three counters from run to run, so no single counter compares
// across runs.
func promptTokens(u report.Usage) int64 {
	return u.InputTokens + u.CacheReadTokens + u.CacheCreationTokens
}

// passPromptTokens is promptTokens for one pass.
func passPromptTokens(p report.PassUsage) int64 {
	return p.InputTokens + p.CacheReadTokens + p.CacheCreationTokens
}

// sumUsage adds every counter of a and b and merges their passes by label into
// a fresh slice, ordered by estimated cost descending, then by prompt tokens
// descending, then by name. A merged pass also sums NumTurns and
// PermissionDenials, merges ToolUses and appends SessionIDs, both into a map
// and a slice of its own, so neither input is aliased or mutated. Passes stays
// nil when neither side has any.
func sumUsage(a, b report.Usage) report.Usage {
	sum := report.Usage{
		Calls:               a.Calls + b.Calls,
		InputTokens:         a.InputTokens + b.InputTokens,
		OutputTokens:        a.OutputTokens + b.OutputTokens,
		CacheReadTokens:     a.CacheReadTokens + b.CacheReadTokens,
		CacheCreationTokens: a.CacheCreationTokens + b.CacheCreationTokens,
		CostUSD:             a.CostUSD + b.CostUSD,
	}
	byPass := make(map[string]report.PassUsage, len(a.Passes)+len(b.Passes))
	for _, p := range slices.Concat(a.Passes, b.Passes) {
		m := byPass[p.Pass]
		m.Pass = p.Pass
		m.Calls += p.Calls
		m.InputTokens += p.InputTokens
		m.OutputTokens += p.OutputTokens
		m.CacheReadTokens += p.CacheReadTokens
		m.CacheCreationTokens += p.CacheCreationTokens
		m.CostUSD += p.CostUSD
		m.NumTurns += p.NumTurns
		m.PermissionDenials += p.PermissionDenials
		if len(p.ToolUses) > 0 && m.ToolUses == nil {
			m.ToolUses = make(map[string]int, len(p.ToolUses))
		}
		for name, n := range p.ToolUses {
			m.ToolUses[name] += n
		}
		// m starts from the zero value, so the first append allocates a slice
		// of m's own and no input's backing array is ever written.
		m.SessionIDs = append(m.SessionIDs, p.SessionIDs...)
		byPass[p.Pass] = m
	}
	sum.Passes = slices.SortedFunc(maps.Values(byPass), func(x, y report.PassUsage) int {
		return cmp.Or(
			cmp.Compare(y.CostUSD, x.CostUSD),
			cmp.Compare(passPromptTokens(y), passPromptTokens(x)),
			cmp.Compare(x.Pass, y.Pass),
		)
	})
	return sum
}

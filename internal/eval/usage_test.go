package eval

import (
	"reflect"
	"slices"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/report"
)

func TestPromptTokens(t *testing.T) {
	u := report.Usage{InputTokens: 100, CacheReadTokens: 2000, CacheCreationTokens: 30, OutputTokens: 999}
	if got := promptTokens(u); got != 2130 {
		t.Errorf("promptTokens = %d, want 2130 (input + cache read + cache creation, output excluded)", got)
	}
	if got := promptTokens(report.Usage{}); got != 0 {
		t.Errorf("promptTokens(zero) = %d, want 0", got)
	}
}

func TestSumUsage(t *testing.T) {
	t.Run("merges passes by label and sums the totals", func(t *testing.T) {
		a := report.Usage{
			Calls: 2, InputTokens: 10, OutputTokens: 5, CacheReadTokens: 100, CacheCreationTokens: 7, CostUSD: 0.5,
			Passes: []report.PassUsage{
				{Pass: "review", Calls: 1, InputTokens: 10, OutputTokens: 5, CacheReadTokens: 100, CacheCreationTokens: 7, CostUSD: 0.4},
				{Pass: "structure", Calls: 1, CostUSD: 0.1},
			},
		}
		b := report.Usage{
			Calls: 3, InputTokens: 20, OutputTokens: 6, CacheReadTokens: 200, CacheCreationTokens: 8, CostUSD: 0.9,
			Passes: []report.PassUsage{
				{Pass: "specialist-security", Calls: 1, CostUSD: 0.2},
				{Pass: "review", Calls: 2, InputTokens: 20, OutputTokens: 6, CacheReadTokens: 200, CacheCreationTokens: 8, CostUSD: 0.7},
			},
		}
		aPasses := slices.Clone(a.Passes)

		got := sumUsage(a, b)

		want := report.Usage{
			Calls: 5, InputTokens: 30, OutputTokens: 11, CacheReadTokens: 300, CacheCreationTokens: 15, CostUSD: 1.4,
			Passes: []report.PassUsage{
				{Pass: "review", Calls: 3, InputTokens: 30, OutputTokens: 11, CacheReadTokens: 300, CacheCreationTokens: 15, CostUSD: 1.1},
				{Pass: "specialist-security", Calls: 1, CostUSD: 0.2},
				{Pass: "structure", Calls: 1, CostUSD: 0.1},
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("sumUsage =\n%+v\nwant\n%+v", got, want)
		}
		if !reflect.DeepEqual(a.Passes, aPasses) {
			t.Errorf("sumUsage mutated a.Passes: %+v, want %+v", a.Passes, aPasses)
		}
	})

	t.Run("orders equal cost by prompt tokens, then by name", func(t *testing.T) {
		got := sumUsage(report.Usage{Passes: []report.PassUsage{
			{Pass: "b", InputTokens: 1},
			{Pass: "c", CacheReadTokens: 50},
			{Pass: "a", InputTokens: 1},
		}}, report.Usage{})
		var names []string
		for _, p := range got.Passes {
			names = append(names, p.Pass)
		}
		if want := []string{"c", "a", "b"}; !slices.Equal(names, want) {
			t.Errorf("pass order = %v, want %v", names, want)
		}
	})

	t.Run("two zero values sum to a zero value with nil passes", func(t *testing.T) {
		got := sumUsage(report.Usage{}, report.Usage{})
		if got.Passes != nil {
			t.Errorf("Passes = %#v, want nil", got.Passes)
		}
		if !reflect.DeepEqual(got, report.Usage{}) {
			t.Errorf("sumUsage(zero, zero) = %+v, want zero", got)
		}
	})
}

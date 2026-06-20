package claude

import (
	"strings"
	"sync"
	"testing"
)

// TestClient_AggregatesUsage verifies the per-Run accumulator sums token counts
// and cost across many concurrent calls and counts each call exactly once. It
// mirrors the review fan-out, which runs several Claude calls on one shared
// Client, so it is run under -race in CI to catch an unguarded accumulator.
func TestClient_AggregatesUsage(t *testing.T) {
	c := NewClient()
	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			c.addUsage(tokenUsage{
				InputTokens:         100,
				OutputTokens:        20,
				CacheReadTokens:     5,
				CacheCreationTokens: 3,
			}, 0.01)
		}()
	}
	wg.Wait()

	got := c.UsageTotals()
	if got.Calls != goroutines {
		t.Errorf("Calls = %d, want %d", got.Calls, goroutines)
	}
	if got.InputTokens != 100*goroutines {
		t.Errorf("InputTokens = %d, want %d", got.InputTokens, 100*goroutines)
	}
	if got.OutputTokens != 20*goroutines {
		t.Errorf("OutputTokens = %d, want %d", got.OutputTokens, 20*goroutines)
	}
	if got.CacheReadTokens != 5*goroutines {
		t.Errorf("CacheReadTokens = %d, want %d", got.CacheReadTokens, 5*goroutines)
	}
	if got.CacheCreationTokens != 3*goroutines {
		t.Errorf("CacheCreationTokens = %d, want %d", got.CacheCreationTokens, 3*goroutines)
	}
	// 50 * 0.01 = 0.5; compare with a tolerance for float accumulation.
	if diff := got.CostUSD - 0.5; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("CostUSD = %v, want ~0.5", got.CostUSD)
	}
}

func TestClient_LogUsageSummary_Format(t *testing.T) {
	c := NewClient()
	// 13_400 in → "13.4k", 4_200 out → "4.2k", across 6 calls, $0.42.
	c.addUsage(tokenUsage{InputTokens: 13_400, OutputTokens: 4_200}, 0.42)
	for range 5 {
		c.addUsage(tokenUsage{}, 0)
	}

	var buf strings.Builder
	c.LogUsageSummary(&buf)
	got := buf.String()
	want := "claude usage: 13.4k in / 4.2k out across 6 calls, est. $0.42\n"
	if got != want {
		t.Errorf("LogUsageSummary = %q, want %q", got, want)
	}
}

// TestClient_LogUsageSummary_SilentWhenNoCalls locks in that a Run that never
// invoked Claude (e.g. --help, a dry run, a print-prompt exit) prints nothing,
// so the summary line never appears on an empty run.
func TestClient_LogUsageSummary_SilentWhenNoCalls(t *testing.T) {
	c := NewClient()
	var buf strings.Builder
	c.LogUsageSummary(&buf)
	if buf.Len() != 0 {
		t.Errorf("LogUsageSummary with no calls = %q, want empty", buf.String())
	}
}

func TestHumanizeTokens(t *testing.T) {
	tests := []struct {
		name string
		in   int64
		want string
	}{
		{name: "zero", in: 0, want: "0"},
		{name: "below 1k stays integer", in: 999, want: "999"},
		{name: "exactly 1k", in: 1_000, want: "1.0k"},
		{name: "thousands", in: 13_400, want: "13.4k"},
		{name: "exactly 1M", in: 1_000_000, want: "1.0M"},
		{name: "millions", in: 2_500_000, want: "2.5M"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := humanizeTokens(tc.in); got != tc.want {
				t.Errorf("humanizeTokens(%d) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

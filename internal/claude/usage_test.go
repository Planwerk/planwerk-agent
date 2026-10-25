package claude

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// TestClient_AggregatesUsage verifies the per-Run accumulator sums token counts
// and cost across many concurrent calls and counts each call exactly once, and
// folds each call's session stats beside them. It mirrors the review fan-out,
// which runs several Claude calls on one shared Client, so it is run under
// -race in CI to catch an unguarded accumulator or tool map.
func TestClient_AggregatesUsage(t *testing.T) {
	c := NewClient()
	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func() {
			defer wg.Done()
			// Two labels, so the per-pass map is written concurrently too — the
			// accumulator this test guards is now a map, not just a struct.
			label := fmt.Sprintf("pass-%d", i%2)
			c.addUsage(label, tokenUsage{
				InputTokens:         100,
				OutputTokens:        20,
				CacheReadTokens:     5,
				CacheCreationTokens: 3,
			}, 0.01)
			c.addSessionStats(label, sessionStats{toolUses: map[string]int{"Read": 1}, numTurns: 2, sessionID: fmt.Sprintf("s-%d", i)})
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
	if len(got.Passes) != 2 {
		t.Fatalf("Passes = %d entries, want 2", len(got.Passes))
	}
	for _, p := range got.Passes {
		if p.Calls != goroutines/2 {
			t.Errorf("pass %q Calls = %d, want %d", p.Pass, p.Calls, goroutines/2)
		}
		if p.InputTokens != 100*goroutines/2 {
			t.Errorf("pass %q InputTokens = %d, want %d", p.Pass, p.InputTokens, 100*goroutines/2)
		}
		if p.NumTurns != 2*goroutines/2 || p.ToolUses["Read"] != goroutines/2 || len(p.SessionIDs) != goroutines/2 {
			t.Errorf("pass %q NumTurns = %d, Read = %d, %d session ids; want %d, %d, %d", p.Pass, p.NumTurns, p.ToolUses["Read"], len(p.SessionIDs), goroutines, goroutines/2, goroutines/2)
		}
	}
}

// TestClient_AddSessionStatsFoldsIntoThePass covers the fold of one session's
// stats: turns and denials add up, tool counts merge into the pass's own map,
// a session id is listed once however often its turns report it, and no call
// is counted. Zero stats create no pass.
func TestClient_AddSessionStatsFoldsIntoThePass(t *testing.T) {
	c := NewClient()
	first := map[string]int{"Read": 2}
	c.addSessionStats("review", sessionStats{toolUses: first, numTurns: 3, permissionDenials: 1, sessionID: "A"})
	first["Read"] = 99
	c.addSessionStats("review", sessionStats{toolUses: map[string]int{"Read": 1, "Grep": 1}, numTurns: 4, sessionID: "A"})

	got := c.UsageTotals()
	want := []report.PassUsage{{
		Pass:              "review",
		NumTurns:          7,
		PermissionDenials: 1,
		ToolUses:          map[string]int{"Read": 3, "Grep": 1},
		SessionIDs:        []string{"A"},
	}}
	if !reflect.DeepEqual(got.Passes, want) {
		t.Errorf("Passes = %+v, want %+v", got.Passes, want)
	}
	if got.Calls != 0 {
		t.Errorf("Calls = %d, want 0: session stats bill no call", got.Calls)
	}

	empty := NewClient()
	empty.addSessionStats("review", sessionStats{})
	if passes := empty.UsageTotals().Passes; passes != nil {
		t.Errorf("zero stats created %+v, want no pass", passes)
	}
}

// TestClient_UsageTotalsCopiesSessionStats locks in that a snapshot owns its
// tool map and session ids: a later fold on the same pass leaves it as it was,
// and writing to it leaves the client's own record alone.
func TestClient_UsageTotalsCopiesSessionStats(t *testing.T) {
	c := NewClient()
	c.addSessionStats("review", sessionStats{toolUses: map[string]int{"Read": 1}, sessionID: "A"})
	before := c.UsageTotals().Passes[0]
	c.addSessionStats("review", sessionStats{toolUses: map[string]int{"Read": 1, "Grep": 1}, sessionID: "B"})

	if want := map[string]int{"Read": 1}; !maps.Equal(before.ToolUses, want) {
		t.Errorf("snapshot ToolUses = %v after a later fold, want %v", before.ToolUses, want)
	}
	if want := []string{"A"}; !slices.Equal(before.SessionIDs, want) {
		t.Errorf("snapshot SessionIDs = %v after a later fold, want %v", before.SessionIDs, want)
	}

	before.ToolUses["Read"] = 99
	if after := c.UsageTotals().Passes[0]; after.ToolUses["Read"] != 2 || !slices.Equal(after.SessionIDs, []string{"A", "B"}) {
		t.Errorf("client record = %+v, want Read 2 and ids [A B]", after)
	}
}

// TestClient_UsageTotalsPassesAreASnapshot locks in that the returned breakdown
// is a copy: a caller that embeds it in a report must not see later calls
// mutate what it already rendered.
func TestClient_UsageTotalsPassesAreASnapshot(t *testing.T) {
	c := NewClient()
	c.addUsage("review", tokenUsage{InputTokens: 10}, 0)
	before := c.UsageTotals()
	c.addUsage("review", tokenUsage{InputTokens: 10}, 0)
	if got := before.Passes[0].InputTokens; got != 10 {
		t.Errorf("snapshot mutated by a later call: InputTokens = %d, want 10", got)
	}
}

// TestClient_UsageTotalsPassesSortedByCost locks the breakdown's order: most
// expensive first, so the reader meets the pass worth acting on at the top.
func TestClient_UsageTotalsPassesSortedByCost(t *testing.T) {
	c := NewClient()
	c.addUsage("structure", tokenUsage{InputTokens: 1_000}, 0.01)
	c.addUsage("implement", tokenUsage{InputTokens: 5_000}, 0.90)
	c.addUsage("review", tokenUsage{InputTokens: 3_000}, 0.30)

	got := c.UsageTotals().Passes
	want := []string{"implement", "review", "structure"}
	if len(got) != len(want) {
		t.Fatalf("Passes = %d entries, want %d", len(got), len(want))
	}
	for i, name := range want {
		if got[i].Pass != name {
			t.Errorf("Passes[%d] = %q, want %q", i, got[i].Pass, name)
		}
	}
}

func TestClient_LogUsageSummary_Format(t *testing.T) {
	c := NewClient()
	// 13_400 in → "13.4k", 4_200 out → "4.2k", across 6 calls, $0.42.
	c.addUsage("review", tokenUsage{InputTokens: 13_400, OutputTokens: 4_200}, 0.42)
	for range 5 {
		c.addUsage("review", tokenUsage{}, 0)
	}

	var buf strings.Builder
	c.LogUsageSummary(&buf)
	got := buf.String()
	// One pass accounts for the whole run, so the totals line says everything a
	// breakdown would and the breakdown is omitted.
	want := "claude usage: 13.4k in / 4.2k out across 6 calls, est. $0.42\n"
	if got != want {
		t.Errorf("LogUsageSummary = %q, want %q", got, want)
	}
}

// TestClient_LogUsageSummary_PerPassBreakdown locks the breakdown a multi-pass
// run prints under the totals line: one row per pass, most expensive first.
func TestClient_LogUsageSummary_PerPassBreakdown(t *testing.T) {
	c := NewClient()
	c.addUsage("implement", tokenUsage{InputTokens: 20_000, OutputTokens: 8_000}, 1.20)
	c.addUsage("adversarial", tokenUsage{InputTokens: 12_000, OutputTokens: 1_000}, 0.30)
	c.addUsage("structure", tokenUsage{InputTokens: 2_000, OutputTokens: 500}, 0.02)
	c.addUsage("structure", tokenUsage{InputTokens: 2_000, OutputTokens: 500}, 0.02)

	var buf strings.Builder
	c.LogUsageSummary(&buf)
	want := "claude usage: 36.0k in / 10.0k out across 4 calls, est. $1.54\n" +
		"  implement                 1 call   20.0k in / 8.0k out, est. $1.20\n" +
		"  adversarial               1 call   12.0k in / 1.0k out, est. $0.30\n" +
		"  structure                 2 calls  4.0k in / 1.0k out, est. $0.04\n"
	if got := buf.String(); got != want {
		t.Errorf("LogUsageSummary =\n%q\nwant\n%q", got, want)
	}
}

// TestClient_LogUsageSummary_AggregatesOverflowPasses locks in that a run with
// more passes than the breakdown shows still accounts for all of them: the
// remainder is summed into a trailing line rather than silently dropped.
func TestClient_LogUsageSummary_AggregatesOverflowPasses(t *testing.T) {
	c := NewClient()
	for i := range maxSummaryPasses + 3 {
		// Descending cost, so the three cheapest fall past the bound.
		c.addUsage(fmt.Sprintf("pass-%02d", i), tokenUsage{InputTokens: 1_000}, float64(maxSummaryPasses+3-i))
	}

	var buf strings.Builder
	c.LogUsageSummary(&buf)
	got := buf.String()
	if lines := strings.Count(got, "\n"); lines != 1+maxSummaryPasses+1 {
		t.Errorf("summary has %d lines, want %d", lines, 1+maxSummaryPasses+1)
	}
	if !strings.Contains(got, "+3 further passes         3 calls  3.0k in / 0 out, est. $6.00") {
		t.Errorf("overflow line missing or wrong:\n%s", got)
	}
}

// TestClient_LogUsageSummary_ShowsTurnsDenialsAndTools locks the rows of passes
// whose sessions were observed: the turns and denials end the row, and a tools
// line, most-used first, sits under it with "tools:" below the call count. A
// pass without stats keeps its row unchanged and gets no tools line.
func TestClient_LogUsageSummary_ShowsTurnsDenialsAndTools(t *testing.T) {
	c := NewClient()
	c.addUsage("review", tokenUsage{InputTokens: 20_000, OutputTokens: 2_000}, 0.50)
	c.addSessionStats("review", sessionStats{toolUses: map[string]int{"Grep": 9, "Bash": 4, "Read": 22}, numTurns: 14, permissionDenials: 2, sessionID: "A"})
	c.addUsage("structure", tokenUsage{InputTokens: 1_000, OutputTokens: 500}, 0.01)

	var buf strings.Builder
	c.LogUsageSummary(&buf)
	want := "claude usage: 21.0k in / 2.5k out across 2 calls, est. $0.51\n" +
		"  review                    1 call   20.0k in / 2.0k out, est. $0.50, 14 turns, 2 denied\n" +
		"                           tools: Read 22, Grep 9, Bash 4\n" +
		"  structure                 1 call   1.0k in / 500 out, est. $0.01\n"
	if got := buf.String(); got != want {
		t.Errorf("LogUsageSummary =\n%q\nwant\n%q", got, want)
	}
}

// TestClient_LogUsageSummary_SinglePassWithStatsShowsItsRow covers the one-pass
// run: the breakdown is printed once the pass has turns, denials or tools to
// show, and stays omitted for a pass with none.
func TestClient_LogUsageSummary_SinglePassWithStatsShowsItsRow(t *testing.T) {
	c := NewClient()
	c.addUsage("implement", tokenUsage{InputTokens: 13_400, OutputTokens: 4_200}, 0.42)
	c.addSessionStats("implement", sessionStats{toolUses: map[string]int{"Edit": 3}, numTurns: 1})

	var buf strings.Builder
	c.LogUsageSummary(&buf)
	want := "claude usage: 13.4k in / 4.2k out across 1 calls, est. $0.42\n" +
		"  implement                 1 call   13.4k in / 4.2k out, est. $0.42, 1 turn\n" +
		"                           tools: Edit 3\n"
	if got := buf.String(); got != want {
		t.Errorf("LogUsageSummary =\n%q\nwant\n%q", got, want)
	}

	quiet := NewClient()
	quiet.addUsage("implement", tokenUsage{InputTokens: 13_400, OutputTokens: 4_200}, 0.42)
	quiet.addSessionStats("implement", sessionStats{sessionID: "A"})
	buf.Reset()
	quiet.LogUsageSummary(&buf)
	if got, want := buf.String(), "claude usage: 13.4k in / 4.2k out across 1 calls, est. $0.42\n"; got != want {
		t.Errorf("LogUsageSummary for a pass with no stats to show = %q, want only the totals line %q", got, want)
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

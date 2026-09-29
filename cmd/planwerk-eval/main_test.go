package main

import (
	"cmp"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/eval"
	"github.com/planwerk/planwerk-agent/internal/report"
)

func TestRetry(t *testing.T) {
	t.Run("a failed attempt is retried and the retry's run returned", func(t *testing.T) {
		want := eval.Run{Usage: report.Usage{Calls: 7}}
		attempts := 0
		got, err := retry(func() (eval.Run, error) {
			attempts++
			if attempts == 1 {
				return eval.Run{}, errors.New("claude timed out")
			}
			return want, nil
		})
		if err != nil {
			t.Fatalf("retry = %v, want the second attempt's run", err)
		}
		if !reflect.DeepEqual(got, want) || attempts != 2 {
			t.Errorf("retry = %+v after %d attempts, want %+v after 2", got, attempts, want)
		}
	})

	t.Run("every attempt failing returns the last error", func(t *testing.T) {
		attempts := 0
		_, err := retry(func() (eval.Run, error) {
			attempts++
			return eval.Run{}, errors.New("claude timed out")
		})
		if err == nil || err.Error() != "claude timed out" {
			t.Errorf("retry = %v, want the attempt's error", err)
		}
		if attempts != maxRunAttempts {
			t.Errorf("attempts = %d, want %d", attempts, maxRunAttempts)
		}
	})
}

// TestRunGuardsSpendNoTokens locks that run rejects a bad invocation before it
// builds the first client, so a guard moved below the case loop fails here
// instead of spending tokens. Every run in it that gets past the guards fails
// in buildClient on the bogus timeout, and PATH holds neither git nor claude.
func TestRunGuardsSpendNoTokens(t *testing.T) {
	t.Setenv("PLANWERK_CLAUDE_TIMEOUT", "bogus")
	t.Setenv("PATH", t.TempDir())

	dir := t.TempDir()
	write := func(rel, content string) string {
		t.Helper()
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
		return path
	}
	write("corpus/sample/base/main.go.txt", "package main\n")
	write("corpus/sample/head/main.go.txt", "package main // changed\n")
	write("corpus/sample/expected.json", `{"description": "seeded", "clean": false, "findings": [{"file": "main.go", "line": 1, "severity": "WARNING", "keywords": ["bug"]}]}`)
	corpus := filepath.Join(dir, "corpus")
	thorough := write("thorough.json", `{"runs": 3, "thorough": true, "cases": [{"name": "sample"}], "aggregate": {"recall": 1}}`)
	comparable := write("comparable.json", `{"runs": 3, "cases": [{"name": "sample"}], "aggregate": {"recall": 1}}`)

	tests := []struct {
		name     string
		baseline string
		runs     int
		want     string
	}{
		{"zero runs", "", 0, "-runs must be at least 1"},
		{"a missing baseline", filepath.Join(dir, "missing.json"), 3, "reading baseline"},
		{"a baseline run with other passes", thorough, 3, "compare like with like"},
		{"a baseline with too few runs on this side", comparable, 1, "at least 3 runs per side"},
		// The two rows that pass the guards prove the premise: the first client
		// build fails, so no row can reach a model.
		{"no baseline passes the guards", "", 1, "invalid PLANWERK_CLAUDE_TIMEOUT"},
		{"a comparable baseline passes the guards", comparable, 3, "invalid PLANWERK_CLAUDE_TIMEOUT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(corpus, "", tt.baseline, eval.Config{Runs: tt.runs}, false)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("run = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

// The tier a test selects in place of a compiled-in default, the effort no
// tier accepts, and the reason resolveTiers gives for it.
const (
	testTierModel   = "sonnet"
	testTierEffort  = "high"
	testBadEffort   = "maximum"
	badEffortReason = ": must be one of low, medium, high, xhigh, max"
)

// clearTierEnv empties every variable resolveTiers and buildClient read, so a
// test sees only the variables it sets itself.
func clearTierEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		envClaudeModel, envClaudeEffort, envFinderModel, envFinderEffort, envStructureModel, envStructureEffort,
		"PLANWERK_CLAUDE_TIMEOUT", "PLANWERK_CLAUDE_INHERIT_USER_CONFIG", "PLANWERK_SHOW_CLAUDE_OUTPUT",
	} {
		t.Setenv(name, "")
	}
}

func TestResolveTiers(t *testing.T) {
	defaults := eval.Tiers{
		ClaudeModel:     claude.DefaultClaudeModel,
		ClaudeEffort:    claude.DefaultClaudeEffort,
		FinderModel:     cmp.Or(claude.DefaultFinderModel, claude.DefaultClaudeModel),
		FinderEffort:    cmp.Or(claude.DefaultFinderEffort, claude.DefaultClaudeEffort),
		StructureModel:  claude.DefaultStructureModel,
		StructureEffort: claude.DefaultStructureEffort,
	}
	with := func(edit func(*eval.Tiers)) eval.Tiers {
		tiers := defaults
		edit(&tiers)
		return tiers
	}
	tests := []struct {
		name    string
		env     map[string]string
		want    eval.Tiers
		wantErr string
	}{
		{name: "every variable unset leaves the compiled-in defaults", want: defaults},
		{
			name: "a finder effort alone moves only the finder effort",
			env:  map[string]string{envFinderEffort: testTierEffort},
			want: with(func(tr *eval.Tiers) { tr.FinderEffort = testTierEffort }),
		},
		{
			name: "a finder model is trimmed",
			env:  map[string]string{envFinderModel: " " + testTierModel + " "},
			want: with(func(tr *eval.Tiers) { tr.FinderModel = testTierModel }),
		},
		{
			name: "the finder model follows the main model when no finder variable is set",
			env:  map[string]string{envClaudeModel: testTierModel},
			want: with(func(tr *eval.Tiers) {
				tr.ClaudeModel = testTierModel
				tr.FinderModel = cmp.Or(claude.DefaultFinderModel, testTierModel)
			}),
		},
		{
			name: "the finder effort follows the main effort when no finder variable is set",
			env:  map[string]string{envClaudeEffort: testTierEffort},
			want: with(func(tr *eval.Tiers) {
				tr.ClaudeEffort = testTierEffort
				tr.FinderEffort = cmp.Or(claude.DefaultFinderEffort, testTierEffort)
			}),
		},
		{
			name: "whitespace counts as unset",
			env:  map[string]string{envClaudeEffort: " "},
			want: defaults,
		},
		{
			name:    "an invalid finder effort names its variable",
			env:     map[string]string{envFinderEffort: testBadEffort},
			wantErr: `invalid PLANWERK_FINDER_EFFORT="maximum"` + badEffortReason,
		},
		{
			name:    "an invalid main effort names its variable",
			env:     map[string]string{envClaudeEffort: testBadEffort},
			wantErr: `invalid PLANWERK_CLAUDE_EFFORT="maximum"` + badEffortReason,
		},
		{
			name:    "an invalid structure effort names its variable",
			env:     map[string]string{envStructureEffort: testBadEffort},
			wantErr: `invalid PLANWERK_STRUCTURE_EFFORT="maximum"` + badEffortReason,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearTierEnv(t)
			for name, v := range tt.env {
				t.Setenv(name, v)
			}
			got, err := resolveTiers()
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Errorf("resolveTiers() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveTiers() = %v, want no error", err)
			}
			if got != tt.want {
				t.Errorf("resolveTiers() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestBuildClientAppliesTheTiers(t *testing.T) {
	tests := []struct {
		name                                    string
		tiers                                   eval.Tiers
		wantFinderModel, wantFinderEffort       string
		wantStructureModel, wantStructureEffort string
	}{
		{
			name: "every tier set runs the finders and the structuring on their tiers",
			tiers: eval.Tiers{
				ClaudeModel: "opus", ClaudeEffort: "xhigh",
				FinderModel: testTierModel, FinderEffort: testTierEffort,
				StructureModel: "opus", StructureEffort: testTierEffort,
			},
			wantFinderModel:     testTierModel,
			wantFinderEffort:    testTierEffort,
			wantStructureModel:  "opus",
			wantStructureEffort: testTierEffort,
		},
		{
			name:                "the main tier reaches the client and the finders inherit it",
			tiers:               eval.Tiers{ClaudeModel: testTierModel, ClaudeEffort: testTierEffort},
			wantFinderModel:     cmp.Or(claude.DefaultFinderModel, testTierModel),
			wantFinderEffort:    cmp.Or(claude.DefaultFinderEffort, testTierEffort),
			wantStructureModel:  claude.DefaultStructureModel,
			wantStructureEffort: claude.DefaultStructureEffort,
		},
		{
			name:                "the zero tiers leave the compiled-in defaults",
			wantFinderModel:     cmp.Or(claude.DefaultFinderModel, claude.DefaultClaudeModel),
			wantFinderEffort:    cmp.Or(claude.DefaultFinderEffort, claude.DefaultClaudeEffort),
			wantStructureModel:  claude.DefaultStructureModel,
			wantStructureEffort: claude.DefaultStructureEffort,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearTierEnv(t)
			client, err := buildClient(tt.tiers)
			if err != nil {
				t.Fatalf("buildClient = %v, want a client", err)
			}
			if model, effort := client.FinderTier(); model != tt.wantFinderModel || effort != tt.wantFinderEffort {
				t.Errorf("FinderTier() = %q/%q, want %q/%q", model, effort, tt.wantFinderModel, tt.wantFinderEffort)
			}
			if model, effort := client.StructureTier(); model != tt.wantStructureModel || effort != tt.wantStructureEffort {
				t.Errorf("StructureTier() = %q/%q, want %q/%q", model, effort, tt.wantStructureModel, tt.wantStructureEffort)
			}
		})
	}

	t.Run("a bogus timeout is still rejected", func(t *testing.T) {
		clearTierEnv(t)
		t.Setenv("PLANWERK_CLAUDE_TIMEOUT", "bogus")
		if _, err := buildClient(eval.Tiers{}); err == nil || !strings.Contains(err.Error(), "invalid PLANWERK_CLAUDE_TIMEOUT") {
			t.Errorf("buildClient = %v, want an invalid PLANWERK_CLAUDE_TIMEOUT error", err)
		}
	})
}

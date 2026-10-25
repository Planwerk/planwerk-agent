package main

import (
	"cmp"
	"errors"
	"fmt"
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
// instead of spending tokens. Every corpus run in it that gets past the guards
// fails in buildClient on the bogus timeout, every AACR-Bench run in
// PrepareAACR, and PATH holds neither git nor claude.
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
	comparable := write("comparable.json", fmt.Sprintf(`{"runs": 3, "match": %q, "cases": [{"name": "sample"}], "aggregate": {"recall": 1}}`, eval.MatchRule))
	unrecordedMatch := write("unrecorded-match.json", `{"runs": 3, "cases": [{"name": "sample"}], "aggregate": {"recall": 1}}`)
	export := write("aacr.json", `[{"project_main_language": "Go", "pr_url": "https://github.com/acme/widget/pull/7",
		"pr_source_commit": "1111111111111111111111111111111111111111", "pr_target_commit": "2222222222222222222222222222222222222222",
		"note": "a race", "path": "store.go", "side": "right", "from_line": 4, "to_line": 6, "context": "Diff Level", "label": 1}]`)
	bench := aacrOptions{export: export, sample: 1, cacheDir: filepath.Join(dir, "cache")}
	with := func(edit func(*aacrOptions)) aacrOptions {
		o := bench
		edit(&o)
		return o
	}

	tests := []struct {
		name     string
		baseline string
		runs     int
		aacr     aacrOptions
		want     string
	}{
		{"zero runs", "", 0, aacrOptions{}, "-runs must be at least 1"},
		{"a missing baseline", filepath.Join(dir, "missing.json"), 3, aacrOptions{}, "reading baseline"},
		{"a baseline run with other passes", thorough, 3, aacrOptions{}, "compare like with like"},
		{"a baseline with too few runs on this side", comparable, 1, aacrOptions{}, "at least 3 runs per side"},
		{"a baseline with no recorded match rule", unrecordedMatch, 3, aacrOptions{}, "match rule"},
		{"-judge without -aacr", "", 1, aacrOptions{judge: true}, "-judge needs -aacr"},
		{"-aacr-sample without -aacr", "", 1, aacrOptions{sample: 2, aacrSampleSet: true}, "-aacr-sample needs -aacr"},
		{"-aacr-cache without -aacr", "", 1, aacrOptions{cacheDir: filepath.Join(dir, "cache"), aacrCacheSet: true}, "-aacr-cache needs -aacr"},
		{"-verifier-sample without -verifier-bench", "", 1, with(func(o *aacrOptions) { o.verifierSampleSet = true }), "-verifier-sample needs -verifier-bench"},
		{"-aacr-sample 0", "", 1, with(func(o *aacrOptions) { o.sample = 0 }), "-aacr-sample must be at least 1, got 0"},
		{"a missing export", "", 1, with(func(o *aacrOptions) { o.export = filepath.Join(dir, "missing-export.json") }), "reading AACR-Bench export"},
		{"an explicit -corpus with -aacr", "", 1, with(func(o *aacrOptions) { o.corpusSet = true }), "-aacr and -corpus"},
		{"-aacr with a comparable baseline", comparable, 3, bench, "periodic benchmark"},
		// The rows that pass the guards prove the premise: the first client
		// build fails, or for an AACR-Bench run the fetch before it, so no row
		// can reach a model.
		{"no baseline passes the guards", "", 1, aacrOptions{}, "invalid PLANWERK_CLAUDE_TIMEOUT"},
		{"a comparable baseline passes the guards", comparable, 3, aacrOptions{}, "invalid PLANWERK_CLAUDE_TIMEOUT"},
		{"an AACR-Bench run prepares its repositories before any client", "", 1, bench, `"git": executable file not found`},
		{"a judged AACR-Bench run prepares before it builds the judge", "", 1, with(func(o *aacrOptions) { o.judge = true }), `"git": executable file not found`},
		{"an explicit -aacr-sample and -aacr-cache pass the guards with -aacr", "", 1, with(func(o *aacrOptions) { o.aacrSampleSet, o.aacrCacheSet = true, true }), `"git": executable file not found`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(corpus, "", tt.baseline, eval.Config{Runs: tt.runs}, tt.aacr, false)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("run = %v, want an error containing %q", err, tt.want)
			}
		})
	}

	t.Run("an unknown -case with -aacr", func(t *testing.T) {
		if err := run(corpus, "widget-8", "", eval.Config{Runs: 1}, bench, false); err == nil || !strings.Contains(err.Error(), `no AACR-Bench case "widget-8"`) {
			t.Errorf("run = %v, want a no-such-case error", err)
		}
	})
	t.Run("-aacr-sample with -case", func(t *testing.T) {
		sampled := with(func(o *aacrOptions) { o.aacrSampleSet = true })
		if err := run(corpus, "widget-7", "", eval.Config{Runs: 1}, sampled, false); err == nil || !strings.Contains(err.Error(), "-aacr-sample and -case each choose the cases to score") {
			t.Errorf("run = %v, want -aacr-sample refused beside -case", err)
		}
	})
	t.Run("a -case with -aacr passes the guards", func(t *testing.T) {
		if err := run(corpus, "widget-7", "", eval.Config{Runs: 1}, bench, false); err == nil || !strings.Contains(err.Error(), `"git": executable file not found`) {
			t.Errorf("run = %v, want the fetch of the named case to fail", err)
		}
	})
}

func TestVerifierBenchConflicts(t *testing.T) {
	if err := verifierBenchConflicts(map[string]bool{}); err != nil {
		t.Errorf("no flag set = %v, want nil", err)
	}
	for _, name := range []string{"baseline", "case", "corpus", "runs", "thorough", "specialists", "aacr-sample", "judge", "scope-patterns"} {
		t.Run("-"+name+" is refused", func(t *testing.T) {
			err := verifierBenchConflicts(map[string]bool{name: true, "aacr": true})
			if want := "-" + name + " does not apply to -verifier-bench"; err == nil || err.Error() != want {
				t.Errorf("verifierBenchConflicts = %v, want %q", err, want)
			}
		})
	}
	t.Run("the bench's own flags pass", func(t *testing.T) {
		set := map[string]bool{"aacr": true, "aacr-cache": true, "verifier-bench": true, "verifier-sample": true, "json": true}
		if err := verifierBenchConflicts(set); err != nil {
			t.Errorf("verifierBenchConflicts = %v, want nil", err)
		}
	})
	t.Run("the first refused flag is named", func(t *testing.T) {
		if err := verifierBenchConflicts(map[string]bool{"judge": true, "runs": true}); err == nil || !strings.HasPrefix(err.Error(), "-runs ") {
			t.Errorf("verifierBenchConflicts = %v, want -runs named first", err)
		}
	})
}

// TestRunVerifierBenchGuardsSpendNoTokens locks that runVerifierBench rejects
// a bad invocation before its first checkout, as TestRunGuardsSpendNoTokens
// does for run. The valid export fails in buildClient on the bogus timeout,
// which runs before the first fetch, and PATH holds neither git nor claude.
func TestRunVerifierBenchGuardsSpendNoTokens(t *testing.T) {
	t.Setenv("PLANWERK_CLAUDE_TIMEOUT", "bogus")
	t.Setenv("PATH", t.TempDir())

	dir := t.TempDir()
	write := func(name, rows string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("["+rows+"]"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return path
	}
	const row = `{"project_main_language": "Go", "pr_url": "https://github.com/acme/widget/pull/7",
		"pr_source_commit": "1111111111111111111111111111111111111111", "pr_target_commit": "2222222222222222222222222222222222222222",
		"note": "a race", "path": "store.go", "side": %q, "from_line": 4, "to_line": 6, "category": %q, "context": "Diff Level", "label": 1}`
	valid := write("dataset.json", fmt.Sprintf(row, "right", "Code Defect"))
	// A right-side verified comment keeps the export loadable; neither row is
	// one the gate would see.
	ineligible := write("ineligible.json", fmt.Sprintf(row, "right", "Maintainability and Readability")+","+fmt.Sprintf(row, "left", "Code Defect"))

	tests := []struct {
		name     string
		path     string
		perLabel int
		want     string
	}{
		{"no -aacr", "", 20, "-verifier-bench needs -aacr <dataset.json>"},
		{"-verifier-sample 0", valid, 0, "-verifier-sample must be at least 1, got 0"},
		{"a missing export", filepath.Join(dir, "missing.json"), 20, "reading AACR-Bench export"},
		{"an export with no eligible comment", ineligible, 20, "no AACR-Bench comment is eligible for the verifier bench"},
		// The row that passes the guards proves the premise: the client is
		// built, and fails, before the first fetch.
		{"a valid export builds the client before any fetch", valid, 20, "invalid PLANWERK_CLAUDE_TIMEOUT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runVerifierBench(tt.path, filepath.Join(dir, "cache"), tt.perLabel, eval.Tiers{}, false)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("runVerifierBench = %v, want an error containing %q", err, tt.want)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(dir, "cache")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stat cache = %v, want no cache created by a run that stopped at a guard", err)
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
		"PLANWERK_CLAUDE_TIMEOUT", "PLANWERK_CLAUDE_INHERIT_USER_CONFIG", "PLANWERK_DIFF_EXCLUSIONS", "PLANWERK_SHOW_CLAUDE_OUTPUT",
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

	t.Run("PLANWERK_DIFF_EXCLUSIONS turns the exclusions on", func(t *testing.T) {
		clearTierEnv(t)
		// The CLI's vocabulary (lookupBoolEnv), in any letter case.
		for v, want := range map[string]bool{
			"": false, "1": true, "true": true, "TRUE": true, "yes": true, "YES": true, "on": true, "On": true,
			"0": false, "false": false, "no": false, "off": false,
		} {
			t.Setenv("PLANWERK_DIFF_EXCLUSIONS", v)
			client, err := buildClient(eval.Tiers{})
			if err != nil {
				t.Fatalf("PLANWERK_DIFF_EXCLUSIONS=%q: buildClient = %v, want a client", v, err)
			}
			if got := client.DiffExclusions(); got != want {
				t.Errorf("PLANWERK_DIFF_EXCLUSIONS=%q: DiffExclusions() = %v, want %v", v, got, want)
			}
		}
	})

	t.Run("a bogus PLANWERK_DIFF_EXCLUSIONS is rejected", func(t *testing.T) {
		clearTierEnv(t)
		// "t" is a strconv.ParseBool value the CLI does not read as true.
		for _, v := range []string{"maybe", "t"} {
			t.Setenv("PLANWERK_DIFF_EXCLUSIONS", v)
			if _, err := buildClient(eval.Tiers{}); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("invalid PLANWERK_DIFF_EXCLUSIONS=%q", v)) {
				t.Errorf("PLANWERK_DIFF_EXCLUSIONS=%q: buildClient = %v, want an error naming PLANWERK_DIFF_EXCLUSIONS", v, err)
			}
		}
	})

	t.Run("PLANWERK_CLAUDE_INHERIT_USER_CONFIG takes the CLI's values", func(t *testing.T) {
		clearTierEnv(t)
		for _, v := range []string{"yes", "on", "no", "OFF"} {
			t.Setenv("PLANWERK_CLAUDE_INHERIT_USER_CONFIG", v)
			if _, err := buildClient(eval.Tiers{}); err != nil {
				t.Errorf("PLANWERK_CLAUDE_INHERIT_USER_CONFIG=%q: buildClient = %v, want a client", v, err)
			}
		}
		t.Setenv("PLANWERK_CLAUDE_INHERIT_USER_CONFIG", "t")
		if _, err := buildClient(eval.Tiers{}); err == nil || !strings.Contains(err.Error(), `invalid PLANWERK_CLAUDE_INHERIT_USER_CONFIG="t"`) {
			t.Errorf("buildClient = %v, want an error naming PLANWERK_CLAUDE_INHERIT_USER_CONFIG", err)
		}
	})
}

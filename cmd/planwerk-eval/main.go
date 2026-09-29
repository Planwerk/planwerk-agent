// Command planwerk-eval is a dev-only harness that scores the review pipeline's
// output quality against a labeled seeded-bug corpus. It is kept out of the
// shipped planwerk-agent CLI on purpose: it invokes the real claude CLI and
// spends tokens, so it never runs in unit CI. Run it via `make eval`.
//
// It exits non-zero only on a harness error (bad corpus, git failure, pipeline
// or JSON error) — never because scores are low. A low score is a signal to
// read, not a build break. The same holds for -baseline: a REGRESSED verdict
// is printed beside the scores, and only a baseline that cannot be loaded or
// compared is an error.
package main

import (
	"cmp"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/eval"
)

func main() {
	corpusDir := flag.String("corpus", filepath.FromSlash("internal/eval/corpus"), "path to the corpus directory")
	caseName := flag.String("case", "", "run a single case by directory name (default: all cases)")
	thorough := flag.Bool("thorough", false, "run the adversarial (thorough) review pass too")
	specialists := flag.Bool("specialists", false, "run the domain specialist fan-out too")
	runs := flag.Int("runs", 1, "run each case N times, each with a fresh client, and pool the scores")
	baseline := flag.String("baseline", "", fmt.Sprintf("compare against a report written by -json (at least %d runs per side)", eval.MinCompareRuns))
	jsonOut := flag.Bool("json", false, "emit the score report as JSON instead of a table")
	flag.Parse()

	tiers, err := resolveTiers()
	if err != nil {
		fmt.Fprintln(os.Stderr, "planwerk-eval:", err)
		os.Exit(1)
	}
	cfg := eval.Config{Runs: *runs, RunOptions: eval.RunOptions{Thorough: *thorough, Specialists: *specialists}, Tiers: tiers}
	if err := run(*corpusDir, *caseName, *baseline, cfg, *jsonOut); err != nil {
		fmt.Fprintln(os.Stderr, "planwerk-eval:", err)
		os.Exit(1)
	}
}

func run(corpusDir, caseName, baselinePath string, cfg eval.Config, jsonOut bool) error {
	if cfg.Runs < 1 {
		return fmt.Errorf("-runs must be at least 1, got %d", cfg.Runs)
	}
	cases, err := loadCases(corpusDir, caseName)
	if err != nil {
		return err
	}

	// Check the baseline before the first case runs, so a mismatch spends no
	// tokens.
	var baseline *eval.Report
	if baselinePath != "" {
		b, err := eval.LoadReport(baselinePath)
		if err != nil {
			return err
		}
		names := make([]string, 0, len(cases))
		for _, c := range cases {
			names = append(names, c.Name)
		}
		if err := eval.Comparable(b, cfg, names); err != nil {
			return err
		}
		baseline = &b
	}

	scored := make([]eval.Scored, 0, len(cases))
	for _, c := range cases {
		runs := make([]eval.Run, 0, cfg.Runs)
		for i := 1; i <= cfg.Runs; i++ {
			fmt.Fprintf(os.Stderr, "running case %s (run %d/%d) ...\n", c.Name, i, cfg.Runs)
			r, err := retry(func() (eval.Run, error) {
				// A fresh client per attempt: RunCase reads the client's cumulative usage.
				client, err := buildClient(cfg.Tiers)
				if err != nil {
					return eval.Run{}, err
				}
				return eval.RunCase(client, c, cfg.RunOptions)
			})
			if err != nil {
				return err
			}
			runs = append(runs, r)
		}
		scored = append(scored, eval.ScoreRuns(c, runs))
	}

	rep := eval.BuildReport(scored, cfg)
	if baseline != nil {
		// A REGRESSED verdict is printed for a person to read, never exited on:
		// the exit code reports harness errors only (decision 59).
		cmp, err := eval.Compare(*baseline, rep)
		if err != nil {
			return err
		}
		rep.Comparison = &cmp
	}
	if jsonOut {
		return eval.RenderJSON(os.Stdout, rep)
	}
	eval.RenderTable(os.Stdout, rep)
	if rep.Comparison != nil {
		eval.RenderComparison(os.Stdout, *rep.Comparison)
	}
	return nil
}

// maxRunAttempts bounds the attempts at one run of one case. A second attempt
// keeps one transient failure (a timeout, a malformed reply) from discarding
// every run the eval already paid for; a second failure ends the eval.
const maxRunAttempts = 2

// retry calls attempt until it succeeds or maxRunAttempts attempts have failed,
// and returns the last attempt's error when none succeeded.
func retry(attempt func() (eval.Run, error)) (eval.Run, error) {
	var err error
	for n := 1; n <= maxRunAttempts; n++ {
		var r eval.Run
		if r, err = attempt(); err == nil {
			return r, nil
		}
		if n < maxRunAttempts {
			fmt.Fprintf(os.Stderr, "planwerk-eval: %v; retrying\n", err)
		}
	}
	return eval.Run{}, err
}

// loadCases loads the whole corpus, or a single case when caseName is set.
func loadCases(corpusDir, caseName string) ([]eval.Case, error) {
	if caseName != "" {
		c, err := eval.LoadCase(filepath.Join(corpusDir, caseName))
		if err != nil {
			return nil, err
		}
		return []eval.Case{c}, nil
	}
	return eval.LoadCorpus(corpusDir)
}

// The model and effort variables of each tier, named as the root command
// names them. resolveTiers is the one place the eval reads them.
const (
	envClaudeModel     = "PLANWERK_CLAUDE_MODEL"
	envClaudeEffort    = "PLANWERK_CLAUDE_EFFORT"
	envFinderModel     = "PLANWERK_FINDER_MODEL"
	envFinderEffort    = "PLANWERK_FINDER_EFFORT"
	envStructureModel  = "PLANWERK_STRUCTURE_MODEL"
	envStructureEffort = "PLANWERK_STRUCTURE_EFFORT"
)

// resolveTiers resolves the model and effort of the main, finder and
// structure tiers from their PLANWERK_* variables. A variable that is unset
// or only whitespace leaves the compiled-in default, and an empty finder
// value inherits the main tier as claude.Client.FinderTier resolves it, so the
// recorded tier is the tier that runs. It rejects an effort that is not one
// of the levels Claude Code accepts; a compiled-in default is never invalid,
// so an unset variable cannot fail. main calls it before the first case runs.
func resolveTiers() (eval.Tiers, error) {
	env := func(name string) string { return strings.TrimSpace(os.Getenv(name)) }
	model := cmp.Or(env(envClaudeModel), claude.DefaultClaudeModel)
	effort := cmp.Or(env(envClaudeEffort), claude.DefaultClaudeEffort)
	finderModel, finderEffort := claude.NewClient(
		claude.WithModel(model), claude.WithEffort(effort),
		claude.WithFinderModel(env(envFinderModel)), claude.WithFinderEffort(env(envFinderEffort)),
	).FinderTier()
	t := eval.Tiers{
		ClaudeModel:     model,
		ClaudeEffort:    effort,
		FinderModel:     finderModel,
		FinderEffort:    finderEffort,
		StructureModel:  cmp.Or(env(envStructureModel), claude.DefaultStructureModel),
		StructureEffort: cmp.Or(env(envStructureEffort), claude.DefaultStructureEffort),
	}
	for _, e := range []struct{ name, value string }{
		{envClaudeEffort, t.ClaudeEffort},
		{envFinderEffort, t.FinderEffort},
		{envStructureEffort, t.StructureEffort},
	} {
		if !claude.ValidEffort(e.value) {
			return eval.Tiers{}, fmt.Errorf("invalid %s=%q: must be one of %s", e.name, e.value, claude.EffortLevels())
		}
	}
	return t, nil
}

// buildClient constructs a Claude client on tiers, then applies the timeout,
// inherit-user-config and show-output overrides the shipped CLI honors, each
// only when set so the compiled-in defaults otherwise stand. The model and
// effort variables are read in resolveTiers alone. It is the minimal mirror of
// the root command's resolve* helpers, which are unexported in
// cmd/planwerk-agent. Every tier option ignores an empty value, so the zero
// Tiers yields the compiled-in defaults.
func buildClient(tiers eval.Tiers) (*claude.Client, error) {
	opts := []claude.Option{
		claude.WithModel(tiers.ClaudeModel),
		claude.WithEffort(tiers.ClaudeEffort),
		claude.WithStructureModel(tiers.StructureModel),
		claude.WithStructureEffort(tiers.StructureEffort),
		claude.WithFinderModel(tiers.FinderModel),
		claude.WithFinderEffort(tiers.FinderEffort),
	}
	if v := strings.TrimSpace(os.Getenv("PLANWERK_CLAUDE_TIMEOUT")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("invalid PLANWERK_CLAUDE_TIMEOUT=%q: %w", v, err)
		}
		opts = append(opts, claude.WithTimeout(d))
	}
	if v := strings.TrimSpace(os.Getenv("PLANWERK_CLAUDE_INHERIT_USER_CONFIG")); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid PLANWERK_CLAUDE_INHERIT_USER_CONFIG=%q: %w", v, err)
		}
		opts = append(opts, claude.WithInheritUserConfig(b))
	}
	if v := strings.TrimSpace(os.Getenv("PLANWERK_SHOW_CLAUDE_OUTPUT")); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			opts = append(opts, claude.WithShowOutput(b))
		}
	}

	return claude.NewClient(opts...), nil
}

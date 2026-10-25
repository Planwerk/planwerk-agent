// Command planwerk-eval is a dev-only harness that scores the review pipeline's
// output quality against a labeled seeded-bug corpus or, with -aacr, against a
// sample of AACR-Bench's real pull requests. With -verifier-bench it scores no
// review: it measures the claim verifier on AACR-Bench's expert-labeled
// comments instead (decision 148). It is kept out of the shipped
// planwerk-agent CLI on purpose: it invokes the real claude CLI and spends
// tokens, so it never runs in unit CI. Run it via `make eval`.
//
// It exits non-zero only on a harness error (bad corpus, git failure, pipeline
// or JSON error) — never because scores are low. A low score is a signal to
// read, not a build break. The same holds for -baseline: a REGRESSED verdict
// is printed beside the scores, and only a baseline that cannot be loaded or
// compared is an error. A benchmark run is never compared against a baseline:
// -baseline refuses it (decision 147). The verifier bench follows the same
// rule: its refute rates are printed, never exited on.
package main

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/eval"
	"github.com/planwerk/planwerk-agent/internal/hygiene"
	"github.com/planwerk/planwerk-agent/internal/report"
)

func main() {
	corpusDir := flag.String("corpus", filepath.FromSlash("internal/eval/corpus"), "path to the corpus directory")
	caseName := flag.String("case", "", "run a single case by directory name (default: all cases)")
	thorough := flag.Bool("thorough", false, "run the adversarial (thorough) review pass too")
	specialists := flag.Bool("specialists", false, "run the domain specialist fan-out too")
	scopePatterns := flag.Bool("scope-patterns", false, "run the review with --scope-patterns (the pattern bodies chosen from the changed files)")
	runs := flag.Int("runs", 1, "run each case N times, each with a fresh client, and pool the scores")
	baseline := flag.String("baseline", "", fmt.Sprintf("compare against a report written by -json (at least %d runs per side)", eval.MinCompareRuns))
	jsonOut := flag.Bool("json", false, "emit the score report as JSON instead of a table")
	aacr := flag.String("aacr", "", "score an AACR-Bench sample instead of the corpus: path to a downloaded dataset.json export")
	aacrSample := flag.Int("aacr-sample", 10, "with -aacr, the number of pull requests to sample, stratified by language and context")
	aacrCache := flag.String("aacr-cache", eval.DefaultAACRCacheDir(), "with -aacr, the directory the benchmark repositories are fetched into")
	judge := flag.Bool("judge", false, "with -aacr, also ask the structure tier whether each line-overlapping pair raises the same concern")
	verifierBench := flag.Bool("verifier-bench", false, "measure the claim verifier on AACR-Bench's labeled comments instead of scoring the review (needs -aacr)")
	verifierSample := flag.Int("verifier-sample", 20, "comments drawn per label for -verifier-bench")
	flag.Parse()
	set := setFlags()

	if *verifierBench {
		err := verifierBenchConflicts(set)
		var tiers eval.Tiers
		if err == nil {
			tiers, err = resolveTiers()
		}
		if err == nil {
			err = runVerifierBench(*aacr, *aacrCache, *verifierSample, tiers, *jsonOut)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "planwerk-eval:", err)
			os.Exit(1)
		}
		return
	}

	tiers, err := resolveTiers()
	if err != nil {
		fmt.Fprintln(os.Stderr, "planwerk-eval:", err)
		os.Exit(1)
	}
	cfg := eval.Config{Runs: *runs, RunOptions: eval.RunOptions{Thorough: *thorough, Specialists: *specialists, ScopePatterns: *scopePatterns}, Tiers: tiers}
	bench := aacrOptions{
		export: *aacr, sample: *aacrSample, cacheDir: *aacrCache, judge: *judge,
		corpusSet: set["corpus"], aacrSampleSet: set["aacr-sample"], aacrCacheSet: set["aacr-cache"],
		verifierSampleSet: set["verifier-sample"],
	}
	if err := run(*corpusDir, *caseName, *baseline, cfg, bench, *jsonOut); err != nil {
		fmt.Fprintln(os.Stderr, "planwerk-eval:", err)
		os.Exit(1)
	}
}

// aacrOptions carries the AACR-Bench flags: the export to sample (empty for a
// corpus run), the sample size, the cache directory, whether to judge each
// pair, and whether -corpus, -aacr-sample, -aacr-cache and -verifier-sample
// were given explicitly. The zero value runs the corpus.
type aacrOptions struct {
	export            string
	sample            int
	cacheDir          string
	judge             bool
	corpusSet         bool
	aacrSampleSet     bool
	aacrCacheSet      bool
	verifierSampleSet bool
}

// check rejects a combination of the AACR-Bench flags, and of them with
// caseName, that cannot run or that would leave a flag given explicitly
// ignored: -verifier-sample, which a run that scores the review ignores,
// -aacr-sample, -aacr-cache and -judge on a corpus run, and -aacr-sample
// beside -case, which picks any candidate of the export.
func (o aacrOptions) check(caseName string) error {
	if o.verifierSampleSet {
		return errors.New("-verifier-sample needs -verifier-bench")
	}
	if o.export == "" {
		switch {
		case o.judge:
			return errors.New("-judge needs -aacr: the corpus matches on keywords")
		case o.aacrSampleSet:
			return errors.New("-aacr-sample needs -aacr")
		case o.aacrCacheSet:
			return errors.New("-aacr-cache needs -aacr")
		}
		return nil
	}
	if o.corpusSet {
		return errors.New("-aacr and -corpus each name the cases to score; pass one")
	}
	if o.aacrSampleSet && caseName != "" {
		return errors.New("-aacr-sample and -case each choose the cases to score; pass one")
	}
	if o.sample < 1 {
		return fmt.Errorf("-aacr-sample must be at least 1, got %d", o.sample)
	}
	return nil
}

func run(corpusDir, caseName, baselinePath string, cfg eval.Config, aacr aacrOptions, jsonOut bool) error {
	if cfg.Runs < 1 {
		return fmt.Errorf("-runs must be at least 1, got %d", cfg.Runs)
	}
	if err := aacr.check(caseName); err != nil {
		return err
	}
	var cases []eval.Case
	var err error
	if aacr.export != "" {
		cases, cfg.AACR, err = loadAACRCases(aacr, caseName)
	} else {
		cases, err = loadCases(corpusDir, caseName)
	}
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

	if cfg.AACR != nil {
		// Fetch every sampled repository before the first client is built, so
		// a pull request that cannot be fetched spends no tokens.
		if err := eval.PrepareAACR(aacr.cacheDir, cases, os.Stderr); err != nil {
			return err
		}
	}
	// One client judges every run, so its cumulative usage is the judge's
	// cost.
	var judgeClient *claude.Client
	var judge eval.SameIntentFunc
	if aacr.judge {
		if judgeClient, err = buildClient(cfg.Tiers); err != nil {
			return err
		}
		judge = judgeWith(judgeClient)
	}

	var judged eval.JudgeTally
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
			if judge != nil {
				fmt.Fprintf(os.Stderr, "judging case %s (run %d/%d) ...\n", c.Name, i, cfg.Runs)
				t, err := retry(func() (eval.JudgeTally, error) { return eval.JudgeRun(c, &r, judge) })
				if err != nil {
					return err
				}
				judged.Pairs += t.Pairs
				judged.Same += t.Same
			}
			runs = append(runs, r)
		}
		scored = append(scored, eval.ScoreRuns(c, runs))
	}

	rep := eval.BuildReport(scored, cfg)
	if judgeClient != nil {
		rep.Judge = &eval.JudgeSummary{Pairs: judged.Pairs, Same: judged.Same, Usage: judgeClient.UsageTotals()}
	}
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

// maxRunAttempts bounds the attempts at one run of one case, at judging one
// run, and at one call of the verifier bench. A second attempt keeps one
// transient failure (a timeout, a malformed reply) from discarding every run
// the eval already paid for; a second failure ends the eval.
const maxRunAttempts = 2

// retry calls attempt until it succeeds or maxRunAttempts attempts have failed,
// and returns the last attempt's error when none succeeded.
func retry[T any](attempt func() (T, error)) (T, error) {
	var zero T
	var err error
	for n := 1; n <= maxRunAttempts; n++ {
		var r T
		if r, err = attempt(); err == nil {
			return r, nil
		}
		if n < maxRunAttempts {
			fmt.Fprintf(os.Stderr, "planwerk-eval: %v; retrying\n", err)
		}
	}
	return zero, err
}

// setFlags returns the names of the flags the command line set.
func setFlags() map[string]bool {
	set := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

// verifierBenchRefuses lists the flags -verifier-bench refuses, in the order
// verifierBenchConflicts checks them: each selects or shapes review runs, and
// the bench makes none.
var verifierBenchRefuses = []string{"baseline", "case", "corpus", "runs", "thorough", "specialists", "aacr-sample", "judge", "scope-patterns"}

// verifierBenchConflicts returns an error naming the first flag of
// verifierBenchRefuses that set holds, so a flag the bench would ignore is
// not taken for one it honors.
func verifierBenchConflicts(set map[string]bool) error {
	for _, name := range verifierBenchRefuses {
		if set[name] {
			return fmt.Errorf("-%s does not apply to -verifier-bench", name)
		}
	}
	return nil
}

// runVerifierBench measures the claim verifier on perLabel comments of each
// expert label drawn from the AACR-Bench export at datasetPath, checking each
// revision out of the cache under cacheDir (decision 148). It checks its
// arguments, loads and samples the export, and builds the client before the
// first checkout, so a bad invocation or PLANWERK_* variable fails before any
// fetch or call. One client verifies every comment, so its cumulative usage
// is the bench's cost. Each verifier call is retried once; a second failure
// ends the bench.
func runVerifierBench(datasetPath, cacheDir string, perLabel int, tiers eval.Tiers, jsonOut bool) error {
	if datasetPath == "" {
		return errors.New("-verifier-bench needs -aacr <dataset.json>")
	}
	if perLabel < 1 {
		return fmt.Errorf("-verifier-sample must be at least 1, got %d", perLabel)
	}
	exp, err := eval.LoadAACR(datasetPath)
	if err != nil {
		return err
	}
	sample, excluded := eval.SampleVerifierComments(exp.Rows, perLabel)
	if len(sample) == 0 {
		return errors.New("no AACR-Bench comment is eligible for the verifier bench")
	}
	client, err := buildClient(tiers)
	if err != nil {
		return err
	}
	checkout := func(prURL, commit string) (string, func(), error) {
		return eval.CheckoutAACRHead(cacheDir, prURL, commit)
	}
	verify := func(dir string, findings []report.Finding) ([]hygiene.ClaimVerdict, error) {
		return retry(func() ([]hygiene.ClaimVerdict, error) { return client.VerifyFindingClaims(dir, findings) })
	}
	rep, err := eval.RunVerifierBench(sample, checkout, verify, os.Stderr)
	if err != nil {
		return err
	}
	rep.PerLabel, rep.Excluded, rep.Tiers, rep.Usage = perLabel, excluded, tiers, client.UsageTotals()
	if jsonOut {
		return eval.RenderVerifierJSON(os.Stdout, rep)
	}
	eval.RenderVerifierTable(os.Stdout, rep)
	return nil
}

// judgeWith returns the eval.SameIntentFunc that asks client whether a
// prediction raises the same concern as an AACR-Bench reference comment.
func judgeWith(client *claude.Client) eval.SameIntentFunc {
	return func(pred report.Finding, exp eval.ExpectedFinding) (bool, error) {
		return client.JudgeSameIntent(claude.SameIntentPair{Path: exp.File, From: exp.Line, To: exp.LineEnd, Reference: exp.Comment, Finding: pred})
	}
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

// loadAACRCases loads the AACR-Bench export o names and returns the case
// caseName names among all its candidates, or else o.sample of them
// (eval.SampleAACR), with the report's record of what was scored.
func loadAACRCases(o aacrOptions, caseName string) ([]eval.Case, *eval.AACRConfig, error) {
	exp, err := eval.LoadAACR(o.export)
	if err != nil {
		return nil, nil, err
	}
	var cases []eval.Case
	if caseName != "" {
		c, err := eval.FindAACRCase(exp.Cases, caseName)
		if err != nil {
			return nil, nil, err
		}
		cases = []eval.Case{c}
	} else {
		cases = eval.SampleAACR(exp.Cases, o.sample)
	}
	return cases, &eval.AACRConfig{
		Export:     filepath.Base(o.export),
		SHA256:     exp.SHA256,
		Sample:     len(cases),
		Candidates: len(exp.Cases),
		Judge:      o.judge,
	}, nil
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
// inherit-user-config, diff-exclusions and show-output overrides the shipped
// CLI honors, each only when set so the compiled-in defaults otherwise stand.
// The model and effort variables are read in resolveTiers alone. It is the
// minimal mirror of the root command's resolve* helpers, which are unexported
// in cmd/planwerk-agent. Every tier option ignores an empty value, so the zero
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
		b, err := parseBoolEnv("PLANWERK_CLAUDE_INHERIT_USER_CONFIG", v)
		if err != nil {
			return nil, err
		}
		opts = append(opts, claude.WithInheritUserConfig(b))
	}
	if v := strings.TrimSpace(os.Getenv("PLANWERK_DIFF_EXCLUSIONS")); v != "" {
		b, err := parseBoolEnv("PLANWERK_DIFF_EXCLUSIONS", v)
		if err != nil {
			return nil, err
		}
		opts = append(opts, claude.WithDiffExclusions(b))
	}
	if v := strings.TrimSpace(os.Getenv("PLANWERK_SHOW_CLAUDE_OUTPUT")); v != "" {
		if b, err := parseBoolEnv("PLANWERK_SHOW_CLAUDE_OUTPUT", v); err == nil {
			opts = append(opts, claude.WithShowOutput(b))
		}
	}

	return claude.NewClient(opts...), nil
}

// parseBoolEnv parses the value v of the boolean environment variable name in
// the vocabulary the shipped CLI's lookupBoolEnv reads, in any letter case:
// 1, true, yes, on, and 0, false, no, off. Any other value is an error naming
// the variable, so a typo fails before the first case spends tokens.
// lookupBoolEnv lives in cmd/planwerk-agent's package main, which the eval
// cannot import.
func parseBoolEnv(name, v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("invalid %s=%q: want one of 1, true, yes, on, 0, false, no, off", name, v)
}

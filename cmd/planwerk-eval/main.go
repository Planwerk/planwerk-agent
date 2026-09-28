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

	cfg := eval.Config{Runs: *runs, RunOptions: eval.RunOptions{Thorough: *thorough, Specialists: *specialists}}
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
				client, err := buildClient()
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

// buildClient constructs a Claude client from the same PLANWERK_* env overrides
// the shipped CLI honors, applying each only when set so the compiled-in
// defaults otherwise stand. It is the minimal mirror of the root command's
// resolve* helpers, which are unexported in cmd/planwerk-agent.
func buildClient() (*claude.Client, error) {
	var opts []claude.Option

	if v := strings.TrimSpace(os.Getenv("PLANWERK_CLAUDE_MODEL")); v != "" {
		opts = append(opts, claude.WithModel(v))
	}
	if v := strings.TrimSpace(os.Getenv("PLANWERK_CLAUDE_EFFORT")); v != "" {
		opts = append(opts, claude.WithEffort(v))
	}
	if v := strings.TrimSpace(os.Getenv("PLANWERK_STRUCTURE_MODEL")); v != "" {
		opts = append(opts, claude.WithStructureModel(v))
	}
	if v := strings.TrimSpace(os.Getenv("PLANWERK_STRUCTURE_EFFORT")); v != "" {
		opts = append(opts, claude.WithStructureEffort(v))
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

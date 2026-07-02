// Command planwerk-eval is a dev-only harness that scores the review pipeline's
// output quality against a labeled seeded-bug corpus. It is kept out of the
// shipped planwerk-agent CLI on purpose: it invokes the real claude CLI and
// spends tokens, so it never runs in unit CI. Run it via `make eval`.
//
// It exits non-zero only on a harness error (bad corpus, git failure, pipeline
// or JSON error) — never because scores are low. A low score is a signal to
// read, not a build break.
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
	jsonOut := flag.Bool("json", false, "emit the score report as JSON instead of a table")
	flag.Parse()

	if err := run(*corpusDir, *caseName, *thorough, *jsonOut); err != nil {
		fmt.Fprintln(os.Stderr, "planwerk-eval:", err)
		os.Exit(1)
	}
}

func run(corpusDir, caseName string, thorough, jsonOut bool) error {
	cases, err := loadCases(corpusDir, caseName)
	if err != nil {
		return err
	}

	client, err := buildClient()
	if err != nil {
		return err
	}

	var scored []eval.Scored
	for _, c := range cases {
		fmt.Fprintf(os.Stderr, "running case %s ...\n", c.Name)
		result, err := eval.RunCase(client, c, thorough)
		if err != nil {
			return err
		}
		scored = append(scored, eval.Scored{Case: c, Score: eval.ScoreCase(c, result)})
	}

	rep := eval.BuildReport(scored)
	if jsonOut {
		return eval.RenderJSON(os.Stdout, rep)
	}
	eval.RenderTable(os.Stdout, rep)
	return nil
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

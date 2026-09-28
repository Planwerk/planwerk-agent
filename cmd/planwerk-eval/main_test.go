package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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

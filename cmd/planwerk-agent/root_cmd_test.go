package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/review"
)

// runRootCmd executes the root (review) command hermetically: the
// flag-validation guards return before review.Run, so a refused combination
// touches no git, gh or claude backend. It runs in an empty directory, so no
// .planwerk/config.yaml is read.
func runRootCmd(t *testing.T, args ...string) error {
	t.Helper()
	t.Chdir(t.TempDir())
	// The root command sets the default logger up.
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	var out, errBuf bytes.Buffer
	cmd := newRootCmd(&runtimeDeps{version: "test"})
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	return cmd.Execute()
}

func TestRootCmd_ResolveOutdatedRequiresInline(t *testing.T) {
	// --resolve-outdated acts on the threads an inline review opened, so it
	// is refused at parse time without --inline, --post-review alone included.
	for _, args := range [][]string{
		{"--resolve-outdated", testRepoRef + "#1"},
		{"--resolve-outdated", "--post-review", testRepoRef + "#1"},
	} {
		err := runRootCmd(t, args...)
		if err == nil || !strings.Contains(err.Error(), "--resolve-outdated requires --inline") {
			t.Errorf("%q: expected the resolve-outdated guard error, got %v", args, err)
		}
	}
}

func TestValidateReviewFormat(t *testing.T) {
	tests := []struct {
		name    string
		cfg     review.Options
		wantErr string // "" accepts the combination; otherwise the exact error
	}{
		{"markdown takes every posting flag", review.Options{Format: "markdown", PostReview: true, InlineReview: true, CoverageMap: true}, ""},
		{"json", review.Options{Format: "json"}, ""},
		{"sarif", review.Options{Format: "sarif"}, ""},
		{"an unknown format lists sarif", review.Options{Format: "yaml"}, `unknown format "yaml", supported: markdown, json, sarif`},
		{"json refuses --post-review", review.Options{Format: "json", PostReview: true}, "--post-review cannot be used with --format json"},
		{"json refuses --inline", review.Options{Format: "json", InlineReview: true}, "--inline cannot be used with --format json"},
		{"sarif refuses --post-review", review.Options{Format: "sarif", PostReview: true}, "--post-review cannot be used with --format sarif"},
		{"sarif refuses --inline", review.Options{Format: "sarif", InlineReview: true}, "--inline cannot be used with --format sarif"},
		{"sarif refuses --coverage-map", review.Options{Format: "sarif", CoverageMap: true}, "--coverage-map cannot be used with --format sarif: SARIF has no place for the coverage map"},
		{"json keeps --coverage-map", review.Options{Format: "json", CoverageMap: true}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateReviewFormat(tt.cfg)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("validateReviewFormat(%+v) = %v, want nil", tt.cfg, err)
			case tt.wantErr != "" && (err == nil || err.Error() != tt.wantErr):
				t.Errorf("validateReviewFormat(%+v) = %v, want %q", tt.cfg, err, tt.wantErr)
			}
		})
	}
}

func TestRootCmd_InlineWithSARIFNamesTheTypedFlag(t *testing.T) {
	// --inline implies --post-review only after the format check, so the
	// refusal names --inline, the flag the user typed.
	err := runRootCmd(t, "--inline", "--format", "sarif", testRepoRef+"#1")
	if err == nil || err.Error() != "--inline cannot be used with --format sarif" {
		t.Fatalf("expected the inline/sarif guard error, got %v", err)
	}
}

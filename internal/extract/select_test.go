package extract

import (
	"bytes"
	"strings"
	"testing"
)

func testEntries() []entry {
	return []entry{
		{Stem: "alpha", Name: "Alpha", Severity: "WARNING"},
		{Stem: "beta", Name: "Beta", Severity: "INFO"},
		{Stem: "gamma", Name: "Gamma", Severity: "CRITICAL"},
	}
}

func stems(entries []entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Stem
	}
	return out
}

func TestSelectEntries_All(t *testing.T) {
	var w bytes.Buffer
	got, err := selectEntries(&w, Options{All: true}, nil, nil, testEntries())
	if err != nil {
		t.Fatalf("selectEntries: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("--all should select every entry, got %d", len(got))
	}
}

func TestSelectEntries_PatternMatchesSubsetAndWarnsOnUnknown(t *testing.T) {
	var w bytes.Buffer
	got, err := selectEntries(&w, Options{Patterns: []string{"beta", "nope"}}, nil, nil, testEntries())
	if err != nil {
		t.Fatalf("selectEntries: %v", err)
	}
	if want := []string{"beta"}; !equalStrings(stems(got), want) {
		t.Fatalf("selected stems = %v, want %v", stems(got), want)
	}
	if !strings.Contains(w.String(), "--pattern nope did not match") {
		t.Fatalf("expected an unmatched-pattern warning, got: %q", w.String())
	}
}

func TestSelectEntries_NonTTYRequiresExplicitSelection(t *testing.T) {
	var w bytes.Buffer
	// isTTY returns false and neither --all nor --pattern is set: a
	// non-interactive run must fail closed rather than silently extracting (and,
	// in the default mode, pushing into a PR) every pattern off the untrusted,
	// world-editable wiki.
	got, err := selectEntries(&w, Options{}, nil, func() bool { return false }, testEntries())
	if err == nil || !strings.Contains(err.Error(), "not a TTY") {
		t.Fatalf("expected a non-TTY selection error, got %v (selected %d)", err, len(got))
	}
}

func TestSelectEntries_Interactive(t *testing.T) {
	var w bytes.Buffer
	// y -> alpha selected, N -> beta skipped, q -> stop before gamma.
	in := strings.NewReader("y\nN\nq\n")
	got, err := selectEntries(&w, Options{}, in, func() bool { return true }, testEntries())
	if err != nil {
		t.Fatalf("selectEntries: %v", err)
	}
	if want := []string{"alpha"}; !equalStrings(stems(got), want) {
		t.Fatalf("selected stems = %v, want %v", stems(got), want)
	}
}

func TestRunInteractiveSelection_EOFEndsCleanly(t *testing.T) {
	var w bytes.Buffer
	// A closed stream (no answers) must not error — it finishes with nothing
	// selected, mirroring the address selector's EOF handling.
	got, err := runInteractiveSelection(&w, strings.NewReader(""), testEntries())
	if err != nil {
		t.Fatalf("runInteractiveSelection: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no selections on immediate EOF, got %d", len(got))
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

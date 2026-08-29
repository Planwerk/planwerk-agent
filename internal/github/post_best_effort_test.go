package github

import (
	"bytes"
	"errors"
	"testing"
)

func TestPostBestEffort_ReportsSuccessWithURL(t *testing.T) {
	var out bytes.Buffer
	PostBestEffort(&out, "fix report", "PR #7", func() (string, error) { return "https://example/c/1", nil })
	if got, want := out.String(), "\nPosted the fix report as a comment on PR #7 (https://example/c/1)\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestPostBestEffort_OmitsEmptyURLAndTarget(t *testing.T) {
	var out bytes.Buffer
	PostBestEffort(&out, "capture proposals", "", func() (string, error) { return "", nil })
	if got, want := out.String(), "\nPosted the capture proposals as a comment\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestPostBestEffort_ReportsFailureWithoutReturningIt(t *testing.T) {
	var out bytes.Buffer
	PostBestEffort(&out, "implementation plan", "issue #42", func() (string, error) { return "", errors.New("github down") })
	if got, want := out.String(), "\nCould not post the implementation plan as a comment on issue #42: github down\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

package report

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestNewPassStatus pins what a pass status keeps of an error: the first line
// only, secrets redacted, at most maxPassErrorLen bytes cut on a rune boundary,
// and a fallback for an error with no text, since the error is rendered in a
// PR comment every reader of the repository sees.
func TestNewPassStatus(t *testing.T) {
	t.Run("a nil error ran", func(t *testing.T) {
		got := NewPassStatus("adversarial", nil)
		want := PassStatus{Pass: "adversarial", Status: PassStatusRan}
		if got != want {
			t.Errorf("NewPassStatus = %+v, want %+v", got, want)
		}
	})

	t.Run("keeps only the first line", func(t *testing.T) {
		err := errors.New("running security specialist review: claude (model opus): exit status 1\nstderr: long text")
		got := NewPassStatus("specialist-security", err)
		want := PassStatus{
			Pass:   "specialist-security",
			Status: PassStatusFailed,
			Error:  "running security specialist review: claude (model opus): exit status 1",
		}
		if got != want {
			t.Errorf("NewPassStatus = %+v, want %+v", got, want)
		}
	})

	t.Run("cuts a long multibyte error on a rune boundary", func(t *testing.T) {
		// "ä" is two bytes, so 250 of them are 500 bytes and every odd byte
		// offset falls inside a rune.
		got := NewPassStatus("coverage", errors.New(strings.Repeat("ä", 250))).Error
		if len(got) > maxPassErrorLen {
			t.Errorf("len(Error) = %d, want at most %d", len(got), maxPassErrorLen)
		}
		if !utf8.ValidString(got) {
			t.Errorf("Error %q is not valid UTF-8", got)
		}
		if !strings.HasSuffix(got, passErrorEllipsis) {
			t.Errorf("Error %q does not end in the ellipsis", got)
		}
	})

	t.Run("redacts a secret", func(t *testing.T) {
		token := "ghp_" + strings.Repeat("a1B2", 9)
		got := NewPassStatus("compliance", errors.New("fetching with "+token+": 401")).Error
		if strings.Contains(got, token) {
			t.Errorf("Error %q keeps the token", got)
		}
		if !strings.Contains(got, "[REDACTED:github-token]") {
			t.Errorf("Error %q carries no redaction marker", got)
		}
	})

	t.Run("an empty message gets the fallback", func(t *testing.T) {
		got := NewPassStatus("adversarial", errors.New("\nstderr only"))
		if got.Status != PassStatusFailed || got.Error != passErrorFallback {
			t.Errorf("NewPassStatus = %+v, want failed with %q", got, passErrorFallback)
		}
	})
}

func TestReviewResult_FailedPasses(t *testing.T) {
	t.Run("returns the failed entries in recorded order", func(t *testing.T) {
		r := &ReviewResult{Passes: []PassStatus{
			{Pass: "adversarial", Status: PassStatusFailed, Error: "a"},
			{Pass: "coverage", Status: PassStatusRan},
			{Pass: "specialist-testing", Status: PassStatusGatedOut},
			{Pass: "specialist-security", Status: PassStatusFailed, Error: "b"},
		}}
		want := []PassStatus{
			{Pass: "adversarial", Status: PassStatusFailed, Error: "a"},
			{Pass: "specialist-security", Status: PassStatusFailed, Error: "b"},
		}
		if got := r.FailedPasses(); !reflect.DeepEqual(got, want) {
			t.Errorf("FailedPasses = %+v, want %+v", got, want)
		}
	})

	t.Run("nil when nothing was recorded", func(t *testing.T) {
		if got := (&ReviewResult{}).FailedPasses(); got != nil {
			t.Errorf("FailedPasses = %+v, want nil", got)
		}
	})
}

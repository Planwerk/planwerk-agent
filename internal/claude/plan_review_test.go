package claude

import (
	"errors"
	"strings"
	"testing"
)

const planReviewTestPlan = "## Implementation Plan (issue #42)\n\nSTATUS: PLAN_READY"

// TestReviewPlan_ClampsScore locks that a score outside 0-10 is bounded before
// the runner compares it to the bar or renders it.
func TestReviewPlan_ClampsScore(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		out  string
		want int
	}{
		{`{"gaps": [], "to_reach_ten": "", "score": 14}`, 10},
		{`{"gaps": ["x"], "to_reach_ten": "y", "score": -3}`, 0},
	} {
		c, _ := scriptedClient(t, func(int, runSpec, string) (string, string, error) { return tc.out, "", nil })
		review, _, err := c.ReviewPlan("", goldenImplementContext(), planReviewTestPlan)
		if err != nil {
			t.Fatalf("ReviewPlan(%s) returned error: %v", tc.out, err)
		}
		if review.Score != tc.want {
			t.Errorf("ReviewPlan(%s).Score = %d, want %d", tc.out, review.Score, tc.want)
		}
	}
}

// TestReviewPlan_SessionErrorIsWrapped locks that a failed session reaches the
// runner named as the plan review, with the cause intact.
func TestReviewPlan_SessionErrorIsWrapped(t *testing.T) {
	t.Parallel()

	c, _ := scriptedClient(t, func(int, runSpec, string) (string, string, error) { return "", "", errors.New("boom") })
	_, _, err := c.ReviewPlan("", goldenImplementContext(), planReviewTestPlan)
	if err == nil || !strings.HasPrefix(err.Error(), "running plan review: ") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("ReviewPlan returned %v, want an error starting with \"running plan review: \" that carries boom", err)
	}
}

// TestReviewPlan_ProseOnlyOutputFails locks that a session which ended on prose
// returns no verdict and pays for no repair session.
func TestReviewPlan_ProseOnlyOutputFails(t *testing.T) {
	t.Parallel()

	for _, out := range []string{"The plan looks fine.", ""} {
		c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) { return out, "", nil })
		_, _, err := c.ReviewPlan("", goldenImplementContext(), planReviewTestPlan)
		if err == nil || !strings.HasPrefix(err.Error(), "parsing plan review as JSON: the output holds no JSON value") {
			t.Errorf("ReviewPlan(%q) returned %v, want the no-JSON error", out, err)
		}
		if len(*calls) != 1 {
			t.Errorf("ReviewPlan(%q) ran %d sessions, want 1 (no repair)", out, len(*calls))
		}
	}
}

package github

import (
	"encoding/json"
	"testing"
)

func TestParseActionsRunID(t *testing.T) {
	cases := []struct {
		url  string
		want int64
	}{
		{"https://github.com/o/r/actions/runs/123/job/456", 123},
		{"https://github.com/o/r/actions/runs/9876543210", 9876543210},
		{"https://example.com/some/other/url", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := parseActionsRunID(c.url); got != c.want {
			t.Errorf("parseActionsRunID(%q) = %d, want %d", c.url, got, c.want)
		}
	}
}

func TestSummarizeChecks(t *testing.T) {
	runs := []CheckRun{
		{ID: 1, Name: "lint", Status: "completed", Conclusion: "success"},
		{ID: 2, Name: "test", Status: "completed", Conclusion: "failure"},
		{ID: 3, Name: "build", Status: "in_progress"},
		{ID: 4, Name: "skipped-job", Status: "completed", Conclusion: "skipped"},
		{ID: 5, Name: "cancelled-job", Status: "completed", Conclusion: "cancelled"},
	}
	s := SummarizeChecks(runs)
	if s.Total != 5 {
		t.Fatalf("Total = %d, want 5", s.Total)
	}
	if len(s.Passed) != 2 {
		t.Errorf("Passed = %d, want 2", len(s.Passed))
	}
	if len(s.Failed) != 2 { // failure + cancelled
		t.Errorf("Failed = %d, want 2", len(s.Failed))
	}
	if len(s.Pending) != 1 {
		t.Errorf("Pending = %d, want 1", len(s.Pending))
	}
	if s.AllPassed() {
		t.Errorf("AllPassed should be false when failures and pending exist")
	}
	if !s.AnyFailed() {
		t.Errorf("AnyFailed should be true")
	}
	if !s.AnyPending() {
		t.Errorf("AnyPending should be true")
	}
}

func TestSummarizeChecksDeduplicatesReruns(t *testing.T) {
	runs := []CheckRun{
		{ID: 10, Name: "test", Status: "completed", Conclusion: "failure"},
		{ID: 20, Name: "test", Status: "completed", Conclusion: "success"}, // rerun
	}
	s := SummarizeChecks(runs)
	if s.Total != 1 {
		t.Fatalf("Total = %d, want 1 (rerun should dedupe)", s.Total)
	}
	if len(s.Passed) != 1 || s.Passed[0].ID != 20 {
		t.Errorf("expected only the most recent (ID=20) run kept, got %+v", s.Passed)
	}
}

func TestSummarizeChecksAllPassedRequiresNonEmpty(t *testing.T) {
	if (CheckRunSummary{}).AllPassed() {
		t.Errorf("AllPassed should be false when no checks exist")
	}
}

func TestCheckRunStateHelpers(t *testing.T) {
	cases := []struct {
		c                   CheckRun
		completed, failed, passed bool
	}{
		{CheckRun{Status: "in_progress"}, false, false, false},
		{CheckRun{Status: "completed", Conclusion: "success"}, true, false, true},
		{CheckRun{Status: "completed", Conclusion: "failure"}, true, true, false},
		{CheckRun{Status: "completed", Conclusion: "timed_out"}, true, true, false},
		{CheckRun{Status: "completed", Conclusion: "action_required"}, true, true, false},
		{CheckRun{Status: "completed", Conclusion: "skipped"}, true, false, true},
		{CheckRun{Status: "completed", Conclusion: "neutral"}, true, false, true},
		{CheckRun{Status: "completed", Conclusion: "cancelled"}, true, false, false},
	}
	for _, tc := range cases {
		if got := tc.c.IsCompleted(); got != tc.completed {
			t.Errorf("IsCompleted(%+v) = %v, want %v", tc.c, got, tc.completed)
		}
		if got := tc.c.IsFailed(); got != tc.failed {
			t.Errorf("IsFailed(%+v) = %v, want %v", tc.c, got, tc.failed)
		}
		if got := tc.c.IsPassed(); got != tc.passed {
			t.Errorf("IsPassed(%+v) = %v, want %v", tc.c, got, tc.passed)
		}
	}
}

// TestSummarizeChecksKeepsSameNameInDifferentSuites covers the case that made
// name-only de-duplication unsafe: two workflows each define a job called
// "build". Collapsing them onto one key hid the failing one behind the
// higher-ID passing one, and the fix loop declared CI green.
func TestSummarizeChecksKeepsSameNameInDifferentSuites(t *testing.T) {
	ci := CheckRun{ID: 10, Name: "build", Status: "completed", Conclusion: "failure"}
	ci.CheckSuite.ID = 1
	release := CheckRun{ID: 20, Name: "build", Status: "completed", Conclusion: "success"}
	release.CheckSuite.ID = 2

	s := SummarizeChecks([]CheckRun{ci, release})
	if s.Total != 2 {
		t.Fatalf("Total = %d, want 2 (same name, different suites are different checks)", s.Total)
	}
	if len(s.Failed) != 1 || s.Failed[0].ID != 10 {
		t.Errorf("Failed = %+v, want the failing ci.yml run (ID=10)", s.Failed)
	}
	if s.AllPassed() {
		t.Error("AllPassed() = true although one check failed")
	}
}

// TestSummarizeChecksDeduplicatesRerunsWithinSuite is the counterpart: a re-run
// inside one suite still collapses onto the most recent attempt.
func TestSummarizeChecksDeduplicatesRerunsWithinSuite(t *testing.T) {
	first := CheckRun{ID: 10, Name: "test", Status: "completed", Conclusion: "failure"}
	first.CheckSuite.ID = 7
	rerun := CheckRun{ID: 20, Name: "test", Status: "completed", Conclusion: "success"}
	rerun.CheckSuite.ID = 7

	s := SummarizeChecks([]CheckRun{first, rerun})
	if s.Total != 1 {
		t.Fatalf("Total = %d, want 1 (a re-run within one suite dedupes)", s.Total)
	}
	if len(s.Passed) != 1 || s.Passed[0].ID != 20 {
		t.Errorf("Passed = %+v, want only the most recent attempt (ID=20)", s.Passed)
	}
}

// TestCheckRunDecodesCheckSuiteID pins the wire field the de-duplication key
// reads, so a renamed JSON tag fails here rather than silently reverting to
// name-only collapsing.
func TestCheckRunDecodesCheckSuiteID(t *testing.T) {
	var r CheckRun
	if err := json.Unmarshal([]byte(`{"id":1,"name":"build","check_suite":{"id":4242}}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if r.CheckSuite.ID != 4242 {
		t.Errorf("CheckSuite.ID = %d, want 4242", r.CheckSuite.ID)
	}
}

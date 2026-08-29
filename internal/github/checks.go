package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Check status / conclusion constants mirror the GitHub Checks API vocabulary.
// See https://docs.github.com/rest/checks/runs.
const (
	CheckStatusQueued     = "queued"
	CheckStatusInProgress = "in_progress"
	CheckStatusCompleted  = "completed"

	CheckConclusionSuccess        = "success"
	CheckConclusionFailure        = "failure"
	CheckConclusionCancelled      = "cancelled"
	CheckConclusionTimedOut       = "timed_out"
	CheckConclusionActionRequired = "action_required"
	CheckConclusionNeutral        = "neutral"
	CheckConclusionSkipped        = "skipped"
)

// CheckRun is the subset of fields the fix subcommand needs from a Checks API
// response. Conclusion is empty when Status != "completed".
type CheckRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HTMLURL    string `json:"html_url"`
	DetailsURL string `json:"details_url"`
	Output     struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
	} `json:"output"`
	// CheckSuite identifies the suite the run belongs to — one suite per
	// workflow run. It is what makes a check run's *name* unique: two
	// workflows may both define a job called "build", and only the suite
	// tells them apart.
	CheckSuite struct {
		ID int64 `json:"id"`
	} `json:"check_suite"`
	// WorkflowRunID is the GitHub Actions workflow run id parsed from
	// DetailsURL when the check is backed by Actions; 0 otherwise.
	WorkflowRunID int64 `json:"-"`
}

// IsCompleted reports whether the check has finished running.
func (c CheckRun) IsCompleted() bool { return c.Status == CheckStatusCompleted }

// IsFailed reports whether the check completed with a non-passing,
// non-neutral conclusion that warrants intervention.
func (c CheckRun) IsFailed() bool {
	if !c.IsCompleted() {
		return false
	}
	switch c.Conclusion {
	case CheckConclusionFailure, CheckConclusionTimedOut, CheckConclusionActionRequired:
		return true
	default:
		return false
	}
}

// IsPassed reports whether the check completed successfully or was a
// non-blocking outcome (skipped/neutral).
func (c CheckRun) IsPassed() bool {
	if !c.IsCompleted() {
		return false
	}
	switch c.Conclusion {
	case CheckConclusionSuccess, CheckConclusionSkipped, CheckConclusionNeutral:
		return true
	default:
		return false
	}
}

type checkRunsResponse struct {
	TotalCount int        `json:"total_count"`
	CheckRuns  []CheckRun `json:"check_runs"`
}

// actionsRunIDRe extracts the workflow-run id from an Actions details URL of
// the form ".../actions/runs/<id>/job/<jobid>".
var actionsRunIDRe = regexp.MustCompile(`/actions/runs/(\d+)`)

// ListChecks returns the most recent check runs for the given commit SHA.
// It pages through the Checks API so check suites with > 100 runs are not
// silently truncated.
func (Client) ListChecks(owner, name, sha string) ([]CheckRun, error) {
	var all []CheckRun
	page := 1
	for {
		path := fmt.Sprintf("repos/%s/%s/commits/%s/check-runs?per_page=100&page=%d",
			owner, name, sha, page)
		ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
		cmd := exec.CommandContext(ctx, "gh", "api", path)
		out, err := cmd.Output()
		cancel()
		if err != nil {
			return nil, fmt.Errorf("gh api %s: %w", path, err)
		}
		var resp checkRunsResponse
		if err := json.Unmarshal(out, &resp); err != nil {
			return nil, fmt.Errorf("parsing check-runs response: %w", err)
		}
		for i := range resp.CheckRuns {
			resp.CheckRuns[i].WorkflowRunID = parseActionsRunID(resp.CheckRuns[i].DetailsURL)
		}
		all = append(all, resp.CheckRuns...)
		if len(resp.CheckRuns) < 100 {
			return all, nil
		}
		page++
	}
}

func parseActionsRunID(detailsURL string) int64 {
	m := actionsRunIDRe.FindStringSubmatch(detailsURL)
	if m == nil {
		return 0
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// CheckRunSummary aggregates a slice of check runs into the buckets the fix
// loop branches on. A check is only counted in one bucket: failed > pending
// > passed.
type CheckRunSummary struct {
	Total   int
	Passed  []CheckRun
	Failed  []CheckRun
	Pending []CheckRun
}

// AllPassed reports whether every check has completed with a non-failing
// conclusion. Returns false when any check is still pending.
func (s CheckRunSummary) AllPassed() bool {
	return len(s.Failed) == 0 && len(s.Pending) == 0 && len(s.Passed) > 0
}

// AnyFailed reports whether at least one check has completed with a failing
// conclusion.
func (s CheckRunSummary) AnyFailed() bool { return len(s.Failed) > 0 }

// AnyPending reports whether at least one check is queued or in progress.
func (s CheckRunSummary) AnyPending() bool { return len(s.Pending) > 0 }

// checkKey identifies a check run for de-duplication: its name within its
// check suite. Name alone is not enough — two workflows may each define a job
// called "build", and collapsing them onto one key would hide whichever lost
// the highest-ID contest, including a failing one.
type checkKey struct {
	suite int64
	name  string
}

// SummarizeChecks buckets check runs into passed / failed / pending. When the
// same check name has multiple entries within one check suite (re-runs), only
// the most recent (highest ID) is kept; the same name in a different suite is
// a different check and is kept alongside it.
func SummarizeChecks(runs []CheckRun) CheckRunSummary {
	latest := make(map[checkKey]CheckRun, len(runs))
	for _, r := range runs {
		k := checkKey{suite: r.CheckSuite.ID, name: r.Name}
		if existing, ok := latest[k]; !ok || r.ID > existing.ID {
			latest[k] = r
		}
	}
	var s CheckRunSummary
	s.Total = len(latest)
	for _, r := range latest {
		switch {
		case r.IsFailed():
			s.Failed = append(s.Failed, r)
		case !r.IsCompleted():
			s.Pending = append(s.Pending, r)
		case r.IsPassed():
			s.Passed = append(s.Passed, r)
		default:
			// e.g. cancelled — treat as failed so the loop reports it
			s.Failed = append(s.Failed, r)
		}
	}
	return s
}

// FailedRunLogs returns the failed-step logs for the given GitHub Actions
// workflow run. Returns ("", nil) when the run has no associated logs (for
// example, third-party check providers without an Actions backing).
func (Client) FailedRunLogs(owner, name string, runID int64) (string, error) {
	if runID == 0 {
		return "", nil
	}
	repo := fmt.Sprintf("%s/%s", owner, name)
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "run", "view", strconv.FormatInt(runID, 10),
		"--repo", repo,
		"--log-failed")
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("gh run view --log-failed: %s: %w",
				strings.TrimSpace(string(exitErr.Stderr)), err)
		}
		return "", fmt.Errorf("gh run view --log-failed: %w", err)
	}
	return string(out), nil
}

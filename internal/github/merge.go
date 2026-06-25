package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Mergeability is the subset of a pull request's merge state the ship command
// gates an autonomous merge on. It is read from gh in a single `gh pr view`
// call. The zero value is "not mergeable" so a decode failure never green-lights
// a merge.
type Mergeability struct {
	// Mergeable is GitHub's mergeability verdict: "MERGEABLE", "CONFLICTING", or
	// "UNKNOWN" (still being computed).
	Mergeable string `json:"mergeable"`
	// MergeStateStatus is the detailed merge-state: "CLEAN", "BLOCKED" (a required
	// check or review is missing), "BEHIND", "DIRTY" (conflicts), "DRAFT", etc.
	MergeStateStatus string `json:"mergeStateStatus"`
	// ReviewDecision is "APPROVED", "REVIEW_REQUIRED", "CHANGES_REQUESTED", or ""
	// when the repository requires no review.
	ReviewDecision string `json:"reviewDecision"`
	IsDraft        bool   `json:"isDraft"`
	// HeadSHA is the commit at the tip of the PR's head branch at the moment this
	// mergeability snapshot was read. ship pins the merge to it (gh pr merge
	// --match-head-commit) so a commit pushed after the snapshot — one that never
	// passed the CI gate — cannot be merged in its place.
	HeadSHA string `json:"headRefOid"`
}

// CanMerge reports whether the pull request can be merged right now without
// forcing past a protection rule. It is deliberately conservative: it requires a
// clean, non-draft, conflict-free, fully-reviewed state and refuses anything
// GitHub flags as blocked, behind, dirty, or still-computing. ship skips (rather
// than force-merges) a Sub Issue whose PR does not pass this gate, honoring the
// merge-safety contract that ship never builds on or pushes past failed checks
// or required reviews.
func (m Mergeability) CanMerge() bool {
	if m.IsDraft {
		return false
	}
	if !strings.EqualFold(m.Mergeable, "MERGEABLE") {
		return false
	}
	switch strings.ToUpper(m.MergeStateStatus) {
	case "CLEAN", "HAS_HOOKS", "UNSTABLE":
		// CLEAN: ready. HAS_HOOKS: ready, a post-merge hook will run. UNSTABLE:
		// only non-required checks are failing, so the merge is still permitted.
	default:
		return false
	}
	switch strings.ToUpper(m.ReviewDecision) {
	case "CHANGES_REQUESTED", "REVIEW_REQUIRED":
		return false
	}
	return true
}

// PRMergeability reads the pull request's mergeability state via gh.
func PRMergeability(owner, name string, number int) (*Mergeability, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", prMergeabilityArgs(owner, name, number)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh pr view (mergeability): %s: %w", strings.TrimSpace(string(out)), err)
	}
	var m Mergeability
	if err := json.Unmarshal(out, &m); err != nil {
		return nil, fmt.Errorf("parsing gh pr view mergeability output: %w", err)
	}
	return &m, nil
}

// prMergeabilityArgs builds the gh argv that reads the PR's merge-state fields.
// Kept separate so the argument assembly is unit-testable without invoking gh.
func prMergeabilityArgs(owner, name string, number int) []string {
	return []string{"pr", "view", strconv.Itoa(number),
		"--repo", fmt.Sprintf("%s/%s", owner, name),
		"--json", "mergeable,mergeStateStatus,reviewDecision,isDraft,headRefOid",
	}
}

// MarkPRReady takes a draft pull request out of draft so its checks become the
// real merge gate rather than a draft placeholder.
func MarkPRReady(owner, name string, number int) error {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", markPRReadyArgs(owner, name, number)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh pr ready: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// markPRReadyArgs builds the gh argv that undrafts a PR. Kept separate so the
// argument assembly is unit-testable without invoking gh.
func markPRReadyArgs(owner, name string, number int) []string {
	return []string{"pr", "ready", strconv.Itoa(number),
		"--repo", fmt.Sprintf("%s/%s", owner, name),
	}
}

// MergePR merges the pull request with the given method ("rebase", "squash", or
// "merge"). An unknown method is rejected rather than silently defaulted. When
// headSHA is non-empty the merge is pinned to it (--match-head-commit), so
// GitHub refuses the merge if the PR head moved after the SHA was validated.
func MergePR(owner, name string, number int, method, headSHA string) error {
	args, err := mergePRArgs(owner, name, number, method, headSHA)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh pr merge --%s: %s: %w", method, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// mergePRArgs builds the gh argv that merges the PR with the chosen method,
// mapping it to the corresponding gh flag. It returns an error for any method
// other than rebase/squash/merge. When headSHA is non-empty it appends
// --match-head-commit so gh refuses the merge if the PR head moved off that SHA.
// Kept separate so the argument assembly is unit-testable without invoking gh.
func mergePRArgs(owner, name string, number int, method, headSHA string) ([]string, error) {
	var flag string
	switch method {
	case "rebase":
		flag = "--rebase"
	case "squash":
		flag = "--squash"
	case "merge":
		flag = "--merge"
	default:
		return nil, fmt.Errorf("unknown merge method %q: want rebase, squash, or merge", method)
	}
	args := []string{"pr", "merge", strconv.Itoa(number),
		"--repo", fmt.Sprintf("%s/%s", owner, name),
		flag,
	}
	if headSHA != "" {
		args = append(args, "--match-head-commit", headSHA)
	}
	return args, nil
}

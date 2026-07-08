package github

import (
	"fmt"
	"strings"
	"testing"
)

// testFeatureBranch is the sample implement feature branch the resume tests use,
// shared so the branch name lives in one place across this package's tests.
const testFeatureBranch = "implement/issue-42-foo"

// initResumeRepo builds a working repo on branch main with one base commit, a
// bare origin that tracks main, and refs/remotes/origin/HEAD pointed at
// origin/main — the shape CurrentBranchRef (and thus PrepareResume /
// CurrentFeatureProgress) needs to resolve the base branch offline.
func initResumeRepo(t *testing.T) (dir, bare string) {
	t.Helper()
	dir, bare, _ = initRebaseRepo(t)
	git(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	return dir, bare
}

// commitFeature checks out a fresh feature branch off main and adds n commits to
// it, leaving the working tree on that branch. File names are derived from the
// slash-free branch slug so distinct branches never collide.
func commitFeature(t *testing.T, dir, branch string, n int) {
	t.Helper()
	slug := strings.ReplaceAll(branch, "/", "-")
	git(t, dir, "checkout", "-q", "-b", branch, "main")
	for i := 1; i <= n; i++ {
		writeRepoFile(t, dir, fmt.Sprintf("%s-%d.txt", slug, i), "x\n")
		git(t, dir, "add", ".")
		git(t, dir, "commit", "-q", "-m", fmt.Sprintf("feature commit %d", i))
	}
}

// TestPrepareResumeCurrentBranch resumes the branch the checkout is already on —
// the shape a --local abort leaves behind — returning its commits oldest-first.
func TestPrepareResumeCurrentBranch(t *testing.T) {
	dir, _ := initResumeRepo(t)
	commitFeature(t, dir, testFeatureBranch, 2)

	state, err := PrepareResume(dir, 42)
	if err != nil {
		t.Fatalf("PrepareResume: %v", err)
	}
	if state == nil {
		t.Fatal("PrepareResume = nil, want the current feature branch")
	}
	if state.Branch != testFeatureBranch {
		t.Errorf("Branch = %q, want implement/issue-42-foo", state.Branch)
	}
	if len(state.Commits) != 2 {
		t.Fatalf("resumed %d commits, want 2", len(state.Commits))
	}
	if state.Commits[0].Subject != "feature commit 1" || state.Commits[1].Subject != "feature commit 2" {
		t.Errorf("commit order wrong: %+v", state.Commits)
	}
}

// TestPrepareResumeLocalBranchWhenOnBase resumes an issue's local branch even
// when the operator has switched back to the base branch in between — and checks
// it out.
func TestPrepareResumeLocalBranchWhenOnBase(t *testing.T) {
	dir, _ := initResumeRepo(t)
	commitFeature(t, dir, testFeatureBranch, 1)
	git(t, dir, "checkout", "-q", "main")

	state, err := PrepareResume(dir, 42)
	if err != nil {
		t.Fatalf("PrepareResume: %v", err)
	}
	if state == nil || state.Branch != testFeatureBranch {
		t.Fatalf("PrepareResume = %+v, want branch implement/issue-42-foo", state)
	}
	if head := gitOut(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); head != testFeatureBranch {
		t.Errorf("checkout left HEAD on %q, want implement/issue-42-foo", head)
	}
}

// TestPrepareResumeRemoteOnlyBranch resumes a branch that exists only on origin —
// the clone-mode case where a prior abort pushed partial progress — by
// materializing a local tracking branch and checking it out.
func TestPrepareResumeRemoteOnlyBranch(t *testing.T) {
	dir, _ := initResumeRepo(t)
	commitFeature(t, dir, testFeatureBranch, 1)
	git(t, dir, "push", "-q", "origin", testFeatureBranch)
	// Drop the local branch so only origin/implement/issue-42-foo remains, as in a
	// fresh clone that only has the remote-tracking ref.
	git(t, dir, "checkout", "-q", "main")
	git(t, dir, "branch", "-q", "-D", testFeatureBranch)

	state, err := PrepareResume(dir, 42)
	if err != nil {
		t.Fatalf("PrepareResume: %v", err)
	}
	if state == nil || state.Branch != testFeatureBranch {
		t.Fatalf("PrepareResume = %+v, want branch implement/issue-42-foo", state)
	}
	if len(state.Commits) != 1 {
		t.Errorf("resumed %d commits, want 1", len(state.Commits))
	}
	if head := gitOut(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); head != testFeatureBranch {
		t.Errorf("checkout left HEAD on %q, want a local implement/issue-42-foo", head)
	}
}

// TestPrepareResumeNothingToResume returns (nil, nil) when no branch for the
// issue carries commits — including when a branch for a DIFFERENT issue exists.
func TestPrepareResumeNothingToResume(t *testing.T) {
	dir, _ := initResumeRepo(t)
	commitFeature(t, dir, "implement/issue-99-bar", 1) // different issue
	git(t, dir, "checkout", "-q", "main")

	state, err := PrepareResume(dir, 42)
	if err != nil {
		t.Fatalf("PrepareResume: %v", err)
	}
	if state != nil {
		t.Errorf("PrepareResume = %+v, want nil (no branch for issue 42)", state)
	}
}

// TestCurrentFeatureProgressOnBase reports no progress when the checkout is still
// on the base branch.
func TestCurrentFeatureProgressOnBase(t *testing.T) {
	dir, _ := initResumeRepo(t)

	state, err := CurrentFeatureProgress(dir)
	if err != nil {
		t.Fatalf("CurrentFeatureProgress: %v", err)
	}
	if state != nil {
		t.Errorf("CurrentFeatureProgress = %+v, want nil on the base branch", state)
	}
}

// TestCurrentFeatureProgressAhead reports the current branch and its commits when
// the checkout is ahead of the base — what the orchestrator pushes after an abort.
func TestCurrentFeatureProgressAhead(t *testing.T) {
	dir, _ := initResumeRepo(t)
	commitFeature(t, dir, testFeatureBranch, 2)

	state, err := CurrentFeatureProgress(dir)
	if err != nil {
		t.Fatalf("CurrentFeatureProgress: %v", err)
	}
	if state == nil || state.Branch != testFeatureBranch {
		t.Fatalf("CurrentFeatureProgress = %+v, want branch implement/issue-42-foo", state)
	}
	if len(state.Commits) != 2 {
		t.Errorf("progress carried %d commits, want 2", len(state.Commits))
	}
}

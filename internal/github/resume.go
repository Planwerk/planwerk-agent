package github

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ResumeState describes a feature branch that carries commits over the base
// branch: the branch to operate on and the commits already on it (oldest-first).
// It is the input the implement orchestrator needs to resume an aborted run —
// PrepareResume returns the branch a previous run left behind so the next run
// continues on it, and CurrentFeatureProgress returns the branch the just-aborted
// run committed on so its partial progress can be pushed for a later resume.
type ResumeState struct {
	Branch  string
	Commits []Commit
}

// resumeBranchPrefix is the required feature-branch prefix the implement prompt
// mandates for a given issue ("implement/issue-<N>-<slug>"). PrepareResume keys
// its local/remote branch discovery on it, so the prompt's prefix rule and this
// detection must stay in lockstep. The diagnose skill relies on it too: it names
// its branches "diagnose/issue-<N>-<slug>" so implement never resumes one.
func resumeBranchPrefix(issueNumber int) string {
	return fmt.Sprintf("implement/issue-%d-", issueNumber)
}

// PrepareResume finds a feature branch left behind by an earlier, aborted
// implement run for issueNumber, checks it out, and returns the commits already
// on it. It returns (nil, nil) when there is nothing to resume — no matching
// branch carries commits over the base — so the caller falls through to a normal
// fresh implementation.
//
// Discovery is ordered by how directly the branch is already in hand:
//  1. the currently checked-out branch, when it is not the base and is ahead of
//     it (the shape a --local checkout is left in after an abort);
//  2. a local branch named "implement/issue-<N>-*" (a --local run where the user
//     switched back to the base branch in between);
//  3. a remote branch "origin/implement/issue-<N>-*" (a clone-mode run whose
//     partial progress a prior abort pushed; a fresh `gh repo clone` already
//     carries the remote-tracking ref).
//
// The first candidate that carries at least one commit over the base wins. A
// local/current branch is checked out in place; a remote-only branch is
// materialized as a local tracking branch. Base resolution or a checkout failure
// is returned as an error; the caller treats resume as best-effort and proceeds
// without it.
func (c Client) PrepareResume(dir string, issueNumber int) (*ResumeState, error) {
	ref, err := c.CurrentBranchRef(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving branch ref for resume: %w", err)
	}
	base := baseRef(dir, ref.BaseBranch)
	if base == "" {
		// Without a resolvable base we cannot tell which commits are "ahead", so
		// there is nothing safe to resume onto.
		return nil, nil
	}
	prefix := resumeBranchPrefix(issueNumber)

	// 1. The currently checked-out branch, when it is this issue's feature branch
	// ahead of the base — the branch a --local abort leaves the working tree on.
	// It must carry the issue's prefix so an unrelated branch the operator happens
	// to be sitting on is never mistaken for this issue's partial progress.
	if strings.HasPrefix(ref.HeadBranch, prefix) && ref.HeadBranch != ref.BaseBranch {
		if commits, _ := c.CommitsInRange(dir, base+"..HEAD"); len(commits) > 0 {
			return &ResumeState{Branch: ref.HeadBranch, Commits: commits}, nil
		}
	}

	// 2. Local branches matching the issue's feature-branch prefix.
	locals, err := listBranches(dir, prefix+"*", false)
	if err != nil {
		return nil, err
	}
	for _, b := range locals {
		if b == ref.HeadBranch {
			continue // already tried as the current branch above
		}
		commits, err := c.CommitsInRange(dir, base+".."+b)
		if err != nil || len(commits) == 0 {
			continue
		}
		if err := checkoutBranch(dir, b); err != nil {
			return nil, err
		}
		return &ResumeState{Branch: b, Commits: commits}, nil
	}

	// 3. Remote branches matching the prefix (e.g. "origin/implement/issue-42-x").
	remotes, err := listBranches(dir, "origin/"+prefix+"*", true)
	if err != nil {
		return nil, err
	}
	for _, r := range remotes {
		commits, err := c.CommitsInRange(dir, base+".."+r)
		if err != nil || len(commits) == 0 {
			continue
		}
		local := strings.TrimPrefix(r, "origin/")
		if err := checkoutTrackingBranch(dir, local, r); err != nil {
			return nil, err
		}
		return &ResumeState{Branch: local, Commits: commits}, nil
	}

	return nil, nil
}

// CurrentFeatureProgress reports the commits the current checkout has committed
// over the base branch, or (nil, nil) when the working tree is still on the base
// or carries no commits ahead of it. The implement orchestrator calls it after an
// aborted session to decide whether there is partial progress worth pushing for a
// later resume.
func (c Client) CurrentFeatureProgress(dir string) (*ResumeState, error) {
	ref, err := c.CurrentBranchRef(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving branch ref for partial progress: %w", err)
	}
	if ref.HeadBranch == "" || ref.HeadBranch == ref.BaseBranch {
		return nil, nil
	}
	base := baseRef(dir, ref.BaseBranch)
	if base == "" {
		return nil, nil
	}
	commits, err := c.CommitsInRange(dir, base+"..HEAD")
	if err != nil || len(commits) == 0 {
		return nil, err
	}
	return &ResumeState{Branch: ref.HeadBranch, Commits: commits}, nil
}

// baseRef resolves the range endpoint for "commits ahead of the base": the
// remote-tracking ref "origin/<base>" when present (the usual clone shape),
// falling back to a local "<base>" branch, or "" when neither resolves — in which
// case the caller cannot compute a commit range and declines to resume.
func baseRef(dir, base string) string {
	if base == "" {
		return ""
	}
	if refExists(dir, "origin/"+base) {
		return "origin/" + base
	}
	if refExists(dir, base) {
		return base
	}
	return ""
}

// refExists reports whether ref resolves to a commit in dir.
func refExists(dir, ref string) bool {
	_, err := gitOutput(dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

// listBranches returns the branch names in dir matching pattern, sorted for a
// deterministic pick. remote selects `git branch -r` (remote-tracking refs, e.g.
// "origin/implement/issue-42-x") over `git branch` (local names). An empty result
// is not an error.
func listBranches(dir, pattern string, remote bool) ([]string, error) {
	args := []string{"branch"}
	if remote {
		args = append(args, "-r")
	}
	args = append(args, "--list", pattern, "--format=%(refname:short)")
	out, err := gitOutput(dir, args...)
	if err != nil {
		return nil, fmt.Errorf("listing branches %q: %w", pattern, err)
	}
	var branches []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			branches = append(branches, line)
		}
	}
	sort.Strings(branches)
	return branches, nil
}

// checkoutBranch switches the working tree in dir to an existing local branch.
func checkoutBranch(dir, branch string) error {
	if err := runGit(dir, "checkout", branch); err != nil {
		return fmt.Errorf("git checkout %s: %w", branch, err)
	}
	return nil
}

// checkoutTrackingBranch materializes a remote-only branch as a local branch and
// checks it out (`git checkout -b <local> <remoteRef>`), so a clone-mode resume
// continues on the commits a prior abort pushed to origin.
func checkoutTrackingBranch(dir, local, remoteRef string) error {
	if err := runGit(dir, "checkout", "-b", local, remoteRef); err != nil {
		return fmt.Errorf("git checkout -b %s %s: %w", local, remoteRef, err)
	}
	return nil
}

// BranchCommit is one commit a checkout carries over its base branch, with its
// whole message. The implement command's commit progress prints each one as it
// appears. AuthorTime is the author date in Unix seconds: with the subject it
// identifies a commit across the rewrites that give it a new SHA (an amend, or
// the autosquash rebase of the simplify and review passes), since those keep
// both.
type BranchCommit struct {
	SHA        string
	AuthorTime int64
	Subject    string
	Body       string
}

// CommitsAhead lists the commits HEAD carries over the repository's default
// branch, oldest first: "origin/<base>..HEAD", or "<base>..HEAD" in a checkout
// without the remote-tracking ref. A checkout still on its base yields none.
// Unlike CurrentFeatureProgress it follows HEAD wherever it points and returns
// each commit's body, and a base that does not resolve is an error, so a caller
// that polls can say why it sees nothing.
func (Client) CommitsAhead(dir string) ([]BranchCommit, error) {
	branch, err := defaultBranch(dir)
	if err != nil {
		return nil, err
	}
	base := baseRef(dir, branch)
	if base == "" {
		return nil, fmt.Errorf("base branch %q resolves to no commit", branch)
	}
	// US (0x1f) separates the fields and RS (0x1e) ends a commit, so a body with
	// blank lines survives intact.
	out, err := gitBytes(dir, "log", "--reverse", "--format=%H%x1f%at%x1f%s%x1f%b%x1e", base+"..HEAD")
	if err != nil {
		return nil, err
	}
	var commits []BranchCommit
	for _, rec := range strings.Split(string(out), "\x1e") {
		fields := strings.SplitN(strings.TrimLeft(rec, "\n"), "\x1f", 4)
		if len(fields) != 4 {
			continue // the remainder after the last record separator
		}
		at, _ := strconv.ParseInt(fields[1], 10, 64)
		commits = append(commits, BranchCommit{
			SHA:        fields[0],
			AuthorTime: at,
			Subject:    fields[2],
			Body:       strings.TrimSpace(fields[3]),
		})
	}
	return commits, nil
}

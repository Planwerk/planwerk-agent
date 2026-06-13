package github

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Commit is a single commit in a rev range: its full SHA and subject line.
// The rebase orchestrator uses these to drive per-commit conflict resolution
// and post-rebase analysis.
type Commit struct {
	SHA     string
	Subject string
}

// RebaseState is the outcome of a rebase step (StartRebase / RebaseContinue).
// Exactly one of Done or Conflicted is true for a recognized stop; when the
// rebase paused on a conflict, ConflictedFiles plus StoppedSHA/StoppedSubject
// describe the commit git could not apply cleanly.
type RebaseState struct {
	Done            bool
	Conflicted      bool
	ConflictedFiles []string
	StoppedSHA      string
	StoppedSubject  string
}

// MergeBase returns the best common ancestor of ref1 and ref2 (`git
// merge-base`). The rebase command uses it to pin the PR's original fork point
// before replaying onto a freshly fetched base.
func MergeBase(dir, ref1, ref2 string) (string, error) {
	out, err := gitCapture(dir, "merge-base", ref1, ref2)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// CommitsInRange lists the commits in rangeExpr (e.g. "origin/main..HEAD")
// oldest-first, each as a Commit with its full SHA and subject. The unit
// separator (US, 0x1f) splits SHA from subject so subjects containing spaces
// survive intact.
func CommitsInRange(dir, rangeExpr string) ([]Commit, error) {
	out, err := gitCapture(dir, "log", "--reverse", "--format=%H%x1f%s", rangeExpr)
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x1f", 2)
		c := Commit{SHA: parts[0]}
		if len(parts) == 2 {
			c.Subject = parts[1]
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// FetchBranch fetches branch from origin so origin/<branch> reflects the latest
// remote state before a rebase. It generalizes localFetchBase for an explicit,
// required branch name.
func FetchBranch(dir, branch string) error {
	if branch == "" {
		return fmt.Errorf("fetch branch: empty branch name")
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitRemoteTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "fetch", "origin", branch)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git fetch origin %s: %w", branch, err)
	}
	return nil
}

// StartRebase replays the current branch onto origin/<onto> with `git rebase`.
// It preserves individual commits (no squash). A clean replay returns
// RebaseState{Done: true}; a conflict returns RebaseState{Conflicted: true}
// with the stopped commit and its conflicted files. A non-conflict failure
// (bad ref, dirty tree) is returned as an error.
func StartRebase(dir, onto string) (RebaseState, error) {
	runErr := runRebaseCommand(dir, "rebase", "origin/"+onto)
	return rebaseStateFrom(dir, runErr)
}

// RebaseContinue resumes a paused rebase after the conflicted files have been
// staged. GIT_EDITOR/GIT_SEQUENCE_EDITOR are forced to `true` so git never
// opens an interactive commit-message or todo editor. It returns the next
// RebaseState: Done when the rebase finished, or Conflicted at the next stop.
func RebaseContinue(dir string) (RebaseState, error) {
	runErr := runRebaseCommand(dir, "rebase", "--continue")
	return rebaseStateFrom(dir, runErr)
}

// RebaseAbort aborts an in-progress rebase, restoring the branch and working
// tree to their pre-rebase state. Used to back out cleanly on unrecoverable
// failure or after a dry-run probe.
func RebaseAbort(dir string) error {
	return runGit(dir, "rebase", "--abort")
}

// ResetHard moves the current branch and working tree to ref (`git reset
// --hard`). The dry-run probe uses it to undo a rebase that applied cleanly,
// so --dry-run never leaves the checkout rewritten.
func ResetHard(dir, ref string) error {
	return runGit(dir, "reset", "--hard", ref)
}

// ConflictedFiles returns the repo-relative paths git marked as unmerged
// (`git diff --name-only --diff-filter=U`) — the files a paused rebase needs
// resolved before it can continue.
func ConflictedFiles(dir string) ([]string, error) {
	out, err := gitCapture(dir, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// ForceWithLeasePush publishes the rewritten branch with
// `git push --force-with-lease`. A rebase rewrites commit SHAs, so a plain push
// is rejected; --force-with-lease publishes the rewrite while refusing to
// clobber commits the local checkout has not seen. Plain --force is never used.
func ForceWithLeasePush(dir, branch string) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitRemoteTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "push", "--force-with-lease", "origin", "HEAD:"+branch)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git push --force-with-lease origin HEAD:%s: %w", branch, err)
	}
	return nil
}

// runRebaseCommand runs a `git rebase ...` invocation whose non-zero exit on a
// conflict is an expected outcome, not a hard error. It forces non-interactive
// editors and returns the raw run error for rebaseStateFrom to classify.
func runRebaseCommand(dir string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitRemoteTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true")
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stderr
	return cmd.Run()
}

// rebaseStateFrom classifies the result of a rebase step. When a rebase is
// still in progress afterwards, the step stopped on a conflict; otherwise a
// nil runErr means the rebase finished cleanly and a non-nil runErr is a hard
// failure (bad ref, dirty tree) the caller must surface.
func rebaseStateFrom(dir string, runErr error) (RebaseState, error) {
	inProgress, err := rebaseInProgress(dir)
	if err != nil {
		return RebaseState{}, err
	}
	if inProgress {
		files, err := ConflictedFiles(dir)
		if err != nil {
			return RebaseState{}, err
		}
		sha, subject := stoppedCommit(dir)
		return RebaseState{
			Conflicted:      true,
			ConflictedFiles: files,
			StoppedSHA:      sha,
			StoppedSubject:  subject,
		}, nil
	}
	if runErr != nil {
		return RebaseState{}, fmt.Errorf("git rebase: %w", runErr)
	}
	return RebaseState{Done: true}, nil
}

// rebaseInProgress reports whether a rebase is paused in dir, by probing for
// the rebase-merge / rebase-apply state directories git creates. `git rev-parse
// --git-path` resolves the correct location even inside a linked worktree.
func rebaseInProgress(dir string) (bool, error) {
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		out, err := gitCapture(dir, "rev-parse", "--git-path", name)
		if err != nil {
			return false, err
		}
		path := strings.TrimSpace(out)
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		if _, err := os.Stat(path); err == nil {
			return true, nil
		}
	}
	return false, nil
}

// stoppedCommit returns the SHA and subject of the commit a paused rebase could
// not apply. REBASE_HEAD points at that commit while the rebase is in progress.
// On any lookup failure it returns what it has so the caller can still name the
// stop approximately rather than failing outright.
func stoppedCommit(dir string) (sha, subject string) {
	out, err := gitCapture(dir, "rev-parse", "REBASE_HEAD")
	if err != nil {
		return "", ""
	}
	sha = strings.TrimSpace(out)
	subj, err := gitCapture(dir, "log", "-1", "--format=%s", "REBASE_HEAD")
	if err != nil {
		return sha, ""
	}
	return sha, strings.TrimSpace(subj)
}

// gitCapture runs git in dir, returning its stdout. On failure it wraps the
// trimmed stderr so callers can log the cause. Used for the read-only queries
// (merge-base, log, rev-parse, diff) the rebase plumbing depends on.
func gitCapture(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitRemoteTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

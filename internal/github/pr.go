package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// ghTimeout is the maximum time allowed for a gh CLI invocation.
	ghTimeout = 2 * time.Minute
	// gitCloneTimeout is the maximum time allowed for a git clone operation.
	gitCloneTimeout = 5 * time.Minute
	// gitRemoteTimeout is the maximum time allowed for git remote operations (e.g. ls-remote).
	gitRemoteTimeout = 2 * time.Minute
)

type PR struct {
	Owner        string
	Repo         string
	Number       int
	Title        string
	Body         string
	HeadSHA      string
	BaseBranch   string   // base branch name (e.g. "main")
	HeadBranch   string   // head branch name (e.g. "feature/CC-0042")
	Dir          string   // local checkout directory (temp dir, caller must clean up)
	ChangedFiles []string // repo-relative paths of files changed between base and head
	// Local marks a PR whose Dir is the user's working tree (via --local). When
	// set, Cleanup is a no-op so the user's checkout is never deleted.
	Local bool
}

// FetchAndCheckout retrieves PR metadata and checks out the PR locally into a temp directory.
func FetchAndCheckout(ref string) (*PR, error) {
	owner, repo, number, err := ParseRef(ref)
	if err != nil {
		return nil, err
	}

	fullName := fmt.Sprintf("%s/%s", owner, repo)

	pr := &PR{
		Owner:  owner,
		Repo:   repo,
		Number: number,
	}

	// Fetch PR metadata
	meta, err := ghJSON(fullName, number)
	if err != nil {
		return nil, fmt.Errorf("fetching PR metadata: %w", err)
	}
	pr.Title = meta.Title
	pr.Body = meta.Body
	pr.HeadSHA = meta.HeadRefOid
	pr.BaseBranch = meta.BaseRefName
	pr.HeadBranch = meta.HeadRefName

	// Clone and checkout PR into temp directory
	dir, err := checkoutPR(fullName, number)
	if err != nil {
		return nil, fmt.Errorf("checking out PR: %w", err)
	}
	pr.Dir = dir
	changed, err := diffNames(dir, pr.BaseBranch)
	if err != nil {
		slog.Warn("listing changed files failed; feature detection and specialist gating may be degraded", "err", err, "dir", dir, "base", pr.BaseBranch)
	}
	pr.ChangedFiles = changed

	return pr, nil
}

// diffNames returns repo-relative paths of files changed between the base
// branch and HEAD. An empty dir or baseBranch yields a nil slice and no error.
// On subprocess failure it returns a nil slice and an error wrapping git's
// stderr, so callers can log the cause before degrading gracefully.
func diffNames(dir, baseBranch string) ([]string, error) {
	if dir == "" || baseBranch == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitRemoteTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "diff", "--name-only", "origin/"+baseBranch+"...HEAD")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --name-only origin/%s...HEAD: %w: %s", baseBranch, err, strings.TrimSpace(stderr.String()))
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// Cleanup removes the temporary checkout directory. It is a no-op for a
// Local PR: the Dir is the user's own working tree and must never be deleted.
func (pr *PR) Cleanup() {
	if pr.Local {
		return
	}
	if pr.Dir != "" {
		_ = os.RemoveAll(pr.Dir)
	}
}

type prMeta struct {
	Title       string `json:"title"`
	Body        string `json:"body"`
	HeadRefOid  string `json:"headRefOid"`
	BaseRefName string `json:"baseRefName"`
	HeadRefName string `json:"headRefName"`
}

func ghJSON(repo string, number int) (prMeta, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", strconv.Itoa(number),
		"--repo", repo,
		"--json", "title,body,headRefOid,baseRefName,headRefName")
	out, err := cmd.Output()
	if err != nil {
		return prMeta{}, fmt.Errorf("gh pr view: %w", err)
	}
	var m prMeta
	if err := json.Unmarshal(out, &m); err != nil {
		return prMeta{}, fmt.Errorf("parsing gh output: %w", err)
	}
	return m, nil
}

func checkoutPR(repo string, number int) (string, error) {
	dir, err := os.MkdirTemp("", "planwerk-review-*")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}

	// Clone via gh so private repos work using the user's gh authentication.
	cloneCtx, cloneCancel := context.WithTimeout(context.Background(), gitCloneTimeout)
	defer cloneCancel()
	clone := exec.CommandContext(cloneCtx, "gh", "repo", "clone", repo, dir, "--", "--filter=blob:none")
	clone.Stderr = os.Stderr
	if err := clone.Run(); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("gh repo clone: %w", err)
	}

	// Checkout the PR using gh
	checkoutCtx, checkoutCancel := context.WithTimeout(context.Background(), ghTimeout)
	defer checkoutCancel()
	checkout := exec.CommandContext(checkoutCtx, "gh", "pr", "checkout", strconv.Itoa(number), "--repo", repo)
	checkout.Dir = dir
	checkout.Stderr = os.Stderr
	if err := checkout.Run(); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("gh pr checkout: %w", err)
	}

	return dir, nil
}

var (
	// https://github.com/owner/repo/pull/123
	urlRe = regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/pull/(\d+)`)
	// owner/repo#123
	shortRe = regexp.MustCompile(`^([^/]+)/([^#]+)#(\d+)$`)
	// Bare PR number, resolved against GITHUB_REPOSITORY
	numberRe = regexp.MustCompile(`^(\d+)$`)
	// Valid GitHub owner/repo name characters
	validNameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
)

// ParseRef parses a PR reference. Accepted forms:
//   - https://github.com/owner/repo/pull/N
//   - owner/repo#N
//   - N (bare number; owner/repo is taken from $GITHUB_REPOSITORY, e.g. inside
//     a GitHub Actions workflow)
func ParseRef(ref string) (owner, repo string, number int, err error) {
	if m := urlRe.FindStringSubmatch(ref); m != nil {
		number, _ = strconv.Atoi(m[3])
		owner, repo = m[1], m[2]
		if err := validateOwnerRepo(owner, repo); err != nil {
			return "", "", 0, err
		}
		return owner, repo, number, nil
	}
	ref = strings.TrimSpace(ref)
	if m := shortRe.FindStringSubmatch(ref); m != nil {
		number, _ = strconv.Atoi(m[3])
		owner, repo = m[1], m[2]
		if err := validateOwnerRepo(owner, repo); err != nil {
			return "", "", 0, err
		}
		return owner, repo, number, nil
	}
	if m := numberRe.FindStringSubmatch(ref); m != nil {
		repoEnv := os.Getenv("GITHUB_REPOSITORY")
		if repoEnv == "" {
			return "", "", 0, fmt.Errorf("invalid PR reference %q: bare PR number requires GITHUB_REPOSITORY (use URL or owner/repo#number outside GitHub Actions)", ref)
		}
		parts := strings.SplitN(repoEnv, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", "", 0, fmt.Errorf("invalid GITHUB_REPOSITORY %q: expected owner/repo", repoEnv)
		}
		owner, repo = parts[0], parts[1]
		if err := validateOwnerRepo(owner, repo); err != nil {
			return "", "", 0, err
		}
		number, _ = strconv.Atoi(m[1])
		return owner, repo, number, nil
	}
	return "", "", 0, fmt.Errorf("invalid PR reference %q: expected URL, owner/repo#number, or bare PR number with GITHUB_REPOSITORY set", ref)
}

func validateOwnerRepo(owner, repo string) error {
	if !validNameRe.MatchString(owner) {
		return fmt.Errorf("invalid owner name %q: must contain only alphanumeric characters, dots, hyphens, or underscores", owner)
	}
	if !validNameRe.MatchString(repo) {
		return fmt.Errorf("invalid repo name %q: must contain only alphanumeric characters, dots, hyphens, or underscores", repo)
	}
	return nil
}

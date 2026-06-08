package github

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// Repo holds information about a cloned repository.
type Repo struct {
	Owner string
	Name  string
	Dir   string // local clone directory (temp dir, caller must clean up)
	// Local marks a repo whose Dir is the user's working tree (via --local).
	// When set, Cleanup is a no-op so the user's checkout is never deleted.
	Local bool
}

// CloneRepo clones the given repository into a temp directory and returns a Repo.
func CloneRepo(ref string) (*Repo, error) {
	owner, name, err := ParseRepoRef(ref)
	if err != nil {
		return nil, err
	}

	dir, err := cloneRepo(owner, name)
	if err != nil {
		return nil, fmt.Errorf("cloning repo: %w", err)
	}

	return &Repo{
		Owner: owner,
		Name:  name,
		Dir:   dir,
	}, nil
}

// Cleanup removes the temporary clone directory. It is a no-op for a Local
// repo: the Dir is the user's own working tree and must never be deleted.
func (r *Repo) Cleanup() {
	if r.Local {
		return
	}
	if r.Dir != "" {
		_ = os.RemoveAll(r.Dir)
	}
}

// FullName returns "owner/name".
func (r *Repo) FullName() string {
	return fmt.Sprintf("%s/%s", r.Owner, r.Name)
}

// BranchHeadSHA returns the HEAD commit SHA of the named branch on the
// remote repository via the gh API. Used by the fix loop to detect when CI
// has run against a new commit (i.e. our follow-up push has landed).
func BranchHeadSHA(owner, repo, branch string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitRemoteTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "api",
		fmt.Sprintf("repos/%s/%s/branches/%s", owner, repo, branch),
		"--jq", ".commit.sha")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh api branches/%s: %w", branch, err)
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", fmt.Errorf("gh api returned no SHA for branch %s of %s/%s", branch, owner, repo)
	}
	return sha, nil
}

// DefaultBranchHEAD returns the HEAD SHA of the default branch via the gh API.
// Used via gh so authentication works for private repos. This is used for
// cache invalidation so proposals are refreshed when the repo changes.
func DefaultBranchHEAD(owner, repo string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitRemoteTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "api", "graphql",
		"-F", "owner="+owner,
		"-F", "name="+repo,
		"-f", `query=query($owner: String!, $name: String!) { repository(owner: $owner, name: $name) { defaultBranchRef { target { oid } } } }`,
		"--jq", ".data.repository.defaultBranchRef.target.oid")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh api graphql: %w", err)
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", fmt.Errorf("gh api returned no default branch SHA for %s/%s", owner, repo)
	}
	return sha, nil
}

func cloneRepo(owner, name string) (string, error) {
	dir, err := os.MkdirTemp("", "planwerk-propose-*")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), gitCloneTimeout)
	defer cancel()
	clone := exec.CommandContext(ctx, "gh", "repo", "clone",
		fmt.Sprintf("%s/%s", owner, name), dir,
		"--", "--filter=blob:none")
	clone.Stderr = os.Stderr
	if err := clone.Run(); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("gh repo clone: %w", err)
	}

	return dir, nil
}

var (
	// https://github.com/owner/repo or https://github.com/owner/repo.git
	repoURLRe = regexp.MustCompile(`github\.com/([^/]+)/([^/]+?)(?:\.git)?/?$`)
	// owner/repo (no # or / at end)
	repoShortRe = regexp.MustCompile(`^([^/#]+)/([^/#]+)$`)
)

// ParseRepoRef parses a repo reference in URL or short form (owner/repo).
func ParseRepoRef(ref string) (owner, repo string, err error) {
	ref = strings.TrimSpace(ref)

	if m := repoURLRe.FindStringSubmatch(ref); m != nil {
		owner, repo = m[1], m[2]
		if err := validateOwnerRepo(owner, repo); err != nil {
			return "", "", err
		}
		return owner, repo, nil
	}

	if m := repoShortRe.FindStringSubmatch(ref); m != nil {
		owner, repo = m[1], m[2]
		if err := validateOwnerRepo(owner, repo); err != nil {
			return "", "", err
		}
		return owner, repo, nil
	}

	return "", "", fmt.Errorf("invalid repo reference %q: expected URL (https://github.com/owner/repo) or short form (owner/repo)", ref)
}

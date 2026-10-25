package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	// gitWaitDelay bounds how long a gitBytes call may still block once git
	// has exited or its timeout has killed it. A PR clone is blobless, so a
	// diff that reads file contents starts a lazy fetch, which inherits the
	// output pipes and would keep Wait reading them until it ends; this long
	// after, Wait closes them instead (diffscope.gitWaitDelay).
	gitWaitDelay = 2 * time.Second
)

// ErrHeadMismatch is returned when a pull request's checkout is not at the
// head GitHub reports for it, even after pinHead re-read that head. Exported
// so the CLI (and tests) can match against it with errors.Is, like
// ErrOriginMismatch.
var ErrHeadMismatch = errors.New("checkout is not at the pull request head")

type PR struct {
	Owner        string
	Repo         string
	Number       int
	Title        string
	Body         string
	HeadSHA      string   // commit checked out in Dir; pinHead verifies it against the head GitHub reports
	BaseBranch   string   // base branch name (e.g. "main")
	HeadBranch   string   // head branch name (e.g. "feature/CC-0042")
	Dir          string   // local checkout directory (temp dir, caller must clean up)
	ChangedFiles []string // repo-relative paths of files changed between base and head
	// FileChanges lists every file changed between base and head with its
	// status and line counts (DiffFileChanges); nil when git could not list
	// them. The review prompt's Changed Files section is built from it
	// (decision 138).
	FileChanges []FileChange
	// Local marks a PR whose Dir is the user's working tree (via --local). When
	// set, Cleanup is a no-op so the user's checkout is never deleted.
	Local bool
}

// BranchRef identifies a checkout's current (head) branch and the repository's
// default (base) branch, resolved from git alone — no pull request required. It
// is the no-PR analog of the PR metadata the implement command's simplify,
// review, and finalize passes need: they scope the diff and the fold-rebase
// range against the base branch before any pull request exists (the finalize
// pass opens the PR last, once those passes have run on the local branch).
type BranchRef struct {
	BaseBranch string // repository default branch (e.g. "main"), read from origin/HEAD
	HeadBranch string // currently checked-out branch (e.g. "implement/issue-42-foo")
}

// CurrentBranchRef resolves the checkout's current branch and the repository's
// default branch from git in dir, without consulting GitHub. The head branch
// comes from `git rev-parse --abbrev-ref HEAD`; the base branch is read from
// refs/remotes/origin/HEAD (the default branch the remote HEAD points at, set
// by clone). It is the no-PR replacement for the old CurrentPR: the implement
// command now runs its simplify and review passes on the local feature branch
// before any pull request exists, so it cannot ask gh for a PR's base/head.
func (Client) CurrentBranchRef(dir string) (*BranchRef, error) {
	head, err := gitOutput(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("resolving current branch: %w", err)
	}
	base, err := defaultBranch(dir)
	if err != nil {
		return nil, err
	}
	return &BranchRef{BaseBranch: base, HeadBranch: head}, nil
}

// HeadSHA resolves the checkout's current commit in dir. The implement command's
// review loop records it before an editing pass runs, so the next round can scope
// its re-review to what that pass changed (`git diff <sha>`). The full object id
// is returned rather than a ref name because the editing passes fold their fixes
// into the commits that introduced them, rewriting the branch: a branch name or a
// HEAD~n offset would move under that rewrite, while the recorded object stays
// diffable against the rewritten HEAD.
func (Client) HeadSHA(dir string) (string, error) {
	sha, err := gitOutput(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolving HEAD: %w", err)
	}
	return sha, nil
}

// HasCommit reports whether sha names a commit object the checkout in dir can
// diff against. A resumed implement run reads the pre-fix commit its last review
// round recorded on the issue and must not hand the finder a base the repository
// no longer has (a fresh clone carries only the rewritten branch, not the
// commits an autosquash replaced); an unresolvable sha is reported as absent
// rather than as an error, since the caller falls back to the branch-wide scope
// either way.
func (Client) HasCommit(dir, sha string) bool {
	if sha == "" {
		return false
	}
	_, err := gitOutput(dir, "cat-file", "-e", sha+"^{commit}")
	return err == nil
}

// defaultBranch resolves the repository's default branch name (e.g. "main" or
// "master") for the checkout in dir from refs/remotes/origin/HEAD, which clone
// points at the remote's default branch. The symbolic ref reads back as
// "origin/<branch>"; stripParseDefaultBranch trims the remote prefix.
func defaultBranch(dir string) (string, error) {
	out, err := gitOutput(dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", fmt.Errorf("resolving default branch from origin/HEAD: %w", err)
	}
	return parseDefaultBranch(out), nil
}

// parseDefaultBranch turns the `git symbolic-ref --short refs/remotes/origin/HEAD`
// output (e.g. "origin/main") into the bare branch name ("main"). Split out so
// the prefix stripping is unit-testable without a git subprocess.
func parseDefaultBranch(symbolicRef string) string {
	return strings.TrimPrefix(strings.TrimSpace(symbolicRef), "origin/")
}

// gitOutput runs a git command in dir and returns its trimmed stdout, wrapping
// any failure with the command and git's stderr so callers can log the cause.
func gitOutput(dir string, args ...string) (string, error) {
	out, err := gitBytes(dir, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// gitBytes is gitOutput for output whose bytes must survive as git wrote
// them, such as the NUL-terminated paths of a -z listing: same timeout, same
// error wrapping, stdout untrimmed. A process git leaves holding the output
// pipes keeps it waiting no longer than gitWaitDelay.
func gitBytes(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitRemoteTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.WaitDelay = gitWaitDelay
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// FetchAndCheckout retrieves PR metadata and checks out the PR locally into a
// temp directory, then pins HeadSHA to the checked-out commit (pinHead). On a
// pin failure the temp directory is removed before the error is returned.
func (c Client) FetchAndCheckout(ref string) (*PR, error) {
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
	if err := pinHead(pr, func() (string, error) {
		m, err := ghJSON(fullName, number)
		return m.HeadRefOid, err
	}); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("pinning the PR head: %w", err)
	}
	changed, err := c.DiffNames(dir, pr.BaseBranch)
	if err != nil {
		slog.Warn("listing changed files failed; feature detection and specialist gating may be degraded", "err", err, "dir", dir, "base", pr.BaseBranch)
	}
	pr.ChangedFiles = changed
	fileChanges, err := c.DiffFileChanges(dir, pr.BaseBranch)
	if err != nil {
		slog.Warn("listing changed-file stats failed; the review lists no changed files and records no file coverage", "err", err, "dir", dir, "base", pr.BaseBranch)
	}
	pr.FileChanges = fileChanges

	return pr, nil
}

// pinHead verifies that the commit checked out in pr.Dir is the pull
// request's head and makes pr.HeadSHA name it. It compares `git rev-parse
// HEAD` with pr.HeadSHA and calls refresh, at most once and only on a
// mismatch, for the head GitHub reports now. When that names the checked-out
// commit, the branch moved between the metadata read and the checkout:
// pr.HeadSHA takes that commit and a warning logs both SHAs. When it does
// not, the error wraps ErrHeadMismatch and, for a Local PR, names the
// remedy. pr.HeadSHA is unchanged on every error (decision 130).
func pinHead(pr *PR, refresh func() (string, error)) error {
	head, err := gitOutput(pr.Dir, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("resolving the checked-out head: %w", err)
	}
	if head == pr.HeadSHA {
		return nil
	}
	current, err := refresh()
	if err != nil {
		return fmt.Errorf("re-reading the head of %s/%s#%d: %w", pr.Owner, pr.Repo, pr.Number, err)
	}
	if current != head {
		if pr.Local {
			return fmt.Errorf("%w: %s/%s#%d reports head %s, but the local checkout is at %s; if the branch carries commits the pull request does not have, push them or reset the branch to the pull request head with gh pr checkout --force %d; then re-run (or run without --local)",
				ErrHeadMismatch, pr.Owner, pr.Repo, pr.Number, current, head, pr.Number)
		}
		return fmt.Errorf("%w: %s/%s#%d reports head %s, but the checkout is at %s; the branch may have moved again, re-run",
			ErrHeadMismatch, pr.Owner, pr.Repo, pr.Number, current, head)
	}
	slog.Warn("pull request head moved between the metadata read and the checkout; using the checked-out commit",
		"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number), "reported", pr.HeadSHA, "head", head)
	pr.HeadSHA = head
	return nil
}

// DiffNames returns repo-relative paths of files changed between the base
// branch and HEAD (the range origin/<base>...HEAD). An empty dir or baseBranch
// yields a nil slice and no error. On subprocess failure it returns a nil slice
// and an error wrapping git's stderr, so callers can log the cause before
// degrading gracefully. It is exported so the implement command's review pass
// can adaptively gate its specialist fan-out on the branch's changed files.
// A path is returned as stored, bytes above 0x7F and whitespace at either end
// included; one holding a double quote, a backslash, or an ASCII control
// character (below 0x20, or DEL) comes back C-quoted and matches no file. git
// does not quote the C1 controls or U+2028 and U+2029, so a caller that lists
// the paths one per line cannot rely on git to hold each to one line
// (decision 131).
func (Client) DiffNames(dir, baseBranch string) ([]string, error) {
	if dir == "" || baseBranch == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitRemoteTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-c", "core.quotePath=false", "diff", "--name-only", "origin/"+baseBranch+"...HEAD")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --name-only origin/%s...HEAD: %w: %s", baseBranch, err, strings.TrimSpace(stderr.String()))
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if line != "" {
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
	dir, err := os.MkdirTemp("", "planwerk-agent-*")
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

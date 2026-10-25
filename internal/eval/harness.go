package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
	"github.com/planwerk/planwerk-agent/internal/review"
)

// evalBaseBranch is the base branch every eval repo is built on and the
// review pipeline diffs against, and evalHeadBranch the branch the reviewed
// change is checked out on.
const (
	evalBaseBranch = "main"
	evalHeadBranch = "eval/head"
)

// gitTimeout bounds each git subprocess in the harness setup.
const gitTimeout = 60 * time.Second

// gitWaitDelay bounds how long a gitRun call may still block once git has
// exited or its timeout has killed it. The remote helper a fetch starts
// inherits git's stderr and outlives a killed git, so Wait would keep reading
// the pipe until the helper ends, which on a stalled connection is never;
// this long after, Wait closes it instead (diffscope.gitWaitDelay).
const gitWaitDelay = 2 * time.Second

// evalGoMod is the go.mod ensureGoMod writes into every corpus repo, so
// technology detection tags the repo "go" and the review loads the Go patterns
// a real Go repository gets (decision 106). An AACR-Bench repo is a real
// repository and gets none: decision 106 applies to the corpus only.
const evalGoMod = "module planwerk-eval/corpus\n\ngo 1.22\n"

// evalGitHubClient is a review.GitHubClient stub for the eval harness. It hands
// the pipeline a pre-built local PR rooted at the case's repo and no-ops every
// posting/fetching method — no gh CLI, no network. The PR is marked Local so the
// pipeline's deferred pr.Cleanup never deletes the repo; the harness removes the
// temp dir itself.
type evalGitHubClient struct {
	pr *github.PR
}

func (c *evalGitHubClient) FetchAndCheckout(string) (*github.PR, error) { return c.pr, nil }

func (c *evalGitHubClient) OpenLocalPR(string, github.LocalOptions) (*github.PR, error) {
	return c.pr, nil
}

func (c *evalGitHubClient) PostPRComment(string, string, int, string) (string, error) {
	return "", nil
}

func (c *evalGitHubClient) AddPRComment(string, string, int, string) (string, error) {
	return "", nil
}

func (c *evalGitHubClient) SubmitPRReview(string, string, int, string, string, []github.ReviewComment) (string, error) {
	return "", nil
}

func (c *evalGitHubClient) FetchDiff(string, string, int) (string, error) { return "", nil }

func (c *evalGitHubClient) FetchReviewComment(string, string, int) (string, bool, error) {
	return "", false, nil
}

func (c *evalGitHubClient) FetchReviewThreads(string, string, int) ([]github.ReviewThread, error) {
	return nil, nil
}

func (c *evalGitHubClient) ResolveReviewThread(string) error { return nil }

func (c *evalGitHubClient) ViewerLogin() (string, error) { return "", nil }

// RunOptions selects the review passes a case run adds to the primary review.
// Thorough mirrors `review --thorough` (the adversarial pass) and Specialists
// mirrors `review --specialists` (the domain specialist fan-out).
// ScopePatterns mirrors `review --scope-patterns`, which narrows the primary
// review's pattern bodies to the changed files (decision 141).
type RunOptions struct {
	Thorough      bool `json:"thorough"`
	Specialists   bool `json:"specialists"`
	ScopePatterns bool `json:"scope_patterns,omitempty"`
}

// reviewOptions returns the review.Options a case run passes to the shipped
// pipeline for opts: a fresh, uncached, capture-free JSON review at the lowest
// thresholds, with the passes and the scope opts selects. RunCase adds what the
// client decides (DiffExclusions).
func reviewOptions(opts RunOptions) review.Options {
	return review.Options{
		NoCache:       true,
		NoCapture:     true,
		Format:        "json",
		MinSeverity:   report.SeverityInfo,
		MinConfidence: report.ConfidenceUncertain,
		Thorough:      opts.Thorough,
		Specialists:   opts.Specialists,
		ScopePatterns: opts.ScopePatterns,
	}
}

// Run is one review of one case: the parsed JSON review result and the token
// usage the run spent. Verdicts holds JudgeRun's answers for an AACR-Bench run
// judged under -judge, keyed by the pair's indices; nil means the run was not
// judged, and ScoreRun then matches on lines alone.
type Run struct {
	Result   report.ReviewResult
	Usage    report.Usage
	Verdicts map[PairKey]bool
}

// failedPassesError names each requested pass the review recorded as failed,
// with its error, or returns nil when none did. The pipeline drops a failed
// pass and goes on, so scoring the run would count the lost pass as one that
// found nothing (decisions 106 and 135).
func failedPassesError(result report.ReviewResult) error {
	failed := result.FailedPasses()
	if len(failed) == 0 {
		return nil
	}
	msgs := make([]string, len(failed))
	for i, p := range failed {
		msgs[i] = p.Pass + ": " + p.Error
	}
	return errors.New(strings.Join(msgs, "; "))
}

// RunCase builds c's repository in a throwaway directory (checkoutCase), runs
// the shipped review pipeline against it with the passes opts selects, and
// returns the parsed JSON review result with the run's usage. An AACR-Bench
// case must have been through PrepareAACR. It returns an error only for
// harness failures (git, materialization, pipeline, JSON, and a requested
// pass the result records as failed: the adversarial pass, a specialist, the
// feature-compliance check, the coverage map), never for a low-quality
// result, which is scored, not errored.
//
// client must be fresh: Usage is client.UsageTotals() read after the pipeline
// returns, which is cumulative over the client's life, so a reused client folds
// every earlier run into this one.
func RunCase(client *claude.Client, c Case, opts RunOptions) (Run, error) {
	repoDir, err := os.MkdirTemp("", "planwerk-eval-"+c.Name+"-*")
	if err != nil {
		return Run{}, fmt.Errorf("case %s: temp repo: %w", c.Name, err)
	}
	defer func() { _ = os.RemoveAll(repoDir) }()

	pr, err := checkoutCase(repoDir, c)
	if err != nil {
		return Run{}, fmt.Errorf("case %s: %w", c.Name, err)
	}

	runner := review.NewRunner(client)
	runner.GitHub = &evalGitHubClient{pr: pr}
	// Keep the run hermetic: never resolve a real wiki (no clone, no network).
	runner.ResolveWiki = func(string, string, patterns.WikiOptions, patterns.RemoteOptions) patterns.ResolvedWiki {
		return patterns.ResolvedWiki{}
	}

	reviewOpts := reviewOptions(opts)
	// PLANWERK_DIFF_EXCLUSIONS reaches the review session as it reaches the
	// finder passes. The corpus holds no file it leaves out; an AACR-Bench
	// pull request can.
	reviewOpts.DiffExclusions = client.DiffExclusions()

	var buf bytes.Buffer
	if err := runner.Run(&buf, reviewOpts); err != nil {
		return Run{}, fmt.Errorf("case %s: review pipeline: %w", c.Name, err)
	}

	var result report.ReviewResult
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		return Run{}, fmt.Errorf("case %s: parsing review JSON: %w", c.Name, err)
	}
	if err := failedPassesError(result); err != nil {
		return Run{}, fmt.Errorf("case %s: a review pass failed, and scoring the run would count it as a miss: %w", c.Name, err)
	}
	return Run{Result: result, Usage: client.UsageTotals()}, nil
}

// checkoutCase builds c's repository in repoDir and returns the local pull
// request the stub client hands the pipeline: the corpus trees (setupRepo)
// under a placeholder owner and number for a corpus case, the benchmark
// revision (setupAACRRepo) under its real owner, repository and number for an
// AACR-Bench case. An AACR-Bench pull request has no title or body: the export
// holds no title, and the reference comments must not reach the review
// prompt, which fences the title in. Either pull request carries the
// FileChanges FetchAndCheckout would list, so the review prompt lists the
// changed files and the run records which the session read, as on a real
// pull request (decision 138).
func checkoutCase(repoDir string, c Case) (*github.PR, error) {
	if src := c.AACR; src != nil {
		headSHA, changed, err := setupAACRRepo(repoDir, src)
		if err != nil {
			return nil, err
		}
		// origin/main carries the merge base's tree and HEAD the revision's,
		// so these are the revision's own changes.
		fileChanges, err := (github.Client{}).DiffFileChanges(repoDir, evalBaseBranch)
		if err != nil {
			return nil, fmt.Errorf("listing changed files: %w", err)
		}
		return &github.PR{
			Owner:  src.Owner,
			Repo:   src.Repo,
			Number: src.Number,
			// The harness's own head commit, which HEAD is, not the export's
			// pr_target_commit.
			HeadSHA:      headSHA,
			BaseBranch:   evalBaseBranch,
			HeadBranch:   evalHeadBranch,
			Dir:          repoDir,
			ChangedFiles: changed,
			FileChanges:  fileChanges,
			Local:        true,
		}, nil
	}

	changed, err := setupRepo(repoDir, c)
	if err != nil {
		return nil, err
	}
	fileChanges, err := (github.Client{}).DiffFileChanges(repoDir, evalBaseBranch)
	if err != nil {
		return nil, fmt.Errorf("listing changed files: %w", err)
	}
	return &github.PR{
		Owner:        "planwerk-eval",
		Repo:         "corpus",
		Number:       1,
		Title:        c.Expected.Description,
		HeadSHA:      "eval-head",
		BaseBranch:   evalBaseBranch,
		HeadBranch:   "eval/head",
		Dir:          repoDir,
		ChangedFiles: changed,
		FileChanges:  fileChanges,
		Local:        true,
	}, nil
}

// setupRepo builds a git repo in dir: base/ committed on main together with a
// go.mod (see ensureGoMod), an origin/main ref pointing at that commit (so the
// pipeline's `git diff origin/main` scoping resolves), then head/ overlaid and
// committed on a feature branch. It returns the repo-relative paths overlaid
// from head/ (the changed-files set the PR carries).
func setupRepo(dir string, c Case) ([]string, error) {
	if err := initRepo(dir); err != nil {
		return nil, err
	}

	if _, err := materialize(filepath.Join(c.Dir, "base"), dir); err != nil {
		return nil, fmt.Errorf("materializing base: %w", err)
	}
	if err := ensureGoMod(dir); err != nil {
		return nil, err
	}
	if err := git(dir, "add", "-A"); err != nil {
		return nil, err
	}
	if err := git(dir, "commit", "-m", "base"); err != nil {
		return nil, err
	}
	// Point origin/main at the base commit and origin/HEAD at it, mirroring a
	// fresh clone so the pipeline's origin/<base> references resolve.
	if err := git(dir, "update-ref", "refs/remotes/origin/"+evalBaseBranch, "HEAD"); err != nil {
		return nil, err
	}
	if err := git(dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/"+evalBaseBranch); err != nil {
		return nil, err
	}

	if err := git(dir, "checkout", "-b", "eval/head"); err != nil {
		return nil, err
	}
	changed, err := materialize(filepath.Join(c.Dir, "head"), dir)
	if err != nil {
		return nil, fmt.Errorf("materializing head: %w", err)
	}
	if err := git(dir, "add", "-A"); err != nil {
		return nil, err
	}
	if err := git(dir, "commit", "-m", "head"); err != nil {
		return nil, err
	}
	return changed, nil
}

// initRepo runs git init in dir with HEAD on main, whatever the user's
// init.defaultBranch, and sets the committer identity and unsigned commits the
// harness's own commits need.
func initRepo(dir string) error {
	if err := git(dir, "init"); err != nil {
		return err
	}
	if err := git(dir, "symbolic-ref", "HEAD", "refs/heads/"+evalBaseBranch); err != nil {
		return err
	}
	for _, kv := range [][2]string{
		{"user.email", "eval@planwerk.invalid"},
		{"user.name", "planwerk-eval"},
		{"commit.gpgsign", "false"},
	} {
		if err := git(dir, "config", kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

// ensureGoMod writes evalGoMod to dir/go.mod when no go.mod exists there, and
// leaves an existing one untouched. setupRepo calls it before the base commit,
// so the file never appears in the changed-files set; setupAACRRepo never
// calls it.
func ensureGoMod(dir string) error {
	f, err := os.OpenFile(filepath.Join(dir, "go.mod"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("writing go.mod: %w", err)
	}
	if _, err := f.WriteString(evalGoMod); err != nil {
		_ = f.Close()
		return fmt.Errorf("writing go.mod: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("writing go.mod: %w", err)
	}
	return nil
}

// materialize copies every file under srcDir into dstDir, stripping the .txt from
// any .go.txt name (see the package doc) so corpus sources land as real .go
// files. It returns the repo-relative slash paths of the files written, which for
// the head overlay is the changed-files set.
func materialize(srcDir, dstDir string) ([]string, error) {
	var written []string
	err := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := materializedName(rel)
		dst := filepath.Join(dstDir, target)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o750)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return err
		}
		if err := copyFile(path, dst); err != nil {
			return err
		}
		written = append(written, filepath.ToSlash(target))
		return nil
	})
	return written, err
}

// copyFile streams the contents of src into a freshly created dst with
// owner-only permissions.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// materializedName maps a corpus-relative name to its materialized name: a
// .go.txt source becomes .go; everything else is copied verbatim.
func materializedName(name string) string {
	if strings.HasSuffix(name, ".go.txt") {
		return strings.TrimSuffix(name, ".txt")
	}
	return name
}

// git runs a git subprocess in dir under gitTimeout, wrapping any failure with
// the command and git's stderr so a setup failure names its cause.
func git(dir string, args ...string) error {
	_, err := gitRun(dir, gitTimeout, nil, args...)
	return err
}

// gitRun runs a git subprocess in dir under timeout, and gitWaitDelay past it,
// with env added to the process environment, and returns its stdout with
// surrounding space trimmed. A failure is wrapped with the command and git's
// stderr, as git wraps it.
func gitRun(dir string, timeout time.Duration, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.WaitDelay = gitWaitDelay
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

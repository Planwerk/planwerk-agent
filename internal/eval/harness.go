package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// evalBaseBranch is the base branch the corpus repos are built on and the
// review pipeline diffs against.
const evalBaseBranch = "main"

// gitTimeout bounds each git subprocess in the harness setup.
const gitTimeout = 60 * time.Second

// evalGitHubClient is a review.GitHubClient stub for the eval harness. It hands
// the pipeline a pre-built local PR rooted at the corpus repo and no-ops every
// posting/fetching method — no gh CLI, no network. The PR is marked Local so the
// pipeline's deferred pr.Cleanup never deletes the repo; the harness removes the
// temp dir itself.
type evalGitHubClient struct {
	pr *github.PR
}

func (c *evalGitHubClient) FetchAndCheckout(string) (*github.PR, error) { return c.pr, nil }

func (c *evalGitHubClient) FetchAndCheckoutLocal(string, github.LocalOptions) (*github.PR, error) {
	return c.pr, nil
}

func (c *evalGitHubClient) PostPRComment(string, string, int, string) (string, error) {
	return "", nil
}

func (c *evalGitHubClient) SubmitPRReview(string, string, int, string, string, []github.ReviewComment) (string, error) {
	return "", nil
}

func (c *evalGitHubClient) FetchDiff(string, string, int) (string, error) { return "", nil }

func (c *evalGitHubClient) FetchReviewComment(string, string, int) (string, bool, error) {
	return "", false, nil
}

// RunCase materializes c into a throwaway git repo, runs the shipped review
// pipeline against it, and returns the parsed JSON review result. It returns an
// error only for harness failures (git, materialization, pipeline, JSON) — never
// for a low-quality result, which is scored, not errored. thorough enables the
// adversarial pass, mirroring `review --thorough`.
func RunCase(client *claude.Client, c Case, thorough bool) (report.ReviewResult, error) {
	repoDir, err := os.MkdirTemp("", "planwerk-eval-"+c.Name+"-*")
	if err != nil {
		return report.ReviewResult{}, fmt.Errorf("case %s: temp repo: %w", c.Name, err)
	}
	defer os.RemoveAll(repoDir)

	changed, err := setupRepo(repoDir, c)
	if err != nil {
		return report.ReviewResult{}, fmt.Errorf("case %s: %w", c.Name, err)
	}

	pr := &github.PR{
		Owner:        "planwerk-eval",
		Repo:         "corpus",
		Number:       1,
		Title:        c.Expected.Description,
		HeadSHA:      "eval-head",
		BaseBranch:   evalBaseBranch,
		HeadBranch:   "eval/head",
		Dir:          repoDir,
		ChangedFiles: changed,
		Local:        true,
	}

	runner := review.NewRunner(client)
	runner.GitHub = &evalGitHubClient{pr: pr}
	// Keep the run hermetic: never resolve a real wiki (no clone, no network).
	runner.ResolveWiki = func(string, string, patterns.WikiOptions, patterns.RemoteOptions) patterns.ResolvedWiki {
		return patterns.ResolvedWiki{}
	}

	opts := review.Options{
		NoCache:       true,
		NoCapture:     true,
		Format:        "json",
		MinSeverity:   report.SeverityInfo,
		MinConfidence: report.ConfidenceUncertain,
		Thorough:      thorough,
	}

	var buf bytes.Buffer
	if err := runner.Run(&buf, opts); err != nil {
		return report.ReviewResult{}, fmt.Errorf("case %s: review pipeline: %w", c.Name, err)
	}

	var result report.ReviewResult
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		return report.ReviewResult{}, fmt.Errorf("case %s: parsing review JSON: %w", c.Name, err)
	}
	return result, nil
}

// setupRepo builds a git repo in dir: base/ committed on main, an origin/main ref
// pointing at that commit (so the pipeline's `git diff origin/main` scoping
// resolves), then head/ overlaid and committed on a feature branch. It returns
// the repo-relative paths overlaid from head/ (the changed-files set the PR
// carries).
func setupRepo(dir string, c Case) ([]string, error) {
	// Force the first commit onto main regardless of the user's init.defaultBranch.
	if err := git(dir, "init"); err != nil {
		return nil, err
	}
	if err := git(dir, "symbolic-ref", "HEAD", "refs/heads/"+evalBaseBranch); err != nil {
		return nil, err
	}
	for _, kv := range [][2]string{
		{"user.email", "eval@planwerk.invalid"},
		{"user.name", "planwerk-eval"},
		{"commit.gpgsign", "false"},
	} {
		if err := git(dir, "config", kv[0], kv[1]); err != nil {
			return nil, err
		}
	}

	if _, err := materialize(filepath.Join(c.Dir, "base"), dir); err != nil {
		return nil, fmt.Errorf("materializing base: %w", err)
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
			return os.MkdirAll(dst, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
		written = append(written, filepath.ToSlash(target))
		return nil
	})
	return written, err
}

// materializedName maps a corpus-relative name to its materialized name: a
// .go.txt source becomes .go; everything else is copied verbatim.
func materializedName(name string) string {
	if strings.HasSuffix(name, ".go.txt") {
		return strings.TrimSuffix(name, ".txt")
	}
	return name
}

// git runs a git subprocess in dir, wrapping any failure with the command and
// git's stderr so a setup failure names its cause.
func git(dir string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

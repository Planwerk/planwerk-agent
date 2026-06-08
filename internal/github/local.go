package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"

	"github.com/planwerk/planwerk-review/internal/workspace"
)

// ErrOriginMismatch is returned when an explicit owner/repo reference does not
// match the origin remote of the current working directory. Exported so the
// CLI (and tests) can match against it with errors.Is.
var ErrOriginMismatch = errors.New("origin mismatch")

// LocalOptions configures the --local constructors OpenLocalPR and
// UseLocalRepo. Force skips the dirty-working-tree confirmation prompt;
// Prompter is the interface used to ask that question when stdin is a TTY.
type LocalOptions struct {
	Force    bool
	Prompter workspace.Prompter
}

// OpenLocalPR builds a *PR rooted at the current working directory instead of
// cloning into a temp dir. It is the --local mirror of FetchAndCheckout:
//
//  1. Gate on the dirty-working-tree check (workspace.EnsureClean).
//  2. Resolve the cwd's origin owner/name.
//  3. Resolve PR metadata — from the branch's PR when ref == "", otherwise
//     from the explicit ref (rejected when its owner/name != origin).
//  4. `gh pr checkout <number>` to switch the working tree to the PR head.
//  5. `git fetch origin <base>` so origin/<base> exists for the diff query.
//
// The returned PR has Local: true, so PR.Cleanup never deletes the cwd.
func OpenLocalPR(ref string, opts LocalOptions) (*PR, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolving working directory: %w", err)
	}

	// Gate before any state-changing step (gh pr checkout, git fetch).
	if err := workspace.EnsureClean(dir, opts.Force, opts.Prompter); err != nil {
		return nil, err
	}

	originOwner, originName, err := workspace.DetectOrigin(dir)
	if err != nil {
		return nil, err
	}

	pr := &PR{Owner: originOwner, Repo: originName, Dir: dir, Local: true}

	if ref == "" {
		meta, err := ghLocalPRView(dir)
		if err != nil {
			return nil, fmt.Errorf("no PR reference given and could not infer one from the current branch; pass an explicit PR reference: %w", err)
		}
		pr.Number = meta.Number
		pr.Title = meta.Title
		pr.Body = meta.Body
		pr.HeadSHA = meta.HeadRefOid
		pr.BaseBranch = meta.BaseRefName
		pr.HeadBranch = meta.HeadRefName
	} else {
		owner, repo, number, err := ParseRef(ref)
		if err != nil {
			return nil, err
		}
		if owner != originOwner || repo != originName {
			return nil, fmt.Errorf("%w: ref %s/%s does not match origin %s/%s in %s",
				ErrOriginMismatch, owner, repo, originOwner, originName, dir)
		}
		meta, err := ghJSON(fmt.Sprintf("%s/%s", owner, repo), number)
		if err != nil {
			return nil, fmt.Errorf("fetching PR metadata: %w", err)
		}
		pr.Owner = owner
		pr.Repo = repo
		pr.Number = number
		pr.Title = meta.Title
		pr.Body = meta.Body
		pr.HeadSHA = meta.HeadRefOid
		pr.BaseBranch = meta.BaseRefName
		pr.HeadBranch = meta.HeadRefName
	}

	// Switch the working tree to the PR head — no restore. Safety #3.
	if err := localPRCheckout(dir, pr.Number); err != nil {
		return nil, err
	}
	// Ensure origin/<base> exists for the diff query in diffNames. Safety #5.
	if err := localFetchBase(dir, pr.BaseBranch); err != nil {
		return nil, err
	}

	pr.ChangedFiles = diffNames(dir, pr.BaseBranch)
	return pr, nil
}

// UseLocalRepo builds a *Repo rooted at the current working directory instead
// of cloning into a temp dir. It is the --local mirror of CloneRepo, minus the
// PR checkout: it only gates on the dirty tree, resolves origin, and (when an
// explicit ref is given) rejects an origin mismatch. The returned Repo has
// Local: true so Repo.Cleanup never deletes the cwd.
func UseLocalRepo(ref string, opts LocalOptions) (*Repo, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolving working directory: %w", err)
	}

	if err := workspace.EnsureClean(dir, opts.Force, opts.Prompter); err != nil {
		return nil, err
	}

	originOwner, originName, err := workspace.DetectOrigin(dir)
	if err != nil {
		return nil, err
	}

	owner, name := originOwner, originName
	if ref != "" {
		o, n, err := ParseRepoRef(ref)
		if err != nil {
			return nil, err
		}
		if o != originOwner || n != originName {
			return nil, fmt.Errorf("%w: ref %s/%s does not match origin %s/%s in %s",
				ErrOriginMismatch, o, n, originOwner, originName, dir)
		}
		owner, name = o, n
	}

	return &Repo{Owner: owner, Name: name, Dir: dir, Local: true}, nil
}

// prMetaLocal mirrors prMeta but also carries the PR number so the ref-less
// `gh pr view` (which infers the PR from the current branch) can report which
// PR it resolved.
type prMetaLocal struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	HeadRefOid  string `json:"headRefOid"`
	BaseRefName string `json:"baseRefName"`
	HeadRefName string `json:"headRefName"`
}

// ghLocalPRView runs `gh pr view` in dir with no --repo so gh infers the PR
// associated with the current branch.
func ghLocalPRView(dir string) (prMetaLocal, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "pr", "view",
		"--json", "number,title,body,headRefOid,baseRefName,headRefName")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return prMetaLocal{}, fmt.Errorf("gh pr view: %w", err)
	}
	var m prMetaLocal
	if err := json.Unmarshal(out, &m); err != nil {
		return prMetaLocal{}, fmt.Errorf("parsing gh output: %w", err)
	}
	return m, nil
}

// localPRCheckout switches the working tree in dir to the PR head via gh.
func localPRCheckout(dir string, number int) error {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "pr", "checkout", strconv.Itoa(number))
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gh pr checkout %d: %w", number, err)
	}
	return nil
}

// localFetchBase fetches the base branch so origin/<base> exists for the diff
// query in diffNames. Best-effort beyond a hard error: an empty base is a
// no-op (the diff query degrades gracefully).
func localFetchBase(dir, baseBranch string) error {
	if baseBranch == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitRemoteTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "fetch", "origin", baseBranch)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git fetch origin %s: %w", baseBranch, err)
	}
	return nil
}

// PullFFOnly fast-forwards the checkout in dir to the latest commits on branch.
// The fix loop uses it in --local mode to pick up the previous iteration's
// follow-up commit without a re-clone.
func PullFFOnly(dir, branch string) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitRemoteTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "pull", "--ff-only", "origin", branch)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git pull --ff-only origin %s: %w", branch, err)
	}
	return nil
}

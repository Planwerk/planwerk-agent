package github

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ImprovementFile is a single repo-relative file rewrite that
// OpenImprovementPR will stage, commit, and push.
type ImprovementFile struct {
	RelativePath string
	Content      []byte
}

// ImprovementPROptions configures OpenImprovementPR.
type ImprovementPROptions struct {
	// Branch is the head branch for the PR. Required.
	Branch string
	// Base is the target branch. Empty falls back to whatever the local
	// clone's HEAD points at (typically the default branch).
	Base string
	// Title is the PR title.
	Title string
	// Body is the PR body (Markdown).
	Body string
	// Commit is the commit subject (and optional body, separated by \n\n).
	Commit string
	// Files is the set of file rewrites the commit should contain.
	Files []ImprovementFile
}

// OpenImprovementPR writes every file from opts.Files into the cloned repo
// at repo.Dir, creates a fresh branch, commits the rewrites, pushes, and
// opens a pull request via gh. It returns the URL of the new PR.
//
// The function is intentionally narrow: it does not amend existing branches,
// does not handle merge conflicts, and does not retry on a remote-side
// rejection — the caller is expected to invoke it on a fresh clone whose
// working tree has not been touched by anything else.
func OpenImprovementPR(repo *Repo, opts ImprovementPROptions) (string, error) {
	if repo == nil || repo.Dir == "" {
		return "", fmt.Errorf("OpenImprovementPR: repo with a working directory is required")
	}
	if opts.Branch == "" {
		return "", fmt.Errorf("OpenImprovementPR: branch name is required")
	}
	if len(opts.Files) == 0 {
		return "", fmt.Errorf("OpenImprovementPR: at least one file is required")
	}
	if opts.Title == "" {
		return "", fmt.Errorf("OpenImprovementPR: title is required")
	}
	if opts.Commit == "" {
		opts.Commit = opts.Title
	}

	if err := writeFiles(repo.Dir, opts.Files); err != nil {
		return "", fmt.Errorf("writing improvement files: %w", err)
	}

	if err := runGit(repo.Dir, "checkout", "-b", opts.Branch); err != nil {
		return "", fmt.Errorf("creating branch %s: %w", opts.Branch, err)
	}

	addArgs := append([]string{"add", "--"}, relPaths(opts.Files)...)
	if err := runGit(repo.Dir, addArgs...); err != nil {
		return "", fmt.Errorf("staging improvement files: %w", err)
	}

	// Skip a no-op commit so a Claude run that produced unchanged output
	// (semantically identical to the original) does not open an empty PR.
	dirty, err := hasStagedChanges(repo.Dir)
	if err != nil {
		return "", err
	}
	if !dirty {
		return "", fmt.Errorf("no changes to commit — Claude returned files identical to the originals")
	}

	if err := runGit(repo.Dir, "commit", "-m", opts.Commit); err != nil {
		return "", fmt.Errorf("committing improvements: %w", err)
	}

	pushArgs := []string{"push", "-u", "origin", opts.Branch}
	if err := runGit(repo.Dir, pushArgs...); err != nil {
		return "", fmt.Errorf("pushing branch %s: %w", opts.Branch, err)
	}

	createArgs := []string{
		"pr", "create",
		"--title", opts.Title,
		"--body", opts.Body,
		"--head", opts.Branch,
	}
	if opts.Base != "" {
		createArgs = append(createArgs, "--base", opts.Base)
	}
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", createArgs...)
	cmd.Dir = repo.Dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh pr create: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func writeFiles(repoDir string, files []ImprovementFile) error {
	for _, f := range files {
		clean := filepath.Clean(f.RelativePath)
		if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			return fmt.Errorf("refusing to write outside repo: %s", f.RelativePath)
		}
		full := filepath.Join(repoDir, clean)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, f.Content, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", clean, err)
		}
	}
	return nil
}

func relPaths(files []ImprovementFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, filepath.Clean(f.RelativePath))
	}
	return out
}

func runGit(dir string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitRemoteTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stderr
	return cmd.Run()
}

func hasStagedChanges(dir string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitRemoteTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "diff", "--cached", "--quiet")
	cmd.Dir = dir
	err := cmd.Run()
	if err == nil {
		return false, nil // exit 0 = no diff
	}
	var exitErr *exec.ExitError
	if asExit(err, &exitErr) && exitErr.ExitCode() == 1 {
		return true, nil // exit 1 = diff present
	}
	return false, fmt.Errorf("git diff --cached: %w", err)
}

// asExit unwraps an *exec.ExitError without pulling errors.As into every
// caller; lives here so the helper is usable without an extra import in
// upstream packages.
func asExit(err error, target **exec.ExitError) bool {
	if err == nil {
		return false
	}
	if e, ok := err.(*exec.ExitError); ok {
		*target = e
		return true
	}
	return false
}

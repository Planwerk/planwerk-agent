package patterns

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// wikiPushTimeout bounds the git add/rm/commit/push that write a wiki
// reconciliation or capture back to the remote.
const wikiPushTimeout = 2 * time.Minute

// Wiki write-back commits are authored by the tool, not a human: the temp clone
// the deletions or additions are applied in carries no user identity, so the
// committer is pinned here rather than relying on ambient git config (absent in
// CI).
const (
	wikiCommitterName  = "planwerk-agent"
	wikiCommitterEmail = "planwerk-agent@users.noreply.github.com"
)

// CloneWikiAuthenticated makes a fresh, full clone of the target repo's wiki into
// a private temp directory and returns the clone root, its resolved HEAD commit,
// and a cleanup function the caller must defer. repo is "owner/name"; ref pins a
// branch (empty uses the wiki's default branch). The clone authenticates a
// private wiki with a `gh auth token` injected via the GIT_CONFIG_* environment
// (never the URL or argv), reusing fetchRemote — the same machinery that clones a
// wiki knowledge source — so the push that follows works against the wiki's
// default branch.
//
// It is a dedicated fresh clone rather than the TTL-cached read clone because the
// write phase mutates and pushes: a shared cache entry could be refreshed or read
// concurrently mid-write. cleanup is always non-nil and safe to call on error.
func CloneWikiAuthenticated(repo, ref string) (dir, headSHA string, cleanup func(), err error) {
	tmp, err := os.MkdirTemp("", "planwerk-wiki-write-")
	if err != nil {
		return "", "", func() {}, fmt.Errorf("creating wiki write workspace: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }

	dest := filepath.Join(tmp, "repo")
	headSHA, err = MirrorWiki(repo, ref, dest)
	if err != nil {
		cleanup()
		return "", "", func() {}, err
	}
	return dest, headSHA, cleanup, nil
}

// MirrorWiki makes a full clone of the wiki of repo ("owner/name") at dest and
// returns its HEAD commit, or "" when the HEAD cannot be resolved (an empty
// wiki). ref pins a branch, tag, or commit; empty uses the wiki's default
// branch. The clone authenticates like CloneWikiAuthenticated. An existing
// dest is replaced only after the clone and the checkout of ref succeeded, so
// a failed call leaves it as it was.
func MirrorWiki(repo, ref, dest string) (headSHA string, err error) {
	p, err := parseWikiURI(prefixWiki + repo)
	if err != nil {
		return "", fmt.Errorf("deriving wiki clone URL for %q: %w", repo, err)
	}
	p.ref = ref
	if err := fetchRemote(p, dest); err != nil {
		return "", fmt.Errorf("cloning wiki %s: %w", repo, err)
	}
	return wikiHeadSHA(dest), nil
}

// PushWikiDeletions removes relPaths (wiki-relative, slash form) from the clone at
// dir, commits the removal with commitMsg, and pushes it to the wiki's default
// branch. The clone's .git/config holds no credential (CloneWikiAuthenticated
// injects the token transiently), so the push re-injects a `gh auth token` via
// the GIT_CONFIG_* http.extraHeader, keeping it out of argv and the clone config.
// A public wiki pushes anonymously when no token is available, which a private
// wiki rejects with an authentication error the caller surfaces.
func PushWikiDeletions(dir string, relPaths []string, commitMsg string) error {
	if len(relPaths) == 0 {
		return fmt.Errorf("no wiki entries to delete")
	}
	return pushWiki(dir, relPaths, commitMsg, ghAuthToken(context.Background()))
}

// pushWiki performs the git rm/commit/push for PushWikiDeletions. It is a package
// variable so tests can substitute an offline fake, mirroring fetchRemote.
var pushWiki = func(dir string, relPaths []string, commitMsg, token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), wikiPushTimeout)
	defer cancel()

	// relPaths are caller-supplied wiki-relative pathspecs — the sync write phase
	// passes only paths it has matched against the enumerated wiki entries — not
	// options, so the plain `--` separator (not --end-of-options) is the correct
	// guard: git reads the tokens after it as pathspecs.
	rmArgs := append([]string{"-C", dir, "rm", "--"}, relPaths...)
	if err := runWikiGit(ctx, nil, rmArgs...); err != nil {
		return fmt.Errorf("git rm: %w", err)
	}

	commitArgs := []string{
		"-C", dir,
		"-c", "user.name=" + wikiCommitterName,
		"-c", "user.email=" + wikiCommitterEmail,
		"commit", "-m", commitMsg,
	}
	if err := runWikiGit(ctx, nil, commitArgs...); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}

	if err := runWikiGit(ctx, wikiPushEnv(token), "-C", dir, "push", "origin", "HEAD"); err != nil {
		return fmt.Errorf("git push origin HEAD: %w", err)
	}
	return nil
}

// WikiFile is one page to write into the wiki clone: a wiki-relative slash path
// (e.g. "review_patterns/<slug>.md") and the full page bytes (provenance marker
// included). It is the additive counterpart to the relPaths PushWikiDeletions
// removes.
type WikiFile struct {
	Path    string
	Content string
}

// PushWikiAdditions writes files into the clone at dir, commits them with
// commitMsg, and pushes to the wiki's default branch — the additive counterpart
// to PushWikiDeletions. The clone's .git/config holds no credential
// (CloneWikiAuthenticated injects the token transiently), so the push re-injects
// a `gh auth token` via the GIT_CONFIG_* http.extraHeader, keeping it out of
// argv and the clone config. A public wiki pushes anonymously when no token is
// available, which a private wiki rejects with an authentication error the
// caller surfaces.
//
// Unlike deletions, additions handle the uninitialized-wiki case: a wiki that
// has never been written has an empty clone with no commits, so write+add+commit
// creates its first commit and the push creates the default branch.
func PushWikiAdditions(dir string, files []WikiFile, commitMsg string) error {
	if len(files) == 0 {
		return fmt.Errorf("no wiki pages to write")
	}
	return pushWikiAdditions(dir, files, commitMsg, ghAuthToken(context.Background()))
}

// pushWikiAdditions performs the write/add/commit/push for PushWikiAdditions. It
// is a package variable so tests can substitute an offline fake, mirroring
// pushWiki.
var pushWikiAdditions = func(dir string, files []WikiFile, commitMsg, token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), wikiPushTimeout)
	defer cancel()

	// The clone is a checkout of a wiki anyone may be able to edit, so a
	// directory on a page's path can be a symbolic link that points out of it.
	// Writing through the root refuses such a path, and a "../" path with it,
	// where os.WriteFile would follow the link and put the page outside the
	// clone.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("opening the wiki clone: %w", err)
	}
	defer func() { _ = root.Close() }()

	paths := make([]string, 0, len(files))
	for _, f := range files {
		name := filepath.FromSlash(f.Path)
		if err := root.MkdirAll(filepath.Dir(name), 0o750); err != nil {
			return fmt.Errorf("creating wiki page directory for %q: %w", f.Path, err)
		}
		if err := root.WriteFile(name, []byte(f.Content), 0o600); err != nil {
			return fmt.Errorf("writing wiki page %q: %w", f.Path, err)
		}
		paths = append(paths, f.Path)
	}

	// paths are caller-supplied wiki-relative pathspecs the capture write phase
	// rendered, not options, so the plain `--` separator is the correct guard:
	// git reads the tokens after it as pathspecs. Mirrors pushWiki's `git rm`.
	addArgs := append([]string{"-C", dir, "add", "--"}, paths...)
	if err := runWikiGit(ctx, nil, addArgs...); err != nil {
		return fmt.Errorf("git add: %w", err)
	}

	commitArgs := []string{
		"-C", dir,
		"-c", "user.name=" + wikiCommitterName,
		"-c", "user.email=" + wikiCommitterEmail,
		"commit", "-m", commitMsg,
	}
	if err := runWikiGit(ctx, nil, commitArgs...); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}

	if err := runWikiGit(ctx, wikiPushEnv(token), "-C", dir, "push", "origin", "HEAD"); err != nil {
		// A concurrent --capture-wiki run can advance the wiki between our fresh
		// clone and this push, rejecting it as a non-fast-forward. Rebase our commit
		// onto the updated remote HEAD and retry the push once so this run's pages
		// are not silently dropped. The committer identity is pinned for the rebase
		// the same way the commit pins it, since the clone carries no ambient git
		// config. A still-failing retry (e.g. a rebase conflict from a same-path
		// concurrent write) surfaces an error that names the dropped pages.
		rebaseArgs := []string{
			"-C", dir,
			"-c", "user.name=" + wikiCommitterName,
			"-c", "user.email=" + wikiCommitterEmail,
			"pull", "--rebase", "origin", "HEAD",
		}
		if rebaseErr := runWikiGit(ctx, wikiPushEnv(token), rebaseArgs...); rebaseErr != nil {
			return fmt.Errorf("git push origin HEAD rejected and rebasing onto the updated wiki failed, so this run's pages were not written: %w", err)
		}
		if err := runWikiGit(ctx, wikiPushEnv(token), "-C", dir, "push", "origin", "HEAD"); err != nil {
			return fmt.Errorf("git push origin HEAD still rejected after rebasing onto the updated wiki, so this run's pages were not written: %w", err)
		}
	}
	return nil
}

// wikiPushEnv builds the environment for the authenticated push. When a token is
// supplied it is injected as an http.extraHeader through the GIT_CONFIG_*
// environment, NOT a `-c http.extraHeader=` argv entry: a process's argv is
// world-readable on a shared host, so a token there would leak to any local user
// for the push window. A nil return inherits the parent environment, which is the
// anonymous (public-wiki) path. Mirrors wikiCloneCmd.
func wikiPushEnv(token string) []string {
	if token == "" {
		return nil
	}
	return append(os.Environ(),
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.extraHeader",
		"GIT_CONFIG_VALUE_0="+wikiAuthHeader(token),
	)
}

// runWikiGit runs `git <args...>` with the given environment (nil inherits the
// parent process environment) and surfaces git's stderr.
func runWikiGit(ctx context.Context, env []string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	if env != nil {
		cmd.Env = env
	}
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

package patterns

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Remote pattern source URIs come in three flavors:
//
//	git+https://example.com/repo.git[#ref[:subpath]]
//	github:owner/repo[/subpath][@ref]
//	wiki:owner/repo[/subpath][@ref]
//
// The wiki: form points at a GitHub repository's standalone wiki clone
// (<owner>/<repo>.wiki.git), distinct from cloning the code repo. Anything that
// does not start with a known prefix is treated as a local directory path so
// existing call sites keep working unchanged.
const (
	prefixGitHTTPS = "git+https://"
	prefixGitHTTP  = "git+http://"
	prefixGitHub   = "github:"
	prefixWiki     = "wiki:"
)

// DefaultRemoteTTL is the default age at which a cached remote pattern
// repository is refreshed. It is exposed so the CLI can reference the same
// constant when defining its flag default.
const DefaultRemoteTTL = 24 * time.Hour

// RemoteOptions configures how remote pattern sources are materialized into
// the local cache. The zero value resolves to the user cache directory and
// the default TTL, so callers can pass RemoteOptions{} for default behavior.
type RemoteOptions struct {
	// CacheDir is the root directory remote pattern repos are cached under.
	// When empty, defaults to <UserCacheDir>/planwerk-review/patterns.
	CacheDir string
	// TTL is the age after which a cached repo is refreshed. A value <= 0
	// disables refresh — once cached, the repo is reused indefinitely.
	TTL time.Duration
	// Now is the time source used for TTL comparisons; defaults to time.Now.
	// Tests override this to exercise refresh logic deterministically.
	Now func() time.Time
}

// IsRemote reports whether src looks like a remote pattern URI rather than a
// local directory path. The check is purely syntactic.
func IsRemote(src string) bool {
	return strings.HasPrefix(src, prefixGitHTTPS) ||
		strings.HasPrefix(src, prefixGitHTTP) ||
		strings.HasPrefix(src, prefixGitHub) ||
		strings.HasPrefix(src, prefixWiki)
}

// parsedURI is the internal representation of a remote pattern URI after
// env-var expansion and parsing.
type parsedURI struct {
	raw      string // original URI as the user typed it (post env-var expansion)
	scheme   string // "github", "git", or "wiki"
	cloneURL string // URL passed to git/gh (never carries a token; see fetchRemote)
	ref      string // branch/tag/sha to check out, or "" for default
	subpath  string // path inside the repo to load patterns from, or "" for root
}

// fingerprint returns a stable short hash of the cache-affecting portions of
// the URI. The subpath is intentionally excluded so two URIs that differ only
// in subpath share the same clone — the loader points at different
// directories within it.
func (p parsedURI) fingerprint() string {
	h := sha256.Sum256([]byte(p.scheme + "|" + p.cloneURL + "|" + p.ref))
	return fmt.Sprintf("%x", h[:8])
}

// remoteMeta is persisted next to each cached clone to record when it was
// last refreshed and which URI populated it. The URI is kept for operator
// debugging — `planwerk-review` itself only consults FetchedAt.
type remoteMeta struct {
	URI       string    `json:"uri"`
	FetchedAt time.Time `json:"fetchedAt"`
}

// ResolveRemote ensures the URI is materialized in the cache and returns the
// local directory the loader should read patterns from. It clones on first
// use, refreshes when the cached copy is older than opts.TTL, and reuses the
// existing checkout otherwise. Local-looking inputs are rejected — call
// IsRemote first or use LoadFilteredWithOptions which handles the dispatch.
func ResolveRemote(src string, opts RemoteOptions) (string, error) {
	if !IsRemote(src) {
		return "", fmt.Errorf("not a remote pattern URI: %q", src)
	}

	expanded := os.ExpandEnv(src)
	p, err := parseRemoteURI(expanded)
	if err != nil {
		return "", err
	}

	root, err := resolveCacheRoot(opts.CacheDir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("creating remote pattern cache dir: %w", err)
	}

	entryDir := filepath.Join(root, p.fingerprint())
	repoDir := filepath.Join(entryDir, "repo")
	metaPath := filepath.Join(entryDir, "meta.json")

	now := opts.Now
	if now == nil {
		now = time.Now
	}

	// Serialize concurrent invocations targeting the same URI so two clones
	// don't race for the same destination.
	unlock, err := acquireLock(entryDir)
	if err != nil {
		return "", err
	}
	defer unlock()

	needFetch := true
	if meta, ok := readRemoteMeta(metaPath); ok {
		if _, err := os.Stat(repoDir); err == nil {
			if opts.TTL <= 0 || now().Sub(meta.FetchedAt) <= opts.TTL {
				needFetch = false
			}
		}
	}

	if needFetch {
		if err := fetchRemote(p, repoDir); err != nil {
			return "", fmt.Errorf("fetching remote pattern source %q: %w", src, err)
		}
		if err := writeRemoteMeta(metaPath, remoteMeta{URI: src, FetchedAt: now().UTC()}); err != nil {
			slog.Warn("could not write remote pattern cache metadata", "err", err)
		}
	}

	final := repoDir
	if p.subpath != "" {
		final = filepath.Join(repoDir, filepath.FromSlash(p.subpath))
		info, err := os.Stat(final)
		if err != nil {
			return "", fmt.Errorf("subpath %q not found in remote pattern repo (%s): %w", p.subpath, src, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("subpath %q in remote pattern repo (%s) is not a directory", p.subpath, src)
		}
	}
	return final, nil
}

// parseRemoteURI splits a remote URI into its scheme, clone URL, ref, and
// subpath components after env-var expansion has already been applied by the
// caller.
func parseRemoteURI(uri string) (parsedURI, error) {
	switch {
	case strings.HasPrefix(uri, prefixGitHTTPS), strings.HasPrefix(uri, prefixGitHTTP):
		return parseGitURI(uri)
	case strings.HasPrefix(uri, prefixGitHub):
		return parseGitHubURI(uri)
	case strings.HasPrefix(uri, prefixWiki):
		return parseWikiURI(uri)
	default:
		return parsedURI{}, fmt.Errorf("unrecognized remote pattern URI: %q", uri)
	}
}

func parseGitURI(uri string) (parsedURI, error) {
	// strip the git+ prefix; what remains is an http(s):// URL optionally
	// followed by #ref[:subpath].
	rest := strings.TrimPrefix(uri, "git+")
	var ref, subpath string
	if i := strings.Index(rest, "#"); i >= 0 {
		frag := rest[i+1:]
		rest = rest[:i]
		if j := strings.Index(frag, ":"); j >= 0 {
			ref = frag[:j]
			subpath = strings.TrimPrefix(frag[j+1:], "/")
		} else {
			ref = frag
		}
	}
	if rest == "" {
		return parsedURI{}, fmt.Errorf("empty git URL in pattern URI %q", uri)
	}
	return parsedURI{
		raw:      uri,
		scheme:   "git",
		cloneURL: rest,
		ref:      ref,
		subpath:  subpath,
	}, nil
}

// githubRepoRe matches the owner/repo head of a github: URI. Owners and
// repos follow GitHub's own restrictions: alnum, hyphen, underscore, dot;
// no leading/trailing slash.
var githubRepoRe = regexp.MustCompile(`^([A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?)/([A-Za-z0-9._-]+)`)

// parseOwnerRepoURI splits the body of a github:/wiki: URI (everything after the
// scheme prefix) into its owner, repo, subpath, and ref. ok is false when the
// body does not start with a valid owner/repo, leaving each caller to build its
// own scheme-specific error.
func parseOwnerRepoURI(body string) (owner, repo, subpath, ref string, ok bool) {
	if i := strings.Index(body, "@"); i >= 0 {
		ref = body[i+1:]
		body = body[:i]
	}
	m := githubRepoRe.FindStringSubmatch(body)
	if m == nil {
		return "", "", "", "", false
	}
	owner, repo = m[1], m[2]
	subpath = strings.TrimPrefix(body[len(m[0]):], "/")
	return owner, repo, subpath, ref, true
}

func parseGitHubURI(uri string) (parsedURI, error) {
	owner, repo, subpath, ref, ok := parseOwnerRepoURI(strings.TrimPrefix(uri, prefixGitHub))
	if !ok {
		return parsedURI{}, fmt.Errorf("invalid github: pattern URI %q (expected github:owner/repo[/subpath][@ref])", uri)
	}
	return parsedURI{
		raw:      uri,
		scheme:   "github",
		cloneURL: owner + "/" + repo,
		ref:      ref,
		subpath:  subpath,
	}, nil
}

// parseWikiURI parses a wiki:owner/repo[/subpath][@ref] URI into a clone of the
// repository's standalone GitHub wiki (https://github.com/owner/repo.wiki.git),
// which is a separate git repository from the code repo. It mirrors
// parseGitHubURI's owner/repo parsing but derives the .wiki.git clone URL and
// uses the "wiki" scheme so fetchRemote knows to authenticate it with a GitHub
// token. A trailing ".git" on the repo segment (wiki:owner/repo.git) is stripped
// so the derived URL never becomes "repo.git.wiki.git".
func parseWikiURI(uri string) (parsedURI, error) {
	owner, repo, subpath, ref, ok := parseOwnerRepoURI(strings.TrimPrefix(uri, prefixWiki))
	if !ok {
		return parsedURI{}, fmt.Errorf("invalid wiki: pattern URI %q (expected wiki:owner/repo[/subpath][@ref])", uri)
	}
	repo = strings.TrimSuffix(repo, ".git")
	return parsedURI{
		raw:      uri,
		scheme:   "wiki",
		cloneURL: "https://github.com/" + owner + "/" + repo + ".wiki.git",
		ref:      ref,
		subpath:  subpath,
	}, nil
}

// resolveCacheRoot returns the directory remote pattern clones are stored
// under. An empty configured value falls back to a per-user cache directory.
func resolveCacheRoot(configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "planwerk-review", "patterns"), nil
	}
	return filepath.Join(dir, "planwerk-review", "patterns"), nil
}

func readRemoteMeta(path string) (remoteMeta, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return remoteMeta{}, false
	}
	var m remoteMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return remoteMeta{}, false
	}
	return m, true
}

func writeRemoteMeta(path string, m remoteMeta) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// fetchRemote (re-)materializes the URI into dest. Implemented as a package
// variable so tests can substitute an offline fake. Production behavior
// removes any existing checkout and clones fresh — pattern repos are small
// and refresh is rare, so a clean slate is simpler than reconciling state.
var fetchRemote = func(p parsedURI, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return fmt.Errorf("preparing clone parent dir: %w", err)
	}
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("clearing existing clone: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), remoteCloneTimeout)
	defer cancel()

	var cmd *exec.Cmd
	switch p.scheme {
	case "github":
		args := []string{"repo", "clone", p.cloneURL, dest, "--", "--filter=blob:none"}
		cmd = exec.CommandContext(ctx, "gh", args...)
	case "wiki":
		// A repo's wiki is a standalone .wiki.git clone that `gh repo clone`
		// cannot fetch, so it goes through plain `git clone`. A private wiki
		// needs auth: pass a GitHub token (best-effort, via `gh auth token`) as
		// a one-shot http.extraHeader through the GIT_CONFIG_* environment (see
		// wikiCloneCmd) so it never lands in the process command line, the cloned
		// repo's .git/config, or git's stderr — the clone URL itself stays
		// tokenless. A full clone (no blob:none filter) keeps every ref's objects
		// local so a later `git checkout <wiki-ref>` needs no second authenticated
		// fetch. When no token is available the clone proceeds anonymously, which
		// still works for a public wiki.
		cmd = wikiCloneCmd(ctx, p.cloneURL, dest, ghAuthToken(ctx))
	default:
		args := []string{"clone", "--filter=blob:none", p.cloneURL, dest}
		cmd = exec.CommandContext(ctx, "git", args...)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("clone: %w", err)
	}

	if p.ref != "" {
		coCtx, coCancel := context.WithTimeout(context.Background(), remoteCheckoutTimeout)
		defer coCancel()
		if err := gitCheckout(coCtx, dest, p.ref); err != nil {
			return fmt.Errorf("checkout %s: %w", p.ref, err)
		}
	}
	return nil
}

// wikiCloneCmd builds the `git clone` command for a wiki source. When a token is
// supplied it is passed as an http.extraHeader through the GIT_CONFIG_*
// environment, NOT a `-c http.extraHeader=...` argv entry: a process's argv is
// world-readable on a shared host (`ps auxww`, /proc/<pid>/cmdline), so a token
// there would leak to any local user for the whole clone window, whereas the
// environment is readable only by the process owner. GIT_CONFIG_COUNT /
// GIT_CONFIG_KEY_0 / GIT_CONFIG_VALUE_0 is git's supported way to inject one-shot
// config without writing it to the clone's .git/config.
func wikiCloneCmd(ctx context.Context, cloneURL, dest, token string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", "clone", cloneURL, dest)
	if token != "" {
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http.extraHeader",
			"GIT_CONFIG_VALUE_0="+wikiAuthHeader(token),
		)
	}
	return cmd
}

// gitCheckout checks out ref in the repository at dest. ref is treated strictly
// as a revision: the --end-of-options guard stops git from parsing a ref that
// begins with '-' as an option. p.ref is unvalidated and can reach here from an
// attacker-controlled wiki.ref / config / URI fragment (e.g. "--orphan" or
// "--detach"); without the guard git would execute it as a command-line option.
// A plain `--` separator is the wrong tool — git reads the token after it as a
// pathspec and fails to switch — so --end-of-options, which ends option parsing
// without entering pathspec mode, is the correct guard. It is applied uniformly
// to the github/git/wiki checkout paths, all of which take an unvalidated ref.
func gitCheckout(ctx context.Context, dest, ref string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", dest, "checkout", "--end-of-options", ref)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

const (
	remoteCloneTimeout    = 5 * time.Minute
	remoteCheckoutTimeout = 30 * time.Second
	remoteLockTimeout     = 5 * time.Minute
	remoteLockPoll        = 100 * time.Millisecond
	ghAuthTokenTimeout    = 10 * time.Second
)

// ghAuthToken returns a GitHub token for authenticating a private wiki clone, or
// "" when none is available. It shells out to `gh auth token`; any failure (gh
// missing, not logged in, no token) yields "" so a public wiki still clones
// anonymously rather than the run aborting. The token is never logged.
func ghAuthToken(parent context.Context) string {
	ctx, cancel := context.WithTimeout(parent, ghAuthTokenTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// wikiAuthHeader builds the HTTP Basic Authorization header value git sends when
// cloning a private wiki over https. GitHub accepts a personal/installation
// token as the password with any username; "x-access-token" is the conventional
// username for token auth.
func wikiAuthHeader(token string) string {
	cred := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	return "Authorization: Basic " + cred
}

// acquireLock takes an exclusive lock on the cache entry directory by
// creating a sentinel file with O_CREATE|O_EXCL. Concurrent processes spin
// until the file disappears or the timeout elapses. The returned function
// removes the sentinel; callers must defer it.
func acquireLock(entryDir string) (release func(), err error) {
	if err := os.MkdirAll(entryDir, 0o700); err != nil {
		return nil, fmt.Errorf("preparing lock dir: %w", err)
	}
	lockPath := filepath.Join(entryDir, ".lock")
	deadline := time.Now().Add(remoteLockTimeout)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("acquiring remote pattern lock: %w", err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for remote pattern lock at %s", lockPath)
		}
		time.Sleep(remoteLockPoll)
	}
}

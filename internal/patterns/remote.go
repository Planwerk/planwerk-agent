package patterns

import (
	"context"
	"crypto/sha256"
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

// Remote pattern source URIs come in two flavors:
//
//	git+https://example.com/repo.git[#ref[:subpath]]
//	github:owner/repo[/subpath][@ref]
//
// Anything that does not start with a known prefix is treated as a local
// directory path so existing call sites keep working unchanged.
const (
	prefixGitHTTPS = "git+https://"
	prefixGitHTTP  = "git+http://"
	prefixGitHub   = "github:"
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

// remoteOpts is the package-level RemoteOptions consulted by LoadFiltered
// when resolving remote pattern sources. The CLI sets this once at startup
// via SetRemoteOptions; tests reassign it directly.
var remoteOpts RemoteOptions

// SetRemoteOptions installs opts as the package-level configuration used by
// LoadFiltered for remote pattern resolution. It returns a function that
// restores the previous value, intended for use in tests.
func SetRemoteOptions(opts RemoteOptions) (restore func()) {
	old := remoteOpts
	remoteOpts = opts
	return func() { remoteOpts = old }
}

// IsRemote reports whether src looks like a remote pattern URI rather than a
// local directory path. The check is purely syntactic.
func IsRemote(src string) bool {
	return strings.HasPrefix(src, prefixGitHTTPS) ||
		strings.HasPrefix(src, prefixGitHTTP) ||
		strings.HasPrefix(src, prefixGitHub)
}

// parsedURI is the internal representation of a remote pattern URI after
// env-var expansion and parsing.
type parsedURI struct {
	raw     string // original URI as the user typed it (post env-var expansion)
	scheme  string // "github" or "git"
	cloneURL string // URL passed to git/gh
	ref     string // branch/tag/sha to check out, or "" for default
	subpath string // path inside the repo to load patterns from, or "" for root
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

func parseGitHubURI(uri string) (parsedURI, error) {
	body := strings.TrimPrefix(uri, prefixGitHub)
	var ref string
	if i := strings.Index(body, "@"); i >= 0 {
		ref = body[i+1:]
		body = body[:i]
	}
	m := githubRepoRe.FindStringSubmatch(body)
	if m == nil {
		return parsedURI{}, fmt.Errorf("invalid github: pattern URI %q (expected github:owner/repo[/subpath][@ref])", uri)
	}
	owner, repo := m[1], m[2]
	subpath := strings.TrimPrefix(body[len(m[0]):], "/")
	return parsedURI{
		raw:      uri,
		scheme:   "github",
		cloneURL: owner + "/" + repo,
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
		co := exec.CommandContext(coCtx, "git", "-C", dest, "checkout", p.ref)
		co.Stderr = os.Stderr
		if err := co.Run(); err != nil {
			return fmt.Errorf("checkout %s: %w", p.ref, err)
		}
	}
	return nil
}

const (
	remoteCloneTimeout    = 5 * time.Minute
	remoteCheckoutTimeout = 30 * time.Second
	remoteLockTimeout     = 5 * time.Minute
	remoteLockPoll        = 100 * time.Millisecond
)

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

package sync

import (
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/patterns"
)

// ClaudeSyncer performs the read-only wiki-reconciliation analysis for a cloned
// repo. The sync package depends on this interface rather than the concrete
// claude package so tests can inject fakes. Mirrors propose.ClaudeAnalyzer.
type ClaudeSyncer interface {
	Sync(dir string, ctx SyncContext) (*SyncResult, error)
}

// SyncFn is the bare-function form of ClaudeSyncer the CLI passes in. It is
// adapted to the interface via syncFnAdapter.
type SyncFn func(dir string, ctx SyncContext) (*SyncResult, error)

type syncFnAdapter struct {
	fn SyncFn
}

func (a syncFnAdapter) Sync(dir string, ctx SyncContext) (*SyncResult, error) {
	return a.fn(dir, ctx)
}

// GitHubClient wraps the single GitHub operation the read pass needs: cloning the
// target repo so the analysis can verify wiki references against the code. It is
// an interface so tests can inject a fake that hands out a controlled working
// tree without touching git or gh.
type GitHubClient interface {
	CloneRepo(ref string) (*github.Repo, error)
}

// resolveWikiFn resolves the target repo's wiki to its clone root, review-patterns
// directory, memory, and commit. It matches patterns.ResolveWiki and is a Runner
// seam so tests can stand in a temp directory without cloning a real wiki.
type resolveWikiFn func(owner, name string, wopts patterns.WikiOptions, ropts patterns.RemoteOptions) patterns.ResolvedWiki

// WikiWriter performs the write phase: a fresh authenticated clone of the wiki
// and the deletion+push of the flagged entries. It is an interface so the write
// phase can be exercised without cloning or pushing a real wiki. The default
// implementation is backed by the patterns package's write-back helpers.
type WikiWriter interface {
	// Clone makes a fresh authenticated clone of repo (an "owner/name") at ref
	// and returns the clone root, its HEAD commit, and a cleanup function.
	Clone(repo, ref string) (dir, headSHA string, cleanup func(), err error)
	// ApplyDeletions removes relPaths (wiki-relative, slash form) from the clone
	// at dir, commits with msg, and pushes.
	ApplyDeletions(dir string, relPaths []string, msg string) error
}

// defaultWikiWriter is the production WikiWriter backed by the patterns package.
type defaultWikiWriter struct{}

func (defaultWikiWriter) Clone(repo, ref string) (string, string, func(), error) {
	return patterns.CloneWikiAuthenticated(repo, ref)
}

func (defaultWikiWriter) ApplyDeletions(dir string, relPaths []string, msg string) error {
	return patterns.PushWikiDeletions(dir, relPaths, msg)
}

// The production client satisfies the interface structurally; a drift in
// either fails the build here rather than at the call site.
var _ GitHubClient = github.Client{}

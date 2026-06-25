package sync

import (
	"bytes"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/patterns"
)

// --- fakes -----------------------------------------------------------------

type fakeSyncer struct {
	result *SyncResult
	calls  int
	gotCtx SyncContext
}

func (f *fakeSyncer) Sync(_ string, ctx SyncContext) (*SyncResult, error) {
	f.calls++
	f.gotCtx = ctx
	return f.result, nil
}

type fakeGitHub struct {
	dir   string
	calls int
}

func (f *fakeGitHub) CloneRepo(string) (*github.Repo, error) {
	f.calls++
	return &github.Repo{Owner: "acme", Name: "widgets", Dir: f.dir}, nil
}

type fakeWikiWriter struct {
	cloneDir   string
	cloneHead  string
	cloneCalls int
	applyCalls int
	applied    []string
}

func (f *fakeWikiWriter) Clone(string, string) (string, string, func(), error) {
	f.cloneCalls++
	return f.cloneDir, f.cloneHead, func() {}, nil
}

func (f *fakeWikiWriter) ApplyDeletions(_ string, relPaths []string, _ string) error {
	f.applyCalls++
	f.applied = append([]string(nil), relPaths...)
	return nil
}

// --- helpers ---------------------------------------------------------------

const (
	wikiCommit      = "0123456789abcdef"
	wikiPatternPath = "review_patterns/no-raw-sql.md"
)

// seededWikiResolver returns a resolver pointing at a temp wiki dir holding one
// review pattern and one memory page, plus that dir.
func seededWikiResolver(t *testing.T) (resolveWikiFn, string) {
	t.Helper()
	dir := t.TempDir()
	writeWikiFile(t, dir, wikiPatternPath, "# Review Pattern: No raw SQL\n")
	writeWikiFile(t, dir, "memory/old.md", "An old decision.\n")
	return func(string, string, patterns.WikiOptions, patterns.RemoteOptions) patterns.ResolvedWiki {
		return patterns.ResolvedWiki{Repo: "acme/widgets", CommitSHA: wikiCommit, Dir: dir}
	}, dir
}

// flaggedResult is a result flagging both seeded entries.
func flaggedResult() *SyncResult {
	return &SyncResult{Entries: []FlaggedEntry{
		{Path: wikiPatternPath, Kind: KindPattern, Classification: ClassStale, Reason: "references internal/db/legacy.go, removed"},
		{Path: "memory/old.md", Kind: KindMemory, Classification: ClassRedundant, Reason: "superseded by memory/decisions.md", SupersededBy: "memory/decisions.md"},
	}}
}

func newRunner(t *testing.T, claude ClaudeSyncer, gh *fakeGitHub, writer *fakeWikiWriter) (*Runner, resolveWikiFn) {
	t.Helper()
	resolve, _ := seededWikiResolver(t)
	return &Runner{Claude: claude, GitHub: gh, ResolveWiki: resolve, Writer: writer, IsTTY: func() bool { return false }}, resolve
}

// --- tests -----------------------------------------------------------------

func TestRun_DryRunReportsAndDoesNotWrite(t *testing.T) {
	syncer := &fakeSyncer{result: flaggedResult()}
	gh := &fakeGitHub{dir: t.TempDir()}
	writer := &fakeWikiWriter{}
	r, _ := newRunner(t, syncer, gh, writer)

	var w bytes.Buffer
	if err := r.Run(&w, Options{RepoRef: "acme/widgets", Version: "v1"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if syncer.calls != 1 {
		t.Fatalf("expected exactly one analysis call, got %d", syncer.calls)
	}
	// The analysis receives the enumerated wiki entries and the repo name.
	if len(syncer.gotCtx.Entries) != 2 || syncer.gotCtx.RepoName != "acme/widgets" {
		t.Fatalf("analysis context not populated: %+v", syncer.gotCtx)
	}
	if writer.cloneCalls != 0 || writer.applyCalls != 0 {
		t.Fatalf("a dry run must not touch the wiki writer (clone=%d apply=%d)", writer.cloneCalls, writer.applyCalls)
	}
	out := w.String()
	if !strings.Contains(out, "## Stale (1)") || !strings.Contains(out, "## Redundant (1)") {
		t.Errorf("report missing flagged sections:\n%s", out)
	}
	if !strings.Contains(out, "acme/widgets.wiki @ 0123456") {
		t.Errorf("report missing wiki provenance:\n%s", out)
	}
}

func TestRun_PruneWithYesDeletesExactlyFlaggedPaths(t *testing.T) {
	cloneDir := t.TempDir()
	// The fresh wiki clone holds both flagged files, so neither is skipped.
	writeWikiFile(t, cloneDir, wikiPatternPath, "stale\n")
	writeWikiFile(t, cloneDir, "memory/old.md", "old\n")

	writer := &fakeWikiWriter{cloneDir: cloneDir, cloneHead: wikiCommit}
	gh := &fakeGitHub{dir: t.TempDir()}
	r, _ := newRunner(t, &fakeSyncer{result: flaggedResult()}, gh, writer)

	var w bytes.Buffer
	if err := r.Run(&w, Options{RepoRef: "acme/widgets", Prune: true, Yes: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if writer.cloneCalls != 1 || writer.applyCalls != 1 {
		t.Fatalf("--prune --yes should clone and apply once each (clone=%d apply=%d)", writer.cloneCalls, writer.applyCalls)
	}
	want := []string{wikiPatternPath, "memory/old.md"}
	if strings.Join(writer.applied, ",") != strings.Join(want, ",") {
		t.Errorf("ApplyDeletions got %v, want %v", writer.applied, want)
	}
	if !strings.Contains(w.String(), "Pruned 2 entries") {
		t.Errorf("expected a prune confirmation, got:\n%s", w.String())
	}
}

func TestRun_PruneRefusesUnenumeratedPath(t *testing.T) {
	cloneDir := t.TempDir()
	// The fresh wiki clone holds both the enumerated entry and a navigation page
	// that ReadWikiEntries never enumerates; on-disk existence alone would delete
	// both, so only the allowlist keeps the navigation page safe.
	writeWikiFile(t, cloneDir, wikiPatternPath, "stale\n")
	writeWikiFile(t, cloneDir, "Home.md", "# Home\n")

	// The analysis is driven by untrusted wiki bodies: it flags the enumerated
	// entry and, via injection, the navigation page that was never enumerated.
	result := &SyncResult{Entries: []FlaggedEntry{
		{Path: wikiPatternPath, Kind: KindPattern, Classification: ClassStale, Reason: "references internal/db/legacy.go, removed"},
		{Path: "Home.md", Kind: KindMemory, Classification: ClassStale, Reason: "injected via the wiki body"},
	}}
	writer := &fakeWikiWriter{cloneDir: cloneDir, cloneHead: wikiCommit}
	gh := &fakeGitHub{dir: t.TempDir()}
	r, _ := newRunner(t, &fakeSyncer{result: result}, gh, writer)

	var w bytes.Buffer
	if err := r.Run(&w, Options{RepoRef: "acme/widgets", Prune: true, Yes: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(writer.applied) != 1 || writer.applied[0] != wikiPatternPath {
		t.Errorf("only the enumerated entry should be deleted, got %v", writer.applied)
	}
	if !strings.Contains(w.String(), `Refusing to prune "Home.md"`) {
		t.Errorf("expected a refusal for the unenumerated page:\n%s", w.String())
	}
}

func TestRun_PruneAllUnenumeratedDeletesNothing(t *testing.T) {
	// Every flagged path is a page ReadWikiEntries never enumerated, so none is in
	// the allowlist and the write phase must not clone or delete anything.
	result := &SyncResult{Entries: []FlaggedEntry{
		{Path: "Home.md", Kind: KindMemory, Classification: ClassStale, Reason: "injected"},
		{Path: "SOURCES.md", Kind: KindMemory, Classification: ClassStale, Reason: "injected"},
	}}
	writer := &fakeWikiWriter{}
	gh := &fakeGitHub{dir: t.TempDir()}
	r, _ := newRunner(t, &fakeSyncer{result: result}, gh, writer)

	var w bytes.Buffer
	if err := r.Run(&w, Options{RepoRef: "acme/widgets", Prune: true, Yes: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if writer.cloneCalls != 0 || writer.applyCalls != 0 {
		t.Fatalf("no enumerated path matched: nothing should be cloned or applied (clone=%d apply=%d)", writer.cloneCalls, writer.applyCalls)
	}
	if !strings.Contains(w.String(), "no flagged entry matched an enumerated wiki page") {
		t.Errorf("expected a nothing-matched message, got:\n%s", w.String())
	}
}

func TestRun_PruneDeclinedDoesNotWrite(t *testing.T) {
	writer := &fakeWikiWriter{cloneDir: t.TempDir(), cloneHead: wikiCommit}
	gh := &fakeGitHub{dir: t.TempDir()}
	resolve, _ := seededWikiResolver(t)
	r := &Runner{
		Claude:      &fakeSyncer{result: flaggedResult()},
		GitHub:      gh,
		ResolveWiki: resolve,
		Writer:      writer,
		In:          strings.NewReader("n\n"),
		IsTTY:       func() bool { return true },
	}

	var w bytes.Buffer
	if err := r.Run(&w, Options{RepoRef: "acme/widgets", Prune: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if writer.cloneCalls != 0 || writer.applyCalls != 0 {
		t.Fatalf("a declined prune must not clone or apply (clone=%d apply=%d)", writer.cloneCalls, writer.applyCalls)
	}
	if !strings.Contains(w.String(), "Aborted") {
		t.Errorf("expected an abort message, got:\n%s", w.String())
	}
}

func TestRun_PruneNoTTYWithoutYesIsRefused(t *testing.T) {
	writer := &fakeWikiWriter{}
	gh := &fakeGitHub{dir: t.TempDir()}
	r, _ := newRunner(t, &fakeSyncer{result: flaggedResult()}, gh, writer) // IsTTY false

	err := r.Run(&bytes.Buffer{}, Options{RepoRef: "acme/widgets", Prune: true})
	if err == nil || !strings.Contains(err.Error(), "not a TTY") {
		t.Fatalf("expected a no-TTY refusal, got %v", err)
	}
	if writer.applyCalls != 0 {
		t.Fatal("a refused prune must not apply deletions")
	}
}

func TestRun_PruneSkipsAlreadyDeletedEntry(t *testing.T) {
	cloneDir := t.TempDir()
	// Only one of the two flagged files still exists in the fresh clone (the
	// other was removed on the wiki since analysis).
	writeWikiFile(t, cloneDir, wikiPatternPath, "stale\n")

	writer := &fakeWikiWriter{cloneDir: cloneDir, cloneHead: "ffffffffffffffff"} // moved wiki
	gh := &fakeGitHub{dir: t.TempDir()}
	r, _ := newRunner(t, &fakeSyncer{result: flaggedResult()}, gh, writer)

	var w bytes.Buffer
	if err := r.Run(&w, Options{RepoRef: "acme/widgets", Prune: true, Yes: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(writer.applied) != 1 || writer.applied[0] != wikiPatternPath {
		t.Errorf("only the still-present entry should be deleted, got %v", writer.applied)
	}
	out := w.String()
	if !strings.Contains(out, "Skipped memory/old.md") {
		t.Errorf("expected the already-gone entry to be reported as skipped:\n%s", out)
	}
	if !strings.Contains(out, "wiki moved since analysis") {
		t.Errorf("expected a moved-wiki note:\n%s", out)
	}
}

func TestRun_NoFlaggedEntriesSkipsWritePhase(t *testing.T) {
	writer := &fakeWikiWriter{}
	gh := &fakeGitHub{dir: t.TempDir()}
	// An empty result: the wiki is in sync.
	r, _ := newRunner(t, &fakeSyncer{result: &SyncResult{}}, gh, writer)

	var w bytes.Buffer
	if err := r.Run(&w, Options{RepoRef: "acme/widgets", Prune: true, Yes: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if writer.cloneCalls != 0 {
		t.Fatal("--prune with nothing flagged must not clone the wiki")
	}
	if !strings.Contains(w.String(), "in sync with the code") {
		t.Errorf("expected an in-sync report, got:\n%s", w.String())
	}
}

func TestRun_EmptyWikiReportsNothingToReconcile(t *testing.T) {
	emptyResolve := func(string, string, patterns.WikiOptions, patterns.RemoteOptions) patterns.ResolvedWiki {
		return patterns.ResolvedWiki{Repo: "acme/widgets", Dir: t.TempDir()} // no entries
	}
	syncer := &fakeSyncer{result: flaggedResult()}
	gh := &fakeGitHub{dir: t.TempDir()}
	r := &Runner{Claude: syncer, GitHub: gh, ResolveWiki: emptyResolve, Writer: &fakeWikiWriter{}, IsTTY: func() bool { return false }}

	var w bytes.Buffer
	if err := r.Run(&w, Options{RepoRef: "acme/widgets"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if syncer.calls != 0 || gh.calls != 0 {
		t.Fatalf("an empty wiki must not clone the repo or run analysis (sync=%d clone=%d)", syncer.calls, gh.calls)
	}
	if !strings.Contains(w.String(), "no review_patterns/ or memory/ entries") {
		t.Errorf("expected a nothing-to-reconcile message, got:\n%s", w.String())
	}
}

func TestRun_MissingWikiIsError(t *testing.T) {
	noWiki := func(string, string, patterns.WikiOptions, patterns.RemoteOptions) patterns.ResolvedWiki {
		return patterns.ResolvedWiki{} // Dir == "": disabled, uninitialized, or offline
	}
	r := &Runner{Claude: &fakeSyncer{}, GitHub: &fakeGitHub{}, ResolveWiki: noWiki, Writer: &fakeWikiWriter{}, IsTTY: func() bool { return false }}

	err := r.Run(&bytes.Buffer{}, Options{RepoRef: "acme/widgets"})
	if err == nil || !strings.Contains(err.Error(), "no wiki to reconcile") {
		t.Fatalf("expected a missing-wiki error, got %v", err)
	}
}

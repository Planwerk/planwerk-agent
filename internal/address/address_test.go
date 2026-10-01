package address

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/github/githubtest"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/patterns/patternstest"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// Thread IDs reused across the address tests' fixtures and assertions.
const (
	threadID1 = "RT_1"
	threadID2 = "RT_2"
	threadID3 = "RT_3"
)

// fakeClaude returns scripted AddressResults in sequence (the last repeats when
// exhausted) and records every Context it was handed.
type fakeClaude struct {
	called  atomic.Int32
	results []*report.AddressResult
	err     error
	ctxs    []Context
	// onAddress, when set, runs inside Address after the call is recorded,
	// so a test can observe on-disk state while the session "runs".
	onAddress func(Context)
}

func (f *fakeClaude) Address(_ string, ctx Context) (*report.AddressResult, error) {
	i := int(f.called.Add(1)) - 1
	f.ctxs = append(f.ctxs, ctx)
	if f.onAddress != nil {
		f.onAddress(ctx)
	}
	if f.err != nil {
		return nil, f.err
	}
	if len(f.results) == 0 {
		return &report.AddressResult{Status: "DONE"}, nil
	}
	if i >= len(f.results) {
		i = len(f.results) - 1
	}
	return f.results[i], nil
}

func sampleThreads() []github.ReviewThread {
	return []github.ReviewThread{
		{ID: threadID1, Path: "a.go", Line: 1, Comments: []github.ReviewThreadComment{{Author: "rev", Body: "fix a"}}},
		{ID: threadID2, Path: "b.go", Line: 2, Comments: []github.ReviewThreadComment{{Author: "rev", Body: "fix b"}}},
	}
}

func doneResult(threadID string) *report.AddressResult {
	return &report.AddressResult{
		Threads: []report.AddressedThread{{ThreadID: threadID, Status: "DONE", Summary: "addressed " + threadID, Files: []string{"f.go"}}},
		Summary: "addressed " + threadID,
		Status:  "DONE",
	}
}

// newRunner wires the fakes with a non-TTY default; tests that exercise the
// interactive selector override In and IsTTY.
func newRunner(gh *githubtest.Fake, cl *fakeClaude) *Runner {
	return &Runner{
		Claude: cl,
		GitHub: gh,
		In:     strings.NewReader(""),
		IsTTY:  func() bool { return false },
	}
}

// baseOpts returns Options with the production defaults (--reply on, --resolve
// off, one commit per thread) that the CLI would supply.
func baseOpts(prRef string) Options {
	return Options{PRRef: prRef, Reply: true, OneCommitPerThread: true}
}

// writeSamplePattern seeds a --patterns directory holding one pattern named
// "Sample wiring check", whose catalog file is sampleCatalogFile, and returns
// the directory.
func writeSamplePattern(t *testing.T) string {
	t.Helper()
	patternsDir := t.TempDir()
	patternFile := patternsDir + "/sample.md"
	const patternBody = `# Review Pattern: Sample wiring check
**Review-Area**: meta
**Detection-Hint**: anything
**Severity**: WARNING

## Rule
Wired patterns must reach the address Context.
`
	if err := os.WriteFile(patternFile, []byte(patternBody), 0o644); err != nil {
		t.Fatalf("seeding pattern file: %v", err)
	}
	return patternsDir
}

// sampleCatalogFile is the catalog file name of the pattern writeSamplePattern
// seeds.
const sampleCatalogFile = "sample-wiring-check.md"

// withSamplePattern restricts opts to the pattern in patternsDir, so the
// loaded set is exactly that one pattern.
func withSamplePattern(opts Options, patternsDir string) Options {
	opts.PatternDirs = []string{patternsDir}
	opts.NoLocalPatterns = true
	opts.NoRepoPatterns = true
	return opts
}

func TestRun_NoThreads(t *testing.T) {
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}}
	cl := &fakeClaude{}
	r := newRunner(gh, cl)

	var buf bytes.Buffer
	if err := r.Run(&buf, baseOpts("o/r#1")); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 0 {
		t.Errorf("Claude.Address called %d times, want 0 with no threads", cl.called.Load())
	}
	if !strings.Contains(buf.String(), "No unresolved review threads") {
		t.Errorf("missing no-threads notice: %s", buf.String())
	}
}

func TestRun_AllPerThread(t *testing.T) {
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, Threads: sampleThreads()}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID1), doneResult(threadID2)}}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.All = true
	var buf bytes.Buffer
	if err := r.Run(&buf, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 2 {
		t.Errorf("Claude.Address called %d times, want 2 (one per thread)", cl.called.Load())
	}
	if gh.Count("PushHead") != 2 {
		t.Errorf("PushHead called %d times, want 2", gh.Count("PushHead"))
	}
	// --reply on by default, --resolve off by default.
	if len(gh.Replies()) != 2 {
		t.Errorf("replied to %v, want both threads", gh.Replies())
	}
	if len(gh.Resolved()) != 0 {
		t.Errorf("resolved %v, want none without --resolve", gh.Resolved())
	}
	if gh.Count("AddPRComment") != 1 {
		t.Errorf("AddPRComment called %d times, want 1 aggregate report", gh.Count("AddPRComment"))
	}
}

func TestRun_Aggregate(t *testing.T) {
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, Threads: sampleThreads()}
	cl := &fakeClaude{results: []*report.AddressResult{{
		Threads: []report.AddressedThread{
			{ThreadID: threadID1, Status: "DONE", Summary: "a"},
			{ThreadID: threadID2, Status: "DONE", Summary: "b"},
		},
		Summary: "both",
		Status:  "DONE",
	}}}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.All = true
	opts.OneCommitPerThread = false
	if err := r.Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 1 {
		t.Errorf("Claude.Address called %d times, want 1 in aggregate mode", cl.called.Load())
	}
	if gh.Count("PushHead") != 1 {
		t.Errorf("PushHead called %d times, want 1 in aggregate mode", gh.Count("PushHead"))
	}
	if cl.ctxs[0].OneCommitPerThread {
		t.Error("aggregate session should get OneCommitPerThread=false")
	}
	if len(cl.ctxs[0].Threads) != 2 {
		t.Errorf("aggregate session got %d threads, want both", len(cl.ctxs[0].Threads))
	}
}

// TestRun_PassesPatternCatalogToClaude locks that one address run writes one
// pattern catalog, shares it with every per-thread session, and removes it
// when the run ends.
func TestRun_PassesPatternCatalogToClaude(t *testing.T) {
	patternsDir := writeSamplePattern(t)

	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, Dir: t.TempDir(), Threads: sampleThreads()}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID1), doneResult(threadID2)}}
	cl.onAddress = func(ctx Context) {
		if ctx.Catalog.Dir == "" {
			t.Errorf("address session got no catalog directory, want the one Run wrote")
			return
		}
		if _, err := os.Stat(filepath.Join(ctx.Catalog.Dir, sampleCatalogFile)); err != nil {
			t.Errorf("stat %s in the catalog during the session: %v, want the file on disk", sampleCatalogFile, err)
		}
	}
	r := newRunner(gh, cl)

	opts := withSamplePattern(baseOpts("o/r#1"), patternsDir)
	opts.All = true
	if err := r.Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if got := len(cl.ctxs); got != 2 {
		t.Fatalf("Claude.Address called %d times, want 2 (one per thread)", got)
	}
	for i, ctx := range cl.ctxs {
		if got := len(ctx.Patterns); got != 1 {
			t.Fatalf("session %d got %d patterns, want 1 (wiring broken)", i+1, got)
		}
		if ctx.Patterns[0].Name != "Sample wiring check" {
			t.Errorf("session %d got pattern name %q, want %q", i+1, ctx.Patterns[0].Name, "Sample wiring check")
		}
	}
	dir := cl.ctxs[0].Catalog.Dir
	if dir == "" {
		t.Fatalf("Claude got no catalog directory, want the one Run wrote")
	}
	if got := cl.ctxs[1].Catalog.Dir; got != dir {
		t.Errorf("second session got catalog %q, want the first session's %q", got, dir)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stat %s after Run: %v, want fs.ErrNotExist (the catalog is removed when the run ends)", dir, err)
	}
}

// TestRun_OneMemoryDirectoryPerRun locks the lifetime of the project memory
// in an address run: the wiki is resolved once, every per-thread session
// reads the pages from the same directory, and the directory is gone when Run
// returns.
func TestRun_OneMemoryDirectoryPerRun(t *testing.T) {
	tmp := patternstest.IsolateTempDir(t)
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, Threads: sampleThreads()}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID1), doneResult(threadID2)}}
	cl.onAddress = func(ctx Context) { patternstest.AssertMemoryPageReadable(t, ctx.Memory) }
	seam := &patternstest.WikiSeam{Wiki: patternstest.MemoryWiki()}
	r := newRunner(gh, cl)
	r.ResolveWiki = seam.Resolve

	opts := baseOpts("o/r#1")
	opts.All = true
	if err := r.Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if got := len(cl.ctxs); got != 2 {
		t.Fatalf("Claude.Address called %d times, want 2 (one per thread)", got)
	}
	if seam.Calls != 1 {
		t.Errorf("the wiki was resolved %d times, want once for the whole run", seam.Calls)
	}
	first, second := cl.ctxs[0].Memory.Dir, cl.ctxs[1].Memory.Dir
	if first == "" || second != first {
		t.Errorf("memory directories = %q, %q, want one directory shared by both sessions", first, second)
	}
	patternstest.AssertNoMemoryDir(t, tmp)
}

// TestRun_AggregateSessionReadsTheMemory covers the aggregate mode: its one
// session over all threads is handed the same memory directory a per-thread
// session is, and the directory is gone when Run returns.
func TestRun_AggregateSessionReadsTheMemory(t *testing.T) {
	tmp := patternstest.IsolateTempDir(t)
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, Threads: sampleThreads()}
	cl := &fakeClaude{results: []*report.AddressResult{{
		Threads: []report.AddressedThread{
			{ThreadID: threadID1, Status: "DONE", Summary: "a"},
			{ThreadID: threadID2, Status: "DONE", Summary: "b"},
		},
		Summary: "both",
		Status:  "DONE",
	}}}
	cl.onAddress = func(ctx Context) { patternstest.AssertMemoryPageReadable(t, ctx.Memory) }
	r := newRunner(gh, cl)
	r.ResolveWiki = (&patternstest.WikiSeam{Wiki: patternstest.MemoryWiki()}).Resolve

	opts := baseOpts("o/r#1")
	opts.All = true
	opts.OneCommitPerThread = false
	if err := r.Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if len(cl.ctxs) != 1 {
		t.Fatalf("Claude.Address called %d times, want 1 in aggregate mode", len(cl.ctxs))
	}
	if mem := cl.ctxs[0].Memory; len(mem.Pages) != 1 || mem.Dir == "" {
		t.Errorf("aggregate session Memory = %+v, want the one page and its directory", mem)
	}
	patternstest.AssertNoMemoryDir(t, tmp)
}

// TestRun_NoWikiResolveOnEarlyReturns locks the lazy resolve: a run that
// dispatches no session never pays for the wiki, and a printed prompt, which
// outlives the run, carries no project memory.
func TestRun_NoWikiResolveOnEarlyReturns(t *testing.T) {
	cases := []struct {
		name    string
		threads []github.ReviewThread
		modify  func(*Options)
	}{
		{"no unresolved threads", nil, func(*Options) {}},
		{"a dry run", sampleThreads(), func(o *Options) { o.DryRun = true }},
		{"a printed prompt", sampleThreads(), func(o *Options) { o.PrintPrompt = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, Threads: tc.threads}
			cl := &fakeClaude{}
			seam := &patternstest.WikiSeam{Wiki: patternstest.MemoryWiki()}
			r := newRunner(gh, cl)
			r.ResolveWiki = seam.Resolve
			// Stands in for claude.BuildAddressPrompt, which renders the
			// section exactly when the context carries memory pages.
			r.BuildPrompt = func(ctx Context) string {
				if len(ctx.Memory.Pages) > 0 {
					return "PROMPT\n## Project Memory\n"
				}
				return "PROMPT\n"
			}

			opts := baseOpts("o/r#1")
			opts.Wiki = patterns.WikiOptions{Enabled: true}
			tc.modify(&opts)
			var out bytes.Buffer
			if err := r.Run(&out, opts); err != nil {
				t.Fatalf("Run returned %v, want nil", err)
			}
			if seam.Calls != 0 {
				t.Errorf("the wiki was resolved %d times, want 0", seam.Calls)
			}
			if cl.called.Load() != 0 {
				t.Errorf("Claude.Address called %d times, want 0", cl.called.Load())
			}
			if opts.PrintPrompt && !strings.Contains(out.String(), "PROMPT") {
				t.Fatalf("the prompt was not printed: %q", out.String())
			}
			if strings.Contains(out.String(), "## Project Memory") {
				t.Errorf("the output carries the project memory: %q", out.String())
			}
		})
	}
}

// TestRun_AddressErrorRemovesTheMemoryDirectory covers the failing session:
// Run wraps the error and still removes the memory directory.
func TestRun_AddressErrorRemovesTheMemoryDirectory(t *testing.T) {
	tmp := patternstest.IsolateTempDir(t)
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, Threads: sampleThreads()[:1]}
	boom := errors.New("boom")
	cl := &fakeClaude{err: boom}
	cl.onAddress = func(ctx Context) { patternstest.AssertMemoryPageReadable(t, ctx.Memory) }
	r := newRunner(gh, cl)
	r.ResolveWiki = (&patternstest.WikiSeam{Wiki: patternstest.MemoryWiki()}).Resolve

	opts := baseOpts("o/r#1")
	opts.All = true
	err := r.Run(io.Discard, opts)
	if !errors.Is(err, boom) || !strings.HasPrefix(err.Error(), "claude address:") {
		t.Fatalf("Run error = %v, want \"claude address:\" wrapping the session error", err)
	}
	patternstest.AssertNoMemoryDir(t, tmp)
}

// TestRun_PassesWikiOptionsToTheSeam locks the wiring of the wiki opt-in: the
// resolver sees the pull request's repository and the options the command
// resolved.
func TestRun_PassesWikiOptionsToTheSeam(t *testing.T) {
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, Threads: sampleThreads()[:1]}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID1)}}
	seam := &patternstest.WikiSeam{}
	r := newRunner(gh, cl)
	r.ResolveWiki = seam.Resolve

	opts := baseOpts("o/r#1")
	opts.All = true
	opts.Wiki = patterns.WikiOptions{Enabled: true, Repo: "acme/handbook", Ref: "v1"}
	opts.Remote = patterns.RemoteOptions{TTL: time.Hour}
	if err := r.Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if seam.Repo != "o/r" || seam.Opts != opts.Wiki || seam.Remote.TTL != time.Hour {
		t.Errorf("resolver got %s, %+v, %+v; want o/r and the run's options", seam.Repo, seam.Opts, seam.Remote)
	}
}

// TestRun_WikiPatternsReachTheContext locks the wiki tier of the pattern
// load: a pattern file under the resolved wiki's review_patterns/ is part of
// the catalog the change is held to.
func TestRun_WikiPatternsReachTheContext(t *testing.T) {
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, Dir: t.TempDir(), Threads: sampleThreads()[:1]}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID1)}}
	r := newRunner(gh, cl)
	// writeSamplePattern stands in for the wiki's review_patterns/ directory.
	r.ResolveWiki = (&patternstest.WikiSeam{Wiki: patterns.ResolvedWiki{Repo: "o/r", CommitSHA: "wikisha", PatternsDir: writeSamplePattern(t)}}).Resolve

	opts := baseOpts("o/r#1")
	opts.All = true
	opts.NoLocalPatterns, opts.NoRepoPatterns = true, true
	if err := r.Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if len(cl.ctxs) != 1 {
		t.Fatalf("Claude.Address called %d times, want 1", len(cl.ctxs))
	}
	if got := cl.ctxs[0].Patterns; len(got) != 1 || got[0].Name != "Sample wiring check" {
		t.Errorf("Context.Patterns = %+v, want the one wiki pattern", got)
	}
}

// TestRun_DisabledWikiLeavesMemoryZero covers the run without a wiki: the
// zero ResolvedWiki (disabled, uninitialized, or unresolvable) hands the
// session no memory and writes no directory.
func TestRun_DisabledWikiLeavesMemoryZero(t *testing.T) {
	tmp := patternstest.IsolateTempDir(t)
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, Threads: sampleThreads()[:1]}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID1)}}
	r := newRunner(gh, cl)
	r.ResolveWiki = (&patternstest.WikiSeam{}).Resolve

	opts := baseOpts("o/r#1")
	opts.All = true
	if err := r.Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if len(cl.ctxs) != 1 {
		t.Fatalf("Claude.Address called %d times, want 1", len(cl.ctxs))
	}
	if !reflect.DeepEqual(cl.ctxs[0].Memory, patterns.MemoryCatalog{}) {
		t.Errorf("Context.Memory = %+v, want the zero MemoryCatalog", cl.ctxs[0].Memory)
	}
	patternstest.AssertNoMemoryDir(t, tmp)
}

func TestRun_ThreadIDSelectsSubset(t *testing.T) {
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, Threads: sampleThreads()}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID2)}}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.ThreadIDs = []string{threadID2}
	if err := r.Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 1 {
		t.Fatalf("Claude.Address called %d times, want 1", cl.called.Load())
	}
	if cl.ctxs[0].Threads[0].ID != threadID2 {
		t.Errorf("addressed thread %q, want RT_2", cl.ctxs[0].Threads[0].ID)
	}
}

func TestRun_ThreadIDUnknownWarns(t *testing.T) {
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, Threads: sampleThreads()}
	cl := &fakeClaude{}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.ThreadIDs = []string{"RT_999"}
	var buf bytes.Buffer
	if err := r.Run(&buf, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 0 {
		t.Errorf("Claude.Address called %d times, want 0 (no thread matched)", cl.called.Load())
	}
	if !strings.Contains(buf.String(), "did not match") {
		t.Errorf("expected an unknown-thread warning, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "No threads selected") {
		t.Errorf("expected the no-selection notice, got: %s", buf.String())
	}
}

func TestRun_Resolve(t *testing.T) {
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, Threads: sampleThreads()[:1]}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID1)}}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.All = true
	opts.Resolve = true
	if err := r.Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if len(gh.Resolved()) != 1 || gh.Resolved()[0] != threadID1 {
		t.Errorf("resolved %v, want [RT_1] with --resolve", gh.Resolved())
	}
}

func TestRun_ReplyResolveFailuresAreNonFatal(t *testing.T) {
	gh := &githubtest.Fake{
		PR:         github.PR{HeadBranch: "feat/x"},
		Threads:    sampleThreads()[:1],
		ReplyErr:   errors.New("reply down"),
		ResolveErr: errors.New("resolve down"),
		CommentErr: errors.New("comment down"),
	}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID1)}}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.All = true
	opts.Resolve = true
	var buf bytes.Buffer
	if err := r.Run(&buf, opts); err != nil {
		t.Fatalf("Run returned %v, want nil despite best-effort failures", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Could not reply to thread RT_1") {
		t.Errorf("expected a non-fatal reply warning, got: %s", out)
	}
	if !strings.Contains(out, "Could not resolve thread RT_1") {
		t.Errorf("expected a non-fatal resolve warning, got: %s", out)
	}
	if !strings.Contains(out, "Could not post the address report") {
		t.Errorf("expected a non-fatal comment warning, got: %s", out)
	}
}

func TestRun_NoAddressCommentSkipsComment(t *testing.T) {
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, Threads: sampleThreads()[:1]}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID1)}}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.All = true
	opts.NoAddressComment = true
	if err := r.Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if gh.Count("AddPRComment") != 0 {
		t.Errorf("AddPRComment called %d times, want 0 with --no-address-comment", gh.Count("AddPRComment"))
	}
}

func TestRun_EscalationStopsAndStillPostsComment(t *testing.T) {
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, Threads: sampleThreads()}
	blocked := &report.AddressResult{
		Threads: []report.AddressedThread{{ThreadID: threadID1, Status: "BLOCKED", Summary: "stale ref"}},
		Summary: "blocked",
		Status:  "BLOCKED",
	}
	cl := &fakeClaude{results: []*report.AddressResult{blocked}}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.All = true
	err := r.Run(io.Discard, opts)
	if err == nil || !strings.Contains(err.Error(), "escalated") {
		t.Fatalf("Run err = %v, want an escalation error", err)
	}
	if cl.called.Load() != 1 {
		t.Errorf("Claude.Address called %d times, want 1 (stopped after escalation)", cl.called.Load())
	}
	// A BLOCKED thread was not committed: no push, no reply, no resolve.
	if gh.Count("PushHead") != 0 {
		t.Errorf("PushHead called %d times, want 0 for a blocked thread", gh.Count("PushHead"))
	}
	if len(gh.Replies()) != 0 {
		t.Errorf("replied %v, want none for a blocked thread", gh.Replies())
	}
	// The escalated report must still reach the PR.
	if gh.Count("AddPRComment") != 1 {
		t.Errorf("AddPRComment called %d times, want 1 — escalated report must still post", gh.Count("AddPRComment"))
	}
}

func TestRun_ExhaustsMaxIterations(t *testing.T) {
	threads := []github.ReviewThread{
		{ID: threadID1, Comments: []github.ReviewThreadComment{{Body: "a"}}},
		{ID: threadID2, Comments: []github.ReviewThreadComment{{Body: "b"}}},
		{ID: threadID3, Comments: []github.ReviewThreadComment{{Body: "c"}}},
	}
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, Threads: threads}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID1), doneResult(threadID2)}}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.All = true
	opts.MaxIterations = 2
	err := r.Run(io.Discard, opts)
	if !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("Run err = %v, want ErrMaxIterations", err)
	}
	if cl.called.Load() != 2 {
		t.Errorf("Claude.Address called %d times, want 2 (capped)", cl.called.Load())
	}
}

func TestRun_PropagatesClaudeError(t *testing.T) {
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, Threads: sampleThreads()[:1]}
	cl := &fakeClaude{err: errors.New("boom")}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.All = true
	err := r.Run(io.Discard, opts)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected wrapped 'boom' error, got %v", err)
	}
}

func TestRun_PushFailureIsFatal(t *testing.T) {
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, Threads: sampleThreads()[:1], PushErr: errors.New("push rejected")}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID1)}}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.All = true
	err := r.Run(io.Discard, opts)
	if err == nil || !strings.Contains(err.Error(), "push rejected") {
		t.Fatalf("expected a fatal push error, got %v", err)
	}
}

func TestRun_NonTTYDefaultsToAll(t *testing.T) {
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, Threads: sampleThreads()}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID1), doneResult(threadID2)}}
	r := newRunner(gh, cl) // IsTTY returns false

	// No --all, no --thread, no TTY → defaults to addressing every thread.
	if err := r.Run(io.Discard, baseOpts("o/r#1")); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 2 {
		t.Errorf("Claude.Address called %d times, want 2 (no-TTY default to all)", cl.called.Load())
	}
}

func TestRun_InteractiveSelectsSubset(t *testing.T) {
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, Threads: sampleThreads()}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID2)}}
	r := newRunner(gh, cl)
	r.IsTTY = func() bool { return true }
	// Skip RT_1, address RT_2.
	r.In = strings.NewReader("n\ny\n")

	if err := r.Run(io.Discard, baseOpts("o/r#1")); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 1 {
		t.Fatalf("Claude.Address called %d times, want 1 (only RT_2 selected)", cl.called.Load())
	}
	if cl.ctxs[0].Threads[0].ID != threadID2 {
		t.Errorf("addressed %q, want RT_2", cl.ctxs[0].Threads[0].ID)
	}
}

func TestRun_DryRunSkipsClaude(t *testing.T) {
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, Threads: sampleThreads()}
	cl := &fakeClaude{}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.DryRun = true
	var buf bytes.Buffer
	if err := r.Run(&buf, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 0 {
		t.Errorf("Claude.Address called %d times in dry-run, want 0", cl.called.Load())
	}
	if gh.Count("PushHead") != 0 {
		t.Errorf("PushHead called %d times in dry-run, want 0", gh.Count("PushHead"))
	}
	out := buf.String()
	if !strings.Contains(out, "[dry-run]") || !strings.Contains(out, threadID1) {
		t.Errorf("dry-run output missing plan or threads: %s", out)
	}
}

func TestRun_PrintPromptWritesPromptAndSkipsClaude(t *testing.T) {
	patternsDir := writeSamplePattern(t)
	// Every t.TempDir above is created before TMPDIR points at tmp, so tmp
	// holds only what Run writes.
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, Threads: sampleThreads()}
	cl := &fakeClaude{}
	r := newRunner(gh, cl)
	var got Context
	r.BuildPrompt = func(ctx Context) string {
		got = ctx
		return "PROMPT threads=" + itoa(len(ctx.Threads)) + " first=" + ctx.Threads[0].ID
	}

	opts := withSamplePattern(baseOpts("o/r#1"), patternsDir)
	opts.PrintPrompt = true
	var buf bytes.Buffer
	if err := r.Run(&buf, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 0 {
		t.Errorf("Claude.Address called %d times in print-prompt mode, want 0", cl.called.Load())
	}
	if gh.Count("PushHead") != 0 {
		t.Errorf("PushHead called %d times in print-prompt mode, want 0", gh.Count("PushHead"))
	}
	out := buf.String()
	// Per-thread mode renders the first thread's prompt.
	if !strings.Contains(out, "PROMPT threads=1 first=RT_1") {
		t.Errorf("unexpected print-prompt output: %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("prompt output should end with a newline: %q", out)
	}
	// A printed prompt outlives the run, so it carries the pattern bodies
	// and no catalog directory is written for it.
	if n := len(got.Patterns); n != 1 {
		t.Fatalf("printed prompt got %d patterns, want 1", n)
	}
	if got.Catalog.Dir != "" || len(got.Catalog.Entries) != 0 {
		t.Errorf("printed prompt got catalog %+v, want the zero Catalog", got.Catalog)
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("reading TMPDIR: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), patterns.CatalogDirPrefix) {
			t.Errorf("TMPDIR holds %s, want no catalog directory for a printed prompt", e.Name())
		}
	}
}

func TestRun_PrintPromptWithoutBuilderErrors(t *testing.T) {
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, Threads: sampleThreads()}
	r := newRunner(gh, &fakeClaude{})
	// BuildPrompt left nil intentionally.
	opts := baseOpts("o/r#1")
	opts.PrintPrompt = true
	err := r.Run(io.Discard, opts)
	if err == nil || !strings.Contains(err.Error(), "prompt builder") {
		t.Fatalf("expected prompt-builder error, got %v", err)
	}
}

func TestRun_RequiresRefWithoutLocal(t *testing.T) {
	r := newRunner(&githubtest.Fake{}, &fakeClaude{})
	err := r.Run(io.Discard, baseOpts(""))
	if err == nil || !strings.Contains(err.Error(), "PR reference is required") {
		t.Fatalf("expected a missing-ref error, got %v", err)
	}
}

func TestRun_LocalSkipsCloneAndSurvives(t *testing.T) {
	gh := &githubtest.Fake{
		PR:      github.PR{HeadBranch: "feat/x", BaseBranch: "main"},
		Dir:     t.TempDir(),
		Threads: sampleThreads()[:1],
	}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID1)}}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.All = true
	opts.Local = true
	if err := r.Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if gh.Count("OpenLocalPR") != 1 {
		t.Errorf("OpenLocalPR calls = %d, want 1", gh.Count("OpenLocalPR"))
	}
	if gh.Count("FetchAndCheckout") != 0 {
		t.Errorf("FetchAndCheckout (clone) calls = %d, want 0 in local mode", gh.Count("FetchAndCheckout"))
	}
	if !cl.ctxs[0].Local {
		t.Error("Claude context Local = false, want true in --local mode")
	}
}

func TestRun_FetchThreadsErrorIsFatal(t *testing.T) {
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x"}, ThreadsErr: errors.New("graphql down")}
	r := newRunner(gh, &fakeClaude{})
	err := r.Run(io.Discard, baseOpts("o/r#1"))
	if err == nil || !strings.Contains(err.Error(), "graphql down") {
		t.Fatalf("expected a fatal fetch-threads error, got %v", err)
	}
}

// itoa avoids pulling strconv into the test for a single conversion.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

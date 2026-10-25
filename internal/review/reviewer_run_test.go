package review

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/cache"
	"github.com/planwerk/planwerk-agent/internal/capture"
	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/diffscope"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/github/githubtest"
	"github.com/planwerk/planwerk-agent/internal/hygiene"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/planwerk"
	"github.com/planwerk/planwerk-agent/internal/report"
	"github.com/planwerk/planwerk-agent/internal/search"
)

// initGitRepoTwoCommits creates a git repo with two commits: the first adds
// unchanged.go and changed.go; the second modifies only changed.go. It returns
// the repo dir and the first commit's SHA, so a diff from that SHA to HEAD lists
// exactly changed.go.
func initGitRepoTwoCommits(t *testing.T) (dir, firstSHA string) {
	t.Helper()
	dir = t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "tester")
	write("unchanged.go", "package x\n")
	write("changed.go", "package x\n// v1\n")
	run("add", "-A")
	run("commit", "-q", "-m", "first")
	firstSHA = run("rev-parse", "HEAD")
	write("changed.go", "package x\n// v2\n")
	run("add", "-A")
	run("commit", "-q", "-m", "second")
	return dir, firstSHA
}

func TestGetCommitLog(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		dir, firstSHA := initGitRepoTwoCommits(t)
		// Point origin/main at the first commit so origin/main..HEAD resolves
		// offline, with no real remote or network.
		cmd := exec.Command("git", "update-ref", "refs/remotes/origin/main", firstSHA)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git update-ref: %v\n%s", err, out)
		}
		log, err := getCommitLog(dir, "main")
		if err != nil {
			t.Fatalf("getCommitLog returned error: %v", err)
		}
		if !strings.Contains(log, "second") {
			t.Errorf("commit log = %q, want it to mention the second commit", log)
		}
	})

	t.Run("missing remote", func(t *testing.T) {
		// initGitRepoTwoCommits does not create origin/main, so the git log
		// query fails as it would on a missing remote or auth failure.
		dir, _ := initGitRepoTwoCommits(t)
		log, err := getCommitLog(dir, "main")
		if err == nil {
			t.Fatal("expected an error when origin/main is missing, got nil")
		}
		if log != "" {
			t.Errorf("log = %q, want empty on error", log)
		}
		if !strings.Contains(err.Error(), "git log") {
			t.Errorf("error %q does not name the git command", err)
		}
	})
}

// TestChangedFilesSince_NonASCIIPath covers the re-review whose push changed
// a non-ASCII file: under git's default quoting the set held only the quoted
// name, so suppression read the finding's file as unchanged and hid it.
// Setting core.quotePath=true in the repo config overrides a developer's
// global setting, so the test fails without the -c option. The two names with
// whitespace at an end show that no byte of a path is trimmed:
// strings.TrimSpace also strips U+00A0 (NO-BREAK SPACE).
func TestChangedFilesSince_NonASCIIPath(t *testing.T) {
	const name = "\u00e4.go"
	const leading, trailing = " lead.go", "nbsp.go\u00a0"
	dir, firstSHA := initGitRepoTwoCommits(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("config", "core.quotePath", "true")
	for _, n := range []string{name, leading, trailing} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("package x\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}
	git("add", "-A")
	git("commit", "-q", "-m", "third")

	changed, ok := changedFilesSince(dir, firstSHA)
	if !ok {
		t.Fatal("changedFilesSince ok = false, want true")
	}
	for _, want := range []string{name, leading, trailing, "changed.go"} {
		if !changed[want] {
			t.Errorf("changed = %v, want %q in it", changed, want)
		}
	}
	for _, absent := range []string{"unchanged.go", "lead.go", "nbsp.go"} {
		if changed[absent] {
			t.Errorf("changed = %v, want %q absent", changed, absent)
		}
	}
}

func TestRun_CommitLogFailureLogsWarning(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable and
	// would race with concurrent Run tests.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	// Capture slog so we can assert the warning is emitted. fakePR's Dir is a
	// non-git temp dir, so getCommitLog's git log subprocess fails.
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	pr := fakePR(t, "acme", "widgets", 99, "sha-nolog")
	claudeMock := &configurableClaude{}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	var out bytes.Buffer
	if err := runner.Run(&out, baseOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(logBuf.String(), "fetching commit log failed") {
		t.Errorf("expected a warning when the commit log fetch fails, got:\n%s", logBuf.String())
	}
}

func TestRun_VerifyClaimsDemotesRefuted(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 81, "sha-verify")
	var sentToVerify []report.Finding
	claudeMock := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{
				Findings: []report.Finding{
					{ID: "B-001", Severity: report.SeverityBlocking, Title: "auth bypass", File: "a.go", Line: 1, Problem: "p", Action: "a"},
					{ID: "C-001", Severity: report.SeverityCritical, Title: "nil deref", File: "b.go", Line: 2, Problem: "p", Action: "a"},
					{ID: "W-001", Severity: report.SeverityWarning, Title: "nit", File: "c.go", Line: 3, Problem: "p", Action: "a"},
				},
			}, nil
		},
		verifyClaims: func(dir string, findings []report.Finding) ([]hygiene.ClaimVerdict, error) {
			sentToVerify = findings
			return []hygiene.ClaimVerdict{
				{Index: 0, Verdict: "confirmed"},
				{Index: 1, Verdict: "refuted", Reason: "b.go:2 is already nil-guarded"},
			}, nil
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	var out bytes.Buffer
	if err := runner.Run(&out, baseOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if claudeMock.verifyClaimsCalls != 1 {
		t.Fatalf("VerifyFindingClaims calls = %d, want 1 (batched)", claudeMock.verifyClaimsCalls)
	}
	if len(sentToVerify) != 2 {
		t.Fatalf("expected only BLOCKING+CRITICAL sent, got %d: %+v", len(sentToVerify), sentToVerify)
	}

	cached, ok := readCachedResult(t, cache.Key(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, baseOptsPatternFlag()))
	if !ok {
		t.Fatal("expected a cached result carrying the demotion")
	}
	byID := map[string]report.Finding{}
	for _, f := range cached.Findings {
		byID[f.ID] = f
	}
	if got := byID["C-001"]; got.Confidence != report.ConfidenceUncertain || got.VerificationNote == "" {
		t.Errorf("refuted C-001 must be demoted with a note, got confidence=%q note=%q", got.Confidence, got.VerificationNote)
	}
	if got := byID["B-001"]; got.VerificationNote != "" {
		t.Errorf("confirmed B-001 must be untouched, got note=%q", got.VerificationNote)
	}
	// The per-finding records and run-level counts travel with the cached result,
	// so a later reader computes the refuted/sent ratio from history — including
	// the confirmation the old log-only path never persisted.
	if cached.Gates == nil || cached.Gates.Claim == nil {
		t.Fatalf("cached result missing claim gate stats: %+v", cached.Gates)
	}
	if got := *cached.Gates.Claim; got != (report.ClaimGateStats{Sent: 2, Verdicts: 2, Refuted: 1}) {
		t.Errorf("cached claim stats = %+v, want {Sent:2 Verdicts:2 Refuted:1}", got)
	}
	if cached.Gates.Snippet != nil {
		t.Errorf("snippet gate must not run without changed files (nil = skipped), got %+v", cached.Gates.Snippet)
	}
	if byID["C-001"].ClaimCheck != report.ClaimCheckRefuted {
		t.Errorf("refuted C-001 claim_check = %q, want refuted", byID["C-001"].ClaimCheck)
	}
	if byID["B-001"].ClaimCheck != report.ClaimCheckConfirmed {
		t.Errorf("confirmed B-001 claim_check = %q, want confirmed", byID["B-001"].ClaimCheck)
	}
	if byID["W-001"].ClaimCheck != "" {
		t.Errorf("WARNING W-001 must not be sent or stamped, got %q", byID["W-001"].ClaimCheck)
	}
}

func TestRun_VerifyClaims_ErrorIsNonFatal(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 82, "sha-verifyerr")
	claudeMock := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{Findings: []report.Finding{
				{ID: "C-001", Severity: report.SeverityCritical, Title: "x", File: "b.go", Line: 2, Problem: "p", Action: "a", Confidence: report.ConfidenceVerified},
			}}, nil
		},
		verifyClaims: func(dir string, findings []report.Finding) ([]hygiene.ClaimVerdict, error) {
			return nil, errors.New("verifier down")
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	var out bytes.Buffer
	if err := runner.Run(&out, baseOpts()); err != nil {
		t.Fatalf("verifier error must not fail the review: %v", err)
	}
	cached, ok := readCachedResult(t, cache.Key(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, baseOptsPatternFlag()))
	if !ok || len(cached.Findings) != 1 || cached.Findings[0].Confidence != report.ConfidenceVerified {
		t.Fatalf("finding must survive a failed verification unchanged, got ok=%v %+v", ok, cached.Findings)
	}
	// The fail-open case is now visible in the cache: the gate ran (Sent=1) but
	// the verifier returned nothing (Verdicts=0), and the finding stays
	// no-verdict — distinct from a run that verified and confirmed.
	if cached.Gates == nil || cached.Gates.Claim == nil {
		t.Fatalf("cached result missing claim gate stats after a failed verify: %+v", cached.Gates)
	}
	if got := *cached.Gates.Claim; got != (report.ClaimGateStats{Sent: 1}) {
		t.Errorf("cached claim stats = %+v, want {Sent:1} (fail-open)", got)
	}
	if cached.Findings[0].ClaimCheck != report.ClaimCheckNoVerdict {
		t.Errorf("finding after a failed verify must stay no-verdict, got %q", cached.Findings[0].ClaimCheck)
	}
}

func TestRun_VerifyClaims_SkippedWithoutHighSeverity(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 83, "sha-noverify")
	claudeMock := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{Findings: []report.Finding{
				{ID: "W-001", Severity: report.SeverityWarning, Title: "nit", File: "c.go", Line: 3, Problem: "p", Action: "a"},
			}}, nil
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	var out bytes.Buffer
	if err := runner.Run(&out, baseOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if claudeMock.verifyClaimsCalls != 0 {
		t.Errorf("claim verification must not run without a BLOCKING/CRITICAL finding, got %d calls", claudeMock.verifyClaimsCalls)
	}
}

func TestRun_DedupFilelessFindings(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 71, "sha-dedup")
	claudeMock := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{
				Summary: "primary",
				Findings: []report.Finding{
					{ID: "W-001", Severity: report.SeverityWarning, Title: "Docs drift in README", Problem: "p", Action: "a", Confidence: report.ConfidenceLikely},
				},
			}, nil
		},
		adversarial: func(dir, baseBranch string) (*report.ReviewResult, error) {
			return &report.ReviewResult{
				Findings: []report.Finding{
					{Severity: report.SeverityWarning, Title: "README documentation is stale", Problem: "p", Action: "a"},
				},
			}, nil
		},
		dedup: func(findings []report.Finding) ([][]int, error) {
			return [][]int{{0, 1}}, nil
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	opts := baseOpts()
	opts.Thorough = true
	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if claudeMock.dedupCalls != 1 {
		t.Errorf("DedupFindings calls = %d, want 1", claudeMock.dedupCalls)
	}

	cached, ok := readCachedResult(t, cache.Key(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, "thorough", baseOptsPatternFlag()))
	if !ok {
		t.Fatal("expected a cached result")
	}
	if len(cached.Findings) != 1 {
		t.Fatalf("expected 1 merged finding after dedup, got %d", len(cached.Findings))
	}
	if len(cached.Findings[0].ConfirmedBy) != 2 {
		t.Errorf("merged finding should carry both passes, got %v", cached.Findings[0].ConfirmedBy)
	}
}

func TestRun_DedupFileless_SkippedWithoutSecondaryPass(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 72, "sha-nodedup")
	claudeMock := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{
				Findings: []report.Finding{
					{ID: "W-001", Severity: report.SeverityWarning, Title: "a", Problem: "p", Action: "a"},
					{ID: "W-002", Severity: report.SeverityWarning, Title: "b", Problem: "p", Action: "a"},
				},
			}, nil
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	var out bytes.Buffer
	if err := runner.Run(&out, baseOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if claudeMock.dedupCalls != 0 {
		t.Errorf("DedupFindings must not run without a secondary pass, got %d calls", claudeMock.dedupCalls)
	}
}

func TestRun_DedupFileless_ErrorIsNonFatal(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 73, "sha-dederr")
	claudeMock := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{Findings: []report.Finding{
				{ID: "W-001", Severity: report.SeverityWarning, Title: "x", Problem: "p", Action: "a"},
			}}, nil
		},
		adversarial: func(dir, baseBranch string) (*report.ReviewResult, error) {
			return &report.ReviewResult{Findings: []report.Finding{
				{Severity: report.SeverityWarning, Title: "y", Problem: "p", Action: "a"},
			}}, nil
		},
		dedup: func(findings []report.Finding) ([][]int, error) {
			return nil, errors.New("structure tier down")
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	opts := baseOpts()
	opts.Thorough = true
	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("dedup error must not fail the review: %v", err)
	}
	cached, ok := readCachedResult(t, cache.Key(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, "thorough", baseOptsPatternFlag()))
	if !ok || len(cached.Findings) != 2 {
		t.Fatalf("both findings must survive a failed dedup, got ok=%v findings=%d", ok, len(cached.Findings))
	}
}

// readCachedResult returns the review a run stored under key, so an assertion
// about what was cached does not have to unwrap the envelope itself.
func readCachedResult(t *testing.T, key string) (*report.ReviewResult, bool) {
	t.Helper()
	entry, ok := readCachedReview(key, 0)
	return entry.Result, ok
}

// configurableClaude is a ClaudeRunner whose behavior is set per-test via
// closures. Each closure is also wrapped in a call-counter so the test can
// assert which methods actually ran — essential for verifying that cache
// hits skip Claude entirely and that parallel branches fire on demand.
type configurableClaude struct {
	review            func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error)
	adversarial       func(dir, baseBranch string) (*report.ReviewResult, error)
	coverage          func(dir, baseBranch string) (*report.CoverageResult, error)
	featureCompliance func(dir, baseBranch string, feature *planwerk.Feature) (*report.ReviewResult, error)
	specialist        func(dir, baseBranch, key, focus string) (*report.ReviewResult, error)
	dedup             func(findings []report.Finding) ([][]int, error)
	verifyClaims      func(dir string, findings []report.Finding) ([]hygiene.ClaimVerdict, error)
	usage             report.Usage

	reviewCalls            int32
	adversarialCalls       int32
	coverageCalls          int32
	featureComplianceCalls int32
	specialistCalls        int32
	dedupCalls             int32
	verifyClaimsCalls      int32
}

func (c *configurableClaude) Review(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
	atomic.AddInt32(&c.reviewCalls, 1)
	if c.review == nil {
		return &report.ReviewResult{Summary: "ok"}, nil
	}
	return c.review(dir, ctx)
}

func (c *configurableClaude) AdversarialReview(dir, baseBranch, sinceRef string, pats []patterns.Pattern, maxPatterns int) (*report.ReviewResult, error) {
	atomic.AddInt32(&c.adversarialCalls, 1)
	if c.adversarial == nil {
		return nil, nil
	}
	return c.adversarial(dir, baseBranch)
}

func (c *configurableClaude) CoverageMap(dir, baseBranch string) (*report.CoverageResult, error) {
	atomic.AddInt32(&c.coverageCalls, 1)
	if c.coverage == nil {
		return nil, nil
	}
	return c.coverage(dir, baseBranch)
}

func (c *configurableClaude) FeatureCompliance(dir, baseBranch string, feature *planwerk.Feature) (*report.ReviewResult, error) {
	atomic.AddInt32(&c.featureComplianceCalls, 1)
	if c.featureCompliance == nil {
		return nil, nil
	}
	return c.featureCompliance(dir, baseBranch, feature)
}

func (c *configurableClaude) SpecialistReview(dir, baseBranch string, sp claude.Specialist, pats []patterns.Pattern, maxPatterns int) (*report.ReviewResult, error) {
	atomic.AddInt32(&c.specialistCalls, 1)
	if c.specialist == nil {
		return &report.ReviewResult{}, nil
	}
	return c.specialist(dir, baseBranch, sp.Key, sp.Focus)
}

func (c *configurableClaude) DedupFindings(findings []report.Finding) ([][]int, error) {
	atomic.AddInt32(&c.dedupCalls, 1)
	if c.dedup == nil {
		return nil, nil
	}
	return c.dedup(findings)
}

func (c *configurableClaude) VerifyFindingClaims(dir string, findings []report.Finding) ([]hygiene.ClaimVerdict, error) {
	atomic.AddInt32(&c.verifyClaimsCalls, 1)
	if c.verifyClaims == nil {
		return nil, nil
	}
	return c.verifyClaims(dir, findings)
}

func (c *configurableClaude) UsageTotals() report.Usage {
	return c.usage
}

// fakePR builds a *github.PR whose Dir is a fresh temp directory the caller
// can populate. Passing a nil-safe Cleanup path (just removing the dir) keeps
// tests independent.
func fakePR(t *testing.T, owner, repo string, number int, headSHA string) *github.PR {
	t.Helper()
	dir := t.TempDir()
	return &github.PR{
		Owner:      owner,
		Repo:       repo,
		Number:     number,
		Title:      "Test PR",
		Body:       "Test body",
		HeadSHA:    headSHA,
		BaseBranch: "main",
		HeadBranch: "feature-branch",
		Dir:        dir,
	}
}

// baseOpts returns review Options wired to skip any on-disk pattern lookup so
// tests do not accidentally pick up the repo's own patterns/ directory.
// baseOptsPatternFlag mirrors the pattern-source component Run folds into the
// cache key for baseOpts(), so a test that seeds or reads the cache by hand
// keys the same entry the runner does.
func baseOptsPatternFlag() string {
	o := baseOpts()
	return "patterns=" + patterns.Fingerprint(o.PatternDirs, o.NoRepoPatterns, o.NoLocalPatterns, o.MaxPatterns)
}

func baseOpts() Options {
	return Options{
		PRRef:           "owner/repo#1",
		NoRepoPatterns:  true,
		NoLocalPatterns: true,
		Format:          "markdown",
		MinSeverity:     report.SeverityInfo,
		Version:         "test",
	}
}

func TestRun_CacheMiss_CallsClaudeAndCaches(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable and
	// would race with concurrent Run tests.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 42, "sha-miss-1")
	claudeMock := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{
				Summary: "Primary review summary",
				Findings: []report.Finding{
					{ID: "W-001", Severity: report.SeverityWarning, Title: "Primary finding", File: "main.go", Line: 1, Problem: "p", Action: "a"},
				},
			}, nil
		},
	}
	gh := &githubtest.Fake{
		FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil },
	}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	var out bytes.Buffer
	if err := runner.Run(&out, baseOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if claudeMock.reviewCalls != 1 {
		t.Errorf("Review calls = %d, want 1", claudeMock.reviewCalls)
	}
	if claudeMock.adversarialCalls != 0 {
		t.Errorf("AdversarialReview should not fire without --thorough, got %d calls", claudeMock.adversarialCalls)
	}
	if !strings.Contains(out.String(), "Primary review summary") {
		t.Errorf("rendered output missing primary summary, got:\n%s", out.String())
	}

	// Second run must hit the cache: Claude review should NOT be called again.
	claudeMock2 := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			t.Fatal("Review must not be called on cache hit")
			return nil, nil
		},
	}
	gh2 := &githubtest.Fake{
		FetchAndCheckoutFn: func(ref string) (*github.PR, error) {
			return fakePR(t, "acme", "widgets", 42, "sha-miss-1"), nil
		},
	}
	runner2 := &Runner{Claude: claudeMock2, GitHub: gh2}

	var out2 bytes.Buffer
	if err := runner2.Run(&out2, baseOpts()); err != nil {
		t.Fatalf("Run (cache hit) returned error: %v", err)
	}
	if !strings.Contains(out2.String(), "Primary review summary") {
		t.Errorf("cache-hit output missing primary summary, got:\n%s", out2.String())
	}
}

func TestRun_NoCache_SkipsCachedResult(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable and
	// would race with concurrent Run tests.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	// Seed the cache with a sentinel result.
	pr := fakePR(t, "acme", "widgets", 7, "sha-nocache")
	cachedResult := &report.ReviewResult{Summary: "CACHED SUMMARY SHOULD BE IGNORED"}
	if err := writeCachedReview(cache.Key(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, baseOptsPatternFlag()), cachedResult, nil); err != nil {
		t.Fatalf("seeding cache: %v", err)
	}

	freshResult := &report.ReviewResult{Summary: "Fresh summary"}
	claudeMock := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return freshResult, nil
		},
	}
	gh := &githubtest.Fake{
		FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil },
	}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	opts := baseOpts()
	opts.NoCache = true

	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if claudeMock.reviewCalls != 1 {
		t.Errorf("Review should run even with cached entry when NoCache=true, got %d calls", claudeMock.reviewCalls)
	}
	if strings.Contains(out.String(), "CACHED SUMMARY") {
		t.Error("NoCache run rendered cached sentinel; expected fresh output")
	}
	if !strings.Contains(out.String(), "Fresh summary") {
		t.Errorf("NoCache run missing fresh summary, got:\n%s", out.String())
	}
}

func TestRun_ThoroughMergesAdversarialFindings(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable and
	// would race with concurrent Run tests.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 11, "sha-thorough")
	claudeMock := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{
				Summary: "Base summary",
				Findings: []report.Finding{
					{ID: "W-001", Severity: report.SeverityWarning, Title: "Shared finding", File: "main.go", Line: 10, Problem: "p", Action: "a"},
				},
			}, nil
		},
		adversarial: func(dir, baseBranch string) (*report.ReviewResult, error) {
			return &report.ReviewResult{
				Findings: []report.Finding{
					// Same file+line+title upgrades severity to BLOCKING.
					{ID: "adv-up", Severity: report.SeverityBlocking, Title: "Shared finding", File: "main.go", Line: 10, Problem: "p2", Action: "a2"},
					// Adversarial-only finding: must be appended.
					{ID: "adv-new", Severity: report.SeverityCritical, Title: "Adversarial-only", File: "other.go", Line: 5, Problem: "x", Action: "y"},
				},
			}, nil
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	opts := baseOpts()
	opts.Thorough = true

	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if claudeMock.adversarialCalls != 1 {
		t.Fatalf("AdversarialReview calls = %d, want 1", claudeMock.adversarialCalls)
	}
	body := out.String()
	if !strings.Contains(body, "Adversarial-only") {
		t.Error("merged output missing adversarial-only finding")
	}
	if !strings.Contains(body, "**Passes**: ran "+claude.PassLabelAdversarial+"\n") {
		t.Errorf("the Summary should list the adversarial pass as ran:\n%s", body)
	}
}

// TestRun_AnchorsFindingsBeforeMerge proves the line anchor runs before the
// cross-pass merge: the review names line 2 for code at lines 9-10, the
// adversarial pass names the quoted lines, and the two fold into one finding
// on the quoted code. Without anchoring, |2-9| exceeds the merge's ±3 lines
// and the one defect is reported twice.
func TestRun_AnchorsFindingsBeforeMerge(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 12, "sha-anchor")
	pr.ChangedFiles = []string{"main.go"}
	src := strings.Join([]string{
		"package main",             // 1
		"",                         // 2
		"import \"fmt\"",           // 3
		"",                         // 4
		"func serve(cfg config) {", // 5
		"\tcfg.validate()",         // 6
		"\tdefer cfg.close()",      // 7
		"",                         // 8
		"\tif err := runServer(cfg); err != nil {", // 9
		"\t\tfmt.Println(err)",                     // 10
		"\t}",                                      // 11
		"}",                                        // 12
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(pr.Dir, "main.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	const (
		title   = "Server error is printed, not returned"
		snippet = "if err := runServer(cfg); err != nil {\n\tfmt.Println(err)"
	)
	claudeMock := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{Findings: []report.Finding{
				{ID: "W-001", Severity: report.SeverityWarning, Title: title, File: "main.go", Line: 2, Problem: "p", Action: "a", Confidence: report.ConfidenceLikely, CodeSnippet: snippet},
			}}, nil
		},
		adversarial: func(dir, baseBranch string) (*report.ReviewResult, error) {
			return &report.ReviewResult{Findings: []report.Finding{
				{ID: "W-001", Severity: report.SeverityWarning, Title: title, File: "main.go", Line: 9, LineEnd: 10, Problem: "p", Action: "a", Confidence: report.ConfidenceLikely, CodeSnippet: snippet},
			}}, nil
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	opts := baseOpts()
	opts.Thorough = true
	if err := runner.Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	cached, ok := readCachedResult(t, cache.Key(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, "thorough", baseOptsPatternFlag()))
	if !ok {
		t.Fatal("expected a cached result")
	}
	if len(cached.Findings) != 1 {
		t.Fatalf("got %d findings, want the two passes' findings merged into one: %+v", len(cached.Findings), cached.Findings)
	}
	f := cached.Findings[0]
	if f.Line != 9 || f.LineEnd != 10 || f.LineCheck != report.LineCheckReanchored {
		t.Errorf("merged finding at %d-%d (%q), want 9-10 reanchored", f.Line, f.LineEnd, f.LineCheck)
	}
	if want := []string{hygiene.PassReview, hygiene.PassAdversarial}; !slices.Equal(f.ConfirmedBy, want) {
		t.Errorf("confirmed_by = %v, want %v", f.ConfirmedBy, want)
	}
	if f.Confidence != report.ConfidenceVerified {
		t.Errorf("confidence = %q, want the cross-pass boost to verified", f.Confidence)
	}
	if cached.Gates == nil || cached.Gates.Anchor == nil {
		t.Fatalf("cached result missing anchor stats: %+v", cached.Gates)
	}
	if got := *cached.Gates.Anchor; got != (report.AnchorGateStats{Examined: 2, Reanchored: 1}) {
		t.Errorf("anchor stats = %+v, want {Examined:2 Reanchored:1}", got)
	}
}

// TestRun_MergeKeepsTheAnchoredPlace proves the merge keeps the place one
// pass's quote pinned: the review quotes `return nil`, which occurs twice and
// leaves it unanchored on line 7, the adversarial pass quotes lines 7-8
// exactly, and the folded finding sits on 7-8 as anchored, so --inline still
// comments on it.
func TestRun_MergeKeepsTheAnchoredPlace(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 13, "sha-anchor-merge")
	pr.ChangedFiles = []string{"main.go"}
	src := strings.Join([]string{
		"package main",         // 1
		"",                     // 2
		"func start() error {", // 3
		"\treturn nil",         // 4
		"}",                    // 5
		"",                     // 6
		"func stop() error {",  // 7
		"\treturn nil",         // 8
		"}",                    // 9
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(pr.Dir, "main.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	const title = "stop swallows the shutdown error"
	claudeMock := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{Findings: []report.Finding{
				{ID: "W-001", Severity: report.SeverityWarning, Title: title, File: "main.go", Line: 7, Problem: "p", Action: "a", Confidence: report.ConfidenceLikely, CodeSnippet: "return nil"},
			}}, nil
		},
		adversarial: func(dir, baseBranch string) (*report.ReviewResult, error) {
			return &report.ReviewResult{Findings: []report.Finding{
				{ID: "W-001", Severity: report.SeverityWarning, Title: title, File: "main.go", Line: 7, LineEnd: 8, Problem: "p", Action: "a", Confidence: report.ConfidenceLikely, CodeSnippet: "func stop() error {\n\treturn nil"},
			}}, nil
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	opts := baseOpts()
	opts.Thorough = true
	if err := runner.Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	cached, ok := readCachedResult(t, cache.Key(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, "thorough", baseOptsPatternFlag()))
	if !ok {
		t.Fatal("expected a cached result")
	}
	if len(cached.Findings) != 1 {
		t.Fatalf("got %d findings, want the two passes' findings merged into one: %+v", len(cached.Findings), cached.Findings)
	}
	f := cached.Findings[0]
	if f.Line != 7 || f.LineEnd != 8 || f.LineCheck != report.LineCheckAnchored {
		t.Errorf("merged finding at %d-%d (%q), want the adversarial pass's anchored 7-8", f.Line, f.LineEnd, f.LineCheck)
	}
	if want := []string{hygiene.PassReview, hygiene.PassAdversarial}; !slices.Equal(f.ConfirmedBy, want) {
		t.Errorf("confirmed_by = %v, want %v", f.ConfirmedBy, want)
	}
	// The counts are per pass, before the merge.
	if cached.Gates == nil || cached.Gates.Anchor == nil || *cached.Gates.Anchor != (report.AnchorGateStats{Examined: 2, Unanchored: 1}) {
		t.Errorf("anchor stats = %+v, want {Examined:2 Unanchored:1}", cached.Gates)
	}
}

// TestRun_AnchorRelocatesWithinTheFilesTheDiffKept locks the relocation set
// under --diff-exclusions: the changed files the diff kept. A lock file over
// the anchor's per-file read limit that the diff left out stops no
// relocation, and no finding moves into a vendored file the diff left out.
// Without exclusions both are searched, and the unread lock file stops every
// relocation, as decision 139 has it.
func TestRun_AnchorRelocatesWithinTheFilesTheDiffKept(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)
	t.Setenv("TMPDIR", t.TempDir())

	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	git("init", "-q")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "tester")
	write("a.go", "package app\n")
	write("b.go", "package app\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	git("update-ref", "refs/remotes/origin/main", git("rev-parse", "HEAD"))
	write("a.go", "package app\n\nfunc serve() {}\n")
	write("b.go", "package app\n\nfunc load() {\n\tcfg := readConfigurationFile(path)\n}\n")
	write("vendor/x/codec.go", "package x\n\nfunc init() {\n\tregisterVendoredCodec(codec)\n}\n")
	write("package-lock.json", strings.Repeat("{}\n", 2<<20)) // 6 MiB
	git("add", "-A")
	git("commit", "-q", "-m", "head")

	const (
		inB      = "Configuration read is unchecked"
		inVendor = "Codec registered twice"
	)
	for _, exclude := range []bool{false, true} {
		claudeMock := &configurableClaude{
			review: func(string, claude.ReviewContext) (*report.ReviewResult, error) {
				return &report.ReviewResult{Summary: "ok", Findings: []report.Finding{
					{ID: "W-001", Severity: report.SeverityWarning, Title: inB, File: "a.go", Line: 3, Problem: "p", Action: "a", Confidence: report.ConfidenceLikely, CodeSnippet: "cfg := readConfigurationFile(path)"},
					{ID: "W-002", Severity: report.SeverityWarning, Title: inVendor, File: "a.go", Line: 1, Problem: "p", Action: "a", Confidence: report.ConfidenceLikely, CodeSnippet: "registerVendoredCodec(codec)"},
				}}, nil
			},
		}
		pr := fakePR(t, "acme", "widgets", 14, "sha-anchor-excluded")
		pr.Dir, pr.Local = dir, true
		pr.ChangedFiles = []string{"a.go", "b.go", "package-lock.json", "vendor/x/codec.go"}
		gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
		opts := baseOpts()
		opts.NoCache = true
		opts.Format = formatJSON
		opts.DiffExclusions = exclude

		var out bytes.Buffer
		if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&out, opts); err != nil {
			t.Fatalf("exclude=%v: Run returned error: %v", exclude, err)
		}
		var got report.ReviewResult
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("exclude=%v: decoding the JSON output: %v\n%s", exclude, err, out.String())
		}
		places := map[string]string{}
		for _, f := range got.Findings {
			places[f.Title] = fmt.Sprintf("%s %s:%d", f.LineCheck, f.File, f.Line)
		}
		want := map[string]string{
			inB:      report.LineCheckUnanchored + " a.go:3",
			inVendor: report.LineCheckUnanchored + " a.go:1",
		}
		if exclude {
			want[inB] = report.LineCheckRelocated + " b.go:4"
		}
		if !reflect.DeepEqual(places, want) {
			t.Errorf("exclude=%v: places = %v, want %v", exclude, places, want)
		}
	}
}

func TestRun_SpecialistsFanOutAndMerge(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 31, "sha-spec")
	claudeMock := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{
				Summary: "Base summary",
				Findings: []report.Finding{
					{ID: "W-001", Severity: report.SeverityWarning, Title: "Shared finding", File: "main.go", Line: 10, Confidence: report.ConfidenceLikely, Problem: "p", Action: "a"},
				},
			}, nil
		},
		specialist: func(dir, baseBranch, key, focus string) (*report.ReviewResult, error) {
			if key == "security" {
				return &report.ReviewResult{Findings: []report.Finding{
					// Same file+line+title as the main finding → cross-pass boost.
					{Severity: report.SeverityWarning, Title: "Shared finding", File: "main.go", Line: 10, Problem: "p", Action: "a"},
					// Specialist-only finding → appended.
					{Severity: report.SeverityCritical, Title: "SQL injection", File: "db.go", Line: 3, Problem: "x", Action: "y"},
				}}, nil
			}
			return &report.ReviewResult{}, nil
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	opts := baseOpts()
	opts.Specialists = true

	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if int(claudeMock.specialistCalls) != len(claude.Specialists) {
		t.Fatalf("specialist calls = %d, want %d (one per registered specialist)", claudeMock.specialistCalls, len(claude.Specialists))
	}
	body := out.String()
	if !strings.Contains(body, "SQL injection") {
		t.Error("merged output missing specialist-only finding")
	}
	// Every specialist ran: an empty changed-file set fails the gate open.
	labels := make([]string, len(claude.Specialists))
	for i, sp := range claude.Specialists {
		labels[i] = sp.PassLabel()
	}
	if !strings.Contains(body, "**Passes**: ran "+strings.Join(labels, ", ")+"\n") {
		t.Errorf("the Summary should list every specialist as ran:\n%s", body)
	}
	if !strings.Contains(body, "Confirmed by") {
		t.Error("a finding flagged by review + a specialist should be cross-pass confirmed")
	}
}

func TestRun_SpecialistsDisabledByDefault(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 32, "sha-nospec")
	claudeMock := &configurableClaude{}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	if err := runner.Run(&bytes.Buffer{}, baseOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if claudeMock.specialistCalls != 0 {
		t.Errorf("specialist calls = %d, want 0 without --specialists", claudeMock.specialistCalls)
	}
}

func TestRun_SpecialistsAdaptiveGating(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 33, "sha-gate")
	// A markdown-only PR: only the NeverGate specialists (security,
	// data-migration) have relevant paths the diff touches, so the four
	// gateable specialists are skipped.
	pr.ChangedFiles = []string{"README.md"}

	var (
		mu  sync.Mutex
		ran = map[string]bool{}
	)
	claudeMock := &configurableClaude{
		specialist: func(dir, baseBranch, key, focus string) (*report.ReviewResult, error) {
			mu.Lock()
			ran[key] = true
			mu.Unlock()
			return &report.ReviewResult{}, nil
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	opts := baseOpts()
	opts.Specialists = true

	if err := runner.Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if int(claudeMock.specialistCalls) != 2 {
		t.Fatalf("specialist calls = %d, want 2 for a markdown-only PR", claudeMock.specialistCalls)
	}
	if !ran["security"] || !ran["data-migration"] || len(ran) != 2 {
		t.Errorf("ran specialists = %v, want exactly security and data-migration", ran)
	}
}

func TestRun_SkipSuppressionDropsUnchangedRepeats(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	dir, firstSHA := initGitRepoTwoCommits(t)
	pr := &github.PR{
		Owner: "acme", Repo: "widgets", Number: 40, Title: "PR",
		BaseBranch: "main", HeadBranch: "feature", HeadSHA: "headsha", Dir: dir,
	}

	// Prior review (posted at firstSHA) reported a nit on unchanged.go and a bug
	// on changed.go.
	prior := report.ReviewResult{Findings: []report.Finding{
		{Title: "Nit A", File: "unchanged.go", Line: 5, Severity: report.SeverityWarning},
		{Title: "Bug B", File: "changed.go", Line: 9, Severity: report.SeverityWarning},
	}}
	priorComment := "## Previous review\n" + report.RenderDataBlock(prior, firstSHA, report.Usage{})

	claudeMock := &configurableClaude{
		usage: report.Usage{Calls: 3, InputTokens: 13400, OutputTokens: 4200, CostUSD: 0.42},
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{Summary: "S", Findings: []report.Finding{
				{ID: "W-001", Title: "Nit A", File: "unchanged.go", Line: 5, Severity: report.SeverityWarning, Confidence: report.ConfidenceVerified, Problem: "p", Action: "a"},
				{ID: "W-002", Title: "Bug B", File: "changed.go", Line: 9, Severity: report.SeverityWarning, Confidence: report.ConfidenceVerified, Problem: "p", Action: "a"},
				{ID: "W-003", Title: "New finding", File: "new.go", Line: 1, Severity: report.SeverityWarning, Confidence: report.ConfidenceVerified, Problem: "p", Action: "a"},
			}}, nil
		}}
	var postedBody string
	gh := &githubtest.Fake{
		FetchAndCheckoutFn:   func(ref string) (*github.PR, error) { return pr, nil },
		FetchReviewCommentFn: func(owner, repo string, number int) (string, bool, error) { return priorComment, true, nil },
		PostPRCommentFn: func(owner, repo string, number int, body string) (string, error) {
			postedBody = body
			return "url", nil
		},
	}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	opts := baseOpts()
	opts.PostReview = true

	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	body := out.String()
	// Nit A: previously reported, file unchanged → suppressed from sections.
	if strings.Contains(body, ": Nit A") {
		t.Error("Nit A on an unchanged file should be suppressed from the rendered sections")
	}
	// Bug B: previously reported but its file changed → kept (possible regression).
	if !strings.Contains(body, ": Bug B") {
		t.Error("Bug B on a changed file should be kept")
	}
	// New finding: never seen before → kept.
	if !strings.Contains(body, ": New finding") {
		t.Error("a new finding should be kept")
	}
	if !strings.Contains(body, "suppressed as previously reported") {
		t.Error("expected a suppression note surfacing the dropped finding")
	}
	// The posted comment's data block still carries the full set so the next
	// re-review can compare again.
	if !strings.Contains(postedBody, "planwerk-agent-data") {
		t.Error("posted comment must include the machine-readable data block")
	}
	// The data block also carries the per-Run Claude usage totals for CI
	// extraction (issue #46, AC#3).
	if !strings.Contains(postedBody, `"usage":{"calls":3,"input_tokens":13400`) {
		t.Errorf("posted data block must embed the Claude usage totals; got %q", postedBody)
	}
}

// TestRun_InlineSkipSuppressionLeavesRepeatsUncommented is the --inline twin of
// TestRun_SkipSuppressionDropsUnchangedRepeats: the prior data block comes from
// an inline review's body, the shape the GitHub Action posted before the
// summary moved into the PR comment (decision 128). A repeat on an unchanged
// file gets no inline comment. The summary comment carries the suppression
// note and the full set for the next re-review, and the review carries
// neither (decision 133).
func TestRun_InlineSkipSuppressionLeavesRepeatsUncommented(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	dir, firstSHA := initGitRepoTwoCommits(t)
	pr := &github.PR{
		Owner: "acme", Repo: "widgets", Number: 41, Title: "PR",
		BaseBranch: "main", HeadBranch: "feature", HeadSHA: "headsha", Dir: dir,
	}

	prior := report.ReviewResult{Findings: []report.Finding{
		{Title: "Nit A", File: "unchanged.go", Line: 5, Severity: report.SeverityWarning},
		{Title: "Bug B", File: "changed.go", Line: 9, Severity: report.SeverityWarning},
	}}
	priorReview := "## Previous review\n" + report.RenderDataBlock(prior, firstSHA, report.Usage{}) + "\n<!-- planwerk-agent-inline -->"

	claudeMock := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{Summary: "S", Findings: []report.Finding{
				{ID: "W-001", Title: "Nit A", File: "unchanged.go", Line: 5, Severity: report.SeverityWarning, Confidence: report.ConfidenceVerified, Problem: "p", Action: "a"},
				{ID: "W-002", Title: "Bug B", File: "changed.go", Line: 9, Severity: report.SeverityWarning, Confidence: report.ConfidenceVerified, Problem: "p", Action: "a"},
				{ID: "W-003", Title: "New finding", File: "new.go", Line: 1, Severity: report.SeverityWarning, Confidence: report.ConfidenceVerified, Problem: "p", Action: "a"},
			}}, nil
		}}

	// Every file is new in the PR diff, so each finding's line maps and only
	// suppression can keep one from becoming an inline comment.
	var diff strings.Builder
	for _, file := range []string{"unchanged.go", "changed.go", "new.go"} {
		diff.WriteString("diff --git a/" + file + " b/" + file + "\n--- /dev/null\n+++ b/" + file + "\n@@ -0,0 +1,10 @@\n")
		for range 10 {
			diff.WriteString("+line\n")
		}
	}

	var (
		summary, reviewBody string
		comments            []github.ReviewComment
	)
	gh := &githubtest.Fake{
		FetchAndCheckoutFn:   func(ref string) (*github.PR, error) { return pr, nil },
		FetchReviewCommentFn: func(owner, repo string, number int) (string, bool, error) { return priorReview, true, nil },
		FetchDiffFn:          func(owner, repo string, number int) (string, error) { return diff.String(), nil },
		SubmitPRReviewFn: func(owner, repo string, number int, commitSHA, body string, cs []github.ReviewComment) (string, error) {
			reviewBody = body
			comments = append(comments, cs...)
			return "https://github.com/acme/widgets/pull/41#pullrequestreview-1", nil
		},
		PostPRCommentFn: func(owner, repo string, number int, body string) (string, error) {
			summary = body
			return "https://github.com/acme/widgets/pull/41#issuecomment-1", nil
		},
	}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	opts := baseOpts()
	opts.PostReview = true
	opts.InlineReview = true

	if err := runner.Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	commented := map[string]int{}
	for _, c := range comments {
		commented[c.Path]++
	}
	if commented["unchanged.go"] != 0 {
		t.Error("Nit A, previously reported on an unchanged file, must get no inline comment")
	}
	if commented["changed.go"] != 1 || commented["new.go"] != 1 {
		t.Errorf("inline comments per file = %v, want one each on changed.go and new.go", commented)
	}
	if n := gh.Count("PostPRComment"); n != 1 {
		t.Errorf("PostPRComment calls = %d, want the summary once", n)
	}
	if !strings.Contains(summary, "suppressed as previously reported") {
		t.Error("the summary comment must carry the suppression note")
	}
	if strings.Contains(reviewBody, "suppressed as previously reported") || strings.Contains(reviewBody, "planwerk-agent-data") {
		t.Errorf("the review body must carry neither the note nor a data block:\n%s", reviewBody)
	}
	_, findings, ok := report.ParseDataBlock(summary)
	if !ok {
		t.Fatalf("the summary comment carries no data block:\n%s", summary)
	}
	var titles []string
	for _, f := range findings {
		titles = append(titles, f.Title)
	}
	if want := []string{"Nit A", "Bug B", "New finding"}; !slices.Equal(titles, want) {
		t.Errorf("data block findings = %v, want the full set %v", titles, want)
	}
}

// TestSkipSuppressed_RefusesACommitSHAThatIsNotAnObjectName: the prior data
// block's commit_sha becomes an argument to git diff, so a forged one that git
// would read as an option suppresses nothing and never reaches git (decision
// 128). Read as --output, it would write a file and hide Bug B on changed.go.
func TestSkipSuppressed_RefusesACommitSHAThatIsNotAnObjectName(t *testing.T) {
	dir, _ := initGitRepoTwoCommits(t)
	written := filepath.Join(t.TempDir(), "written-by-git")
	findings := []report.Finding{
		{Title: "Nit A", File: "unchanged.go", Line: 5, Severity: report.SeverityWarning},
		{Title: "Bug B", File: "changed.go", Line: 9, Severity: report.SeverityWarning},
	}
	prior := "## Previous review\n" + report.RenderDataBlock(report.ReviewResult{Findings: findings}, "--output="+written, report.Usage{}) + "\n<!-- planwerk-agent-inline -->"
	gh := &githubtest.Fake{
		FetchReviewCommentFn: func(owner, repo string, number int) (string, bool, error) { return prior, true, nil },
	}
	pr := &github.PR{Owner: "acme", Repo: "widgets", Number: 42, Dir: dir}

	kept, suppressed, _ := newTestRunner(gh).skipSuppressed(&report.ReviewResult{Findings: findings}, pr, report.SeverityInfo, "")
	if suppressed.total() != 0 || len(kept) != len(findings) {
		t.Errorf("kept %d, suppressed %+v; want every finding kept", len(kept), suppressed)
	}
	if _, err := os.Stat(written); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the forged commit_sha reached git as an option: stat %s = %v", written, err)
	}
}

// TestSkipSuppressed_RunsBothStages: the prior data block suppresses a repeat
// on an unchanged file, and a resolved thread dismisses a finding on the file
// the push changed, because the code it quotes is still there; one whose code
// is gone is kept (decision 132). skipSuppressed hands back the threads it
// fetched. When they cannot be fetched it returns none and dismisses nothing,
// while the data block still suppresses.
func TestSkipSuppressed_RunsBothStages(t *testing.T) {
	dir, firstSHA := initGitRepoTwoCommits(t)
	finding := func(id, title, file, snippet string) report.Finding {
		return report.Finding{
			ID: id, Severity: report.SeverityWarning, Confidence: report.ConfidenceVerified,
			Title: title, File: file, Line: 2, CodeSnippet: snippet, Problem: "p", Action: "a",
		}
	}
	repeat := finding("W-001", "Bare package", "unchanged.go", "package x")
	stale := finding("W-002", "Version comment", "changed.go", "// v2")
	gone := finding("W-003", "Old version comment", "changed.go", "// v1")
	prior := "## Previous review\n" + report.RenderDataBlock(report.ReviewResult{Findings: []report.Finding{repeat}}, firstSHA, report.Usage{}) + "\n<!-- planwerk-agent-inline -->"
	threads := []github.ReviewThread{
		ownThread(stale, "github-actions", true, "maintainer"),
		ownThread(gone, "github-actions", true, "maintainer"),
	}
	gh := &githubtest.Fake{
		FetchReviewCommentFn: func(owner, repo string, number int) (string, bool, error) { return prior, true, nil },
		Threads:              threads,
	}
	pr := &github.PR{Owner: "acme", Repo: "widgets", Number: 43, Dir: dir}
	result := &report.ReviewResult{Findings: []report.Finding{repeat, stale, gone}}

	kept, s, got := newTestRunner(gh).skipSuppressed(result, pr, report.SeverityInfo, "")
	if ids := findingIDs(kept); !slices.Equal(ids, []string{"W-003"}) {
		t.Errorf("kept = %v, want [W-003]", ids)
	}
	if ids := findingIDs(s.unchanged); !slices.Equal(ids, []string{"W-001"}) {
		t.Errorf("previously reported = %v, want [W-001]", ids)
	}
	if ids := findingIDs(s.dismissed); !slices.Equal(ids, []string{"W-002"}) {
		t.Errorf("dismissed = %v, want [W-002]", ids)
	}
	if len(got) != len(threads) {
		t.Errorf("returned %d threads, want the %d fetched", len(got), len(threads))
	}

	gh.ThreadsErr = errors.New("graphql: rate limited")
	kept, s, got = newTestRunner(gh).skipSuppressed(result, pr, report.SeverityInfo, "")
	if got != nil || len(s.dismissed) != 0 {
		t.Errorf("with the threads unavailable: threads %+v, dismissed %+v; want neither", got, s.dismissed)
	}
	if ids := findingIDs(s.unchanged); !slices.Equal(ids, []string{"W-001"}) || len(kept) != 2 {
		t.Errorf("with the threads unavailable: previously reported %v, kept %d; want [W-001] and 2", ids, len(kept))
	}
}

// TestSkipSuppressed_SpendsEachEarlierFindingOnce: an earlier finding leaves
// out at most one current finding, even when the prior data block carries it
// and its own thread was resolved, or when later runs posted it again in
// threads of their own, and only a finding the report would show spends it
// (decision 132). Otherwise a CRITICAL raised on the same code, or the listed
// repeat itself, would drop out of the report.
func TestSkipSuppressed_SpendsEachEarlierFindingOnce(t *testing.T) {
	dir, firstSHA := initGitRepoTwoCommits(t)
	finding := func(id string, sev report.Severity, title, file, snippet string) report.Finding {
		return report.Finding{
			ID: id, Severity: sev, Confidence: report.ConfidenceVerified,
			Title: title, File: file, Line: 1, CodeSnippet: snippet, Problem: "p", Action: "a",
		}
	}
	earlier := finding("W-001", report.SeverityWarning, "Unchecked Close", "unchanged.go", "package x")
	critical := finding("C-001", report.SeverityCritical, "Shadowed package", "unchanged.go", "package x")
	nit := finding("I-001", report.SeverityInfo, "Defer in loop style", "unchanged.go", "package x")
	earlierChanged := finding("W-001", report.SeverityWarning, "Version comment", "changed.go", "// v2")
	criticalChanged := finding("C-001", report.SeverityCritical, "Stale version", "changed.go", "// v2")
	resolved := func(f report.Finding) []github.ReviewThread {
		return []github.ReviewThread{ownThread(f, "github-actions", true, "maintainer")}
	}
	// The second run posted f again because its file changed while the first
	// thread was open; a maintainer resolved both.
	resolvedTwice := func(f report.Finding) []github.ReviewThread {
		return append(resolved(f), postedIn(ownThread(f, "github-actions", true, "maintainer"), "PRR_2"))
	}

	for _, tc := range []struct {
		name                                   string
		prior                                  []report.Finding // nil: no prior data block
		threads                                []github.ReviewThread
		current                                []report.Finding
		minSeverity                            report.Severity
		wantKept, wantUnchanged, wantDismissed []string
	}{
		{
			name:  "data block and resolved thread name one earlier finding",
			prior: []report.Finding{earlier}, threads: resolved(earlier),
			current: []report.Finding{critical, earlier}, minSeverity: report.SeverityInfo,
			wantKept: []string{"C-001"}, wantUnchanged: []string{"W-001"},
		},
		{
			name:  "file changed, so the resolved thread alone spends it",
			prior: []report.Finding{earlierChanged}, threads: resolved(earlierChanged),
			current: []report.Finding{criticalChanged, earlierChanged}, minSeverity: report.SeverityInfo,
			wantKept: []string{"C-001"}, wantDismissed: []string{"W-001"},
		},
		{
			name:  "file changed, so threads two runs opened for it spend it once",
			prior: []report.Finding{earlierChanged}, threads: resolvedTwice(earlierChanged),
			current: []report.Finding{criticalChanged, earlierChanged}, minSeverity: report.SeverityInfo,
			wantKept: []string{"C-001"}, wantDismissed: []string{"W-001"},
		},
		{
			name:    "data block: a repeat below the thresholds spends nothing",
			prior:   []report.Finding{earlier},
			current: []report.Finding{earlier, nit}, minSeverity: report.SeverityWarning,
			wantKept: []string{"I-001"}, wantUnchanged: []string{"W-001"},
		},
		{
			name:    "resolved thread: a repeat below the thresholds spends nothing",
			threads: resolved(earlier),
			current: []report.Finding{earlier, nit}, minSeverity: report.SeverityWarning,
			wantKept: []string{"I-001"}, wantDismissed: []string{"W-001"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gh := &githubtest.Fake{Threads: tc.threads}
			if tc.prior != nil {
				prior := "## Previous review\n" + report.RenderDataBlock(report.ReviewResult{Findings: tc.prior}, firstSHA, report.Usage{}) + "\n<!-- planwerk-agent-inline -->"
				gh.FetchReviewCommentFn = func(owner, repo string, number int) (string, bool, error) { return prior, true, nil }
			}
			pr := &github.PR{Owner: "acme", Repo: "widgets", Number: 44, Dir: dir}

			kept, s, _ := newTestRunner(gh).skipSuppressed(&report.ReviewResult{Findings: tc.current}, pr, tc.minSeverity, "")
			if ids := findingIDs(kept); !slices.Equal(ids, tc.wantKept) {
				t.Errorf("kept = %v, want %v", ids, tc.wantKept)
			}
			if ids := findingIDs(s.unchanged); !slices.Equal(ids, tc.wantUnchanged) {
				t.Errorf("previously reported = %v, want %v", ids, tc.wantUnchanged)
			}
			if ids := findingIDs(s.dismissed); !slices.Equal(ids, tc.wantDismissed) {
				t.Errorf("dismissed = %v, want %v", ids, tc.wantDismissed)
			}
		})
	}
}

// findingIDs returns the ID of each finding, in order.
func findingIDs(findings []report.Finding) []string {
	ids := make([]string, 0, len(findings))
	for _, f := range findings {
		ids = append(ids, f.ID)
	}
	return ids
}

func TestRun_WikiDisabledLeavesRunUnchanged(t *testing.T) {
	// With the wiki disabled (the default), ResolveWiki short-circuits without
	// touching the network: no project memory is injected into the review
	// context and no wiki provenance line is rendered, so the run is unchanged.
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 77, "sha-wiki-off")
	gotMemory := patterns.MemoryCatalog{Dir: "unset"}
	claudeMock := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			gotMemory = ctx.Memory
			return &report.ReviewResult{Summary: "ok"}, nil
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	var out bytes.Buffer
	if err := runner.Run(&out, baseOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if gotMemory.Dir != "" || gotMemory.Pages != nil {
		t.Errorf("a disabled wiki must leave ReviewContext.Memory the zero MemoryCatalog, got %+v", gotMemory)
	}
	if strings.Contains(out.String(), "Wiki:") {
		t.Errorf("a wiki-disabled review must not render a Wiki provenance line, got:\n%s", out.String())
	}
}

// memoryWiki is a ResolveWiki seam for a wiki that carries one memory page and
// no clone directory, so the capture pass stays off.
func memoryWiki(_, _ string, _ patterns.WikiOptions, _ patterns.RemoteOptions) patterns.ResolvedWiki {
	return patterns.ResolvedWiki{
		Repo:      "acme/widgets.wiki",
		CommitSHA: "wikisha",
		MemoryPages: []patterns.MemoryPage{
			{Name: "pin-dependencies.md", Title: "Pin every dependency", Summary: "Dependencies are pinned.", Body: "# Pin every dependency"},
		},
	}
}

// memoryDirsIn returns the entries of dir whose name starts with
// patterns.MemoryDirPrefix.
func memoryDirsIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var found []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), patterns.MemoryDirPrefix) {
			found = append(found, e.Name())
		}
	}
	return found
}

// TestRun_MemoryDirectoryLivesForTheReview locks the lifetime of the project
// memory directory: the review session can read the page while it runs, and
// the directory is gone when Run returns.
func TestRun_MemoryDirectoryLivesForTheReview(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	pr := fakePR(t, "acme", "widgets", 78, "sha-wiki-memory")
	var gotMemory patterns.MemoryCatalog
	claudeMock := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			gotMemory = ctx.Memory
			data, err := os.ReadFile(filepath.Join(ctx.Memory.Dir, "pin-dependencies.md"))
			if err != nil {
				t.Errorf("the memory page must be readable while the review runs: %v", err)
			}
			if string(data) != "# Pin every dependency\n" {
				t.Errorf("memory page = %q, want the page body and a newline", data)
			}
			return &report.ReviewResult{Summary: "ok"}, nil
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	runner := &Runner{Claude: claudeMock, GitHub: gh, ResolveWiki: memoryWiki}

	if err := runner.Run(&bytes.Buffer{}, baseOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if claudeMock.reviewCalls != 1 {
		t.Fatalf("Review calls = %d, want 1", claudeMock.reviewCalls)
	}
	if !strings.HasPrefix(filepath.Base(gotMemory.Dir), patterns.MemoryDirPrefix) || len(gotMemory.Pages) != 1 {
		t.Fatalf("ReviewContext.Memory = %+v, want a memory directory and the one page", gotMemory)
	}
	if _, err := os.Stat(gotMemory.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("after Run, stat %s = %v, want not exist", gotMemory.Dir, err)
	}
	if left := memoryDirsIn(t, tmp); len(left) != 0 {
		t.Errorf("TMPDIR holds %v after Run, want no memory directory", left)
	}
}

// TestRun_CacheHitWritesNoMemoryDirectory locks that the memory is written
// after the cache check: a review served from the cache creates no directory.
func TestRun_CacheHitWritesNoMemoryDirectory(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	gh := &githubtest.Fake{
		FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return fakePR(t, "acme", "widgets", 79, "sha-wiki-cached"), nil },
	}
	first := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{Summary: "Primary"}, nil
		},
	}
	if err := (&Runner{Claude: first, GitHub: gh, ResolveWiki: memoryWiki}).Run(&bytes.Buffer{}, baseOpts()); err != nil {
		t.Fatalf("first Run returned error: %v", err)
	}

	// The second run gets a temp dir of its own, and its log is captured:
	// MaterializeMemoryOrWarn logs every directory it writes.
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	second := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			t.Error("Review must not be called on a cache hit")
			return nil, nil
		},
	}
	var out bytes.Buffer
	if err := (&Runner{Claude: second, GitHub: gh, ResolveWiki: memoryWiki}).Run(&out, baseOpts()); err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}
	if !strings.Contains(logBuf.String(), "using cached review result") {
		t.Fatalf("the second run must be served from the cache, got log:\n%s", logBuf.String())
	}
	if strings.Contains(logBuf.String(), "project memory") {
		t.Errorf("a cached review must not write the project memory, got log:\n%s", logBuf.String())
	}
	if left := memoryDirsIn(t, tmp); len(left) != 0 {
		t.Errorf("TMPDIR holds %v, want no memory directory on a cache hit", left)
	}
}

func TestRun_AdversarialErrorDoesNotFailRun(t *testing.T) {
	// Secondary passes (adversarial, coverage, compliance) must not abort the
	// review on failure — the primary result is still valid and is rendered,
	// with the failed pass named in the report.
	// Not t.Parallel(): cache.SetDir mutates a package-level variable and
	// would race with concurrent Run tests.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 12, "sha-adv-err")
	claudeMock := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{Summary: "Primary only"}, nil
		},
		adversarial: func(dir, baseBranch string) (*report.ReviewResult, error) {
			return nil, errors.New("adversarial crashed")
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	opts := baseOpts()
	opts.Thorough = true

	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("Run must not fail when adversarial pass errors: %v", err)
	}
	if !strings.Contains(out.String(), "## Summary\n\nPrimary only\n\n") {
		t.Errorf("expected the primary summary, without a suffix, when adversarial fails, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "**Incomplete review**") || !strings.Contains(out.String(), "> - `"+claude.PassLabelAdversarial+"`: adversarial crashed\n") {
		t.Errorf("expected the alert naming the failed adversarial pass, got:\n%s", out.String())
	}
}

func TestRun_PrimaryReviewErrorPropagates(t *testing.T) {
	// The primary review runs inside errgroup.Go with an explicit error
	// return, so a failure here MUST surface from g.Wait() and halt Run.
	// Not t.Parallel(): cache.SetDir mutates a package-level variable and
	// would race with concurrent Run tests.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 13, "sha-primary-err")
	claudeMock := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return nil, errors.New("claude exploded")
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	var out bytes.Buffer
	err := runner.Run(&out, baseOpts())
	if err == nil {
		t.Fatal("expected error when primary review fails")
	}
	if !strings.Contains(err.Error(), "claude review") || !strings.Contains(err.Error(), "claude exploded") {
		t.Errorf("error should wrap primary review failure, got: %v", err)
	}
}

func TestRun_FetchAndCheckoutErrorFailsFast(t *testing.T) {
	// This test does not touch the cache, but other Run tests mutate the
	// package-level cacheDir so we keep all Run tests serial to avoid races.
	gh := &githubtest.Fake{
		FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return nil, errors.New("gh not logged in") },
	}
	runner := &Runner{Claude: &configurableClaude{}, GitHub: gh}

	var out bytes.Buffer
	err := runner.Run(&out, baseOpts())
	if err == nil {
		t.Fatal("expected error when FetchAndCheckout fails")
	}
	if !strings.Contains(err.Error(), "fetching PR") {
		t.Errorf("error should wrap fetch failure, got: %v", err)
	}
}

func TestRun_CoverageMapRunsConcurrentlyAndRenders(t *testing.T) {
	// Verifies the CoverageMap errgroup branch fires and its result is
	// appended to the rendered output even though it is merged separately
	// from the review findings.
	// Not t.Parallel(): cache.SetDir mutates a package-level variable and
	// would race with concurrent Run tests.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 14, "sha-coverage")
	claudeMock := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{Summary: "Primary"}, nil
		},
		coverage: func(dir, baseBranch string) (*report.CoverageResult, error) {
			return &report.CoverageResult{
				Entries: []report.CoverageEntry{
					{File: "main.go", Function: "Run", Rating: "★★★"},
				},
			}, nil
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	opts := baseOpts()
	opts.CoverageMap = true

	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if claudeMock.coverageCalls != 1 {
		t.Errorf("CoverageMap calls = %d, want 1", claudeMock.coverageCalls)
	}
	if !strings.Contains(out.String(), "main.go") {
		t.Errorf("coverage map not rendered, got:\n%s", out.String())
	}
}

// fakeCapturer is a recording review.capture.Proposer: it counts calls and
// captures the context it was handed so a test can assert the review findings
// flowed into the proposal pass, without invoking Claude.
type fakeCapturer struct {
	calls  atomic.Int32
	ctx    capture.CaptureContext
	result *capture.CaptureResult
	err    error
}

func (f *fakeCapturer) Capture(dir string, ctx capture.CaptureContext) (*capture.CaptureResult, error) {
	f.calls.Add(1)
	f.ctx = ctx
	return f.result, f.err
}

// captureRunner wires a Runner with the capturer and a ResolveWiki seam that
// resolves to a temp wiki dir (so the gate's wiki.Dir != "" check passes without
// cloning a real wiki).
func captureRunner(t *testing.T, gh *githubtest.Fake, cl *configurableClaude, cp *fakeCapturer) *Runner {
	t.Helper()
	r := &Runner{Claude: cl, GitHub: gh, Capturer: cp}
	wikiDir := t.TempDir()
	r.ResolveWiki = func(_, _ string, _ patterns.WikiOptions, _ patterns.RemoteOptions) patterns.ResolvedWiki {
		return patterns.ResolvedWiki{Repo: "acme/widgets.wiki", CommitSHA: "wikisha", Dir: wikiDir}
	}
	return r
}

// reviewFindingClaude returns a configurableClaude whose primary review yields a
// single finding (empty Pattern, so it survives the capture pre-filter).
func reviewFindingClaude() *configurableClaude {
	return &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{
				Summary:  "Review summary",
				Findings: []report.Finding{{ID: "W-1", Severity: report.SeverityWarning, Title: "raw SQL", File: "db.go", Line: 3, Problem: "p", Action: "a"}},
			}, nil
		},
	}
}

func onePatternProposal() *capture.CaptureResult {
	return &capture.CaptureResult{
		Patterns: []capture.ProposedPage{
			{Path: "review_patterns/escape-untrusted-fences.md", Kind: capture.KindPattern, Title: "Escape untrusted fences", Body: "# Review Pattern: Escape untrusted fences\n\n## What to check\n...", Rationale: "recurs"},
		},
	}
}

func TestRun_CaptureProposesOnReview(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 51, "sha-capture")
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	cl := reviewFindingClaude()
	cp := &fakeCapturer{result: onePatternProposal()}
	runner := captureRunner(t, gh, cl, cp)

	var out bytes.Buffer
	if err := runner.Run(&out, baseOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cp.calls.Load() != 1 {
		t.Fatalf("capturer called %d times, want 1 — capture is on by default with a wiki", cp.calls.Load())
	}
	// The review finding (empty Pattern) survives CandidateFindings and reaches
	// the proposal pass as a candidate.
	if len(cp.ctx.Findings) != 1 || cp.ctx.Findings[0].File != "db.go" {
		t.Errorf("capturer got findings %+v, want the single review finding", cp.ctx.Findings)
	}
	if cp.ctx.RepoName != "acme/widgets" || cp.ctx.IssueNumber != 51 {
		t.Errorf("capturer got repo=%q number=%d, want acme/widgets / 51", cp.ctx.RepoName, cp.ctx.IssueNumber)
	}
	if !strings.Contains(out.String(), "Captured knowledge proposals:") {
		t.Errorf("missing the capture proposals on stdout:\n%s", out.String())
	}
}

func TestRun_CaptureNoCommentWithoutPostReview(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 52, "sha-nocomment")
	// postPRComment is left nil: a plain `review --wiki` must not post a PR
	// comment, so calling it would nil-panic and fail the test loudly.
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	cl := reviewFindingClaude()
	cp := &fakeCapturer{result: onePatternProposal()}
	runner := captureRunner(t, gh, cl, cp)

	var out bytes.Buffer
	if err := runner.Run(&out, baseOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cp.calls.Load() != 1 {
		t.Fatalf("capturer called %d times, want 1", cp.calls.Load())
	}
}

// TestRun_SARIFDiscardsTheCaptureRender: the capture pass still runs under
// --format sarif, but its printed proposals are discarded, as under json, so
// stdout stays one SARIF log (decision 134).
func TestRun_SARIFDiscardsTheCaptureRender(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 53, "sha-capture-sarif")
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	cp := &fakeCapturer{result: onePatternProposal()}
	runner := captureRunner(t, gh, reviewFindingClaude(), cp)
	opts := baseOpts()
	opts.Format = "sarif"

	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cp.calls.Load() != 1 {
		t.Fatalf("capturer called %d times, want 1: capture still runs under sarif", cp.calls.Load())
	}
	if strings.Contains(out.String(), "Captured knowledge proposals:") {
		t.Errorf("the capture render must not follow the SARIF log:\n%s", out.String())
	}
	var doc struct {
		Runs []struct {
			Results []struct {
				RuleID    string `json:"ruleId"`
				Locations []struct {
					PhysicalLocation struct {
						ArtifactLocation struct {
							URI string `json:"uri"`
						} `json:"artifactLocation"`
					} `json:"physicalLocation"`
				} `json:"locations"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, out.String())
	}
	if len(doc.Runs) != 1 || len(doc.Runs[0].Results) != 1 {
		t.Fatalf("got %+v, want one run with one result", doc.Runs)
	}
	res := doc.Runs[0].Results[0]
	if res.RuleID != "finding" || len(res.Locations) != 1 || res.Locations[0].PhysicalLocation.ArtifactLocation.URI != "db.go" {
		t.Errorf("result = %+v, want ruleId finding at db.go", res)
	}
}

// TestRun_CapturePostsCommentWithPostReview: with --post-review the capture
// proposals get a comment of their own through AddPRComment. Through
// PostPRComment they replaced the summary comment and its data block, so the
// next re-review found no findings to compare against (decision 133).
func TestRun_CapturePostsCommentWithPostReview(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 53, "sha-comment")
	var summaries, captures []string
	gh := &githubtest.Fake{
		FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil },
		PostPRCommentFn: func(owner, repo string, number int, body string) (string, error) {
			summaries = append(summaries, body)
			return "https://github.com/acme/widgets/pull/53#issuecomment-1", nil
		},
		AddPRCommentFn: func(owner, repo string, number int, body string) (string, error) {
			captures = append(captures, body)
			return "https://github.com/acme/widgets/pull/53#issuecomment-2", nil
		},
	}
	cl := reviewFindingClaude()
	cp := &fakeCapturer{result: onePatternProposal()}
	runner := captureRunner(t, gh, cl, cp)

	opts := baseOpts()
	opts.PostReview = true

	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(captures) != 1 || !strings.Contains(captures[0], "review_patterns/escape-untrusted-fences.md") {
		t.Fatalf("AddPRComment bodies = %q, want the capture proposal once", captures)
	}
	if !strings.Contains(captures[0], "Capture proposals generated by") {
		t.Errorf("capture comment missing its attribution footer:\n%s", captures[0])
	}
	if len(summaries) != 1 {
		t.Fatalf("PostPRComment bodies = %d, want the summary once", len(summaries))
	}
	if strings.Contains(summaries[0], "review_patterns/escape-untrusted-fences.md") {
		t.Errorf("the summary comment carries the capture proposal:\n%s", summaries[0])
	}
	if _, findings, ok := report.ParseDataBlock(summaries[0]); !ok || len(findings) != 1 {
		t.Errorf("summary data block = %+v (ok=%v), want the review finding", findings, ok)
	}
}

// TestRun_InlineSummaryFeedsTheNextRunsSuppression: the data block an --inline
// run writes into the summary comment is the one the next run reads back
// (decision 133). Run 2 reads run 1's summary as FetchReviewComment would find
// it, and leaves the finding repeated on an unchanged file out of its inline
// comments and lists it in the suppression note.
func TestRun_InlineSummaryFeedsTheNextRunsSuppression(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	dir, firstSHA := initGitRepoTwoCommits(t)
	repeat := report.Finding{ID: "W-001", Title: "Nit A", File: "unchanged.go", Line: 5, Severity: report.SeverityWarning, Confidence: report.ConfidenceVerified, Problem: "p", Action: "a"}
	fresh := report.Finding{ID: "W-002", Title: "Bug B", File: "changed.go", Line: 9, Severity: report.SeverityWarning, Confidence: report.ConfidenceVerified, Problem: "p", Action: "a"}
	claudeMock := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{Summary: "S", Findings: []report.Finding{repeat, fresh}}, nil
		}}

	var diff strings.Builder
	for _, file := range []string{"unchanged.go", "changed.go"} {
		diff.WriteString("diff --git a/" + file + " b/" + file + "\n--- /dev/null\n+++ b/" + file + "\n@@ -0,0 +1,10 @@\n")
		for range 10 {
			diff.WriteString("+line\n")
		}
	}

	run := func(headSHA, prior string) (summary string, commented map[string]int, stdout string) {
		t.Helper()
		// Local keeps Run's deferred Cleanup from removing the checkout both
		// runs share.
		pr := &github.PR{
			Owner: "acme", Repo: "widgets", Number: 42, Title: "PR",
			BaseBranch: "main", HeadBranch: "feature", HeadSHA: headSHA, Dir: dir, Local: true,
		}
		commented = map[string]int{}
		gh := &githubtest.Fake{
			FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil },
			FetchReviewCommentFn: func(owner, repo string, number int) (string, bool, error) {
				return prior, prior != "", nil
			},
			FetchDiffFn: func(owner, repo string, number int) (string, error) { return diff.String(), nil },
			PostPRCommentFn: func(owner, repo string, number int, body string) (string, error) {
				summary = body
				return "https://github.com/acme/widgets/pull/42#issuecomment-1", nil
			},
			SubmitPRReviewFn: func(owner, repo string, number int, commitSHA, body string, cs []github.ReviewComment) (string, error) {
				for _, c := range cs {
					commented[c.Path]++
				}
				return "https://github.com/acme/widgets/pull/42#pullrequestreview-1", nil
			},
		}
		opts := baseOpts()
		opts.PostReview = true
		opts.InlineReview = true
		opts.NoCache = true
		var out bytes.Buffer
		if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&out, opts); err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
		return summary, commented, out.String()
	}

	// Run 1 reviews firstSHA; only changed.go changes after it.
	first, commented, _ := run(firstSHA, "")
	if commented["unchanged.go"] != 1 || commented["changed.go"] != 1 {
		t.Fatalf("run 1 inline comments per file = %v, want one each", commented)
	}
	sha, findings, ok := report.ParseDataBlock(first)
	if !ok || sha != firstSHA || len(findings) != 2 {
		t.Fatalf("run 1 summary data block = %q, %d findings (ok=%v); want %s and both findings", sha, len(findings), ok, firstSHA)
	}

	_, commented, stdout := run("headsha", first)
	if commented["unchanged.go"] != 0 || commented["changed.go"] != 1 {
		t.Errorf("run 2 inline comments per file = %v, want none on unchanged.go and one on changed.go", commented)
	}
	if !strings.Contains(stdout, "suppressed as previously reported") || !strings.Contains(stdout, "- Nit A (unchanged.go)") {
		t.Errorf("run 2 does not list Nit A in the suppression note:\n%s", stdout)
	}
}

func TestRun_CaptureSkippedWithoutWiki(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 54, "sha-nowiki")
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	cl := reviewFindingClaude()
	cp := &fakeCapturer{result: onePatternProposal()}
	runner := captureRunner(t, gh, cl, cp)
	// A wiki that did not resolve (no --wiki): capture has nowhere to propose to.
	runner.ResolveWiki = func(_, _ string, _ patterns.WikiOptions, _ patterns.RemoteOptions) patterns.ResolvedWiki {
		return patterns.ResolvedWiki{}
	}

	if err := runner.Run(&bytes.Buffer{}, baseOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cp.calls.Load() != 0 {
		t.Errorf("capturer called %d times, want 0 without a resolved wiki", cp.calls.Load())
	}
}

func TestRun_CaptureSkippedByNoCapture(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 55, "sha-nocap")
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	cl := reviewFindingClaude()
	cp := &fakeCapturer{result: onePatternProposal()}
	runner := captureRunner(t, gh, cl, cp)

	opts := baseOpts()
	opts.NoCapture = true

	if err := runner.Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cp.calls.Load() != 0 {
		t.Errorf("capturer called %d times, want 0 with --no-capture", cp.calls.Load())
	}
}

func TestRun_CaptureNeverPushesForReview(t *testing.T) {
	// review analyzes an untrusted PR head and its proposal pass reads
	// attacker-controlled source, so its capture pass is propose-only: it must
	// never push the (free-form, injectable) proposal pages to the wiki. The
	// supported way to grow the wiki is to capture from a trusted source
	// (implement or audit).
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 56, "sha-write")
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	cl := reviewFindingClaude()
	cp := &fakeCapturer{result: onePatternProposal()}
	runner := captureRunner(t, gh, cl, cp)

	var out bytes.Buffer
	if err := runner.Run(&out, baseOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	// The propose-only pass still runs and surfaces its proposals...
	if cp.calls.Load() != 1 {
		t.Fatalf("capturer called %d times, want 1 — review still proposes", cp.calls.Load())
	}
	body := out.String()
	// ...but it never writes to or pushes the wiki. A push attempt would clone via
	// the default writer and surface "Wrote ..." / "Capture write-back ..." on out.
	if strings.Contains(body, "Wrote ") || strings.Contains(body, "Capture write-back") {
		t.Errorf("review must never push to the wiki, got:\n%s", body)
	}
}

func TestRun_LocalUsesCwdAndKeepsTree(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	dir := t.TempDir()
	pr := &github.PR{
		Owner: "acme", Repo: "widgets", Number: 42, Title: "PR", Body: "b",
		BaseBranch: "main", HeadBranch: "feature", HeadSHA: "sha-local", Dir: dir, Local: true,
	}
	var localCalls int32
	claudeMock := &configurableClaude{
		review: func(string, claude.ReviewContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{Summary: "Local review summary"}, nil
		},
	}
	gh := &githubtest.Fake{
		OpenLocalPRFn: func(ref string, _ github.LocalOptions) (*github.PR, error) {
			atomic.AddInt32(&localCalls, 1)
			return pr, nil
		},
		// fetchAndCheckout is intentionally left nil so a non-local clone panics.
	}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	opts := baseOpts()
	opts.Local = true
	opts.NoCache = true

	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if localCalls != 1 {
		t.Errorf("OpenLocalPR calls = %d, want 1", localCalls)
	}
	if !strings.Contains(out.String(), "Local review summary") {
		t.Errorf("output missing summary, got:\n%s", out.String())
	}
	// Cleanup is a no-op for a Local PR — the working tree must survive.
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("local checkout must survive the run: %v", err)
	}
}

// TestRun_CoverageMapRenderedOnCacheHit pins the coverage map to the entry that
// was asked for it. --coverage-map is part of the cache key, so a re-run with
// the flag hits the entry the flagged run wrote — but the map itself was never
// stored, so the section silently vanished on every run after the first.
func TestRun_CoverageMapRenderedOnCacheHit(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	coverage := func(dir, baseBranch string) (*report.CoverageResult, error) {
		return &report.CoverageResult{Entries: []report.CoverageEntry{
			{File: "main.go", Function: "Run", Rating: "★★★"},
		}}, nil
	}
	claudeMock := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{Summary: "Primary"}, nil
		},
		coverage: coverage,
	}
	gh := &githubtest.Fake{
		FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return fakePR(t, "acme", "widgets", 14, "sha-coverage-hit"), nil },
	}
	opts := baseOpts()
	opts.CoverageMap = true

	var first bytes.Buffer
	if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&first, opts); err != nil {
		t.Fatalf("first Run returned error: %v", err)
	}
	if !strings.Contains(first.String(), "## Test Coverage Map") || !strings.Contains(first.String(), "main.go") {
		t.Fatalf("first run did not render the coverage map:\n%s", first.String())
	}

	second := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			t.Error("Review must not be called on a cache hit")
			return nil, nil
		},
		coverage: func(dir, baseBranch string) (*report.CoverageResult, error) {
			t.Error("CoverageMap must not be called on a cache hit")
			return nil, nil
		},
	}
	var out bytes.Buffer
	if err := (&Runner{Claude: second, GitHub: gh}).Run(&out, opts); err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}
	if !strings.Contains(out.String(), "## Test Coverage Map") || !strings.Contains(out.String(), "main.go") {
		t.Errorf("the cached run dropped the coverage map it was asked for:\n%s", out.String())
	}
}

// TestRun_CachedReviewRejectsUnusablePayloads covers the two entries a hit must
// refuse: one written before the coverage map was stored beside the result,
// which decodes cleanly into an envelope with no result in it, and one that does
// not decode at all. Both must re-run the review rather than render nothing, and
// the entry they leave behind must be readable by the next run.
func TestRun_CachedReviewRejectsUnusablePayloads(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload []byte
	}{
		{"an entry predating the stored coverage map", []byte(`{"findings":[],"summary":"STALE SHAPE","recommendation":"r"}`)},

		// PutRaw stores the payload as a JSON value, so a malformed one cannot be
		// written; what can still arrive is a well-formed value of the wrong
		// shape, from a corrupted file or a future payload format.
		{"an entry of the wrong shape", []byte(`[1,2,3]`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Not t.Parallel(): cache.SetDir mutates a package-level variable.
			restore := cache.SetDir(t.TempDir())
			t.Cleanup(restore)

			pr := fakePR(t, "acme", "widgets", 21, "sha-unusable")
			key := cache.Key(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, baseOptsPatternFlag())
			if err := cache.PutRaw(key, cache.CommandReview, tc.payload); err != nil {
				t.Fatalf("seeding cache: %v", err)
			}

			claudeMock := &configurableClaude{
				review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
					return &report.ReviewResult{Summary: "Fresh summary"}, nil
				},
			}
			gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}

			var out bytes.Buffer
			if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&out, baseOpts()); err != nil {
				t.Fatalf("Run returned error: %v", err)
			}
			if claudeMock.reviewCalls != 1 {
				t.Errorf("Review calls = %d, want 1 — an unusable entry is a miss", claudeMock.reviewCalls)
			}
			if strings.Contains(out.String(), "STALE SHAPE") {
				t.Errorf("rendered the unusable entry:\n%s", out.String())
			}
			if !strings.Contains(out.String(), "Fresh summary") {
				t.Errorf("missing the fresh review:\n%s", out.String())
			}
			// The run replaced it with an entry the next run can read.
			if cached, ok := readCachedResult(t, key); !ok || cached.Summary != "Fresh summary" {
				t.Errorf("the unusable entry was not replaced, got ok=%v %+v", ok, cached)
			}
		})
	}
}

// TestRun_FailedPassIsNotCached proves a run in which a requested pass failed
// is rendered but not cached. The key names the passes the run asked for, so a
// cached entry would serve the incomplete review to every later run of the same
// commit and flags; the next run must retry every pass and cache only once they
// succeed.
func TestRun_FailedPassIsNotCached(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	security := claude.Specialist{Key: "security"}
	failing := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{Summary: "Primary"}, nil
		},
		adversarial: func(dir, baseBranch string) (*report.ReviewResult, error) {
			return nil, errors.New("adversarial crashed")
		},
		coverage: func(dir, baseBranch string) (*report.CoverageResult, error) {
			return nil, errors.New("coverage failed")
		},
		specialist: func(dir, baseBranch, key, focus string) (*report.ReviewResult, error) {
			if key == security.Key {
				return nil, errors.New("security timed out")
			}
			return &report.ReviewResult{}, nil
		},
	}
	pr := fakePR(t, "acme", "widgets", 15, "sha-pass-failed")
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	opts := baseOpts()
	opts.Thorough = true
	opts.Specialists = true
	opts.CoverageMap = true
	key := cache.Key(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, "thorough", "specialists", "coverage-map", baseOptsPatternFlag())

	var first bytes.Buffer
	if err := (&Runner{Claude: failing, GitHub: gh}).Run(&first, opts); err != nil {
		t.Fatalf("first Run returned error: %v", err)
	}
	body := first.String()
	if !strings.Contains(body, "**Incomplete review** — 3 requested pass(es) failed") {
		t.Errorf("first run must render the alert for three failed passes:\n%s", body)
	}
	for _, want := range []string{
		"> - `" + claude.PassLabelAdversarial + "`: adversarial crashed\n",
		"> - `" + claude.PassLabelCoverage + "`: coverage failed\n",
		"> - `" + security.PassLabel() + "`: security timed out\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("alert missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "## Test Coverage Map") {
		t.Errorf("a failed coverage pass must render no map:\n%s", body)
	}
	if _, ok := readCachedResult(t, key); ok {
		t.Fatal("a run with a failed pass was cached")
	}

	succeeding := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{Summary: "Primary"}, nil
		},
		coverage: func(dir, baseBranch string) (*report.CoverageResult, error) {
			return &report.CoverageResult{Entries: []report.CoverageEntry{{File: "main.go", Function: "Run", Rating: "★★★"}}}, nil
		},
	}
	var second bytes.Buffer
	if err := (&Runner{Claude: succeeding, GitHub: gh}).Run(&second, opts); err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}
	if succeeding.reviewCalls != 1 || succeeding.coverageCalls != 1 {
		t.Errorf("second run called Review %d and CoverageMap %d times, want 1 each: nothing was cached to serve it", succeeding.reviewCalls, succeeding.coverageCalls)
	}
	if strings.Contains(second.String(), "**Incomplete review**") {
		t.Errorf("the retried run must render no alert:\n%s", second.String())
	}
	cached, ok := readCachedResult(t, key)
	if !ok {
		t.Fatal("the run whose passes all succeeded was not cached")
	}
	if failed := cached.FailedPasses(); len(failed) != 0 {
		t.Errorf("cached result records failed passes %+v", failed)
	}
}

// TestRun_FailedComplianceIsRecordedAndNotCached proves the feature-compliance
// check, which no flag requests but a detected feature file does, is recorded
// like the flagged passes: its failure is named in the alert with the first
// line of its error and on the Passes line, and the run is not cached.
func TestRun_FailedComplianceIsRecordedAndNotCached(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 17, "sha-compliance-failed")
	// The branch names the feature, so DetectFeature picks the file.
	pr.HeadBranch = "feature/CC-0042-thing"
	featureDir := filepath.Join(pr.Dir, ".planwerk", "features")
	if err := os.MkdirAll(featureDir, 0o750); err != nil {
		t.Fatalf("creating %s: %v", featureDir, err)
	}
	if err := os.WriteFile(filepath.Join(featureDir, "cc.json"), []byte(`{"feature_id": "CC-0042", "title": "Thing"}`), 0o600); err != nil {
		t.Fatalf("writing the feature file: %v", err)
	}
	claudeMock := &configurableClaude{
		featureCompliance: func(dir, baseBranch string, feature *planwerk.Feature) (*report.ReviewResult, error) {
			return nil, errors.New("compliance timed out\nstderr: the session's log")
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}

	var out bytes.Buffer
	if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&out, baseOpts()); err != nil {
		t.Fatalf("Run must not fail when the compliance check errors: %v", err)
	}
	if claudeMock.featureComplianceCalls != 1 {
		t.Fatalf("FeatureCompliance calls = %d, want 1: the feature file was not detected", claudeMock.featureComplianceCalls)
	}
	body := out.String()
	if !strings.Contains(body, "> - `"+claude.PassLabelCompliance+"`: compliance timed out\n") {
		t.Errorf("expected the alert naming the failed compliance check with its first error line, got:\n%s", body)
	}
	if strings.Contains(body, "stderr:") {
		t.Errorf("the alert must keep only the error's first line, got:\n%s", body)
	}
	if !strings.Contains(body, "**Passes**: failed "+claude.PassLabelCompliance+"\n") {
		t.Errorf("expected the Passes line to list the compliance check as failed, got:\n%s", body)
	}
	if _, ok := readCachedResult(t, cache.Key(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, baseOptsPatternFlag())); ok {
		t.Error("a run whose compliance check failed was cached")
	}
}

// formatJSON is the --format value that renders the review as JSON.
const formatJSON = "json"

// TestRun_PassStatusInJSONAndCache proves the pass records reach --format json
// in launch order, the adversarial pass first and then the specialists in
// registry order with the gated-out ones included, ride the cached result, and
// render the Passes line on a cache hit.
func TestRun_PassStatusInJSONAndCache(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 16, "sha-pass-json")
	// A markdown-only PR: only the NeverGate specialists run.
	pr.ChangedFiles = []string{"README.md"}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	opts := baseOpts()
	opts.Format = formatJSON
	opts.Thorough = true
	opts.Specialists = true

	var out bytes.Buffer
	if err := (&Runner{Claude: &configurableClaude{}, GitHub: gh}).Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	var got report.ReviewResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decoding the JSON output: %v\n%s", err, out.String())
	}
	want := []report.PassStatus{{Pass: claude.PassLabelAdversarial, Status: report.PassStatusRan}}
	for _, sp := range claude.Specialists {
		status := report.PassStatusGatedOut
		if sp.NeverGate {
			status = report.PassStatusRan
		}
		want = append(want, report.PassStatus{Pass: sp.PassLabel(), Status: status})
	}
	if !slices.Equal(got.Passes, want) {
		t.Errorf("JSON Passes = %+v, want %+v", got.Passes, want)
	}

	cached, ok := readCachedResult(t, cache.Key(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, "thorough", "specialists", baseOptsPatternFlag()))
	if !ok {
		t.Fatal("the run was not cached")
	}
	if !slices.Equal(cached.Passes, want) {
		t.Errorf("cached Passes = %+v, want %+v", cached.Passes, want)
	}

	opts.Format = "markdown"
	hitClaude := &configurableClaude{}
	var hit bytes.Buffer
	if err := (&Runner{Claude: hitClaude, GitHub: gh}).Run(&hit, opts); err != nil {
		t.Fatalf("cache-hit Run returned error: %v", err)
	}
	if hitClaude.reviewCalls != 0 {
		t.Errorf("Review calls = %d on what should be a cache hit, want 0", hitClaude.reviewCalls)
	}
	if !strings.Contains(hit.String(), "**Passes**: ran "+claude.PassLabelAdversarial+", ") {
		t.Errorf("the cache hit should render the Passes line:\n%s", hit.String())
	}
}

// brainSurface returns an enabled search surface whose mirror stands at
// revision.
func brainSurface(revision string) search.Surface {
	return search.Surface{
		Command:  "/usr/local/bin/planwerk-agent brain search acme/widgets",
		SyncedAt: "2026-10-02T09:00:00Z",
		Revision: revision,
	}
}

// TestRun_BrainSurfaceReachesTheReviewAndTheCacheKey runs the review with and
// without the brain. The surface is resolved only for a run that opted in and
// reaches the review session. The mirror's revision is part of the cache key,
// and a run with the brain off, or without a finished mirror, keeps the key a
// review has without the brain.
func TestRun_BrainSurfaceReachesTheReviewAndTheCacheKey(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	const headSHA = "sha-brain"
	var sessions []search.Surface // the surface of every review session, in order
	claudeMock := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			sessions = append(sessions, ctx.Brain)
			return &report.ReviewResult{Summary: "ok"}, nil
		},
	}
	gh := &githubtest.Fake{
		FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return fakePR(t, "acme", "widgets", 91, headSHA), nil },
	}
	var surface search.Surface
	resolved := 0
	runner := &Runner{Claude: claudeMock, GitHub: gh, ResolveBrain: func(owner, name string) search.Surface {
		resolved++
		if owner != "acme" || name != "widgets" {
			t.Errorf("ResolveBrain(%q, %q), want acme and widgets", owner, name)
		}
		return surface
	}}
	plainKeyHolds := func() bool {
		_, ok := readCachedResult(t, cache.Key("acme", "widgets", 91, headSHA, baseOptsPatternFlag()))
		return ok
	}

	off := baseOpts()
	on := baseOpts()
	on.Brain = true
	onNoCache := on
	onNoCache.NoCache = true
	rev1, rev2 := brainSurface("rev-1"), brainSurface("rev-2")
	steps := []struct {
		why          string
		opts         Options
		surface      search.Surface // what the seam answers
		wantSessions int
		wantResolved int
		wantPlainKey bool // the key of a run without the brain holds a result
	}{
		{"the first run with the brain starts a session", on, rev1, 1, 1, false},
		{"the same revision is served from the cache", on, rev1, 1, 2, false},
		{"another revision starts a session again", on, rev2, 2, 3, false},
		{"a run with the brain off starts a session and resolves nothing", off, rev2, 3, 3, true},
		{"a run with the brain off is served from its own entry", off, rev2, 3, 3, true},
		{"a run without a finished mirror has the key of a run with the brain off", on, search.Surface{}, 3, 4, true},
		{"without the cache, that run hands its session no search", onNoCache, search.Surface{}, 4, 5, true},
	}
	for _, step := range steps {
		surface = step.surface
		if err := runner.Run(&bytes.Buffer{}, step.opts); err != nil {
			t.Fatalf("%s: Run returned error: %v", step.why, err)
		}
		if len(sessions) != step.wantSessions || resolved != step.wantResolved {
			t.Fatalf("%s: %d sessions and %d resolves, want %d and %d", step.why, len(sessions), resolved, step.wantSessions, step.wantResolved)
		}
		if got := plainKeyHolds(); got != step.wantPlainKey {
			t.Fatalf("%s: the key of a run without the brain holds a result = %v, want %v", step.why, got, step.wantPlainKey)
		}
	}
	if want := []search.Surface{rev1, rev2, {}, {}}; !slices.Equal(sessions, want) {
		t.Errorf("the sessions were handed %+v, want %+v", sessions, want)
	}
}

// TestRun_ListsExcludedFiles locks what a review whose diff left files out
// reports: the Markdown carries the note naming them, --format json carries
// excluded_files and no note, --inline posts the note and the list in the
// living summary comment, --format sarif names them in a note-level
// notification and carries no Markdown note, and a cache hit renders the note
// again (decision 137).
func TestRun_ListsExcludedFiles(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	excluded := []report.ExcludedFile{
		{Path: "go.sum", Reason: report.ExcludedLockfile},
		{Path: "vendor/", Reason: report.ExcludedVendored, Files: 3},
	}
	claudeMock := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{Summary: "ok", ExcludedFiles: excluded}, nil
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) {
		return fakePR(t, "acme", "widgets", 23, "sha-excluded"), nil
	}}
	const note = "> [!NOTE]\n> 4 changed file(s) were left out of the diff the review read (--diff-exclusions)"

	var md bytes.Buffer
	if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&md, baseOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(md.String(), note) || !strings.Contains(md.String(), "> - `vendor/` (vendored, 3 files)\n") {
		t.Errorf("the Markdown report must list the excluded files:\n%s", md.String())
	}

	jsonOpts := baseOpts()
	jsonOpts.Format = formatJSON
	jsonOpts.NoCache = true
	var js bytes.Buffer
	if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&js, jsonOpts); err != nil {
		t.Fatalf("JSON Run returned error: %v", err)
	}
	var got report.ReviewResult
	if err := json.Unmarshal(js.Bytes(), &got); err != nil {
		t.Fatalf("decoding the JSON output: %v\n%s", err, js.String())
	}
	if !slices.Equal(got.ExcludedFiles, excluded) {
		t.Errorf("JSON ExcludedFiles = %+v, want %+v", got.ExcludedFiles, excluded)
	}
	if strings.Contains(js.String(), "[!NOTE]") {
		t.Errorf("--format json must carry no Markdown note:\n%s", js.String())
	}

	var summary string
	inlineGH := &githubtest.Fake{
		FetchAndCheckoutFn: gh.FetchAndCheckoutFn,
		PostPRCommentFn: func(owner, repo string, number int, body string) (string, error) {
			summary = body
			return "https://github.com/acme/widgets/pull/23#issuecomment-1", nil
		},
	}
	inlineOpts := baseOpts()
	inlineOpts.NoCache = true
	inlineOpts.PostReview = true
	inlineOpts.InlineReview = true
	if err := (&Runner{Claude: claudeMock, GitHub: inlineGH}).Run(&bytes.Buffer{}, inlineOpts); err != nil {
		t.Fatalf("inline Run returned error: %v", err)
	}
	if !strings.Contains(summary, note) || !strings.Contains(summary, `"excluded_files":[{"path":"go.sum","reason":"lockfile"}`) {
		t.Errorf("the living summary comment must carry the note and the data block's excluded_files:\n%s", summary)
	}

	sarifOpts := baseOpts()
	sarifOpts.Format = formatSARIF
	sarifOpts.NoCache = true
	var sarif bytes.Buffer
	if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&sarif, sarifOpts); err != nil {
		t.Fatalf("SARIF Run returned error: %v", err)
	}
	if strings.Contains(sarif.String(), "[!NOTE]") ||
		!strings.Contains(sarif.String(), "4 changed file(s) were left out of the diff the review read (--diff-exclusions), so no result covers them: go.sum (lock file); vendor/ (vendored, 3 files)") {
		t.Errorf("--format sarif must name the excluded files in a notification and carry no Markdown note:\n%s", sarif.String())
	}

	hitClaude := &configurableClaude{}
	var hit bytes.Buffer
	if err := (&Runner{Claude: hitClaude, GitHub: gh}).Run(&hit, baseOpts()); err != nil {
		t.Fatalf("cache-hit Run returned error: %v", err)
	}
	if hitClaude.reviewCalls != 0 {
		t.Fatalf("Review calls = %d on what should be a cache hit, want 0", hitClaude.reviewCalls)
	}
	if !strings.Contains(hit.String(), note) {
		t.Errorf("the cache hit must render the note again:\n%s", hit.String())
	}
}

// TestRun_ReviewReadsTheRunsDiff locks the review session's diff: Run writes
// it from the checkout before the review starts, with the exclusions
// Options.DiffExclusions asks for, hands it over in ReviewContext.Diff, and
// removes it when Run returns (decision 137).
func TestRun_ReviewReadsTheRunsDiff(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	// checkout returns a pull request clone whose HEAD changes app.go and
	// go.sum on top of origin/main. Run removes it with the PR, so every run
	// gets its own.
	checkout := func() string {
		t.Helper()
		dir := t.TempDir()
		git := func(args ...string) string {
			t.Helper()
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
			return strings.TrimSpace(string(out))
		}
		write := func(name, content string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
		git("init", "-q")
		git("config", "user.email", "t@example.com")
		git("config", "user.name", "tester")
		write("app.go", "package app\n")
		write("go.sum", "a v1 h1:x\n")
		git("add", "-A")
		git("commit", "-q", "-m", "base")
		git("update-ref", "refs/remotes/origin/main", git("rev-parse", "HEAD"))
		write("app.go", "package app\n\nfunc New() {}\n")
		write("go.sum", "a v2 h1:y\n")
		git("commit", "-q", "-am", "head")
		return dir
	}

	for _, tc := range []struct {
		exclude      bool
		wantExcluded []report.ExcludedFile
	}{
		{false, nil},
		{true, []report.ExcludedFile{{Path: "go.sum", Reason: report.ExcludedLockfile}}},
	} {
		var got claude.ReviewContext
		claudeMock := &configurableClaude{
			review: func(_ string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
				got = ctx
				data, err := os.ReadFile(ctx.Diff.File)
				if err != nil {
					t.Errorf("the diff must be readable while the review runs: %v", err)
				}
				if !strings.Contains(string(data), "+func New() {}\n") || strings.Contains(string(data), "go.sum") == tc.exclude {
					t.Errorf("exclude=%v: the diff file holds:\n%s", tc.exclude, data)
				}
				return &report.ReviewResult{Summary: "ok", ExcludedFiles: ctx.Diff.Excluded}, nil
			},
		}
		pr := fakePR(t, "acme", "widgets", 24, "sha-diff")
		pr.Dir = checkout()
		gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
		opts := baseOpts()
		opts.NoCache = true
		opts.DiffExclusions = tc.exclude

		if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&bytes.Buffer{}, opts); err != nil {
			t.Fatalf("exclude=%v: Run returned error: %v", tc.exclude, err)
		}
		if !strings.HasPrefix(filepath.Base(got.Diff.Dir), diffscope.DirPrefix) || got.Diff.Range != "origin/main...HEAD" {
			t.Fatalf("exclude=%v: ReviewContext.Diff = %+v, want a written origin/main...HEAD diff", tc.exclude, got.Diff)
		}
		if !slices.Equal(got.Diff.Excluded, tc.wantExcluded) {
			t.Errorf("exclude=%v: Diff.Excluded = %+v, want %+v", tc.exclude, got.Diff.Excluded, tc.wantExcluded)
		}
		if _, err := os.Stat(got.Diff.Dir); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("exclude=%v: after Run, stat %s = %v, want not exist", tc.exclude, got.Diff.Dir, err)
		}
	}
}

// TestRun_FileLedgerReachesPromptAndRecord locks the changed-file check end to
// end: the review is handed the PR's changed files minus the planning
// artifacts, its account is recorded in gates.files before the cache write,
// and the Markdown report, on the run and on a cache hit, names the file it
// skipped (decision 138).
func TestRun_FileLedgerReachesPromptAndRecord(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 25, "sha-ledger")
	pr.FileChanges = []github.FileChange{
		{Path: "main.go", Status: github.FileModified, Added: 4, Deleted: 1},
		{Path: ".planwerk/plan.md", Status: github.FileAdded, Added: 30},
		{Path: "main_test.go", Status: github.FileAdded, Added: 20},
		{Path: "go.sum", Status: github.FileModified, Added: 2, Deleted: 2},
	}
	var listed []string
	claudeMock := &configurableClaude{
		review: func(_ string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			listed = ledgerPaths(ctx.ChangedFiles)
			return &report.ReviewResult{Summary: "ok", FilesReviewed: []report.FileReview{
				{File: "main.go", Status: report.FileReviewed},
				{File: "main_test.go", Status: report.FileReviewed},
				{File: "go.sum", Status: report.FileSkipped, Reason: "generated checksum file"},
			}}, nil
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}

	var out bytes.Buffer
	if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&out, baseOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if want := []string{"main.go", "main_test.go", "go.sum"}; !slices.Equal(listed, want) {
		t.Errorf("ReviewContext.ChangedFiles lists %q, want %q", listed, want)
	}
	cached, ok := readCachedResult(t, cache.Key(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, baseOptsPatternFlag()))
	if !ok || cached.Gates == nil || cached.Gates.Files == nil {
		t.Fatalf("expected a cached result carrying gates.files, got ok=%v %+v", ok, cached)
	}
	want := report.FileGateStats{Changed: 3, Reviewed: 2, Missing: []report.MissingFile{
		{File: "go.sum", Status: report.FileSkipped, Reason: "generated checksum file"},
	}}
	if !reflect.DeepEqual(*cached.Gates.Files, want) {
		t.Errorf("cached gates.files = %+v, want %+v", *cached.Gates.Files, want)
	}
	const section = "## Files Not Reviewed (1 of 3)\n"
	const line = "- `go.sum`: skipped (generated checksum file)\n"
	if !strings.Contains(out.String(), section) || !strings.Contains(out.String(), line) {
		t.Errorf("the Markdown report must name the skipped file:\n%s", out.String())
	}

	hitClaude := &configurableClaude{}
	var hit bytes.Buffer
	if err := (&Runner{Claude: hitClaude, GitHub: gh}).Run(&hit, baseOpts()); err != nil {
		t.Fatalf("cache-hit Run returned error: %v", err)
	}
	if hitClaude.reviewCalls != 0 {
		t.Fatalf("Review calls = %d on what should be a cache hit, want 0", hitClaude.reviewCalls)
	}
	if !strings.Contains(hit.String(), section) {
		t.Errorf("the cache hit must render the section again:\n%s", hit.String())
	}
}

// TestRun_FileLedgerLeavesOutExcludedFiles locks that the list and the run's
// diff agree: a file --diff-exclusions left out of the diff is not listed
// either, so the review is never asked to account for a file it was not shown
// (decision 138).
func TestRun_FileLedgerLeavesOutExcludedFiles(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	git("init", "-q")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "tester")
	write("app.go", "package app\n")
	write("go.sum", "a v1 h1:x\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	git("update-ref", "refs/remotes/origin/main", git("rev-parse", "HEAD"))
	write("app.go", "package app\n\nfunc New() {}\n")
	write("go.sum", "a v2 h1:y\n")
	git("commit", "-q", "-am", "head")

	fileChanges, err := (github.Client{}).DiffFileChanges(dir, "main")
	if err != nil {
		t.Fatalf("DiffFileChanges: %v", err)
	}
	for _, exclude := range []bool{false, true} {
		var listed []string
		claudeMock := &configurableClaude{
			review: func(_ string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
				listed = ledgerPaths(ctx.ChangedFiles)
				return &report.ReviewResult{Summary: "ok"}, nil
			},
		}
		pr := fakePR(t, "acme", "widgets", 26, "sha-ledger-excluded")
		pr.Dir, pr.Local, pr.FileChanges = dir, true, fileChanges
		gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
		opts := baseOpts()
		opts.NoCache = true
		opts.DiffExclusions = exclude

		if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&bytes.Buffer{}, opts); err != nil {
			t.Fatalf("exclude=%v: Run returned error: %v", exclude, err)
		}
		want := []string{"app.go", "go.sum"}
		if exclude {
			want = []string{"app.go"}
		}
		if !slices.Equal(listed, want) {
			t.Errorf("exclude=%v: ReviewContext.ChangedFiles lists %q, want %q", exclude, listed, want)
		}
	}
}

// TestRun_FileLedgerOverCapRecordsNothing locks the cap: a pull request with
// more than maxLedgerFiles listed files gets no list and no record, whatever
// the session answers (decision 138).
func TestRun_FileLedgerOverCapRecordsNothing(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 27, "sha-ledger-cap")
	for i := range maxLedgerFiles + 1 {
		pr.FileChanges = append(pr.FileChanges, github.FileChange{Path: fmt.Sprintf("gen/f%03d.go", i), Status: github.FileAdded, Added: 1})
	}
	var handed []github.FileChange
	claudeMock := &configurableClaude{
		review: func(_ string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			handed = ctx.ChangedFiles
			return &report.ReviewResult{Summary: "ok", FilesReviewed: []report.FileReview{{File: "gen/f000.go", Status: report.FileReviewed}}}, nil
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}

	var out bytes.Buffer
	if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&out, baseOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(handed) != 0 {
		t.Errorf("ReviewContext.ChangedFiles holds %d files over the cap, want none", len(handed))
	}
	cached, ok := readCachedResult(t, cache.Key(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, baseOptsPatternFlag()))
	if !ok {
		t.Fatal("expected a cached result")
	}
	if cached.Gates != nil && cached.Gates.Files != nil {
		t.Errorf("gates.files = %+v over the cap, want none", cached.Gates.Files)
	}
	if strings.Contains(out.String(), "Files Not Reviewed") {
		t.Errorf("no list, so no section may render:\n%s", out.String())
	}
}

// TestRun_NoFileChangesRecordsNothing locks the run whose changed files could
// not be listed: no Changed Files section, no record (decision 138).
func TestRun_NoFileChangesRecordsNothing(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 28, "sha-ledger-none")
	var handed []github.FileChange
	claudeMock := &configurableClaude{
		review: func(_ string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			handed = ctx.ChangedFiles
			return &report.ReviewResult{Summary: "ok"}, nil
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}

	if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&bytes.Buffer{}, baseOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if handed != nil {
		t.Errorf("ReviewContext.ChangedFiles = %+v, want nil", handed)
	}
	cached, ok := readCachedResult(t, cache.Key(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, baseOptsPatternFlag()))
	if !ok {
		t.Fatal("expected a cached result")
	}
	if cached.Gates != nil && cached.Gates.Files != nil {
		t.Errorf("gates.files = %+v without a list, want none", cached.Gates.Files)
	}
}

package review

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/planwerk/planwerk-review/internal/cache"
	"github.com/planwerk/planwerk-review/internal/claude"
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/planwerk"
	"github.com/planwerk/planwerk-review/internal/report"
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
	gh := &mockGitHub{fetchAndCheckout: func(ref string) (*github.PR, error) { return pr, nil }}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	var out bytes.Buffer
	if err := runner.Run(&out, baseOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(logBuf.String(), "fetching commit log failed") {
		t.Errorf("expected a warning when the commit log fetch fails, got:\n%s", logBuf.String())
	}
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
	usage             report.Usage

	reviewCalls            int32
	adversarialCalls       int32
	coverageCalls          int32
	featureComplianceCalls int32
	specialistCalls        int32
}

func (c *configurableClaude) Review(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
	atomic.AddInt32(&c.reviewCalls, 1)
	if c.review == nil {
		return &report.ReviewResult{Summary: "ok"}, nil
	}
	return c.review(dir, ctx)
}

func (c *configurableClaude) AdversarialReview(dir, baseBranch string) (*report.ReviewResult, error) {
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

func (c *configurableClaude) SpecialistReview(dir, baseBranch, key, focus string) (*report.ReviewResult, error) {
	atomic.AddInt32(&c.specialistCalls, 1)
	if c.specialist == nil {
		return &report.ReviewResult{}, nil
	}
	return c.specialist(dir, baseBranch, key, focus)
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
	gh := &mockGitHub{
		fetchAndCheckout: func(ref string) (*github.PR, error) { return pr, nil },
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
	gh2 := &mockGitHub{
		fetchAndCheckout: func(ref string) (*github.PR, error) {
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
	if err := cache.Put(cache.Key(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA), cache.CommandReview, cachedResult); err != nil {
		t.Fatalf("seeding cache: %v", err)
	}

	freshResult := &report.ReviewResult{Summary: "Fresh summary"}
	claudeMock := &configurableClaude{
		review: func(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			return freshResult, nil
		},
	}
	gh := &mockGitHub{
		fetchAndCheckout: func(ref string) (*github.PR, error) { return pr, nil },
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
	gh := &mockGitHub{fetchAndCheckout: func(ref string) (*github.PR, error) { return pr, nil }}
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
	if !strings.Contains(body, "adversarial review pass") {
		t.Error("merged summary should mention adversarial pass")
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
	gh := &mockGitHub{fetchAndCheckout: func(ref string) (*github.PR, error) { return pr, nil }}
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
	if !strings.Contains(body, "specialist pass") {
		t.Error("merged summary should mention specialist passes")
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
	gh := &mockGitHub{fetchAndCheckout: func(ref string) (*github.PR, error) { return pr, nil }}
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
	gh := &mockGitHub{fetchAndCheckout: func(ref string) (*github.PR, error) { return pr, nil }}
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
	gh := &mockGitHub{
		fetchAndCheckout:   func(ref string) (*github.PR, error) { return pr, nil },
		fetchReviewComment: func(owner, repo string, number int) (string, bool, error) { return priorComment, true, nil },
		postPRComment: func(owner, repo string, number int, body string) (string, error) {
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
	if !strings.Contains(postedBody, "planwerk-review-data") {
		t.Error("posted comment must include the machine-readable data block")
	}
	// The data block also carries the per-Run Claude usage totals for CI
	// extraction (issue #46, AC#3).
	if !strings.Contains(postedBody, `"usage":{"calls":3,"input_tokens":13400`) {
		t.Errorf("posted data block must embed the Claude usage totals; got %q", postedBody)
	}
}

func TestRun_AdversarialErrorDoesNotFailRun(t *testing.T) {
	// Secondary passes (adversarial, coverage, compliance) log warnings on
	// failure but must not abort the review — the primary result is still
	// load-bearing and should be rendered.
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
	gh := &mockGitHub{fetchAndCheckout: func(ref string) (*github.PR, error) { return pr, nil }}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	opts := baseOpts()
	opts.Thorough = true

	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("Run must not fail when adversarial pass errors: %v", err)
	}
	if !strings.Contains(out.String(), "Primary only") {
		t.Errorf("expected primary summary when adversarial fails, got:\n%s", out.String())
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
	gh := &mockGitHub{fetchAndCheckout: func(ref string) (*github.PR, error) { return pr, nil }}
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
	gh := &mockGitHub{
		fetchAndCheckout: func(ref string) (*github.PR, error) { return nil, errors.New("gh not logged in") },
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
	gh := &mockGitHub{fetchAndCheckout: func(ref string) (*github.PR, error) { return pr, nil }}
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
	gh := &mockGitHub{
		fetchAndCheckoutLocal: func(ref string, _ github.LocalOptions) (*github.PR, error) {
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
		t.Errorf("FetchAndCheckoutLocal calls = %d, want 1", localCalls)
	}
	if !strings.Contains(out.String(), "Local review summary") {
		t.Errorf("output missing summary, got:\n%s", out.String())
	}
	// Cleanup is a no-op for a Local PR — the working tree must survive.
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("local checkout must survive the run: %v", err)
	}
}

package review

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/cache"
	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/diffscope"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/github/githubtest"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// previewHeadSHA is a full-length head commit, so the text form's short SHA
// is a real abbreviation.
const previewHeadSHA = "0123456789abcdef0123456789abcdef01234567"

// claudeCalls sums every configurableClaude call counter.
func claudeCalls(c *configurableClaude) int32 {
	return c.reviewCalls + c.adversarialCalls + c.coverageCalls + c.featureComplianceCalls +
		c.specialistCalls + c.dedupCalls + c.verifyClaimsCalls
}

// cacheEntries returns how many entries the test's cache directory holds.
func cacheEntries(t *testing.T) int {
	t.Helper()
	stats, err := cache.Stats()
	if err != nil {
		t.Fatalf("cache stats: %v", err)
	}
	return len(stats.Entries)
}

// diffDirsIn returns the entries of dir whose name starts with
// diffscope.DirPrefix.
func diffDirsIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var found []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), diffscope.DirPrefix) {
			found = append(found, e.Name())
		}
	}
	return found
}

// runPreview runs a JSON dry run of the pull request gh hands out and
// decodes its report.
func runPreview(t *testing.T, runner *Runner, opts Options) Preview {
	t.Helper()
	opts.DryRun = true
	opts.Format = formatJSON
	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("dry run returned error: %v", err)
	}
	var p Preview
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		t.Fatalf("decoding the dry-run report: %v\n%s", err, out.String())
	}
	return p
}

// previewRow returns the first row of p for pass.
func previewRow(t *testing.T, p Preview, pass string) PreviewPass {
	t.Helper()
	for _, row := range p.Passes {
		if row.Pass == pass {
			return row
		}
	}
	t.Fatalf("the dry run has no %s row: %+v", pass, p.Passes)
	return PreviewPass{}
}

// TestRun_DryRunStartsNoSessionWritesNoCachePostsNothing locks the dry run's
// guarantees with every pass and the posting on: no Claude call, no cache
// entry, no GitHub post, and a report naming the key and the head commit
// (decision 143).
func TestRun_DryRunStartsNoSessionWritesNoCachePostsNothing(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 143, previewHeadSHA)
	pr.ChangedFiles = []string{"internal/api/users.go"}
	claudeMock := &configurableClaude{}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	opts := baseOpts()
	opts.DryRun = true
	opts.Thorough = true
	opts.Specialists = true
	opts.CoverageMap = true
	opts.PostReview = true

	var out bytes.Buffer
	if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if n := claudeCalls(claudeMock); n != 0 {
		t.Errorf("the dry run made %d Claude calls, want 0", n)
	}
	key := cache.Key(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, "thorough", "specialists", "coverage-map", baseOptsPatternFlag())
	if _, ok := readCachedResult(t, key); ok {
		t.Error("the dry run cached a result")
	}
	if n := cacheEntries(t); n != 0 {
		t.Errorf("the cache holds %d entries after a dry run, want 0", n)
	}
	for _, method := range []string{"PostPRComment", "SubmitPRReview", "FetchReviewComment", "FetchDiff"} {
		if n := gh.Count(method); n != 0 {
			t.Errorf("the dry run called %s %d times, want 0", method, n)
		}
	}
	text := out.String()
	if !strings.HasPrefix(text, "[dry-run] Review of acme/widgets#143: no Claude session, no cache write, nothing posted.\n") {
		t.Errorf("the report does not open with the dry-run header:\n%s", text)
	}
	for _, want := range []string{"miss, key " + key + "\n", "0123456 (feature-branch into main)", "Post:         comment\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("the report lacks %q:\n%s", want, text)
		}
	}
}

// TestRun_DryRunReportsACacheHitWithoutServingIt: a dry run of a commit the
// cache holds says a review would be served, and still reports the passes
// instead of the cached review (decision 143).
func TestRun_DryRunReportsACacheHitWithoutServingIt(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 144, "sha-preview-hit")
	key := cache.Key(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, baseOptsPatternFlag())
	if err := writeCachedReview(key, &report.ReviewResult{Summary: "CACHED SUMMARY"}, nil); err != nil {
		t.Fatalf("seeding the cache: %v", err)
	}
	claudeMock := &configurableClaude{}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}

	opts := baseOpts()
	opts.DryRun = true
	var out bytes.Buffer
	if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if strings.Contains(out.String(), "CACHED SUMMARY") || strings.Contains(out.String(), "# Review:") {
		t.Errorf("the dry run rendered the cached review:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "hit, key "+key+": a review would serve the cached result") {
		t.Errorf("the text report does not name the hit:\n%s", out.String())
	}

	p := runPreview(t, &Runner{Claude: claudeMock, GitHub: gh}, baseOpts())
	if p.Cache != previewCacheHit || p.CacheKey != key {
		t.Errorf("cache = %q, key %q; want hit under %q", p.Cache, p.CacheKey, key)
	}
	if row := previewRow(t, p, claude.PassLabelReview); !row.Run || row.PromptBytes == 0 {
		t.Errorf("review row = %+v, want the session a --no-cache run starts, sized", row)
	}
	if claudeMock.reviewCalls != 0 {
		t.Errorf("Review calls = %d, want 0", claudeMock.reviewCalls)
	}

	noCache := baseOpts()
	noCache.NoCache = true
	if p := runPreview(t, &Runner{Claude: claudeMock, GitHub: gh}, noCache); p.Cache != previewCacheOff {
		t.Errorf("cache under --no-cache = %q, want off", p.Cache)
	}
}

// TestRun_DryRunReportsTheSpecialistGate: on a Markdown-only change the dry
// run lists the two specialists that are never gated as running, sized, and
// the other four as skipped, each with the files the diff did not touch.
// Without --specialists every specialist row says so (decision 143).
func TestRun_DryRunReportsTheSpecialistGate(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	claudeMock := &configurableClaude{}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) {
		pr := fakePR(t, "acme", "widgets", 145, "sha-preview-gate")
		pr.ChangedFiles = []string{"README.md"}
		return pr, nil
	}}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	opts := baseOpts()
	opts.Specialists = true
	p := runPreview(t, runner, opts)
	for _, key := range []string{"security", "data-migration"} {
		if row := previewRow(t, p, "specialist-"+key); !row.Run || row.PromptBytes == 0 || row.Reason != "" {
			t.Errorf("%s row = %+v, want it running with a prompt size", key, row)
		}
	}
	for key, reason := range map[string]string{
		"testing":         "the diff changes no source file",
		"performance":     "the diff changes no source file",
		"maintainability": "the diff changes no source file",
		"api-contract":    "the diff changes no route or handler file",
	} {
		if row := previewRow(t, p, "specialist-"+key); row.Run || row.Reason != reason || row.PromptBytes != 0 {
			t.Errorf("%s row = %+v, want it skipped with %q", key, row, reason)
		}
	}

	p = runPreview(t, runner, baseOpts())
	for _, sp := range claude.Specialists {
		if row := previewRow(t, p, sp.PassLabel()); row.Run || row.Reason != "--specialists is off" {
			t.Errorf("%s row without --specialists = %+v, want it skipped as off", sp.Key, row)
		}
	}
	if claudeMock.specialistCalls != 0 {
		t.Errorf("specialist calls = %d, want 0", claudeMock.specialistCalls)
	}
}

// TestRun_DryRunPassesMatchTheLiveRun runs a review and a dry run over the
// same inputs and compares the sessions the review started, and the pass
// records it kept, with the dry run's rows, so the preview cannot drift from
// the dispatch or from the labels the run records (decision 143).
func TestRun_DryRunPassesMatchTheLiveRun(t *testing.T) {
	for _, tc := range []struct {
		name    string
		changed []string
		feature bool
		set     func(*Options)
	}{
		{"the review alone", []string{"main.go"}, false, func(*Options) {}},
		{"every flag on a source change", []string{"internal/store/users.go"}, false, func(o *Options) {
			o.Thorough, o.CoverageMap, o.Specialists = true, true, true
		}},
		{"specialists on a route change", []string{"internal/api/users.go"}, false, func(o *Options) { o.Specialists = true }},
		{"specialists on a docs change", []string{"README.md"}, false, func(o *Options) { o.Specialists = true }},
		{"a feature file adds the compliance check", []string{"main.go"}, true, func(*Options) {}},
		{"a partitioned review", []string{"c/1.go", "a/2.go", "b/1.go", "a/1.go", "b/2.go"}, false, func(o *Options) {
			o.PartitionFiles, o.Thorough = 2, true
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Not t.Parallel(): cache.SetDir mutates a package-level variable.
			restore := cache.SetDir(t.TempDir())
			t.Cleanup(restore)

			// Run removes the checkout, so every run gets a fresh one.
			gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) {
				pr := fakePR(t, "acme", "widgets", 146, "sha-preview-parity")
				pr.ChangedFiles = tc.changed
				if tc.feature {
					pr.HeadBranch = "feature/CC-0042"
					writeFileIn(t, pr.Dir, ".planwerk/features/CC-0042.json", `{"feature_id":"CC-0042","title":"x"}`)
				}
				return pr, nil
			}}
			var (
				mu  sync.Mutex
				ran []string
			)
			claudeMock := &configurableClaude{
				specialist: func(_, _, key, _ string) (*report.ReviewResult, error) {
					mu.Lock()
					ran = append(ran, "specialist-"+key)
					mu.Unlock()
					return &report.ReviewResult{}, nil
				},
			}
			opts := baseOpts()
			opts.NoCache = true
			tc.set(&opts)
			live := opts
			live.Format = formatJSON
			var out bytes.Buffer
			if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&out, live); err != nil {
				t.Fatalf("Run returned error: %v", err)
			}
			// A Decoder, since --coverage-map appends its Markdown after the
			// JSON object.
			var result report.ReviewResult
			if err := json.NewDecoder(&out).Decode(&result); err != nil {
				t.Fatalf("decoding the review: %v\n%s", err, out.String())
			}
			for range claudeMock.reviewCalls {
				ran = append(ran, claude.PassLabelReview)
			}
			for label, calls := range map[string]int32{
				claude.PassLabelAdversarial: claudeMock.adversarialCalls,
				claude.PassLabelCoverage:    claudeMock.coverageCalls,
				claude.PassLabelCompliance:  claudeMock.featureComplianceCalls,
			} {
				for range calls {
					ran = append(ran, label)
				}
			}
			slices.Sort(ran)

			dry := &configurableClaude{}
			p := runPreview(t, &Runner{Claude: dry, GitHub: gh}, opts)
			var previewed []string
			for _, row := range p.Passes {
				if row.Run {
					previewed = append(previewed, row.Pass)
				}
			}
			slices.Sort(previewed)
			if !slices.Equal(previewed, ran) {
				t.Errorf("the dry run runs %v, the review ran %v", previewed, ran)
			}

			// The run's pass records (result.Passes) name every secondary pass
			// it requested, the specialists the gate left out included, under
			// the label the dry run's row carries: a ran pass is a running row,
			// a gated-out one a skipped row, and no running row goes unrecorded.
			rows := make(map[string]PreviewPass)
			for _, row := range p.Passes {
				if row.Pass != claude.PassLabelReview {
					rows[row.Pass] = row
				}
			}
			recorded := make(map[string]bool)
			for _, ps := range result.Passes {
				recorded[ps.Pass] = true
				row, ok := rows[ps.Pass]
				switch {
				case !ok:
					t.Errorf("the review records %s as %s; the dry run has no row for it", ps.Pass, ps.Status)
				case ps.Status == report.PassStatusRan && !row.Run:
					t.Errorf("the review ran %s; the dry run skips it: %+v", ps.Pass, row)
				case ps.Status == report.PassStatusGatedOut && row.Run:
					t.Errorf("the review gated out %s; the dry run runs it: %+v", ps.Pass, row)
				case ps.Status == report.PassStatusFailed:
					t.Errorf("the review records %s as failed: %s", ps.Pass, ps.Error)
				}
			}
			for label, row := range rows {
				if row.Run && !recorded[label] {
					t.Errorf("the dry run runs %s; the review records no status for it: %+v", label, result.Passes)
				}
			}
			if n := claudeCalls(dry); n != 0 {
				t.Errorf("the dry run made %d Claude calls, want 0", n)
			}
		})
	}
}

// TestRun_DryRunReportsTheWikiAndRemovesTheMemoryDirectory: the dry run
// resolves the wiki as a review does, writes the project memory the review
// prompt points at, and removes it before it returns (decision 143).
func TestRun_DryRunReportsTheWikiAndRemovesTheMemoryDirectory(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	logBuf := captureLog(t)

	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) {
		return fakePR(t, "acme", "widgets", 147, "sha-preview-wiki"), nil
	}}
	p := runPreview(t, &Runner{Claude: &configurableClaude{}, GitHub: gh, ResolveWiki: memoryWiki}, baseOpts())
	if p.WikiRepo != "acme/widgets.wiki" || p.WikiCommit != "wikisha" || p.MemoryPages != 1 {
		t.Errorf("wiki = %q at %q with %d pages, want acme/widgets.wiki at wikisha with 1", p.WikiRepo, p.WikiCommit, p.MemoryPages)
	}
	if !strings.Contains(logBuf.String(), "wrote the project memory") {
		t.Errorf("the dry run must write the memory a review reads, to size its prompt; log:\n%s", logBuf.String())
	}
	if left := memoryDirsIn(t, tmp); len(left) != 0 {
		t.Errorf("TMPDIR holds %v after the dry run, want no memory directory", left)
	}
}

// TestRun_DryRunListsThePartitionsAndTheExcludedFiles: on a checkout whose
// diff leaves out a lock file and splits into three partitions, the dry run
// lists both, sizes one review row per partition from the partition's own
// diff, and removes every diff it wrote (decision 143).
func TestRun_DryRunListsThePartitionsAndTheExcludedFiles(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	dir := partitionCheckout(t)
	changed, err := (github.Client{}).DiffNames(dir, "main")
	if err != nil {
		t.Fatalf("DiffNames: %v", err)
	}
	fileChanges, err := (github.Client{}).DiffFileChanges(dir, "main")
	if err != nil {
		t.Fatalf("DiffFileChanges: %v", err)
	}
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	pr := fakePR(t, "acme", "widgets", 148, "sha-preview-partitions")
	pr.Dir, pr.ChangedFiles, pr.FileChanges, pr.Local = dir, changed, fileChanges, true
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	opts := baseOpts()
	opts.DiffExclusions = true
	opts.PartitionFiles = 1

	p := runPreview(t, &Runner{Claude: &configurableClaude{}, GitHub: gh}, opts)
	if want := []report.ExcludedFile{{Path: "go.sum", Reason: report.ExcludedLockfile}}; !slices.Equal(p.ExcludedFiles, want) {
		t.Errorf("excluded_files = %+v, want %+v", p.ExcludedFiles, want)
	}
	want := []PreviewPartition{{Files: []string{"a/x.go"}, Lines: 2}, {Files: []string{"b/y.go"}, Lines: 2}, {Files: []string{"c/new.go"}, Lines: 1}}
	if !reflect.DeepEqual(p.Partitions, want) {
		t.Errorf("partitions = %+v, want %+v", p.Partitions, want)
	}
	var reviews []PreviewPass
	for _, row := range p.Passes {
		if row.Pass == claude.PassLabelReview {
			reviews = append(reviews, row)
		}
	}
	if len(reviews) != 3 {
		t.Fatalf("the dry run has %d review rows, want one per partition: %+v", len(reviews), p.Passes)
	}
	for i, row := range reviews {
		if row.Partition != i+1 || !row.Run || row.PromptBytes == 0 {
			t.Errorf("review row %d = %+v, want partition %d running with a prompt size", i, row, i+1)
		}
	}
	if left := diffDirsIn(t, tmp); len(left) != 0 {
		t.Errorf("TMPDIR holds %v after the dry run, want every diff removed", left)
	}
}

// TestRun_DryRunReportsTheScopedCatalog: under --scope-patterns the dry run
// reports the review prompt's split, sizes the review row by the bodies it
// keeps while the adversarial row keeps the catalog, and removes the catalog
// it wrote (decision 143).
func TestRun_DryRunReportsTheScopedCatalog(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) {
		pr := fakePR(t, "acme", "widgets", 149, "sha-preview-scope")
		writeTestFile(t, pr.Dir, "go.mod", "module example.com/x\n\ngo 1.22\n")
		writeTestFile(t, pr.Dir, "main.go", "package main\n")
		writeTestFile(t, pr.Dir, "deploy.yaml", "apiVersion: apps/v1\nkind: Deployment\n")
		pr.ChangedFiles = []string{"main.go"}
		return pr, nil
	}}
	opts := baseOpts()
	opts.PatternDirs = []string{scopePatternDir(t, nil)}
	opts.ScopePatterns = true
	opts.Thorough = true

	p := runPreview(t, &Runner{Claude: &configurableClaude{}, GitHub: gh}, opts)
	if p.ScopePatterns == nil || *p.ScopePatterns != (PreviewScope{Inline: 2, Indexed: 1}) {
		t.Errorf("scope_patterns = %+v, want 2 inline and 1 indexed", p.ScopePatterns)
	}
	if p.Patterns.Loaded != 3 {
		t.Errorf("patterns.loaded = %d, want the 3 loaded before scoping", p.Patterns.Loaded)
	}
	if row := previewRow(t, p, claude.PassLabelReview); row.Patterns != 2 {
		t.Errorf("review row = %+v, want the 2 bodies the scoped prompt carries", row)
	}
	if row := previewRow(t, p, claude.PassLabelAdversarial); row.Patterns != 3 {
		t.Errorf("adversarial row = %+v, want the whole catalog", row)
	}
	if left := catalogDirsIn(t, tmp); len(left) != 0 {
		t.Errorf("TMPDIR holds %v after the dry run, want the catalog removed", left)
	}

	opts.ScopePatterns = false
	if p := runPreview(t, &Runner{Claude: &configurableClaude{}, GitHub: gh}, opts); p.ScopePatterns != nil {
		t.Errorf("scope_patterns without the flag = %+v, want none", p.ScopePatterns)
	}
}

// TestRun_PrintPromptWritesTheReviewPrompt: --print-prompt writes the review
// prompt, pattern bodies inline, and nothing else, starts no session, caches
// and posts nothing, and on a checkout whose diff could be written keeps the
// git command, the form that needs no file (decision 143).
func TestRun_PrintPromptWritesTheReviewPrompt(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := t.TempDir()
	writeTestFile(t, patternDir, "canary.md", "# Review Pattern: Canary Rule\n\n**Review-Area**: quality\n**Detection-Hint**: anything\n**Severity**: WARNING\n**Category**: design-principle\n\n## What to check\n\nThe canary body 7f3a9c.\n")
	dir := partitionCheckout(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	pr := fakePR(t, "acme", "widgets", 150, "sha-print-prompt")
	pr.Dir, pr.ChangedFiles, pr.Local = dir, []string{"a/x.go"}, true
	claudeMock := &configurableClaude{}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	opts := baseOpts()
	opts.PatternDirs = []string{patternDir}
	opts.PrintPrompt = true
	opts.PostReview = true
	opts.Thorough = true

	var out bytes.Buffer
	if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	prompt := out.String()
	if !strings.HasPrefix(prompt, "## Review Scope\n") {
		t.Errorf("stdout does not open with the review prompt:\n%s", prompt)
	}
	for _, want := range []string{"The canary body 7f3a9c.", "Run `git diff origin/main...HEAD`"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the printed prompt lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "[dry-run]") || strings.Contains(prompt, "# Review:") {
		t.Errorf("stdout holds more than the prompt:\n%s", prompt)
	}
	if n := claudeCalls(claudeMock); n != 0 {
		t.Errorf("--print-prompt made %d Claude calls, want 0", n)
	}
	if n := cacheEntries(t); n != 0 {
		t.Errorf("the cache holds %d entries after --print-prompt, want 0", n)
	}
	if n := gh.Count("PostPRComment") + gh.Count("SubmitPRReview"); n != 0 {
		t.Errorf("--print-prompt posted %d times, want 0", n)
	}
	if left := diffDirsIn(t, tmp); len(left) != 0 {
		t.Errorf("--print-prompt wrote the diffs %v, want none", left)
	}
}

// TestRun_PrintPromptCarriesTheMemoryBodies: the printed prompt carries the
// project memory's page bodies rather than an index into a directory removed
// on exit, and writes no directory (decision 143).
func TestRun_PrintPromptCarriesTheMemoryBodies(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	logBuf := captureLog(t)

	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) {
		return fakePR(t, "acme", "widgets", 151, "sha-print-memory"), nil
	}}
	opts := baseOpts()
	opts.PrintPrompt = true
	var out bytes.Buffer
	if err := (&Runner{Claude: &configurableClaude{}, GitHub: gh, ResolveWiki: memoryWiki}).Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	prompt := out.String()
	if !strings.Contains(prompt, "<project-memory>") || !strings.Contains(prompt, "# Pin every dependency") {
		t.Errorf("the printed prompt lacks the memory page body:\n%s", prompt)
	}
	if strings.Contains(prompt, "<project-memory-index>") {
		t.Errorf("the printed prompt carries the memory index:\n%s", prompt)
	}
	if left := memoryDirsIn(t, tmp); len(left) != 0 {
		t.Errorf("TMPDIR holds %v, want no memory directory", left)
	}
	if strings.Contains(logBuf.String(), "wrote the project memory") {
		t.Errorf("--print-prompt wrote the project memory; log:\n%s", logBuf.String())
	}
}

// TestRun_PrintPromptPrintsTheFirstPartition: for a partitioned review the
// printed prompt is the first partition's, and the run says so in its log
// (decision 143).
func TestRun_PrintPromptPrintsTheFirstPartition(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)
	logBuf := captureLog(t)

	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) {
		pr := fakePR(t, "acme", "widgets", 152, "sha-print-partition")
		pr.ChangedFiles = []string{"c/1.go", "a/2.go", "b/1.go", "a/1.go", "b/2.go"}
		return pr, nil
	}}
	opts := baseOpts()
	opts.PrintPrompt = true
	opts.PartitionFiles = 2
	var out bytes.Buffer
	if err := (&Runner{Claude: &configurableClaude{}, GitHub: gh}).Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(out.String(), "## Partition 1 of 3\n") {
		t.Errorf("the printed prompt is not the first partition's:\n%s", out.String())
	}
	if !strings.Contains(logBuf.String(), "printing the first review partition; --dry-run lists them all") || !strings.Contains(logBuf.String(), "partitions=3") {
		t.Errorf("the run does not log that it prints the first of 3 partitions; log:\n%s", logBuf.String())
	}
}

// TestRun_PrintPromptAppliesTheDiffExclusions: under --diff-exclusions the
// printed prompt leaves the lock file the diff leaves out off its changed-file
// lists and splits the review as the dry run does, one session when the kept
// files fit --partition-files, and it writes no diff (decision 143).
func TestRun_PrintPromptAppliesTheDiffExclusions(t *testing.T) {
	for _, tc := range []struct {
		name           string
		partitionFiles int
		partitions     int
	}{
		{"the three kept files fit a cap of three", 3, 0},
		{"a cap of one splits the kept files", 1, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Not t.Parallel(): cache.SetDir mutates a package-level variable.
			restore := cache.SetDir(t.TempDir())
			t.Cleanup(restore)

			dir := partitionCheckout(t)
			changed, err := (github.Client{}).DiffNames(dir, "main")
			if err != nil {
				t.Fatalf("DiffNames: %v", err)
			}
			fileChanges, err := (github.Client{}).DiffFileChanges(dir, "main")
			if err != nil {
				t.Fatalf("DiffFileChanges: %v", err)
			}
			pr := fakePR(t, "acme", "widgets", 153, "sha-print-exclusions")
			pr.Dir, pr.ChangedFiles, pr.FileChanges, pr.Local = dir, changed, fileChanges, true
			gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
			opts := baseOpts()
			opts.DiffExclusions = true
			opts.PartitionFiles = tc.partitionFiles

			if p := runPreview(t, &Runner{Claude: &configurableClaude{}, GitHub: gh}, opts); len(p.Partitions) != tc.partitions {
				t.Fatalf("the dry run lists %d partitions, want %d: %+v", len(p.Partitions), tc.partitions, p.Partitions)
			}

			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			logBuf := captureLog(t)
			opts.PrintPrompt = true
			var out bytes.Buffer
			if err := (&Runner{Claude: &configurableClaude{}, GitHub: gh}).Run(&out, opts); err != nil {
				t.Fatalf("Run returned error: %v", err)
			}
			prompt := out.String()
			listed := fenceBody(prompt, "changed-files") + "\n" + fenceBody(prompt, "other-changed-files")
			if !strings.Contains(listed, "a/x.go") || strings.Contains(listed, "go.sum") {
				t.Errorf("the printed prompt lists %q, want the kept files without go.sum", listed)
			}
			partitioned := strings.Contains(prompt, "## Partition 1 of ")
			if partitioned != (tc.partitions > 0) || (partitioned && !strings.Contains(prompt, fmt.Sprintf("## Partition 1 of %d\n", tc.partitions))) {
				t.Errorf("the printed prompt is partitioned = %v, want %d partitions as the dry run lists:\n%s", partitioned, tc.partitions, prompt)
			}
			if logged := strings.Contains(logBuf.String(), "printing the first review partition"); logged != (tc.partitions > 0) {
				t.Errorf("logged the first partition = %v, want %v; log:\n%s", logged, tc.partitions > 0, logBuf.String())
			}
			if left := diffDirsIn(t, tmp); len(left) != 0 {
				t.Errorf("--print-prompt wrote the diffs %v, want none", left)
			}
		})
	}
}

// fenceBody returns what the first <tag> fence of prompt holds, or "" when
// prompt has none.
func fenceBody(prompt, tag string) string {
	_, rest, ok := strings.Cut(prompt, "<"+tag+">\n")
	if !ok {
		return ""
	}
	body, _, _ := strings.Cut(rest, "\n</"+tag+">")
	return body
}

// TestWritePreview_Text locks the text form's rows: the cache hit, the scope
// line, the partitions, one row per partition session, the specialists
// collapsed when the flag is off, and the closing line on what is not sized.
func TestWritePreview_Text(t *testing.T) {
	t.Parallel()
	p := Preview{
		PR: "acme/widgets#7", Title: "Cache widgets", HeadSHA: previewHeadSHA, HeadBranch: "cache", BaseBranch: "main",
		CacheKey: "k1", Cache: previewCacheHit,
		TechTags:      []string{"go"},
		Patterns:      PreviewPatterns{Loaded: 3, DesignPrinciple: 1, Technology: 2, MaxPatterns: 2},
		ScopePatterns: &PreviewScope{Inline: 1, Indexed: 2},
		ChangedFiles:  []string{"a/x.go", "b/y.go"},
		ExcludedFiles: []report.ExcludedFile{{Path: "go.sum", Reason: report.ExcludedLockfile}},
		Partitions:    []PreviewPartition{{Files: []string{"a/x.go"}, Lines: 1}, {Files: []string{"b/y.go"}, Lines: 3}},
		Passes: []PreviewPass{
			{Pass: "review", Partition: 1, Run: true, Patterns: 1, PromptBytes: 2048},
			{Pass: "review", Partition: 2, Run: true, Patterns: 1, PromptBytes: 900},
			{Pass: "adversarial", Reason: "--thorough is off"},
			{Pass: "specialist-security", Reason: specialistsOff},
			{Pass: "specialist-testing", Reason: specialistsOff},
		},
		Post: previewPostNone,
	}
	var out bytes.Buffer
	if err := writePreview(&out, "markdown", p); err != nil {
		t.Fatalf("writePreview: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"Cache:        hit, key k1: a review would serve the cached result and start no session; --no-cache runs the passes below\n",
		"Head:         0123456 (cache into main)\n",
		"Patterns:     3 loaded (1 design-principle, 2 technology, 0 review, 0 general); --max-patterns 2 caps each prompt\n",
		"Scope:        --scope-patterns: the review carries 1 pattern body and indexes 2\n",
		"Excluded files (1):\n  go.sum (lock file)\n",
		"Partitions (2):\n  1: 1 file, 1 line\n     a/x.go\n  2: 1 file, 3 lines\n     b/y.go\n",
		"  review (partition 1 of 2)  run   1 pattern  2.0 KB\n",
		"  review (partition 2 of 2)  run   1 pattern  900 B\n",
		"  adversarial                skip  --thorough is off\n",
		"  specialists                skip  --specialists is off\n",
		"are not sized.\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the text form lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "specialist-") {
		t.Errorf("the specialist rows are not collapsed:\n%s", text)
	}
}

// TestWritePreview_TextKeepsThePullRequestsTextOnItsLine: the title and the
// feature ID come from the pull request and its head checkout, so a newline
// in either is mapped like one in a path and cannot add a line of its own to
// the text form.
func TestWritePreview_TextKeepsThePullRequestsTextOnItsLine(t *testing.T) {
	t.Parallel()
	forged := "Cache:        hit, key 0000: a review would serve"
	p := Preview{
		PR: "acme/widgets#7", Title: "Cache widgets\n" + forged, HeadSHA: previewHeadSHA, HeadBranch: "cache", BaseBranch: "main",
		CacheKey: "k1", Cache: previewCacheMiss, Feature: "CC-7\n" + forged, Post: previewPostNone,
	}
	var out bytes.Buffer
	if err := writePreview(&out, "markdown", p); err != nil {
		t.Fatalf("writePreview: %v", err)
	}
	text := out.String()
	if strings.Contains(text, "\n"+forged) {
		t.Errorf("a newline in the title or the feature ID starts a line of its own:\n%s", text)
	}
	for _, want := range []string{
		"PR:           acme/widgets#7: Cache widgets\uFFFD" + forged + "\n",
		"Feature:      CC-7\uFFFD" + forged + "\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the text form lacks %q:\n%s", want, text)
		}
	}
}

// writeFileIn writes content to name under dir, creating the parents.
func writeFileIn(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

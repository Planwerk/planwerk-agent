package glossary

import (
	"bytes"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/planwerk/planwerk-review/internal/cache"
	"github.com/planwerk/planwerk-review/internal/github"
)

// testModel is the stub model name fakeGenerator reports back to Run.
const testModel = "model-x"

// fakeGitHub is a test GitHubClient whose CloneRepo / DefaultBranchHEAD behavior
// is configured per-test via closures.
type fakeGitHub struct {
	cloneRepo         func(ref string) (*github.Repo, error)
	defaultBranchHEAD func(owner, name string) (string, error)

	cloneCalls      atomic.Int32
	cloneLocalCalls atomic.Int32
}

func (f *fakeGitHub) CloneRepo(ref string) (*github.Repo, error) {
	f.cloneCalls.Add(1)
	return f.cloneRepo(ref)
}

func (f *fakeGitHub) CloneRepoLocal(ref string, _ github.LocalOptions) (*github.Repo, error) {
	f.cloneLocalCalls.Add(1)
	repo, err := f.cloneRepo(ref)
	if err != nil {
		return nil, err
	}
	repo.Local = true
	return repo, nil
}

func (f *fakeGitHub) DefaultBranchHEAD(owner, name string) (string, error) {
	return f.defaultBranchHEAD(owner, name)
}

// fakeGenerator is a test GlossaryGenerator tracking call count so cache-hit
// tests can assert Claude was skipped.
type fakeGenerator struct {
	calls atomic.Int32
	fn    func(dir string, ctx GenerateContext) (string, string, error)
}

func (f *fakeGenerator) GenerateGlossary(dir string, ctx GenerateContext) (string, string, error) {
	f.calls.Add(1)
	if f.fn == nil {
		return "# Default\n", testModel, nil
	}
	return f.fn(dir, ctx)
}

func fakeRepo(t *testing.T, owner, name string) *github.Repo {
	t.Helper()
	return &github.Repo{Owner: owner, Name: name, Dir: t.TempDir()}
}

func baseGlossaryOpts() Options {
	return Options{RepoRef: "owner/repo"}
}

func TestGlossaryRun_CacheMissThenHit(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	repo := fakeRepo(t, "acme", "widgets")
	gh := &fakeGitHub{
		cloneRepo:         func(string) (*github.Repo, error) { return repo, nil },
		defaultBranchHEAD: func(string, string) (string, error) { return "sha-1", nil },
	}
	gen := &fakeGenerator{
		fn: func(string, GenerateContext) (string, string, error) {
			return "# Billing\n\n## Language\n\n**Invoice**: a statement.", testModel, nil
		},
	}
	runner := &Runner{Claude: gen, GitHub: gh}

	var out bytes.Buffer
	if err := runner.Run(&out, baseGlossaryOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if gen.calls.Load() != 1 {
		t.Errorf("GenerateGlossary calls = %d, want 1", gen.calls.Load())
	}
	if !strings.Contains(out.String(), "# Billing") {
		t.Errorf("output missing generated glossary, got:\n%s", out.String())
	}
	if !strings.HasSuffix(out.String(), "\n") {
		t.Errorf("output should end with exactly one newline, got:\n%q", out.String())
	}

	// Second run: the cache must short-circuit Claude and the clone entirely.
	gh2 := &fakeGitHub{
		cloneRepo: func(string) (*github.Repo, error) {
			t.Fatal("CloneRepo must not be called on cache hit")
			return nil, nil
		},
		defaultBranchHEAD: func(string, string) (string, error) { return "sha-1", nil },
	}
	gen2 := &fakeGenerator{
		fn: func(string, GenerateContext) (string, string, error) {
			t.Fatal("GenerateGlossary must not be called on cache hit")
			return "", "", nil
		},
	}
	runner2 := &Runner{Claude: gen2, GitHub: gh2}
	var out2 bytes.Buffer
	if err := runner2.Run(&out2, baseGlossaryOpts()); err != nil {
		t.Fatalf("cache-hit Run returned error: %v", err)
	}
	if out2.String() != out.String() {
		t.Errorf("cache-hit output differs from fresh output:\nfresh: %q\nhit:   %q", out.String(), out2.String())
	}
}

func TestGlossaryRun_EmptyGenerationIsRejectedAndNotCached(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	gh := &fakeGitHub{
		cloneRepo:         func(string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		defaultBranchHEAD: func(string, string) (string, error) { return "sha-empty", nil },
	}
	gen := &fakeGenerator{
		// A whitespace-only response models what sanitizeGlossary returns for an
		// empty or fence-only generation.
		fn: func(string, GenerateContext) (string, string, error) { return "   \n", testModel, nil },
	}
	runner := &Runner{Claude: gen, GitHub: gh}

	var out bytes.Buffer
	err := runner.Run(&out, baseGlossaryOpts())
	if err == nil {
		t.Fatal("expected error when generation produces no CONTEXT.md document")
	}
	if !strings.Contains(err.Error(), "no CONTEXT.md document") {
		t.Errorf("error should explain the empty generation, got: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("nothing should be written on an empty generation, got:\n%q", out.String())
	}

	// The empty result must NOT have poisoned the cache: a second run with the
	// same HEAD SHA must re-invoke Claude and serve the now-valid glossary.
	gen.fn = func(string, GenerateContext) (string, string, error) {
		return "# Billing\n\n## Language\n\n**Invoice**: a statement.", testModel, nil
	}
	var out2 bytes.Buffer
	if err := runner.Run(&out2, baseGlossaryOpts()); err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}
	if !strings.Contains(out2.String(), "# Billing") {
		t.Errorf("second run should regenerate, got:\n%s", out2.String())
	}
	if gen.calls.Load() != 2 {
		t.Errorf("GenerateGlossary calls = %d, want 2 (an empty result must not be cached)", gen.calls.Load())
	}
}

func TestGlossaryRun_LocalUsesCwdAndKeepsTree(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	repo := fakeRepo(t, "acme", "widgets")
	gh := &fakeGitHub{
		cloneRepo:         func(string) (*github.Repo, error) { return repo, nil },
		defaultBranchHEAD: func(string, string) (string, error) { return "sha-local", nil },
	}
	gen := &fakeGenerator{}
	runner := &Runner{Claude: gen, GitHub: gh}

	opts := baseGlossaryOpts()
	opts.Local = true
	opts.NoCache = true

	if err := runner.Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if gh.cloneLocalCalls.Load() != 1 {
		t.Errorf("CloneRepoLocal calls = %d, want 1", gh.cloneLocalCalls.Load())
	}
	if gh.cloneCalls.Load() != 0 {
		t.Errorf("CloneRepo (temp-dir clone) calls = %d, want 0 in local mode", gh.cloneCalls.Load())
	}
}

func TestGlossaryRun_GeneratorErrorPropagates(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	gh := &fakeGitHub{
		cloneRepo:         func(string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		defaultBranchHEAD: func(string, string) (string, error) { return "sha-err", nil },
	}
	gen := &fakeGenerator{
		fn: func(string, GenerateContext) (string, string, error) {
			return "", "", errors.New("claude exploded")
		},
	}
	runner := &Runner{Claude: gen, GitHub: gh}

	var out bytes.Buffer
	err := runner.Run(&out, baseGlossaryOpts())
	if err == nil {
		t.Fatal("expected error when GenerateGlossary fails")
	}
	if !strings.Contains(err.Error(), "claude glossary generation") {
		t.Errorf("error should wrap the generation failure, got: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("nothing should be written on a generation error, got:\n%s", out.String())
	}
}

func TestGlossaryRun_HEADFailureDisablesCaching(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	gh := &fakeGitHub{
		cloneRepo: func(string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		defaultBranchHEAD: func(string, string) (string, error) {
			return "", errors.New("network unreachable")
		},
	}
	gen := &fakeGenerator{}
	runner := &Runner{Claude: gen, GitHub: gh}

	if err := runner.Run(&bytes.Buffer{}, baseGlossaryOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	// Nothing was cached, so a second run must invoke Claude again.
	if err := runner.Run(&bytes.Buffer{}, baseGlossaryOpts()); err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}
	if gen.calls.Load() != 2 {
		t.Errorf("GenerateGlossary calls after HEAD failure = %d, want 2", gen.calls.Load())
	}
}

func TestGlossaryRun_InvalidRepoRefFailsBeforeHEAD(t *testing.T) {
	gh := &fakeGitHub{
		cloneRepo: func(string) (*github.Repo, error) {
			t.Fatal("CloneRepo must not be called for an invalid repo ref")
			return nil, nil
		},
		defaultBranchHEAD: func(string, string) (string, error) {
			t.Fatal("DefaultBranchHEAD must not be called for an invalid repo ref")
			return "", nil
		},
	}
	runner := &Runner{Claude: &fakeGenerator{}, GitHub: gh}

	opts := baseGlossaryOpts()
	opts.RepoRef = "not a valid ref"
	err := runner.Run(&bytes.Buffer{}, opts)
	if err == nil {
		t.Fatal("expected error for invalid repo ref")
	}
	if !strings.Contains(err.Error(), "parsing repo ref") {
		t.Errorf("error should wrap the parse failure, got: %v", err)
	}
}

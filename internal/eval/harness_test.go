package eval

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/detect"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
	"github.com/planwerk/planwerk-agent/internal/review"
)

func TestEnsureGoMod(t *testing.T) {
	t.Run("writes evalGoMod into a dir without one", func(t *testing.T) {
		dir := t.TempDir()
		if err := ensureGoMod(dir); err != nil {
			t.Fatalf("ensureGoMod: %v", err)
		}
		path := filepath.Join(dir, "go.mod")
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading go.mod: %v", err)
		}
		if string(got) != evalGoMod {
			t.Errorf("go.mod = %q, want %q", got, evalGoMod)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat go.mod: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("go.mod mode = %o, want 600", perm)
		}
		if tags := detect.Technologies(dir); !slices.Contains(tags, "go") {
			t.Errorf("detect.Technologies = %v, want it to include go", tags)
		}
	})

	t.Run("keeps an existing go.mod", func(t *testing.T) {
		dir := t.TempDir()
		const existing = "module example.com/own\n\ngo 1.26\n"
		writeFile(t, filepath.Join(dir, "go.mod"), existing)
		if err := ensureGoMod(dir); err != nil {
			t.Fatalf("ensureGoMod: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err != nil {
			t.Fatalf("reading go.mod: %v", err)
		}
		if string(got) != existing {
			t.Errorf("go.mod = %q, want the existing %q", got, existing)
		}
	})

	t.Run("a dir under a regular file fails", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		writeFile(t, file, "not a dir\n")
		err := ensureGoMod(filepath.Join(file, "sub"))
		if err == nil || !strings.Contains(err.Error(), "writing go.mod") {
			t.Fatalf("ensureGoMod under a file = %v, want a writing go.mod error", err)
		}
	})
}

// TestSetupRepoCommitsGoModInBase locks that the harness-written go.mod lands in
// the base commit, so it never shows up among the files the PR changes.
func TestSetupRepoCommitsGoModInBase(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	c, err := LoadCase(writeValidCase(t, t.TempDir(), "sample"))
	if err != nil {
		t.Fatalf("LoadCase: %v", err)
	}
	repo := t.TempDir()
	changed, err := setupRepo(repo, c)
	if err != nil {
		t.Fatalf("setupRepo: %v", err)
	}
	if !slices.Equal(changed, []string{"main.go"}) {
		t.Errorf("changed = %v, want [main.go]", changed)
	}

	gitOut := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}
		return string(out)
	}
	if got := gitOut("show", evalBaseBranch+":go.mod"); got != evalGoMod {
		t.Errorf("go.mod on %s = %q, want %q", evalBaseBranch, got, evalGoMod)
	}
	if got := strings.TrimSpace(gitOut("diff", "--name-only", evalBaseBranch, "HEAD")); got != "main.go" {
		t.Errorf("files changed against %s = %q, want main.go", evalBaseBranch, got)
	}
}

// failingPasses is a review.ClaudeRunner whose adversarial pass and security
// specialist fail while every other specialist succeeds.
type failingPasses struct{ review.ClaudeRunner }

func (failingPasses) AdversarialReview(string, string, string, []patterns.Pattern, int) (*report.ReviewResult, error) {
	return nil, errors.New("adversarial timed out")
}

func (failingPasses) SpecialistReview(_, _ string, sp claude.Specialist, _ []patterns.Pattern, _ int) (*report.ReviewResult, error) {
	if sp.Key == "security" {
		return nil, errors.New("security timed out")
	}
	return &report.ReviewResult{}, nil
}

// TestPassFailures locks that a secondary pass the pipeline drops on error is
// still seen by RunCase, which would otherwise score the lost pass as a miss.
func TestPassFailures(t *testing.T) {
	t.Run("passes that succeed record nothing", func(t *testing.T) {
		p := &passFailures{ClaudeRunner: failingPasses{}}
		if _, err := p.SpecialistReview("", evalBaseBranch, claude.Specialist{Key: "data-migration"}, nil, 0); err != nil {
			t.Fatalf("SpecialistReview: %v", err)
		}
		if err := p.err(); err != nil {
			t.Errorf("err() = %v, want nil", err)
		}
	})

	t.Run("a failed specialist in the fan-out and a failed adversarial pass are recorded", func(t *testing.T) {
		p := &passFailures{ClaudeRunner: failingPasses{}}
		// The fan-out calls the specialists concurrently.
		claude.RunSpecialistFanOut(nil, func(sp claude.Specialist) (*report.ReviewResult, error) {
			return p.SpecialistReview("", evalBaseBranch, sp, nil, 0)
		})
		if _, err := p.AdversarialReview("", evalBaseBranch, "", nil, 0); err == nil {
			t.Error("AdversarialReview = nil error, want the wrapped runner's error passed through")
		}
		err := p.err()
		if err == nil {
			t.Fatal("err() = nil, want the recorded failures")
		}
		for _, want := range []string{"security timed out", "adversarial timed out"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("err() = %q, want it to contain %q", err, want)
			}
		}
	})
}

package glossary

import "github.com/planwerk/planwerk-review/internal/github"

// GenerateContext is the input to the glossary-generation prompt. The
// vocabulary is extracted from the checkout itself, so the only field the
// prompt needs is RepoName, which seeds the "# {Context Name}" heading hint so
// the generated CONTEXT.md is named after the repository rather than a generic
// placeholder.
type GenerateContext struct {
	RepoName string
}

// GlossaryGenerator produces a CONTEXT.md for a cloned repo via Claude. The
// glossary package depends on this interface rather than the concrete claude
// package so tests can inject a deterministic fake. It returns the generated
// Markdown, the resolved model id, and an error.
type GlossaryGenerator interface {
	GenerateGlossary(dir string, ctx GenerateContext) (string, string, error)
}

// GenerateFn is the bare-function form of GlossaryGenerator that the CLI passes
// in (the claude client's GenerateGlossary method value). It is adapted to the
// interface via generateFnAdapter.
type GenerateFn func(dir string, ctx GenerateContext) (string, string, error)

type generateFnAdapter struct {
	fn GenerateFn
}

func (a generateFnAdapter) GenerateGlossary(dir string, ctx GenerateContext) (string, string, error) {
	return a.fn(dir, ctx)
}

// GitHubClient wraps the GitHub operations the glossary pipeline needs:
// cloning the repository (or using the local checkout) and resolving the
// default-branch HEAD for cache keying. Tests inject a mock to avoid touching
// the real git or gh CLI.
type GitHubClient interface {
	CloneRepo(ref string) (*github.Repo, error)
	CloneRepoLocal(ref string, opts github.LocalOptions) (*github.Repo, error)
	DefaultBranchHEAD(owner, name string) (string, error)
}

// defaultGitHubClient is the production GitHubClient backed by the github package.
type defaultGitHubClient struct{}

func (defaultGitHubClient) CloneRepo(ref string) (*github.Repo, error) {
	return github.CloneRepo(ref)
}

func (defaultGitHubClient) CloneRepoLocal(ref string, opts github.LocalOptions) (*github.Repo, error) {
	return github.UseLocalRepo(ref, opts)
}

func (defaultGitHubClient) DefaultBranchHEAD(owner, name string) (string, error) {
	return github.DefaultBranchHEAD(owner, name)
}

package ship

import (
	"io"

	"github.com/planwerk/planwerk-agent/internal/github"
)

// ImplementFn runs the full implement pipeline (plan → implement → simplify →
// review → finalize) for a single Sub Issue, ending at an open pull request that
// closes it. It returns an error when the Sub Issue could not be carried to an
// open PR — the implement session reported BLOCKED / NEEDS_CONTEXT, or the run
// otherwise failed. ship treats any error as "this Sub Issue could not be
// finished autonomously" and skips it together with everything transitively
// blocked by it.
//
// The CLI wires this to implement.Run with the shared Claude functions and the
// --no-simplify / --no-review / verify / pattern / planning options bound in, so
// ship reuses the implement pipeline exactly as the implement command runs it.
// Tests inject a fake that returns scripted results per issue ref.
type ImplementFn func(w io.Writer, issueRef string) error

// FixFn runs the CI self-heal loop for a single PR: it waits for the PR's checks
// and, when any fail, dispatches fix iterations until the checks pass or the fix
// budget is exhausted. It returns nil when the checks are green and an error when
// they could not be made green within the budget. ship reuses the fix loop for
// the "wait for CI" and "self-heal red CI" steps of the per-issue pipeline.
//
// The CLI wires this to fix.Run with the shared Claude fix function and the
// --interval / --max-fix-iterations options bound in. Tests inject a fake that
// returns scripted results per PR ref.
type FixFn func(w io.Writer, prRef string) error

// GitHubClient is the subset of github operations the ship orchestrator needs:
// reading the Meta Issue and its Sub Issue neighborhood, reading each Sub Issue's
// native blocked_by dependencies, narrating progress on the Meta Issue, and the
// per-PR undraft / mergeability / merge plus the optional Meta Issue close. Each
// method maps to a single gh invocation. ship depends on this interface so tests
// inject a fake without touching gh, exactly as implement / meta / fix do.
type GitHubClient interface {
	GetIssue(owner, name string, number int) (*github.Issue, error)
	GetIssueRelations(owner, name string, number int) (*github.IssueRelations, error)
	BlockedByIssues(owner, name string, number int) ([]github.Issue, error)
	AddIssueComment(owner, name string, number int, body string) (string, error)
	MarkPRReady(owner, name string, number int) error
	PRMergeability(owner, name string, number int) (*github.Mergeability, error)
	MergePR(owner, name string, number int, method, headSHA string) error
	CloseIssue(owner, name string, number int) error
}

// defaultGitHubClient is the production GitHubClient backed by the github
// package. Mirrors the implement/fix/meta adapter shape.
type defaultGitHubClient struct{}

func (defaultGitHubClient) GetIssue(owner, name string, number int) (*github.Issue, error) {
	return github.GetIssue(owner, name, number)
}

func (defaultGitHubClient) GetIssueRelations(owner, name string, number int) (*github.IssueRelations, error) {
	return github.GetIssueRelations(owner, name, number)
}

func (defaultGitHubClient) BlockedByIssues(owner, name string, number int) ([]github.Issue, error) {
	return github.BlockedByIssues(owner, name, number)
}

func (defaultGitHubClient) AddIssueComment(owner, name string, number int, body string) (string, error) {
	return github.AddIssueComment(owner, name, number, body)
}

func (defaultGitHubClient) MarkPRReady(owner, name string, number int) error {
	return github.MarkPRReady(owner, name, number)
}

func (defaultGitHubClient) PRMergeability(owner, name string, number int) (*github.Mergeability, error) {
	return github.PRMergeability(owner, name, number)
}

func (defaultGitHubClient) MergePR(owner, name string, number int, method, headSHA string) error {
	return github.MergePR(owner, name, number, method, headSHA)
}

func (defaultGitHubClient) CloseIssue(owner, name string, number int) error {
	return github.CloseIssue(owner, name, number)
}

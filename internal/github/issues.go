package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// SearchIssues searches for existing issues in the given repo whose title
// matches the query string. It returns matching issue titles and URLs.
func SearchIssues(owner, name, query string) ([]string, error) {
	repo := fmt.Sprintf("%s/%s", owner, name)
	// Quote the query and restrict search to title field for accurate matching
	search := fmt.Sprintf(`"%s" in:title`, query)
	args := []string{"issue", "list",
		"--repo", repo,
		"--search", search,
		"--state", "all",
		"--json", "title,url",
		"--template", `{{range .}}{{.title}}{{"\t"}}{{.url}}{{"\n"}}{{end}}`,
	}

	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh issue list: %s: %w", strings.TrimSpace(string(out)), err)
	}

	output := strings.TrimSpace(string(out))
	if output == "" {
		return nil, nil
	}
	return strings.Split(output, "\n"), nil
}

// CreateIssue creates a GitHub issue in the given repo via the gh CLI.
// It returns the URL of the created issue.
func CreateIssue(owner, name, title, body string) (string, error) {
	repo := fmt.Sprintf("%s/%s", owner, name)
	args := []string{"issue", "create",
		"--repo", repo,
		"--title", title,
		"--body", body,
	}

	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh issue create: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return strings.TrimSpace(string(out)), nil
}

// Issue is the minimal view of a GitHub issue used by the elaborate and
// prompt subcommands: enough to render it back to the user, identify it for
// updates, and feed it to Claude as context.
type Issue struct {
	Owner  string `json:"-"`
	Name   string `json:"-"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	URL    string `json:"url"`
	State  string `json:"state"`
}

var (
	// https://github.com/owner/repo/issues/123
	issueURLRe = regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/issues/(\d+)`)
	// owner/repo#123 — same shape as the PR shortRe but kept separate so
	// callers don't accidentally feed PR refs into issue tooling.
	issueShortRe = regexp.MustCompile(`^([^/]+)/([^#]+)#(\d+)$`)
)

// ParseIssueRef parses a GitHub issue reference in URL or short form
// (owner/repo#123). The same short form syntax used for PRs is accepted —
// `gh issue view` works against both PR and issue numbers, so callers can
// route either kind through the same flag without an extra qualifier.
func ParseIssueRef(ref string) (owner, repo string, number int, err error) {
	ref = strings.TrimSpace(ref)
	if m := issueURLRe.FindStringSubmatch(ref); m != nil {
		owner, repo = m[1], m[2]
		number, _ = strconv.Atoi(m[3])
		if err := validateOwnerRepo(owner, repo); err != nil {
			return "", "", 0, err
		}
		return owner, repo, number, nil
	}
	if m := issueShortRe.FindStringSubmatch(ref); m != nil {
		owner, repo = m[1], m[2]
		number, _ = strconv.Atoi(m[3])
		if err := validateOwnerRepo(owner, repo); err != nil {
			return "", "", 0, err
		}
		return owner, repo, number, nil
	}
	return "", "", 0, fmt.Errorf("invalid issue reference %q: expected URL (https://github.com/owner/repo/issues/123) or short form (owner/repo#123)", ref)
}

// GetIssue fetches the title, body, URL, and state of an issue via gh.
func GetIssue(owner, name string, number int) (*Issue, error) {
	repo := fmt.Sprintf("%s/%s", owner, name)
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "issue", "view", strconv.Itoa(number),
		"--repo", repo,
		"--json", "number,title,body,url,state")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh issue view: %s: %w", strings.TrimSpace(string(out)), err)
	}
	var iss Issue
	if err := json.Unmarshal(out, &iss); err != nil {
		return nil, fmt.Errorf("parsing gh issue view output: %w", err)
	}
	iss.Owner = owner
	iss.Name = name
	return &iss, nil
}

// EditIssueBody replaces the body of an existing issue. The body is passed
// via stdin so it is not subject to argv length limits or shell quoting.
func EditIssueBody(owner, name string, number int, body string) error {
	repo := fmt.Sprintf("%s/%s", owner, name)
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "issue", "edit", strconv.Itoa(number),
		"--repo", repo,
		"--body-file", "-")
	cmd.Stdin = strings.NewReader(body)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh issue edit: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// AddIssueComment posts a new comment on an issue. The body is passed via
// stdin so it is not subject to argv length limits or shell quoting.
func AddIssueComment(owner, name string, number int, body string) (string, error) {
	repo := fmt.Sprintf("%s/%s", owner, name)
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "issue", "comment", strconv.Itoa(number),
		"--repo", repo,
		"--body-file", "-")
	cmd.Stdin = strings.NewReader(body)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh issue comment: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

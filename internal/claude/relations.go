package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/github"
)

// renderIssueRelations writes the "## Meta / Sub-Issue Context" prompt section
// into sb when the issue being elaborated or planned has a Meta/Sub-Issue
// neighborhood — a parent Meta Issue (meta), the Meta Issue's other Sub Issues
// (siblings), or its own Sub Issues (children, present when the issue is itself
// a Meta Issue). It renders nothing when the issue stands alone, so the prompt
// for a plain issue is byte-for-byte unchanged.
//
// The section is the single source of the cross-issue planning guidance shared
// by buildElaboratePrompt and BuildPlanPrompt: it tells the session to scope to
// this issue's slice of the larger effort, honor the Meta Issue's framing, not
// duplicate work a sibling owns, and defer a shared task's remaining part to the
// sibling that carries it — with a concrete `#K` cross-reference. The guidance
// is deliberately artifact-agnostic ("where your output captures out-of-scope
// work") so it reads correctly for both the elaborate Non-Goals and the plan's
// Risks & Open Questions.
//
// repoFullName is the "owner/name" of the repository being planned. A related
// issue outside it is rendered as owner/name#N: in GitHub markdown a bare "#N"
// resolves against the repository the text ends up in, so an unqualified
// cross-repo reference silently points at a different issue.
func renderIssueRelations(sb *strings.Builder, repoFullName string, meta *github.Issue, siblings, children []github.Issue) {
	if meta == nil && len(children) == 0 {
		return
	}

	sb.WriteString("## Meta / Sub-Issue Context\n\n")

	if meta != nil {
		sb.WriteString("This issue is a **Sub Issue** of a Meta Issue — a larger effort split into work packages. Plan ONLY this issue's slice of that effort, grounded in the Meta Issue and the sibling Sub Issues below:\n\n")
		sb.WriteString("- Honor the Meta Issue's framing and shared decisions; do not re-litigate or contradict them.\n")
		sb.WriteString("- Do not duplicate or absorb work a sibling Sub Issue owns. When this issue implements only PART of a shared task because the remaining part lands in another Sub Issue, scope this issue to its part and cross-reference the sibling that carries the rest by number (e.g. \"the remaining X is handled by #K\") — record the deferral where your output captures out-of-scope work. Cross-reference a sibling in another repository by its full `owner/repo#K`, exactly as it is labelled below; a bare `#K` resolves against this repository and points at an unrelated issue.\n")
		sb.WriteString("- A closed sibling is already-implemented context you build on, not work to redo; an open sibling is work that may land in parallel or later, so coordinate rather than collide.\n")
		sb.WriteString("- A sibling Sub Issue may already carry an open pull request (listed under `<linked-prs>` in its block) — a prepared implementation not yet merged to the default branch. Treat that PR as the source of truth for the sibling's slice: build on its direction, do not duplicate or contradict it, and cross-reference it by the reference it is labelled with rather than re-implementing the work. A PR outside this repository is labelled `owner/repo#N`; cite it that way, since a bare `#N` resolves against this repository.\n\n")

		fmt.Fprintf(sb, "<meta-issue number=%d state=%s>\n", meta.Number, issueState(meta.State))
		fmt.Fprintf(sb, "**Meta Issue %s**: %s\n", promptIssueRef(repoFullName, *meta), meta.Title)
		writeIssueBody(sb, meta.Body)
		sb.WriteString("</meta-issue>\n\n")

		if len(siblings) > 0 {
			sb.WriteString("<sibling-sub-issues>\n")
			for _, s := range siblings {
				writeRelatedSubIssue(sb, "sibling", repoFullName, s)
			}
			sb.WriteString("</sibling-sub-issues>\n\n")
		} else {
			sb.WriteString("This Sub Issue has no siblings yet — it is currently the Meta Issue's only Sub Issue.\n\n")
		}
	}

	if len(children) > 0 {
		sb.WriteString("This issue is itself a **Meta Issue** — a larger effort split into the Sub Issues below. Plan it as the umbrella: keep each Sub Issue's slice in mind, do not absorb work a Sub Issue owns, and make sure the slices compose into the whole.\n\n")
		sb.WriteString("A Sub Issue listed below may already carry an open pull request (listed under `<linked-prs>` in its block) — a prepared implementation not yet merged to the default branch. Account for it when checking that the slices compose, and reference it by the reference it is labelled with (`owner/repo#N` for a PR outside this repository, where a bare `#N` would point at an unrelated one) instead of assuming the work is unstarted.\n\n")
		sb.WriteString("<child-sub-issues>\n")
		for _, c := range children {
			writeRelatedSubIssue(sb, "sub-issue", repoFullName, c)
		}
		sb.WriteString("</child-sub-issues>\n\n")
	}
}

// writeRelatedSubIssue writes one sibling or child Sub Issue block: a tagged
// element carrying the issue number and state, the title, and the full body so
// the session can read the Sub Issue's content, not just its title. A Sub Issue
// outside repoFullName is labelled with its full owner/repo#N, which is the form
// the session is told to cross-reference it by.
func writeRelatedSubIssue(sb *strings.Builder, tag, repoFullName string, issue github.Issue) {
	fmt.Fprintf(sb, "<%s number=%d state=%s>\n", tag, issue.Number, issueState(issue.State))
	fmt.Fprintf(sb, "**%s**: %s\n", promptIssueRef(repoFullName, issue), issue.Title)
	writeIssueBody(sb, issue.Body)
	writeLinkedPRs(sb, repoFullName, issue.LinkedPRs)
	fmt.Fprintf(sb, "</%s>\n", tag)
}

// writeLinkedPRs writes a <linked-prs> sub-block listing the open pull requests
// linked to a sibling or child Sub Issue, one metadata line each: the PR
// reference, the state ("draft" for a draft PR, otherwise the PR state), title,
// and URL. It writes nothing when the Sub Issue has no linked PRs, so a Sub Issue
// without prepared work renders byte-for-byte as before.
//
// A PR outside repoFullName is labelled with its full owner/repo#N — a closing
// keyword crosses repositories, so a Sub Issue's linked PR need not live where
// the Sub Issue does — which is the form the session is told to cite it by.
func writeLinkedPRs(sb *strings.Builder, repoFullName string, prs []github.LinkedPR) {
	if len(prs) == 0 {
		return
	}
	sb.WriteString("<linked-prs>\n")
	for _, pr := range prs {
		state := pr.State
		if pr.IsDraft {
			state = "draft"
		}
		fmt.Fprintf(sb, "- PR %s (%s): %s — %s\n", promptPRRef(repoFullName, pr), state, pr.Title, pr.URL)
	}
	sb.WriteString("</linked-prs>\n")
}

// writeIssueBody writes a trimmed issue body on its own lines, preceded by a
// blank line, or nothing when the body is empty.
func writeIssueBody(sb *strings.Builder, body string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	sb.WriteString("\n")
	sb.WriteString(body)
	sb.WriteString("\n")
}

// issueState returns the issue state for the prompt, falling back to "unknown"
// when GitHub did not report one (the GraphQL relations query always sets it,
// but a hand-built context may not).
func issueState(state string) string {
	if state == "" {
		return "unknown"
	}
	return state
}

// promptIssueRef renders a related issue the way the session must cite it: bare
// "#N" inside the repository being planned, and the full "owner/repo#N" outside
// it.
func promptIssueRef(repoFullName string, issue github.Issue) string {
	return promptRef(repoFullName, issue.Owner, issue.Name, issue.Number)
}

// promptPRRef renders a Sub Issue's linked pull request the way the session must
// cite it, by the same rule as promptIssueRef. The PR's own repository decides,
// not the Sub Issue's: a closing keyword works across repositories.
func promptPRRef(repoFullName string, pr github.LinkedPR) string {
	return promptRef(repoFullName, pr.Owner, pr.Name, pr.Number)
}

// promptRef renders an issue or pull request reference for the prompt: bare "#N"
// inside repoFullName, "owner/repo#N" outside it, because a bare "#N" resolves
// against the repository the session writes into. One whose coordinates are
// unknown is treated as local, since that is the only shape single-repo
// neighborhoods produce.
func promptRef(repoFullName, owner, name string, number int) string {
	if owner == "" || name == "" {
		return fmt.Sprintf("#%d", number)
	}
	full := owner + "/" + name
	if repoFullName == "" || strings.EqualFold(full, repoFullName) {
		return fmt.Sprintf("#%d", number)
	}
	return fmt.Sprintf("%s#%d", full, number)
}

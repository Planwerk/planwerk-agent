package ship

import (
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/attribution"
	"github.com/planwerk/planwerk-agent/internal/github"
)

// Merge methods ship can hand to gh pr merge. Rebase is the default: it replays
// the PR's curated commits onto the default branch, preserving the per-commit
// history the simplify/review fixup-folding produces and keeping history linear.
const (
	MergeRebase = "rebase"
	MergeSquash = "squash"
	MergeMerge  = "merge"
)

// Per-Sub-Issue outcome labels surfaced in the progress comments and the final
// summary. statusMerged / statusAlreadyDone / statusNothingToShip count as
// "delivered" and unblock dependents; the rest do not.
const (
	statusMerged        = "merged"
	statusAlreadyDone   = "already merged"
	statusNothingToShip = "nothing to ship"
	statusStopped       = "stopped at green CI"
	statusSkipped       = "skipped"
)

// Options configures a ship run.
type Options struct {
	IssueRef    string // the Meta Issue
	DryRun      bool   // report the planned order without cloning or calling Claude
	NoMerge     bool   // run the whole pipeline but stop at green CI, leaving merges to a human
	MergeMethod string // rebase | squash | merge (default rebase)
	StartAt     int    // Sub Issue number to begin from; 0 = from the top of the order
}

// Runner drives the Meta Issue's Sub Issues to merged using the injected
// implement pipeline, fix CI self-heal loop, and GitHub client. Tests inject
// fakes via the exported fields, exactly as implement / fix / meta do.
type Runner struct {
	GitHub    GitHubClient
	Implement ImplementFn
	Fix       FixFn
}

// NewRunner wires a Runner with the production GitHub backend and the given
// implement-run and fix-run closures. The CLI builds those closures from the
// shared Claude functions and the per-issue implement / fix options, keeping the
// import direction claude → implement/fix → ship.
func NewRunner(implementFn ImplementFn, fixFn FixFn) *Runner {
	return &Runner{
		GitHub:    defaultGitHubClient{},
		Implement: implementFn,
		Fix:       fixFn,
	}
}

// Run is a package-level convenience that delegates to NewRunner(...).Run.
func Run(w io.Writer, opts Options, implementFn ImplementFn, fixFn FixFn) error {
	return NewRunner(implementFn, fixFn).Run(w, opts)
}

// subResult records one Sub Issue's outcome for the final summary. done marks the
// "delivered" outcomes (merged, already merged, nothing to ship) that unblock the
// Sub Issues depending on it.
type subResult struct {
	number int
	title  string
	status string
	detail string
	done   bool
}

// Run executes the ship pipeline: resolve the Meta Issue, discover its Sub Issues
// and their native blocked_by dependencies, schedule them topologically, then walk
// the schedule driving each eligible Sub Issue through implement → mark ready →
// wait for CI → self-heal red CI → merge when green. A Sub Issue that cannot be
// finished is skipped together with everything transitively blocked by it; an
// already-closed Sub Issue is skipped as already delivered (so a re-run resumes
// naturally). Progress is narrated on the Meta Issue, and a final summary is
// posted; when every Sub Issue is delivered the Meta Issue is closed.
func (r *Runner) Run(w io.Writer, opts Options) error {
	if opts.MergeMethod == "" {
		opts.MergeMethod = MergeRebase
	}
	if err := validateMergeMethod(opts.MergeMethod); err != nil {
		return err
	}

	owner, name, number, err := github.ParseIssueRef(opts.IssueRef)
	if err != nil {
		return fmt.Errorf("parsing issue ref: %w", err)
	}
	fullName := fmt.Sprintf("%s/%s", owner, name)
	slog.Info("ship starting",
		"meta", fmt.Sprintf("%s#%d", fullName, number),
		"dry_run", opts.DryRun,
		"no_merge", opts.NoMerge,
		"merge_method", opts.MergeMethod,
		"start_at", opts.StartAt,
	)

	metaIssue, err := r.GitHub.GetIssue(owner, name, number)
	if err != nil {
		return fmt.Errorf("fetching meta issue: %w", err)
	}

	relations, err := r.GitHub.GetIssueRelations(owner, name, number)
	if err != nil {
		return fmt.Errorf("fetching sub-issue relations: %w", err)
	}
	children := relations.Children
	if len(children) == 0 {
		_, _ = fmt.Fprintf(w, "Meta Issue #%d (%s) has no Sub Issues; nothing to ship.\n", number, metaIssue.Title)
		return nil
	}

	edges, err := r.buildEdges(owner, name, children)
	if err != nil {
		return fmt.Errorf("reading sub-issue dependencies: %w", err)
	}
	order, err := Schedule(children, edges)
	if err != nil {
		return fmt.Errorf("scheduling sub-issues: %w", err)
	}

	if opts.StartAt != 0 && !nodeInOrder(order, opts.StartAt) {
		return fmt.Errorf("--start-at #%d is not a Sub Issue of Meta Issue #%d", opts.StartAt, number)
	}

	if opts.DryRun {
		renderPlan(w, fullName, number, opts, order)
		return nil
	}

	results := r.walk(w, owner, name, number, opts, order)
	r.postSummary(w, owner, name, number, opts, results)
	r.maybeCloseMeta(w, owner, name, number, opts, results)
	return nil
}

// buildEdges reads each Sub Issue's native blocked_by dependencies. A read
// failure aborts the whole run rather than degrading the Sub Issue to
// "unblocked": ship's contract is to respect merge order, and a transient 502 is
// indistinguishable from a repo that genuinely exposes no dependencies, so
// defaulting a failed read to "no blockers" could merge a dependent ahead of its
// blocker — onto a default branch missing the blocker's changes.
func (r *Runner) buildEdges(owner, name string, children []github.Issue) (map[int][]int, error) {
	edges := make(map[int][]int)
	for _, c := range children {
		blockers, err := r.GitHub.BlockedByIssues(owner, name, c.Number)
		if err != nil {
			return nil, fmt.Errorf("reading dependencies of #%d: %w", c.Number, err)
		}
		for _, b := range blockers {
			edges[c.Number] = append(edges[c.Number], b.Number)
		}
	}
	return edges, nil
}

// walk drives each Sub Issue in dependency order, propagating skips: a Sub Issue
// whose blocker did not get delivered is skipped, which keeps its own dependents
// from being delivered, so the skip cascades through the DAG.
func (r *Runner) walk(w io.Writer, owner, name string, metaNumber int, opts Options, order []SubNode) []subResult {
	delivered := make(map[int]bool)
	started := opts.StartAt == 0
	results := make([]subResult, 0, len(order))
	for _, node := range order {
		n := node.Issue.Number
		if n == opts.StartAt {
			started = true
		}

		// Resumption: a Sub Issue already closed (its merged PR closed it, or a
		// human closed it) is already delivered. Record it as delivered so its
		// dependents can still run, and skip straight past it.
		if isClosed(node.Issue) {
			delivered[n] = true
			results = append(results, subResult{n, node.Issue.Title, statusAlreadyDone, "Sub Issue already closed", true})
			slog.Info("sub-issue already closed; skipping", "issue", n)
			continue
		}

		if !started {
			results = append(results, subResult{n, node.Issue.Title, statusSkipped, "before --start-at", false})
			continue
		}

		if blocker, blocked := firstUnmetBlocker(node, delivered); blocked {
			detail := fmt.Sprintf("blocked by #%d, which was not delivered", blocker)
			r.postComment(w, owner, name, metaNumber, fmt.Sprintf("Skipping Sub Issue #%d (%s): %s.", n, node.Issue.Title, detail))
			results = append(results, subResult{n, node.Issue.Title, statusSkipped, detail, false})
			continue
		}

		res := r.processSubIssue(w, owner, name, metaNumber, opts, node)
		results = append(results, res)
		if res.done {
			delivered[n] = true
		}
	}
	return results
}

// processSubIssue runs the per–Sub Issue pipeline and returns its outcome. Every
// failure path skips the Sub Issue (res.done false) so the walk does not deliver
// it and its dependents are skipped in turn.
func (r *Runner) processSubIssue(w io.Writer, owner, name string, metaNumber int, opts Options, node SubNode) subResult {
	n := node.Issue.Number
	title := node.Issue.Title
	subRef := fmt.Sprintf("%s/%s#%d", owner, name, n)
	skip := func(detail string) subResult {
		r.postComment(w, owner, name, metaNumber, fmt.Sprintf("Skipping Sub Issue #%d (%s) and everything blocked by it: %s.", n, title, detail))
		return subResult{n, title, statusSkipped, detail, false}
	}

	r.postComment(w, owner, name, metaNumber, fmt.Sprintf("Picking up Sub Issue #%d (%s).", n, title))
	slog.Info("shipping sub-issue", "issue", n)

	if err := r.Implement(w, subRef); err != nil {
		return skip(fmt.Sprintf("implement did not finish: %v", err))
	}

	prNumber, err := r.findOpenPR(owner, name, metaNumber, n)
	if err != nil {
		return skip(fmt.Sprintf("could not re-read relations to find the opened PR: %v", err))
	}
	if prNumber == 0 {
		detail := "implement opened no pull request (empty change set or already implemented)"
		r.postComment(w, owner, name, metaNumber, fmt.Sprintf("Sub Issue #%d (%s): %s; treating it as delivered.", n, title, detail))
		return subResult{n, title, statusNothingToShip, detail, true}
	}
	prRef := fmt.Sprintf("%s/%s#%d", owner, name, prNumber)

	if err := r.GitHub.MarkPRReady(owner, name, prNumber); err != nil {
		return skip(fmt.Sprintf("could not mark PR #%d ready: %v", prNumber, err))
	}

	// fix.Run waits for the PR's checks and self-heals red CI up to the fix
	// budget; a returned error means the checks could not be made green.
	if err := r.Fix(w, prRef); err != nil {
		return skip(fmt.Sprintf("CI on PR #%d did not go green: %v", prNumber, err))
	}

	if opts.NoMerge {
		detail := fmt.Sprintf("--no-merge: PR #%d is green; left for a human to merge", prNumber)
		r.postComment(w, owner, name, metaNumber, fmt.Sprintf("Sub Issue #%d (%s): %s.", n, title, detail))
		return subResult{n, title, statusStopped, detail, false}
	}

	mg, err := r.GitHub.PRMergeability(owner, name, prNumber)
	if err != nil {
		return skip(fmt.Sprintf("could not query mergeability of PR #%d: %v", prNumber, err))
	}
	if !mg.CanMerge() {
		return skip(fmt.Sprintf("PR #%d is not mergeable (branch protection, a required review, or a conflict blocks it); not forcing the merge", prNumber))
	}

	// Pin the merge to the exact head SHA this mergeability check just vetted, so
	// a commit pushed to the PR's head branch after the fix loop went green (most
	// realistic on a fork PR) cannot ride in: GitHub rejects the merge when HEAD
	// has moved off the validated SHA.
	if err := r.GitHub.MergePR(owner, name, prNumber, opts.MergeMethod, mg.HeadSHA); err != nil {
		return skip(fmt.Sprintf("merging PR #%d failed: %v", prNumber, err))
	}

	detail := fmt.Sprintf("PR #%d merged with --%s", prNumber, opts.MergeMethod)
	r.postComment(w, owner, name, metaNumber, fmt.Sprintf("Merged Sub Issue #%d (%s): %s.", n, title, detail))
	slog.Info("merged sub-issue", "issue", n, "pr", prNumber)
	return subResult{n, title, statusMerged, detail, true}
}

// findOpenPR re-reads the Meta Issue's relations to find the open pull request
// the just-run implement pipeline opened for the Sub Issue (the PR that closes
// it). It accepts only a PR authored by the authenticated account: the closed-by
// link is created by any "Closes #N" reference, so an unrelated or attacker
// opened PR (a fork PR on a public repo) would otherwise be picked up, undrafted,
// and merged to the default branch. Returns (0, nil) when no such PR is linked
// yet — an empty change set — which the caller treats as "nothing to ship". A
// relations read failure is returned as an error so the caller skips the Sub
// Issue rather than mistaking a transient 502 for "nothing to ship".
func (r *Runner) findOpenPR(owner, name string, metaNumber, subNumber int) (int, error) {
	relations, err := r.GitHub.GetIssueRelations(owner, name, metaNumber)
	if err != nil {
		return 0, fmt.Errorf("re-reading meta issue relations: %w", err)
	}
	if relations.Viewer == "" {
		return 0, fmt.Errorf("could not resolve the authenticated account to verify pull-request authorship")
	}
	for _, c := range relations.Children {
		if c.Number != subNumber {
			continue
		}
		for _, pr := range c.LinkedPRs {
			if pr.State != "open" && pr.State != "" {
				continue
			}
			if !strings.EqualFold(pr.Author, relations.Viewer) {
				slog.Warn("ignoring linked PR not authored by the authenticated account",
					"issue", subNumber, "pr", pr.Number, "pr_author", pr.Author, "viewer", relations.Viewer)
				continue
			}
			return pr.Number, nil
		}
	}
	return 0, nil
}

// maybeCloseMeta closes the Meta Issue once every Sub Issue has been delivered.
// It is skipped under --no-merge (nothing was merged, so nothing is delivered)
// and never runs in dry-run mode (Run returns before the walk). A close failure
// is logged and surfaced, not fatal — the Sub Issues are already merged.
func (r *Runner) maybeCloseMeta(w io.Writer, owner, name string, number int, opts Options, results []subResult) {
	if opts.NoMerge {
		return
	}
	for _, res := range results {
		if !res.done {
			return
		}
	}
	if err := r.GitHub.CloseIssue(owner, name, number); err != nil {
		slog.Warn("could not close meta issue after delivering every sub-issue", "issue", number, "err", err)
		_, _ = fmt.Fprintf(w, "\nEvery Sub Issue delivered, but closing Meta Issue #%d failed: %v\n", number, err)
		return
	}
	slog.Info("closed meta issue; every sub-issue delivered", "issue", number)
	_, _ = fmt.Fprintf(w, "\nEvery Sub Issue delivered; closed Meta Issue #%d.\n", number)
}

// firstUnmetBlocker returns the first of node's blockers that has not been
// delivered, and whether such a blocker exists.
func firstUnmetBlocker(node SubNode, delivered map[int]bool) (int, bool) {
	for _, b := range node.BlockedBy {
		if !delivered[b] {
			return b, true
		}
	}
	return 0, false
}

// isClosed reports whether the Sub Issue is already closed (delivered before this
// run), matching GetIssue's lowercase state convention case-insensitively.
func isClosed(issue github.Issue) bool {
	return strings.EqualFold(issue.State, "closed")
}

// nodeInOrder reports whether number names a Sub Issue in the schedule.
func nodeInOrder(order []SubNode, number int) bool {
	for _, n := range order {
		if n.Issue.Number == number {
			return true
		}
	}
	return false
}

// validateMergeMethod rejects a merge method gh pr merge does not accept.
func validateMergeMethod(method string) error {
	switch method {
	case MergeRebase, MergeSquash, MergeMerge:
		return nil
	default:
		return fmt.Errorf("unknown merge method %q: want %s, %s, or %s", method, MergeRebase, MergeSquash, MergeMerge)
	}
}

// renderPlan prints the planned ship order for a --dry-run, without cloning or
// calling Claude. Each Sub Issue is shown with its in-Meta blockers so the
// dependency order is legible.
func renderPlan(w io.Writer, fullName string, metaNumber int, opts Options, order []SubNode) {
	_, _ = fmt.Fprintf(w, "[dry-run] ship plan for Meta Issue %s#%d (merge method: %s)\n", fullName, metaNumber, opts.MergeMethod)
	for i, n := range order {
		line := fmt.Sprintf("  %d. #%d %s", i+1, n.Issue.Number, n.Issue.Title)
		if len(n.BlockedBy) > 0 {
			line += fmt.Sprintf(" (blocked by %s)", joinNumbers(n.BlockedBy))
		}
		if isClosed(n.Issue) {
			line += " [already closed]"
		}
		_, _ = fmt.Fprintln(w, line)
	}
}

// postSummary prints and posts the final per–Sub Issue summary on the Meta Issue.
func (r *Runner) postSummary(w io.Writer, owner, name string, number int, opts Options, results []subResult) {
	var b strings.Builder
	fmt.Fprintf(&b, "## Ship summary for Meta Issue #%d\n\n", number)
	for _, res := range results {
		fmt.Fprintf(&b, "- #%d %s — **%s**", res.number, res.title, res.status)
		if res.detail != "" {
			fmt.Fprintf(&b, " (%s)", res.detail)
		}
		b.WriteString("\n")
	}
	summary := b.String()
	_, _ = fmt.Fprintf(w, "\n%s\n", summary)
	r.postComment(w, owner, name, number, summary)
}

// postComment narrates progress on the Meta Issue, footered to mark the comment
// as tool-authored. Posting is best-effort: a failure is logged and surfaced but
// never aborts the run — the work is already done and the next step can proceed.
func (r *Runner) postComment(w io.Writer, owner, name string, number int, body string) {
	full := strings.TrimSpace(body) + "\n\n---\n\n_Ship progress reported by " + attribution.Tool() + "_\n"
	if _, err := r.GitHub.AddIssueComment(owner, name, number, full); err != nil {
		slog.Warn("posting ship comment failed", "issue", number, "err", err)
		_, _ = fmt.Fprintf(w, "\nCould not post ship comment on #%d: %v\n", number, err)
	}
}

// joinNumbers renders a list of issue numbers as "#a, #b" for the dry-run plan.
func joinNumbers(numbers []int) string {
	parts := make([]string, len(numbers))
	for i, n := range numbers {
		parts[i] = fmt.Sprintf("#%d", n)
	}
	return strings.Join(parts, ", ")
}

package capture

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/planwerk/planwerk-review/internal/attribution"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/report"
	"github.com/planwerk/planwerk-review/internal/sync"
	"github.com/planwerk/planwerk-review/internal/workspace"
)

// Proposer runs the read-only knowledge-proposal pass for a checkout: it mines
// the candidate findings, plan, and report for review patterns and memory
// pages, authoring candidate page bytes without writing anything. *claude.Client
// satisfies it; capture depends on this interface rather than the claude package
// so the import direction stays claude -> capture.
type Proposer interface {
	Capture(dir string, ctx CaptureContext) (*CaptureResult, error)
}

// CommentPoster posts the rendered capture proposals as a comment and returns
// the comment URL. Each command supplies its own poster (implement/review post
// to the source issue/PR); audit has nowhere to comment and passes nil, which
// skips the comment without disabling the rest of the pass.
type CommentPoster func(body string) (url string, err error)

// Request carries the per-run inputs for one capture pass: the checkout to mine,
// the source command and repo for the report and provenance, the findings and
// (for implement) the plan/report to propose from, the loaded pattern catalog
// and resolved wiki to deduplicate against, and the write-back gate.
type Request struct {
	// Dir is the checkout the read-only proposal pass runs inside.
	Dir string
	// Command names the source command ("implement", "review", "audit") for the
	// comment footer and log lines.
	Command string
	// Repo is "owner/repo" for the report header and the provenance marker.
	Repo string
	// Number is the source issue/PR number recorded in the provenance marker;
	// 0 for audit, which has no issue or PR behind it.
	Number int
	// BaseBranch scopes the change set the proposal prompt reasons about; empty
	// (audit) falls back to the repository default branch.
	BaseBranch string
	// Findings are the review findings to mine for candidate patterns, pre-filtered
	// by CandidateFindings before the proposal pass.
	Findings []report.Finding
	// Plan and Report are the implementation plan and report mined for durable
	// memory pages; both empty for review/audit, which have neither.
	Plan   string
	Report string
	// Patterns is the loaded review-pattern catalog, a dedup target.
	Patterns []patterns.Pattern
	// Wiki is the resolved target wiki: its Dir is the dedup source and a non-empty
	// Dir is the precondition the caller gates on; Repo/CommitSHA fill the header.
	Wiki patterns.ResolvedWiki
	// WikiRef is the ref the gated write-back clones for the push.
	WikiRef string
	// CaptureWiki gates the write-back; Yes skips its confirmation prompt.
	CaptureWiki bool
	Yes         bool
	// Version names the build in the rendered report footer.
	Version string
}

// Pass is the shared capture orchestrator that drives one propose-then-optionally-write
// pass for implement, review, and audit. It holds the per-runner seams (the
// proposer, the optional comment poster, and the write-back's clone/confirm
// seams); Run takes the per-run Request. Sharing one implementation keeps the
// mechanism in internal/capture so the wiki grows from every findings-producing
// run without each command duplicating the glue.
type Pass struct {
	// Propose runs the read-only proposal pass. Required.
	Propose Proposer
	// PostComment posts the rendered proposals as a comment; nil skips the comment
	// (audit has no PR/issue to post to).
	PostComment CommentPoster
	// Writer performs the gated write-back's clone+add+push. Defaults to
	// DefaultWikiWriter when nil.
	Writer WikiWriter
	// In is the stream the write-back confirmation reads from. Defaults to os.Stdin.
	In io.Reader
	// IsTTY reports whether the write-back may prompt interactively. Defaults to
	// workspace.IsStdinTTY.
	IsTTY func() bool
}

// Run executes one capture pass: read the wiki entries, pre-filter the findings,
// run the read-only proposal pass, and — when it proposes something — render the
// proposals to w, post them as a comment (when a poster is supplied), and run the
// gated write-back (when req.CaptureWiki is set).
//
// The propose half is non-fatal: any failure (reading the wiki, the proposal
// call, the comment post) is logged and surfaced to w but returns nil, so a
// propose-only capture failure never aborts the surrounding command. No proposals
// is a clean no-op beyond a short note. The explicitly-requested write-back
// (req.CaptureWiki) is the exception: a failed clone/push is returned as an error
// so an outward-facing --capture-wiki write that left the wiki unchanged surfaces
// as a non-zero exit instead of a green no-op. The caller gates this on a resolved
// wiki and a non-nil proposer; Run does not re-check those.
func (p Pass) Run(w io.Writer, req Request) error {
	slog.Info("running capture pass", "command", req.Command, "repo", req.Repo)

	entries, err := sync.ReadWikiEntries(req.Wiki.Dir)
	if err != nil {
		slog.Warn("capture could not read wiki entries; skipping", "err", err)
		_, _ = fmt.Fprintf(w, "\nCapture pass skipped: could not read the wiki entries: %v\n", err)
		return nil
	}

	candidates := CandidateFindings(req.Findings, req.Patterns)

	result, err := p.Propose.Capture(req.Dir, CaptureContext{
		RepoName:        req.Repo,
		IssueNumber:     req.Number,
		BaseBranch:      req.BaseBranch,
		Findings:        candidates,
		Plan:            req.Plan,
		ImplementReport: req.Report,
		Entries:         entries,
		Patterns:        req.Patterns,
	})
	if err != nil {
		slog.Warn("capture pass failed", "err", err)
		_, _ = fmt.Fprintf(w, "\nCapture pass could not run: %v\n", err)
		return nil
	}
	// The Proposer contract permits (nil, nil) — production never returns it, but
	// a stubbed or future seam might, and dereferencing it below would crash the
	// surrounding command after its primary work is done.
	if result == nil {
		slog.Warn("capture returned no result; skipping", "command", req.Command)
		_, _ = fmt.Fprintln(w, "\nCapture proposed no new review patterns or memory pages.")
		return nil
	}
	result.WikiRepo = req.Wiki.Repo
	result.WikiCommit = req.Wiki.CommitSHA

	if !result.HasProposals() {
		slog.Info("capture proposed nothing", "command", req.Command)
		_, _ = fmt.Fprintln(w, "\nCapture proposed no new review patterns or memory pages.")
		return nil
	}

	prov := Provenance{Repo: req.Repo, Issue: req.Number}
	var rendered bytes.Buffer
	NewRenderer(&rendered).RenderMarkdown(*result, prov, req.Version)
	_, _ = fmt.Fprintf(w, "\nCaptured knowledge proposals:\n%s\n", rendered.String())

	// Post the proposals as a comment so they surface alongside the run's other
	// reports — propose-only, the wiki was not touched. Best-effort; audit skips
	// it (nil poster).
	if p.PostComment != nil {
		p.postComment(w, req.Command, rendered.String(), result.Model)
	}

	// Gated write-back: under --capture-wiki, push the accepted pages to the wiki.
	// Default off leaves the run propose-only. A failed write of an explicitly
	// requested push is returned (fatal) rather than swallowed, so a CI
	// --capture-wiki run that wrote nothing fails loudly instead of exiting green.
	if req.CaptureWiki {
		if err := p.runWrite(w, req, result, prov); err != nil {
			return err
		}
	}
	slog.Info("capture pass complete", "command", req.Command)
	return nil
}

// postComment posts the rendered proposals via the supplied poster, wrapping them
// in the command-specific comment body. Best-effort: a failure to reach GitHub is
// logged and surfaced but never aborts the run — the proposals are already on w.
func (p Pass) postComment(w io.Writer, command, reportBody, model string) {
	url, err := p.PostComment(formatComment(reportBody, command, model))
	if err != nil {
		slog.Warn("posting capture comment failed", "err", err)
		_, _ = fmt.Fprintf(w, "\nCould not post the capture proposals as a comment: %v\n", err)
		return
	}
	slog.Info("posted capture proposals comment", "url", url)
	_, _ = fmt.Fprint(w, "\nPosted the capture proposals as a comment")
	if url != "" {
		_, _ = fmt.Fprintf(w, " (%s)", url)
	}
	_, _ = fmt.Fprintln(w)
}

// runWrite performs the gated, opt-in write half of the pass under --capture-wiki:
// it pushes the accepted proposal pages to the wiki, authored by the read-only
// proposal pass. Claude never pushes — it authored the page bytes in the read-only
// harness, and this separate phase performs the mechanical add+commit+push.
//
// Unlike the propose half this write is explicitly requested, so a write failure —
// a non-TTY refusal without --yes, or a clone/push error — is logged and surfaced
// to w AND returned, so a --capture-wiki push that failed surfaces as a non-zero
// exit rather than a green build that left the wiki unchanged. A user who declines
// the confirmation or a run with nothing to write is not a failure: WritePhase
// returns nil for those, so they stay exit 0.
func (p Pass) runWrite(w io.Writer, req Request, result *CaptureResult, prov Provenance) error {
	writer := p.Writer
	if writer == nil {
		writer = DefaultWikiWriter{}
	}
	in := p.In
	if in == nil {
		in = os.Stdin
	}
	isTTY := p.IsTTY
	if isTTY == nil {
		isTTY = workspace.IsStdinTTY
	}
	if err := WritePhase(w, in, isTTY, req.Yes, writer, result, prov, req.WikiRef); err != nil {
		slog.Warn("capture write-back failed", "command", req.Command, "err", err)
		_, _ = fmt.Fprintf(w, "\nCapture write-back could not run: %v\n", err)
		return fmt.Errorf("capture write-back: %w", err)
	}
	return nil
}

// commentFooter attributes the posted capture proposals to planwerk-review,
// naming the source command and the model that produced them, matching the footer
// the implement/review comments append.
func commentFooter(command, model string) string {
	return "_Capture proposals generated by " + attribution.Tool() + " " + command + " " + attribution.AssistantWith(model) + "_"
}

// formatComment wraps the rendered capture proposals in the comment body: the
// report verbatim (it already carries its own "# Captured Knowledge" heading)
// followed by the attribution footer.
func formatComment(reportBody, command, model string) string {
	return strings.TrimSpace(reportBody) + "\n\n---\n\n" + commentFooter(command, model) + "\n"
}

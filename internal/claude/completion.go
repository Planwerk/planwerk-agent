package claude

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// maxCompletionNudges bounds the follow-up turns runWithCompletionNudge spends
// on a session that ended without its terminal report. Two attempts cover the
// dominant failure — a session that yielded to "wait" for a backgrounded test
// run needs one resumed turn to finish the verification and emit the report,
// and occasionally a second when that turn is itself interrupted — while
// keeping a session that structurally refuses to report from looping.
const maxCompletionNudges = 2

// Status vocabularies rendered into the completion nudge, matching each
// session's report contract. Only the implement report knows PARTIAL (a
// genuinely interrupted multi-package implementation); every other mutating
// session escalates with BLOCKED / NEEDS_CONTEXT.
const (
	reportStatusChoices          = report.StatusDone + " | " + report.StatusDoneWithConcerns + " | " + report.StatusBlocked + " | " + report.StatusNeedsContext
	implementReportStatusChoices = report.StatusDone + " | " + report.StatusDoneWithConcerns + " | " + report.StatusPartial + " | " + report.StatusBlocked + " | " + report.StatusNeedsContext
)

// runSessionFn is the seam runWithCompletionNudge runs its sessions through.
// It defaults to the production runner; the completion tests swap it for a
// fake that scripts session outputs without invoking the claude CLI, mirroring
// the streamSinkFn override pattern.
var runSessionFn = (*Client).runClaudeWithPermission

// terminalReportComplete returns the completion gate for a session whose
// contract is a terminal report: the output must carry the session's report
// heading AND a terminal STATUS line (report.TerminalStatus). A session that
// yielded mid-work — "waiting for the tests to finish", a bare summary —
// fails the gate, which is exactly the shape the completion nudge repairs.
func terminalReportComplete(heading string) func(string) bool {
	return func(out string) bool {
		return strings.Contains(out, heading) && report.TerminalStatus(out) != ""
	}
}

// completionNudgePrompt is the follow-up turn sent to a resumed session that
// ended without its terminal report. The session keeps its full context, so
// the prompt does not restate the task; it names the one thing the previous
// turn got wrong (no report), warns that backgrounded commands died with that
// turn, and demands the report — honest escalation included — as the turn's
// final output.
func completionNudgePrompt(heading, statuses string) string {
	return fmt.Sprintf(`Your previous turn ended WITHOUT the mandatory terminal report: the orchestrator found neither the %q heading nor a terminal STATUS line, so it treats the session as unfinished and will discard the run unless this turn completes it.

Any command you had running in the background was KILLED when that turn ended — its result or notification will never arrive. Do this now, in this turn:

1. Re-check the state of your work (git status, git log): confirm what is committed and what is not.
2. Re-run whatever verification was still outstanding, in the FOREGROUND, and wait for it to finish. If a command genuinely outlives the Bash tool's foreground time limit, run it in the background and poll its output from within this same turn until it exits — NEVER end the turn to "wait" for it.
3. Commit any finished but uncommitted work.
4. End the turn with the complete report: it MUST carry the %q heading and MUST end with a terminal "STATUS: <%s>" line. If work remains that you cannot finish in this turn, report it honestly with the appropriate escalation status instead of waiting for anything — an honest escalation is accepted; a turn without the report is not.

Output the report as the last thing in this turn, with nothing after it.`, heading, heading, statuses)
}

// runWithCompletionNudge runs a session whose contract is a terminal report
// (heading + STATUS line) and, when the session ends without one, resumes the
// very same session — full context intact — with a completion nudge, up to
// maxCompletionNudges times. The session's id is pinned upfront via
// --session-id so the follow-up turn can find it with --resume.
//
// The nudge only repairs the yielded-mid-work shape (prose without a report);
// a genuine run error — a hit rate limit, an exhausted turn budget — is
// returned unchanged, since a follow-up turn cannot fix an API failure. A
// nudge turn that itself errors returns the previous incomplete output with a
// nil error: the caller's own report gate decides what an incomplete output
// means (the implement orchestrator persists it as resumable progress), which
// beats discarding the session's account behind an error. When the nudges are
// exhausted the last output is likewise returned for the caller's gate.
func (c *Client) runWithCompletionNudge(spec runSpec, prompt, heading, statuses string) (string, string, error) {
	complete := terminalReportComplete(heading)
	spec.sessionID = newSessionID()
	out, model, err := runSessionFn(c, spec, prompt)
	if err != nil || complete(out) || spec.sessionID == "" {
		return out, model, err
	}
	nudge := completionNudgePrompt(heading, statuses)
	for attempt := 1; attempt <= maxCompletionNudges; attempt++ {
		slog.Warn("session ended without its terminal report; resuming it with a completion nudge",
			"label", spec.label, "attempt", attempt, "heading", heading)
		resumed := spec
		resumed.resume = true
		nudgedOut, nudgedModel, nudgeErr := runSessionFn(c, resumed, nudge)
		if nudgeErr != nil {
			slog.Warn("completion nudge failed; returning the incomplete output", "label", spec.label, "err", nudgeErr)
			return out, model, nil
		}
		out = nudgedOut
		if nudgedModel != "" {
			model = nudgedModel
		}
		if complete(out) {
			return out, model, nil
		}
	}
	slog.Warn("session still has no terminal report after the completion nudges; returning its last output",
		"label", spec.label, "nudges", maxCompletionNudges)
	return out, model, nil
}

// runClaudeAutoReport is runClaudeAuto for a mutating session whose contract
// is a terminal report: same auto permission mode and main model, plus the
// completion nudge that resumes the session when it ends without the report.
// The simplify-apply, review-apply, finalize, and fix sessions run through it;
// the implement session builds its own spec (model override, --agents) and
// calls runWithCompletionNudge directly. Sessions without a heading + STATUS
// contract (address returns schema-validated JSON with its own repair
// recovery, the rebase sessions return free-form text) stay on runClaudeAuto.
func (c *Client) runClaudeAutoReport(dir, prompt, label, heading, statuses string) (string, string, error) {
	return c.runWithCompletionNudge(c.autoSpec(dir, label), prompt, heading, statuses)
}

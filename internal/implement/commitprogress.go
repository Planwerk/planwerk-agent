package implement

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/planwerk/planwerk-agent/internal/github"
)

// defaultCommitPollInterval is how often the commit watcher lists the branch's
// commits. One `git log` every few seconds costs nothing next to a session
// that runs for minutes, and a commit is reported soon enough to read as live.
const defaultCommitPollInterval = 5 * time.Second

// commitSequenceHeading is the title of the plan section that lists the
// planned commits, one numbered item each ("### Commit Sequence"). It mirrors
// the plan format in claude.BuildPlanPrompt, which this package cannot import;
// TestPlanFormatCommitSequenceParses in the claude package fails when the two
// drift apart.
const commitSequenceHeading = "Commit Sequence"

// PlannedCommits returns the subjects of the commits an implementation plan
// lists under its Commit Sequence, in order: the text of each numbered item up
// to the dash that starts its explanation. It returns nil for a plan without
// the section or without numbered items in it (no plan at all, or an issue the
// plan found already implemented), which the commit progress reads as "total
// unknown".
//
// Only the items at the indentation of the first one count, so a numbered list
// nested under an item is not taken for further commits, and a fenced block
// inside the section is skipped.
func PlannedCommits(plan string) []string {
	var subjects []string
	inSection, inFence := false, false
	indent := -1
	for _, line := range strings.Split(plan, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if title, ok := strings.CutPrefix(strings.TrimLeft(t, "#"), " "); ok && strings.HasPrefix(t, "#") {
			inSection = strings.EqualFold(strings.TrimSpace(title), commitSequenceHeading)
			continue
		}
		if !inSection || t == "" || t[0] < '0' || t[0] > '9' {
			continue
		}
		item, ok := listItem(t)
		if !ok {
			continue
		}
		lead := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent < 0 {
			indent = lead
		}
		if lead == indent {
			subjects = append(subjects, plannedSubject(item))
		}
	}
	return subjects
}

// plannedSubject cuts a Commit Sequence item down to the commit subject: the
// part before the dash the plan format puts between subject and explanation,
// without the bold, code, or quote marks a model wraps a subject in.
func plannedSubject(item string) string {
	for _, sep := range []string{" — ", " – ", " -- "} {
		if before, _, ok := strings.Cut(item, sep); ok {
			item = before
			break
		}
	}
	item = strings.TrimSpace(item)
	for _, wrap := range []string{"**", "`", `"`} {
		if len(item) > 2*len(wrap) && strings.HasPrefix(item, wrap) && strings.HasSuffix(item, wrap) {
			item = strings.TrimSpace(item[len(wrap) : len(item)-len(wrap)])
		}
	}
	return item
}

// lockedWriter serializes the writes of the run and of its commit watcher,
// which report to the same stream from two goroutines. Every status line is
// one Write, so the lock keeps lines whole.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// commitWatcher reports every new commit on the checked-out branch while a run
// is in progress: its position among the planned commits, its subject and
// body, and the commit the plan lists next. It reads git, not the session, so
// a commit counts whoever made it: the implement session, one of its
// subagents, a later pass, or the operator in a --local checkout.
//
// A commit is identified by author date and subject, not by SHA. An amend and
// the autosquash rebases of the simplify and review passes rewrite commits
// under new SHAs but keep both, so a rewritten commit is not reported twice.
// A fixup!, squash!, or amend! commit is reported without a position: it is
// folded into the commit it names and is no step of the plan.
//
// The position is a count, not a match: the Nth commit on the branch is
// reported as N of the planned total and the plan's item N+1 as next. The
// session may split, reword, or combine the planned commits (decision 61), so
// both say how far the branch is, not which plan item a commit delivers.
type commitWatcher struct {
	w    io.Writer
	list func() ([]github.BranchCommit, error)

	mu      sync.Mutex
	planned []string // subjects of the plan's Commit Sequence; nil = total unknown
	seen    map[string]bool
	warned  bool

	quit chan struct{}
	done chan struct{}
}

// startCommitWatcher records the commits already on the branch as known,
// prints where the branch stands against planned when the plan names its
// commits, and polls list every interval (defaultCommitPollInterval when <= 0)
// until stop. w must be safe for concurrent use.
func startCommitWatcher(w io.Writer, list func() ([]github.BranchCommit, error), planned []string, interval time.Duration) *commitWatcher {
	if interval <= 0 {
		interval = defaultCommitPollInterval
	}
	cw := &commitWatcher{
		w:       w,
		list:    list,
		planned: planned,
		seen:    map[string]bool{},
		quit:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	commits, _ := cw.commits()
	counted := 0
	for _, c := range commits {
		cw.seen[commitKey(c)] = true
		if !isFixupCommit(c.Subject) {
			counted++
		}
	}
	if total := len(planned); total > 0 {
		_, _ = fmt.Fprintf(w, "\nCommit progress: %d/%d planned commits on the branch.\n%s", counted, total, cw.nextLine(counted))
	}
	go func() {
		defer close(cw.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-cw.quit:
				return
			case <-ticker.C:
				cw.poll()
			}
		}
	}()
	return cw
}

// sessionDone reports the commits the implement session made last and then
// drops the plan: a commit a later pass adds is still reported, but as the
// branch's Nth commit, not as a step of a plan the session has finished with.
// A nil watcher is a no-op.
func (cw *commitWatcher) sessionDone() {
	if cw == nil {
		return
	}
	cw.poll()
	cw.mu.Lock()
	cw.planned = nil
	cw.mu.Unlock()
}

// stop ends the polling and reports whatever was committed since the last
// tick. A nil watcher is a no-op.
func (cw *commitWatcher) stop() {
	if cw == nil {
		return
	}
	close(cw.quit)
	<-cw.done
	cw.poll()
}

// commits lists the branch's commits. A failure is logged once as a warning
// and afterwards at debug level: the watcher is a convenience and never fails
// the run, and a checkout whose base does not resolve fails on every tick.
func (cw *commitWatcher) commits() ([]github.BranchCommit, bool) {
	commits, err := cw.list()
	if err == nil {
		return commits, true
	}
	if cw.warned {
		slog.Debug("commit progress: could not list the branch's commits", "err", err)
		return nil, false
	}
	cw.warned = true
	slog.Warn("commit progress: could not list the branch's commits; new commits may go unreported", "err", err)
	return nil, false
}

// poll reports the commits that appeared since the last poll, oldest first, in
// one write.
func (cw *commitWatcher) poll() {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	commits, ok := cw.commits()
	if !ok {
		return
	}
	total := len(cw.planned)
	var sb strings.Builder
	counted, progressed := 0, false
	for _, c := range commits {
		fixup := isFixupCommit(c.Subject)
		if !fixup {
			counted++
		}
		key := commitKey(c)
		if cw.seen[key] {
			continue
		}
		cw.seen[key] = true
		switch {
		case fixup:
			sb.WriteString("\nFixup commit: ")
		case total == 0:
			fmt.Fprintf(&sb, "\nCommit %d: ", counted)
		case counted > total:
			fmt.Fprintf(&sb, "\nCommit %d (plan: %d): ", counted, total)
		default:
			fmt.Fprintf(&sb, "\nCommit %d/%d: ", counted, total)
		}
		fmt.Fprintf(&sb, "%s %s\n", shortSHA(c.SHA), c.Subject)
		if c.Body != "" {
			sb.WriteString("\n")
			for _, line := range strings.Split(c.Body, "\n") {
				sb.WriteString(strings.TrimRight("    "+line, " \t\r") + "\n")
			}
			sb.WriteString("\n")
		}
		progressed = progressed || !fixup
	}
	if sb.Len() == 0 {
		return
	}
	if progressed {
		sb.WriteString(cw.nextLine(counted))
	}
	_, _ = io.WriteString(cw.w, sb.String())
}

// nextLine names the commit the plan lists after the counted ones, says that
// every planned commit is there, or is empty when the plan names no commits or
// the branch already carries more than it planned.
func (cw *commitWatcher) nextLine(counted int) string {
	total := len(cw.planned)
	switch {
	case total == 0 || counted > total:
		return ""
	case counted == total:
		return fmt.Sprintf("All %d planned commits are on the branch.\n", total)
	}
	return fmt.Sprintf("Next per plan (%d/%d): %s\n", counted+1, total, cw.planned[counted])
}

// commitKey identifies a commit across rewrites: see commitWatcher.
func commitKey(c github.BranchCommit) string {
	return fmt.Sprintf("%d\x1f%s", c.AuthorTime, c.Subject)
}

// isFixupCommit reports whether subject marks a commit that `git rebase
// --autosquash` folds into another one.
func isFixupCommit(subject string) bool {
	for _, prefix := range []string{"fixup! ", "squash! ", "amend! "} {
		if strings.HasPrefix(subject, prefix) {
			return true
		}
	}
	return false
}

// shortSHA abbreviates a commit id to the seven characters git shows.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

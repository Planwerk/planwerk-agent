package review

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// The reasons classifyOutdatedThread gives. Every one except threadResolve
// keeps the thread open, and each is a key of the counts
// resolveOutdatedThreads logs.
const (
	threadResolve          = "resolve"
	threadNotOutdated      = "not_outdated"
	threadAlreadyResolved  = "already_resolved"
	threadPartialView      = "partial_view"
	threadNotOurs          = "not_ours"
	threadUnverifiedAuthor = "unverified_author"
	threadReplied          = "replied"
	threadUnlocatablePath  = "unlocatable_path"
	threadNoLine           = "no_line"
	threadOverlap          = "overlap"
)

const (
	// maxResolvePerRun caps the threads one run resolves; the rest wait for
	// the next run.
	maxResolvePerRun = 50
	// resolvePause spaces two resolve mutations, GitHub's minimum spacing for
	// mutating requests.
	resolvePause = time.Second
)

// liveSpans holds the lines the current run's findings cover, per file, in
// the form comparePath gives the path. ranges holds each located finding's
// first and last line; unlocated holds the files a finding names without a
// line.
type liveSpans struct {
	ranges    map[string][][2]int
	unlocated map[string]bool
}

// newLiveSpans collects the spans of findings. A finding without a file
// vetoes nothing; one with a file and no line marks that file unlocated.
func newLiveSpans(findings []report.Finding) liveSpans {
	live := liveSpans{ranges: map[string][][2]int{}, unlocated: map[string]bool{}}
	for _, f := range findings {
		path := comparePath(f.File)
		if path == "" {
			continue
		}
		if f.Line <= 0 {
			live.unlocated[path] = true
			continue
		}
		live.ranges[path] = append(live.ranges[path], [2]int{f.Line, max(f.LineEnd, f.Line)})
	}
	return live
}

// comparePath folds the spellings a model gives a path (surrounding
// whitespace, backslashes, a leading "./" or "/") onto GitHub's repo-relative
// form, so a finding and a thread on the same file compare equal. It does no
// case folding.
func comparePath(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), `\`, "/")
	return strings.TrimLeft(strings.TrimPrefix(p, "./"), "/")
}

// classifyOutdatedThread returns threadResolve when t may be resolved, and
// otherwise the first guard it fails, in this order: GitHub marks it outdated
// and it is unresolved; the whole comment chain is in view; it is
// planwerk-agent's own (github.IsOwnThread); postedByViewer accepts its first
// comment's author; it holds no other comment, since planwerk-agent never
// replies in a thread it opened; no finding of live names its file without a
// line; its original line is known; and no finding of live on its file shares
// a line with its original lines, by plain intersection (decision 133).
func classifyOutdatedThread(t github.ReviewThread, viewer string, live liveSpans) string {
	switch {
	case !t.IsOutdated:
		return threadNotOutdated
	case t.IsResolved:
		return threadAlreadyResolved
	case len(t.Comments) == 0 || t.CommentCount > len(t.Comments):
		return threadPartialView
	case !github.IsOwnThread(t):
		return threadNotOurs
	case !postedByViewer(t.Comments[0], viewer):
		return threadUnverifiedAuthor
	case len(t.Comments) > 1:
		return threadReplied
	case live.unlocated[comparePath(t.Path)]:
		return threadUnlocatablePath
	case t.OriginalLine <= 0:
		return threadNoLine
	}
	start := t.OriginalStartLine
	if start <= 0 || start > t.OriginalLine {
		start = t.OriginalLine
	}
	for _, s := range live.ranges[comparePath(t.Path)] {
		if start <= s[1] && s[0] <= t.OriginalLine {
			return threadOverlap
		}
	}
	return threadResolve
}

// postedByViewer reports whether root was posted under the identity the run
// resolves with: viewer, the login gh is authenticated as, or "" when GitHub
// refused that lookup to an app's token (github.ErrViewerRefused; an Actions
// token may be refused it). A known viewer must be the author, compared
// case-insensitively and without a "[bot]" suffix; under an app's token only a
// bot author is accepted, since only an app can post as one. Either way the
// resolver is the poster, or an app GitHub names no resolver for, so a resolve
// never dismisses the finding (decision 132). A user-typed author under an
// app's token may be a person quoting the tool's comment, and is refused. The
// caller must not pass "" for a login that merely failed to load: that token
// may be a personal one, whose resolve of a bot's thread would dismiss it.
func postedByViewer(root github.ReviewThreadComment, viewer string) bool {
	if root.Author == "" {
		return false
	}
	if viewer == "" {
		return root.AuthorIsBot
	}
	return strings.EqualFold(root.Author, strings.TrimSuffix(viewer, "[bot]"))
}

// resolveOutdatedThreads resolves the review threads of threads, the list
// skipSuppressed read, that classifyOutdatedThread lets go against findings,
// the run's full result. It resolves at most maxResolvePerRun of them, one at a
// time with resolvePause between two, and stops at the first refused mutation:
// resolving needs write access to the repository. It is best-effort and never
// fails the run. An empty threads, which is also what a failed read leaves,
// resolves nothing. A login GitHub refused to an app's token
// (github.ErrViewerRefused) leaves only the threads a bot account opened
// resolvable (postedByViewer); any other failure to read the login, or an
// empty one, resolves nothing this run with a warning, since the token may be
// a personal one. Each resolve is logged at info level, each thread kept open
// at debug level with its reason, and a counts line closes the run
// (decision 133).
func (r *Runner) resolveOutdatedThreads(pr *github.PR, findings []report.Finding, threads []github.ReviewThread) {
	if len(threads) == 0 {
		slog.Debug("no review threads to check; resolving no outdated thread", "pr", pr.Number)
		return
	}
	viewer, err := r.GitHub.ViewerLogin()
	switch {
	case errors.Is(err, github.ErrViewerRefused):
		slog.Debug("the token is an app's and cannot read its login; only threads a bot opened can be resolved", "err", err)
		viewer = ""
	case err != nil || viewer == "":
		slog.Warn("reading the authenticated login failed; resolving no outdated thread this run", "err", err)
		return
	}

	live := newLiveSpans(findings)
	kept := map[string]int{}
	var candidates []github.ReviewThread
	for _, t := range threads {
		reason := classifyOutdatedThread(t, viewer, live)
		if reason == threadResolve {
			candidates = append(candidates, t)
			continue
		}
		kept[reason]++
		if reason != threadNotOutdated && reason != threadAlreadyResolved {
			slog.Debug("keeping outdated review thread open", "thread", t.ID, "path", t.Path, "line", t.OriginalLine, "reason", reason)
		}
	}

	pause := r.Pause
	if pause == nil {
		pause = time.Sleep
	}
	resolved := 0
	for i, t := range candidates {
		if i == maxResolvePerRun {
			break
		}
		if i > 0 {
			pause(resolvePause)
		}
		if err := r.GitHub.ResolveReviewThread(t.ID); err != nil {
			slog.Warn("resolving an outdated review thread failed; resolving no more this run (resolving needs write access to the repository, contents: write for an Actions token)", "thread", t.ID, "err", err)
			break
		}
		resolved++
		slog.Info("resolved outdated review thread", "thread", t.ID, "path", t.Path, "line", t.OriginalLine)
	}
	slog.Info("outdated review threads checked", "threads", len(threads), "candidates", len(candidates), "resolved", resolved, "kept_open", formatReasonCounts(kept))
}

// formatReasonCounts renders counts as "reason:count" pairs sorted by reason
// and joined with commas, or "none" when counts is empty.
func formatReasonCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return "none"
	}
	pairs := make([]string, 0, len(counts))
	for _, reason := range slices.Sorted(maps.Keys(counts)) {
		pairs = append(pairs, fmt.Sprintf("%s:%d", reason, counts[reason]))
	}
	return strings.Join(pairs, ",")
}

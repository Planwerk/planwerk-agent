package address

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/planwerk/planwerk-review/internal/github"
)

// RunInteractiveThreadSelection walks the unresolved review threads, shows each
// one's file:line, author, and a one-line excerpt, and asks whether to address
// it. It returns the selected threads when the user finishes the list or quits
// early, or an error when reading from in fails. Mirrors the y/N/q selector
// shape of github.RunInteractiveIssueCreation.
func RunInteractiveThreadSelection(w io.Writer, in io.Reader, threads []github.ReviewThread) ([]github.ReviewThread, error) {
	reader := bufio.NewReader(in)
	var selected []github.ReviewThread

	for i, t := range threads {
		_, _ = fmt.Fprintf(w, "\n%s\n", strings.Repeat("=", 80))
		_, _ = fmt.Fprintf(w, "Thread %d/%d  %s\n", i+1, len(threads), threadLocation(t))
		_, _ = fmt.Fprintf(w, "%s\n\n", strings.Repeat("=", 80))
		_, _ = fmt.Fprintf(w, "%s\n", threadPreview(t))

		_, _ = fmt.Fprintf(w, "\nAddress this thread? [y/N/q] ")
		input, err := reader.ReadString('\n')
		if err != nil {
			// EOF after the last prompt is not an error: treat a closed stream
			// as "no more answers" and finish with what was selected so far.
			if err == io.EOF && strings.TrimSpace(input) == "" {
				break
			}
			if err != io.EOF {
				return nil, fmt.Errorf("reading input: %w", err)
			}
		}
		switch strings.TrimSpace(strings.ToLower(input)) {
		case "q", "quit":
			_, _ = fmt.Fprintf(w, "\nStopped. Selected %d thread(s).\n", len(selected))
			return selected, nil
		case "y", "yes":
			selected = append(selected, t)
			_, _ = fmt.Fprintf(w, "Selected.\n")
		default:
			_, _ = fmt.Fprintf(w, "Skipped.\n")
		}
		if err == io.EOF {
			break
		}
	}

	_, _ = fmt.Fprintf(w, "\nDone. Selected %d thread(s).\n", len(selected))
	return selected, nil
}

// threadLocation renders the thread's anchor as "path:line" (or just the path
// when no line is known, or a placeholder when neither is set).
func threadLocation(t github.ReviewThread) string {
	if t.Path == "" {
		return "(no file)"
	}
	if t.Line > 0 {
		return fmt.Sprintf("%s:%d", t.Path, t.Line)
	}
	return t.Path
}

// threadPreview renders the author and a one-line excerpt of the thread's first
// comment for the selection list.
func threadPreview(t github.ReviewThread) string {
	if len(t.Comments) == 0 {
		return "(no comments)"
	}
	first := t.Comments[0]
	author := first.Author
	if author == "" {
		author = "(unknown)"
	}
	return fmt.Sprintf("%s: %s", author, oneLineExcerpt(first.Body))
}

// oneLineExcerpt collapses a comment body to its first non-empty line, trimmed
// to a readable length for the selection list.
func oneLineExcerpt(body string) string {
	const maxLen = 100
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > maxLen {
			return line[:maxLen-1] + "…"
		}
		return line
	}
	return "(empty)"
}

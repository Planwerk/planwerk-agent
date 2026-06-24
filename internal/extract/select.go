package extract

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// selectEntries resolves which wiki patterns to extract: explicit --pattern
// stems, --all, or the interactive selector. A non-interactive run (no TTY) with
// neither --all nor --pattern is an error, not a fail-open default: the wiki is
// an untrusted, world-editable source, so extracting (and, in the default mode,
// pushing into a PR) every pattern without an explicit human choice is refused.
// Mirrors address.selectThreads.
func selectEntries(w io.Writer, opts Options, in io.Reader, isTTY func() bool, entries []entry) ([]entry, error) {
	if len(opts.Patterns) > 0 {
		return pickByStem(w, entries, opts.Patterns), nil
	}
	if opts.All {
		return entries, nil
	}
	if !isTTY() {
		return nil, fmt.Errorf("stdin is not a TTY: pass --all to extract every wiki review pattern or --pattern <stem> to choose specific ones")
	}
	return runInteractiveSelection(w, in, entries)
}

// pickByStem returns the entries whose stem is one of the requested stems,
// preserving the input order. Unknown stems are warned about so a typo does not
// silently extract nothing. Mirrors address.pickByID.
func pickByStem(w io.Writer, entries []entry, stems []string) []entry {
	want := make(map[string]bool, len(stems))
	for _, s := range stems {
		want[s] = true
	}
	var out []entry
	seen := make(map[string]bool, len(stems))
	for _, e := range entries {
		if want[e.Stem] {
			out = append(out, e)
			seen[e.Stem] = true
		}
	}
	for _, s := range stems {
		if !seen[s] {
			_, _ = fmt.Fprintf(w, "Warning: --pattern %s did not match any wiki review pattern.\n", s)
		}
	}
	return out
}

// runInteractiveSelection walks the wiki patterns, shows each one's stem, name,
// and severity, and asks whether to anchor it. It returns the selected entries
// when the user finishes the list or quits early, or an error when reading from
// in fails. Mirrors the y/N/q selector shape of
// address.RunInteractiveThreadSelection.
func runInteractiveSelection(w io.Writer, in io.Reader, entries []entry) ([]entry, error) {
	reader := bufio.NewReader(in)
	var selected []entry

	for i, e := range entries {
		_, _ = fmt.Fprintf(w, "\n%s\n", strings.Repeat("=", 80))
		_, _ = fmt.Fprintf(w, "Pattern %d/%d  %s\n", i+1, len(entries), e.Stem)
		_, _ = fmt.Fprintf(w, "%s\n\n", strings.Repeat("=", 80))
		_, _ = fmt.Fprintf(w, "%s\n", entryPreview(e))

		_, _ = fmt.Fprintf(w, "\nAnchor this pattern? [y/N/q] ")
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
			_, _ = fmt.Fprintf(w, "\nStopped. Selected %d pattern(s).\n", len(selected))
			return selected, nil
		case "y", "yes":
			selected = append(selected, e)
			_, _ = fmt.Fprintf(w, "Selected.\n")
		default:
			_, _ = fmt.Fprintf(w, "Skipped.\n")
		}
		if err == io.EOF {
			break
		}
	}

	_, _ = fmt.Fprintf(w, "\nDone. Selected %d pattern(s).\n", len(selected))
	return selected, nil
}

// entryPreview renders the pattern name and severity for the selection list,
// falling back to the stem when the header carries no name.
func entryPreview(e entry) string {
	name := e.Name
	if name == "" {
		name = e.Stem
	}
	if e.Severity != "" {
		return fmt.Sprintf("%s (%s)", name, e.Severity)
	}
	return name
}

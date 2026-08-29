package github

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// DiffMap holds the set of lines that appear in a PR diff on the RIGHT side (new version).
type DiffMap struct {
	files map[string]map[int]bool // file -> set of line numbers visible in diff
}

// FetchDiff retrieves the PR diff using gh api.
func (Client) FetchDiff(owner, repo string, number int) (string, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d", owner, repo, number)
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "api", endpoint,
		"-H", "Accept: application/vnd.github.v3.diff",
	)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("fetching PR diff: %s: %w", strings.TrimSpace(string(exitErr.Stderr)), err)
		}
		return "", fmt.Errorf("fetching PR diff: %w", err)
	}
	return string(out), nil
}

// ParseDiff parses a unified diff into a DiffMap.
// It records which lines appear in diff hunks on the RIGHT side
// (lines in the new version of the file: additions and context lines).
func ParseDiff(rawDiff string) *DiffMap {
	dm := &DiffMap{files: make(map[string]map[int]bool)}
	lines := strings.Split(rawDiff, "\n")

	var currentFile string
	var rightLine int

	for _, line := range lines {
		// Detect file boundary: "diff --git a/FILE b/FILE"
		if strings.HasPrefix(line, "diff --git ") {
			currentFile = ""
			continue
		}

		// Detect new file path: "+++ b/FILE"
		if strings.HasPrefix(line, "+++ b/") {
			currentFile = strings.TrimPrefix(line, "+++ b/")
			if dm.files[currentFile] == nil {
				dm.files[currentFile] = make(map[int]bool)
			}
			continue
		}

		// Skip deleted file marker
		if strings.HasPrefix(line, "+++ /dev/null") {
			currentFile = ""
			continue
		}

		// Detect hunk header: "@@ -OLD,LEN +NEW,LEN @@"
		if strings.HasPrefix(line, "@@") && currentFile != "" {
			rightLine = parseHunkNewStart(line)
			continue
		}

		if currentFile == "" {
			continue
		}

		// Process hunk lines
		switch {
		case strings.HasPrefix(line, "+"):
			// Addition: record on right side, advance right counter
			dm.files[currentFile][rightLine] = true
			rightLine++
		case strings.HasPrefix(line, " "):
			// Context line: appears on both sides, advance right counter
			dm.files[currentFile][rightLine] = true
			rightLine++
		}
		// Deletion ("-") lines: left-side only, do not advance right counter.
		// Other lines (e.g., "\ No newline at end of file") are ignored.
	}

	return dm
}

// Contains returns true if the given file:line appears in the diff on the RIGHT side.
func (dm *DiffMap) Contains(file string, line int) bool {
	if dm == nil || dm.files == nil {
		return false
	}
	fileLines, ok := dm.files[file]
	if !ok {
		return false
	}
	return fileLines[line]
}

// parseHunkNewStart extracts the new-file start line from a hunk header.
// Format: "@@ -OLD,LEN +NEW,LEN @@" or "@@ -OLD +NEW,LEN @@"
func parseHunkNewStart(hunkLine string) int {
	// Find the "+N" part after the first "+"
	plusIdx := strings.Index(hunkLine, "+")
	if plusIdx < 0 {
		return 1
	}
	rest := hunkLine[plusIdx+1:]
	// Extract the number before "," or " "
	end := strings.IndexAny(rest, ", ")
	if end < 0 {
		end = len(rest)
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 1
	}
	return n
}

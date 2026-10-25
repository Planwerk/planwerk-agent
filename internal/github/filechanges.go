package github

import (
	"strconv"
	"strings"
)

// FileChange is one file a pull request changes between its base and its
// head: how it changed and how many lines it added and deleted. Path is the
// path git reports for the change, the new path of a rename or copy and the
// base path of a deletion; OldPath is set for a rename or copy only. Binary
// means git counted no lines for the file, and Added and Deleted stay 0 then.
// The review lists these in its prompt and checks the session's account of
// them (decision 138).
type FileChange struct {
	Path    string
	OldPath string
	Status  string
	Added   int
	Deleted int
	Binary  bool
}

// FileChange.Status values, one per git name-status letter: A, M, D, R, C and
// T. Any other letter reads as FileModified.
const (
	FileAdded       = "added"
	FileModified    = "modified"
	FileDeleted     = "deleted"
	FileRenamed     = "renamed"
	FileCopied      = "copied"
	FileTypeChanged = "type-changed"
)

// nameStatuses maps a git name-status letter to its FileChange.Status.
var nameStatuses = map[byte]string{
	'A': FileAdded,
	'M': FileModified,
	'D': FileDeleted,
	'R': FileRenamed,
	'C': FileCopied,
	'T': FileTypeChanged,
}

// DiffFileChanges returns every file changed between the base branch and HEAD
// (origin/<base>...HEAD), in git's order, with its status and line counts. It
// runs `git -c core.quotePath=false diff --no-ext-diff --no-textconv --no-color
// --no-relative -z -M --name-status` over that range, then the same with
// --numstat. It shares the diff driver, text conversion, color and path quoting
// overrides the review session's diff file is written with (diffscope), so none
// of those settings changes the list. It adds --no-relative, because dir need
// not be the top level of the checkout, and -M, so a diff.renames setting
// cannot change how a rename is listed. The diff file has no -M: under
// diff.renames=false it shows a file this lists as renamed as a deletion and an
// addition. With -z git prints every path raw and NUL-terminated, never
// C-quoted, so a name holding a space, a quote, a newline or non-ASCII bytes
// comes back as it is. Like DiffNames, an empty dir or baseBranch yields nil
// and no error, and a git failure yields nil and an error naming the command
// and git's stderr, which the caller logs before going on without the list
// (decision 138).
func (Client) DiffFileChanges(dir, baseBranch string) ([]FileChange, error) {
	if dir == "" || baseBranch == "" {
		return nil, nil
	}
	diff := func(format string) ([]byte, error) {
		return gitBytes(dir, "-c", "core.quotePath=false", "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--no-relative", "-z", "-M", format, "origin/"+baseBranch+"...HEAD", "--")
	}
	names, err := diff("--name-status")
	if err != nil {
		return nil, err
	}
	stats, err := diff("--numstat")
	if err != nil {
		return nil, err
	}
	files := parseNameStatusZ(names)
	counts := parseNumstatZ(stats)
	for i := range files {
		c := counts[files[i].Path]
		files[i].Added, files[i].Deleted, files[i].Binary = c.added, c.deleted, c.binary
	}
	return files, nil
}

// parseNameStatusZ parses `git diff -z --name-status` output, in git's order.
// Each entry is a status token followed by its path, or, for a status starting
// with R or C (a rename or copy, with its similarity score, such as R085), by
// the old path and then the new one. The letters A, M, D, R, C and T map to
// the FileChange statuses, and any other letter to FileModified.
func parseNameStatusZ(out []byte) []FileChange {
	tokens := strings.Split(string(out), "\x00")
	var files []FileChange
	for i := 0; i < len(tokens); {
		status := tokens[i]
		i++
		if status == "" {
			continue
		}
		paths := 1
		if status[0] == 'R' || status[0] == 'C' {
			paths = 2
		}
		if i+paths > len(tokens) {
			break
		}
		f := FileChange{Path: tokens[i+paths-1], Status: FileModified}
		if s, ok := nameStatuses[status[0]]; ok {
			f.Status = s
		}
		if paths == 2 {
			f.OldPath = tokens[i]
		}
		i += paths
		if f.Path != "" {
			files = append(files, f)
		}
	}
	return files
}

// lineCounts is one file's entry in `git diff --numstat` output.
type lineCounts struct {
	added, deleted int
	binary         bool
}

// parseNumstatZ parses `git diff -z --numstat` output into each file's line
// counts, keyed by the path parseNameStatusZ gives the file: the new path of a
// rename or copy. An entry is `added<TAB>deleted<TAB>path`, or, for a rename or
// copy, `added<TAB>deleted<TAB>` followed by the old and the new path as tokens
// of their own. The counts are split off with SplitN, so a tab inside a path
// survives, and git's "-" counts mark a binary file.
func parseNumstatZ(out []byte) map[string]lineCounts {
	tokens := strings.Split(string(out), "\x00")
	counts := make(map[string]lineCounts)
	for i := 0; i < len(tokens); i++ {
		fields := strings.SplitN(tokens[i], "\t", 3)
		if len(fields) != 3 {
			continue
		}
		path := fields[2]
		if path == "" {
			if i+2 >= len(tokens) {
				break
			}
			path = tokens[i+2]
			i += 2
		}
		c := lineCounts{binary: fields[0] == "-" || fields[1] == "-"}
		if !c.binary {
			c.added, _ = strconv.Atoi(fields[0])
			c.deleted, _ = strconv.Atoi(fields[1])
		}
		counts[path] = c
	}
	return counts
}

package report

import "fmt"

// ExcludedFile is one changed path that --diff-exclusions left out of the diff
// a session read: a lock file, a vendored directory, or a generated file. Path
// is repository-relative, and a vendored directory is one entry whose Path ends
// in "/" and whose Files counts the changed files under it; Files is zero for
// every other entry (decision 137).
type ExcludedFile struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
	Files  int    `json:"files,omitempty"`
}

// ExcludedFile.Reason values: a lock file matched by its base name, a changed
// file under a vendor/ or node_modules/ directory, and a file whose leading
// comment says it is generated.
const (
	ExcludedLockfile  = "lockfile"
	ExcludedVendored  = "vendored"
	ExcludedGenerated = "generated"
)

// ExcludedFilesListLimit is the most excluded entries a prompt, the Markdown
// note or the SARIF notification names one by one; the rest are counted. The
// --format json output and the data block carry every entry.
const ExcludedFilesListLimit = 50

// Label names why e was left out, as the prompt and the Markdown note print it
// after the path: "lock file", "generated", or "vendored, N files" with the
// count of changed files under the directory.
func (e ExcludedFile) Label() string {
	switch e.Reason {
	case ExcludedLockfile:
		return "lock file"
	case ExcludedVendored:
		if e.Files == 1 {
			return "vendored, 1 file"
		}
		return fmt.Sprintf("vendored, %d files", e.Files)
	}
	return e.Reason
}

// ExcludedFileCount returns how many changed files list stands for: Files for
// a vendored directory entry and one for every other entry.
func ExcludedFileCount(list []ExcludedFile) int {
	n := 0
	for _, e := range list {
		if e.Reason == ExcludedVendored {
			n += e.Files
			continue
		}
		n++
	}
	return n
}

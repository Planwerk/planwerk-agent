// Package workspace owns the cross-cutting helpers shared by the --local
// code path: the dirty-working-tree gate, origin detection, the interactive
// y/n prompter, and a stdin-is-a-TTY check.
//
// It deliberately imports nothing from internal/github so the github package
// can depend on it (for LocalOptions.Prompter, EnsureClean, and DetectOrigin)
// without creating an import cycle. The origin URL parser lives here for the
// same reason — and, unlike github.ParseRepoRef, it also understands the SSH
// scp-style remotes (git@github.com:owner/repo.git) that real checkouts use.
package workspace

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

// gitTimeout bounds every git invocation issued from this package.
const gitTimeout = 2 * time.Minute

// Sentinel errors for the dirty-working-tree gate. Callers compare against
// these with errors.Is so the CLI can distinguish "user declined" from
// "no TTY to ask" and surface an actionable message.
var (
	// ErrDirtyTreeDeclined is returned when the user answers "no" to the
	// uncommitted-changes confirmation prompt.
	ErrDirtyTreeDeclined = errors.New("aborted: working tree has uncommitted changes")
	// ErrDirtyTreeNoTTY is returned when the working tree is dirty, --force is
	// not set, and stdin is not a TTY so we cannot ask the user.
	ErrDirtyTreeNoTTY = errors.New("working tree is dirty and stdin is not a TTY")
)

// Prompter asks the user a single yes/no question. It mirrors the fix
// package's Prompter interface so the two are structurally interchangeable.
type Prompter interface {
	Confirm(message string) (bool, error)
}

// StdinPrompter is the production Prompter: it reads a single y/n line from In
// and writes the question to Out (typically stderr so it stays visible when
// the caller redirects stdout).
type StdinPrompter struct {
	In  io.Reader
	Out io.Writer
}

// NewStdinPrompter returns a StdinPrompter wired to os.Stdin / os.Stderr.
func NewStdinPrompter() StdinPrompter {
	return StdinPrompter{In: os.Stdin, Out: os.Stderr}
}

// Confirm prints message to Out and returns true when the user answers y/yes.
func (p StdinPrompter) Confirm(message string) (bool, error) {
	if _, err := fmt.Fprint(p.Out, message); err != nil {
		return false, err
	}
	r := bufio.NewReader(p.In)
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

// stdinIsTerminalFn is overridable in tests.
var stdinIsTerminalFn = IsStdinTTY

// IsStdinTTY reports whether os.Stdin refers to a character device (i.e. an
// interactive terminal). Mirrors claude.stderrIsTerminal.
func IsStdinTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// IsStderrTTY reports whether os.Stderr refers to a character device (i.e. an
// interactive terminal). The draft composer renders to stderr, so it engages
// only when both stdin and stderr are terminals; this is the output half of
// that gate. Mirrors IsStdinTTY.
func IsStderrTTY() bool {
	fi, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// EnsureClean enforces the dirty-working-tree gate before any state-changing
// step of a --local run. It returns nil when the tree is clean. When the tree
// is dirty the behavior depends on force and whether stdin is a TTY:
//   - force=true: logs a warning and proceeds (returns nil).
//   - no TTY:     returns ErrDirtyTreeNoTTY wrapped with a --force hint.
//   - TTY:        prompts the user; returns ErrDirtyTreeDeclined on "no".
//
// The user's changes are never stashed or discarded — every branch either
// prompts, logs+proceeds, or aborts with an actionable error.
func EnsureClean(dir string, force bool, prompter Prompter) error {
	out, err := gitOutput(dir, "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("checking working tree status in %s: %w", dir, err)
	}
	if strings.TrimSpace(out) == "" {
		return nil // clean tree
	}

	if force {
		slog.Warn("proceeding on dirty working tree", "dir", dir)
		return nil
	}

	if !stdinIsTerminalFn() {
		return fmt.Errorf("working tree at %s is dirty and stdin is not a TTY; re-run with --force to proceed: %w", dir, ErrDirtyTreeNoTTY)
	}

	if prompter == nil {
		prompter = NewStdinPrompter()
	}
	ok, err := prompter.Confirm(fmt.Sprintf("Working tree at %s has uncommitted changes. Proceed anyway? (y/N): ", dir))
	if err != nil {
		return fmt.Errorf("confirming dirty working tree: %w", err)
	}
	if !ok {
		return ErrDirtyTreeDeclined
	}
	return nil
}

// DetectOrigin resolves the owner and repo name of the "origin" remote in dir
// by running `git -C <dir> remote get-url origin` and parsing the result.
func DetectOrigin(dir string) (owner, name string, err error) {
	out, err := gitOutput(dir, "remote", "get-url", "origin")
	if err != nil {
		return "", "", fmt.Errorf("resolving origin remote in %s: %w", dir, err)
	}
	url := strings.TrimSpace(out)
	if url == "" {
		return "", "", fmt.Errorf("no origin remote URL configured in %s", dir)
	}
	owner, name, ok := parseOriginURL(url)
	if !ok {
		return "", "", fmt.Errorf("could not parse origin remote URL %q in %s", url, dir)
	}
	return owner, name, nil
}

// parseOriginURL extracts owner/repo from a git remote URL. It understands the
// three forms a real checkout produces:
//
//	git@github.com:owner/repo.git          (scp-style SSH)
//	ssh://git@github.com[:port]/owner/repo  (ssh URL)
//	https://github.com/owner/repo[.git]     (https URL)
//
// plus a bare "owner/repo". The host is intentionally not pinned to github.com
// so self-hosted GitHub Enterprise remotes parse too.
func parseOriginURL(raw string) (owner, name string, ok bool) {
	s := strings.TrimSpace(raw)
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/")

	var path string
	switch {
	case strings.Contains(s, "://"):
		// scheme://[user@]host[:port]/owner/repo
		rest := s[strings.Index(s, "://")+3:]
		slash := strings.Index(rest, "/")
		if slash < 0 {
			return "", "", false
		}
		path = rest[slash+1:]
	case strings.Contains(s, ":"):
		// scp-style: [user@]host:owner/repo
		path = s[strings.LastIndex(s, ":")+1:]
	default:
		path = s
	}

	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 {
		return "", "", false
	}
	owner = parts[len(parts)-2]
	name = parts[len(parts)-1]
	if owner == "" || name == "" {
		return "", "", false
	}
	return owner, name, true
}

// gitOutput runs `git -C <dir> <args...>` with a timeout and returns stdout.
func gitOutput(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

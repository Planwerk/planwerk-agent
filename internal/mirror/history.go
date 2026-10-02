package mirror

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/planwerk/planwerk-agent/internal/github"
)

// historyLine is one line of history.jsonl: a commit of the default branch.
// PR is the merged pull request GitHub associates with the commit, or 0.
type historyLine struct {
	SHA         string `json:"sha"`
	CommittedAt string `json:"committed_at"`
	PR          int    `json:"pr"`
}

// HistoryAdapter mirrors the commit list of the default branch into
// history.jsonl, one line per commit, oldest first. The file is its own
// cursor: the next sync reads GitHub's history down to the commits the file
// holds, and the file's last commit is the head of the branch it was read at.
type HistoryAdapter struct {
	GitHub GitHub
}

// Name returns "history".
func (HistoryAdapter) Name() string { return "history" }

// Sync adds the commits of the default branch the mirror does not hold. It
// reads GitHub's history from the newest commit down and stops once the
// commits it was handed and the mirrored ones it was not are every commit of
// the branch. Stopping at the mirrored head would not do: GitHub lists by
// commit date, so a merge places the commits of an older branch behind that
// head. The commits the read was handed are written in GitHub's order, after
// the mirrored ones it was not handed.
//
// The count is trusted only while the branch still holds the mirrored head.
// When it does not (the branch was rewritten), the read goes to the end and
// the whole history of the branch replaces the mirrored one. An error leaves
// the file as it was.
func (a HistoryAdapter) Sync(m *Mirror) (string, error) {
	old, err := ReadHistory(m.Dir)
	if err != nil {
		return "", err
	}
	head := ""
	if len(old) > 0 {
		head = old[len(old)-1].SHA
	}
	// unlisted holds the mirrored commits the read has not been handed.
	unlisted := make(map[string]bool, len(old))
	for _, c := range old {
		unlisted[c.SHA] = true
	}
	handed := 0
	var headListed, complete bool
	listed, err := a.GitHub.DefaultBranchHistoryUntil(m.Owner, m.Name, func(page []github.HistoryCommit, total int) bool {
		for _, c := range page {
			delete(unlisted, c.SHA)
			headListed = headListed || c.SHA == head
		}
		handed += len(page)
		complete = headListed && len(unlisted)+handed == total
		return complete
	})
	if err != nil {
		return "", err
	}

	// A read that went to the end was handed the whole history of the branch.
	all := listed
	if complete {
		all = make([]github.HistoryCommit, 0, len(unlisted)+len(listed))
		for _, c := range old {
			if unlisted[c.SHA] {
				all = append(all, c)
			}
		}
		all = append(all, listed...)
	}
	if !complete || len(all) != len(old) {
		if err := writeHistory(m.Dir, all); err != nil {
			return "", err
		}
	}
	if head != "" && !headListed {
		slog.Warn("the default branch no longer holds the mirrored head; replacing the mirrored history",
			"repo", m.Owner+"/"+m.Name, "head", head)
		return fmt.Sprintf("history: replaced, %d commits in the mirror", len(all)), nil
	}
	return fmt.Sprintf("history: %d new commits, %d in the mirror", len(all)-len(old), len(all)), nil
}

// ReadHistory returns the commits of history.jsonl in the mirror directory
// dir, oldest first. A missing file holds no commit.
func ReadHistory(dir string) ([]github.HistoryCommit, error) {
	path := filepath.Join(dir, historyFileName)
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var commits []github.HistoryCommit
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		var l historyLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			return nil, fmt.Errorf("parsing %s line %d: %w", path, n, err)
		}
		at, err := time.Parse(time.RFC3339, l.CommittedAt)
		if err != nil {
			return nil, fmt.Errorf("parsing %s line %d: %w", path, n, err)
		}
		commits = append(commits, github.HistoryCommit{SHA: l.SHA, CommittedAt: at, PRNumber: l.PR})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return commits, nil
}

// writeHistory writes commits to history.jsonl in dir, replacing the file.
func writeHistory(dir string, commits []github.HistoryCommit) error {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	for _, c := range commits {
		line := historyLine{SHA: c.SHA, CommittedAt: c.CommittedAt.UTC().Format(time.RFC3339), PR: c.PRNumber}
		if err := enc.Encode(line); err != nil {
			return fmt.Errorf("encoding the history: %w", err)
		}
	}
	return writeFileAtomic(filepath.Join(dir, historyFileName), b.Bytes())
}

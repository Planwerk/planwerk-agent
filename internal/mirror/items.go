package mirror

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/planwerk/planwerk-agent/internal/github"
)

// itemsProgressEvery is the number of fetched items between two progress
// lines of the items adapter.
const itemsProgressEvery = 50

// ItemsAdapter mirrors the issues and pull requests: one file per item under
// issues/ and pulls/. Issues and pull requests share one cursor, because an
// item's update time rises when a comment on it is edited and when a review
// is submitted.
type ItemsAdapter struct {
	GitHub GitHub
}

// Name returns "items".
func (ItemsAdapter) Name() string { return "items" }

// Sync lists the items updated at or after the cursor and fetches every one
// whose file is missing or older than the listing says. The cursor follows the
// entries as they are handled, so after a failed fetch or write the next run
// lists from the last handled entry. The listing includes the cursor's own
// time, so a run right after a sync lists the newest item again and finds its
// file up to date.
func (a ItemsAdapter) Sync(m *Mirror) (string, error) {
	entries, err := a.GitHub.ListUpdatedItems(m.Owner, m.Name, m.State.Items.Cursor)
	if err != nil {
		return "", err
	}
	repo := m.Owner + "/" + m.Name
	fetched := 0
	for i, e := range entries {
		if !itemIsClean(m.Dir, e) {
			item, err := a.GitHub.GetItem(m.Owner, m.Name, e.Number)
			if err != nil {
				return "", fmt.Errorf("fetching %s#%d: %w", repo, e.Number, err)
			}
			path := ItemPath(m.Dir, item.Kind, e.Number)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return "", fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
			}
			if err := writeFileAtomic(path, Render(repo, item)); err != nil {
				return "", err
			}
			fetched++
			if fetched%itemsProgressEvery == 0 {
				slog.Info("mirroring items", "done", i+1, "listed", len(entries))
			}
		}
		m.State.Items.Cursor = laterCursor(m.State.Items.Cursor, e.UpdatedAt)
	}

	issues, err := ItemFiles(m.Dir, github.ItemKindIssue)
	if err != nil {
		return "", err
	}
	pulls, err := ItemFiles(m.Dir, github.ItemKindPull)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("items: %d listed, %d fetched, %d in the mirror (%d issues, %d pull requests)",
		len(entries), fetched, len(issues)+len(pulls), len(issues), len(pulls)), nil
}

// itemIsClean reports whether the file of a listed item is up to date: it
// exists, and the update time in its frontmatter is not before the listed
// one. A file that cannot be read, or whose frontmatter or update time does
// not parse, is not clean, so the item is fetched and the file written again.
func itemIsClean(dir string, e github.UpdatedItem) bool {
	kind := github.ItemKindIssue
	if e.IsPull {
		kind = github.ItemKindPull
	}
	fm, err := ReadFrontmatter(ItemPath(dir, kind, e.Number))
	if err != nil {
		return false
	}
	have, err := time.Parse(time.RFC3339, fm.UpdatedAt)
	if err != nil {
		return false
	}
	listed, err := time.Parse(time.RFC3339, e.UpdatedAt)
	if err != nil {
		return false
	}
	return !have.Before(listed)
}

// laterCursor returns updatedAt when it is a later time than the cursor, or
// when the cursor holds no time, and the cursor otherwise. An update time that
// is not RFC 3339 never becomes the cursor.
func laterCursor(cursor, updatedAt string) string {
	at, err := time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		return cursor
	}
	if cur, err := time.Parse(time.RFC3339, cursor); err == nil && !at.After(cur) {
		return cursor
	}
	return updatedAt
}

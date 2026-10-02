package mirror

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/patterns"
)

// wikiShortSHALen is the length of the commit prefix in the wiki's summary.
const wikiShortSHALen = 7

// WikiAdapter mirrors the wiki as a full git clone under wiki/. Every sync
// makes a fresh clone that replaces the previous one once it succeeded: the
// result equals a pull, and the clone keeps the wiki's whole history.
type WikiAdapter struct {
	Clone CloneWikiFn
	// Options names the wiki (Repo; empty means the mirrored repository) and
	// pins it (Ref). Enabled is not read.
	Options patterns.WikiOptions
}

// Name returns "wiki".
func (WikiAdapter) Name() string { return "wiki" }

// Sync clones the wiki and records it with its HEAD in the state. A clone that
// fails is not an error of the run: a repository without a wiki fails here on
// every sync, and a wiki that cannot be reached now is still mirrored from the
// last run. The failure is logged, and the clone and the state stay as they
// were.
func (a WikiAdapter) Sync(m *Mirror) (string, error) {
	wiki := a.Options.Repo
	if wiki == "" {
		wiki = m.Owner + "/" + m.Name
	}
	head, err := a.Clone(wiki, a.Options.Ref, filepath.Join(m.Dir, wikiDirName))
	if err != nil {
		slog.Warn("could not mirror the wiki; keeping what the mirror holds", "wiki", wiki, "err", err)
		return fmt.Sprintf("wiki: %s.wiki not mirrored", wiki), nil
	}

	unchanged := head != "" && head == m.State.Wiki.Commit && strings.EqualFold(wiki, m.State.Wiki.Repo)
	m.State.Wiki = WikiState{Repo: wiki, Commit: head}

	// The HEAD of an empty wiki cannot be resolved.
	short := "unknown"
	if head != "" {
		short = head[:min(wikiShortSHALen, len(head))]
	}
	summary := fmt.Sprintf("wiki: %s.wiki at %s", wiki, short)
	if unchanged {
		summary += ", unchanged"
	}
	return summary, nil
}

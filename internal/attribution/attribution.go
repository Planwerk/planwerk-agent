// Package attribution centralizes the self-attribution that planwerk-review
// stamps on every artifact it leaves on GitHub — issue bodies, pull request
// descriptions, review comments, thread replies — and prints in its CLI
// previews. Holding the wording in one leaf package (importable by every
// renderer without an import cycle back into the claude package) stops the
// copies from drifting, the same way internal/claude/components.go centralizes
// the shared prompt blocks.
//
// The convention this package pins is the prose-side companion to the
// Assisted-by commit-trailer convention (see commitTrailerBlock in the claude
// package): every artifact names the exact Claude model that produced it. The
// orchestrator passes Claude Code only a model alias ("opus"); the resolved id
// ("claude-opus-4-8") is known only at runtime, so the claude runner records it
// here from each session's stream-init event and the footer helpers read it
// back. When no model has been recorded — a footer rendered with no session
// behind it — the attribution falls back to a bare "with Claude" rather than
// guessing an id.
package attribution

import (
	"strings"
	"sync"
)

const (
	// RepoURL is planwerk-review's repository, linked from every footer so the
	// artifact points back at the tool that produced it.
	RepoURL = "https://github.com/planwerk/planwerk-review"

	// Link is the Markdown link to the repository embedded in the footers.
	Link = "[planwerk-review](" + RepoURL + ")"

	// AssistantMarker is the stable, model-independent prefix of the assistant
	// attribution clause. Assistant() appends ":<model id>" to it when a model
	// is known. It is exported so callers that match a rendered footer as a
	// detection marker (implement keys its posted-plan lookup on it) can do so
	// on a prefix that survives a model change.
	AssistantMarker = "with Claude"
)

var (
	mu            sync.RWMutex
	resolvedModel string
)

// SetModel records the resolved Claude model id (e.g. "claude-opus-4-8") that
// the most recent Claude session announced in its stream-init event. The claude
// runner calls it as each session starts; Assistant() reads it so every footer
// rendered afterwards names the exact model that produced the artifact. Passing
// an empty (or whitespace-only) id clears the record.
func SetModel(id string) {
	mu.Lock()
	resolvedModel = strings.TrimSpace(id)
	mu.Unlock()
}

// Model reports the resolved model id recorded by the last SetModel call, or ""
// when none has been recorded.
func Model() string {
	mu.RLock()
	defer mu.RUnlock()
	return resolvedModel
}

// Assistant renders the assistant attribution clause: "with Claude:claude-opus-4-8"
// when the resolved model is known, and a bare "with Claude" otherwise. It never
// guesses an id, mirroring the Assisted-by commit-trailer convention.
func Assistant() string {
	if m := Model(); m != "" {
		return AssistantMarker + ":" + m
	}
	return AssistantMarker
}

// Package attribution centralizes the self-attribution that planwerk-agent
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
// ("claude-opus-5") is known only at runtime, so each Claude session returns
// it to the caller, which threads it per-run into AssistantWith. Keeping the
// model out of package-level state lets concurrent runners render their own
// model without leaking each other's. When no model is known — a footer
// rendered with no session behind it — the attribution falls back to a bare
// "with Claude" rather than guessing an id.
//
// Every footer also names the planwerk-agent build that produced it — the same
// string "planwerk-agent --version" prints — placed right after the repository
// link so the report headers and the issue/PR/comment footers read identically.
// Unlike the per-run model id, the version is a process-wide fact recorded once
// at startup (SetVersion) and read back by Tool(); renderers that already
// receive it as a parameter pass it to ToolWithVersion directly.
package attribution

import (
	"strings"
	"sync"
)

const (
	// RepoURL is planwerk-agent's repository, linked from every footer so the
	// artifact points back at the tool that produced it.
	RepoURL = "https://github.com/planwerk/planwerk-agent"

	// Link is the Markdown link to the repository embedded in the footers.
	Link = "[planwerk-agent](" + RepoURL + ")"

	// AssistantMarker is the stable, model-independent prefix of the assistant
	// attribution clause. AssistantWith appends ":<model id>" to it when a model
	// is known. It is exported so callers that match a rendered footer as a
	// detection marker (implement keys its posted-plan lookup on it) can do so
	// on a prefix that survives a model change.
	AssistantMarker = "with Claude"
)

var (
	mu          sync.RWMutex
	toolVersion string
)

// SetVersion records the planwerk-agent build version — the same string
// "planwerk-agent --version" prints (e.g. "e1efd0d") — so every footer can name
// the exact build that produced the artifact. The root command calls it once at
// startup from the build-time version var; Tool() reads it back. Passing an
// empty (or whitespace-only) version clears the record, in which case the footer
// falls back to the bare repository link.
func SetVersion(v string) {
	mu.Lock()
	toolVersion = strings.TrimSpace(v)
	mu.Unlock()
}

// Version reports the build version recorded by the last SetVersion call, or ""
// when none has been recorded.
func Version() string {
	mu.RLock()
	defer mu.RUnlock()
	return toolVersion
}

// Tool renders the tool clause — the repository link followed by the recorded
// build version, "[planwerk-agent](url) e1efd0d" — or the bare link when no
// version has been recorded. Footer helpers that have no version in scope use it
// so the version is threaded from a single process-wide source, the same way the
// resolved model is.
func Tool() string {
	return ToolWithVersion(Version())
}

// ToolWithVersion renders the tool clause for an explicit version: the
// repository link followed by version when non-empty, or the bare link
// otherwise. Renderers that already receive the version as a parameter (the
// review/audit/draft headers) pass it directly; Tool() supplies the recorded
// version for the helpers that do not, so both render identically.
func ToolWithVersion(version string) string {
	if v := strings.TrimSpace(version); v != "" {
		return Link + " " + v
	}
	return Link
}

// AssistantWith renders the assistant attribution clause for an explicit model
// id: "with Claude:claude-opus-5" when model is non-empty, and a bare
// "with Claude" otherwise. It never guesses an id, mirroring the Assisted-by
// commit-trailer convention. Callers thread the resolved model per-run (from the
// Claude session that produced the artifact) so concurrent runners cannot leak
// each other's model into a rendered footer.
func AssistantWith(model string) string {
	if m := strings.TrimSpace(model); m != "" {
		return AssistantMarker + ":" + m
	}
	return AssistantMarker
}

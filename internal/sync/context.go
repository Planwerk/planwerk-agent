package sync

// SyncContext carries the inputs the read-only analysis pass grounds itself in:
// the enumerated wiki entries to classify and the repository name for prompt
// context. The pass runs inside the cloned repo, so it verifies each entry's
// references against the actual code.
type SyncContext struct {
	Entries  []Entry
	RepoName string // "owner/repo" for context in the prompt
}

package report

// AddressResult is the structured result the address command's per-thread (or
// aggregate) Claude session returns: for each addressed review thread, what the
// follow-up commit changed and the per-thread terminal status, plus an overall
// summary and the run's terminal status. Unlike the other schemas in
// schema/, this contract is the address session's own output (decoded via
// decodeJSONWithRepair), not a `--format json` stdout payload — the address
// command has no `--format json` mode. It is the contract described by
// schema/address-result.schema.json.
type AddressResult struct {
	Threads []AddressedThread `json:"threads"`
	Summary string            `json:"summary"`
	// Status is the run's terminal status, one of DONE, DONE_WITH_CONCERNS,
	// BLOCKED, NEEDS_CONTEXT — the orchestrator escalates on the last two.
	Status string `json:"status"`
	// Model is the resolved Claude model id (e.g. "claude-opus-5") that
	// produced this result. It is threaded per-run to the attribution footer
	// and excluded from the serialized payload.
	Model string `json:"-"`
}

// AddressedThread is the per-thread verdict: the GraphQL thread ID the session
// acted on, its terminal status, a one-line summary of the change, and the
// files it touched. Files is omitted when the session changed nothing (e.g. a
// thread it could not address).
type AddressedThread struct {
	ThreadID string `json:"thread_id"`
	// Status mirrors the top-level enum: DONE, DONE_WITH_CONCERNS, BLOCKED,
	// NEEDS_CONTEXT. The orchestrator only resolves threads marked DONE or
	// DONE_WITH_CONCERNS, and only under --resolve.
	Status  string   `json:"status"`
	Summary string   `json:"summary"`
	Files   []string `json:"files,omitempty"`
}

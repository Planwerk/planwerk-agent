package elaborate

// Result is the structured output of an elaboration run. Body is the
// rendered Markdown ready to drop into a GitHub issue; the individual
// sections are kept separately so callers can introspect, log, or reformat
// them without re-parsing the rendered string.
type Result struct {
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	Motivation         string   `json:"motivation"`
	AffectedAreas      []string `json:"affected_areas"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	NonGoals           []string `json:"non_goals"`
	References         []string `json:"references"`
	Body               string   `json:"body"`
	// UnresolvedGaps holds reviewer gaps that the refine loop could not close
	// within the iteration budget. Empty when the elaboration cleared the score
	// bar or the optional reviewer pass was not run. Surfaced in the issue body
	// so the gaps are never silently published.
	UnresolvedGaps []string `json:"unresolved_gaps,omitempty"`
	// ReviewScore is the final executability score (0-10) from the optional
	// reviewer pass. A pointer so a legitimate 0 is surfaced and a run without
	// the reviewer omits the field entirely.
	ReviewScore *int `json:"review_score,omitempty"`
	// ReviewTarget describes what a 10/10 plan would look like, carried over
	// from the reviewer on a near-miss. Empty when the elaboration cleared the
	// bar or the reviewer pass was not run.
	ReviewTarget string `json:"review_target,omitempty"`
}

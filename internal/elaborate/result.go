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
}

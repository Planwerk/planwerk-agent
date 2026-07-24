package elaborate

// Story anchors a cluster of acceptance criteria to who benefits and why,
// rendered as "As a {role}, I want {want}, so that {so_that}". Its fields and
// JSON tags mirror planwerk.Story so the vocabulary stays consistent with the
// feature-file parser and the reviewprepared validator; it is a local type so
// the elaborate package stays decoupled from the feature-file parser.
type Story struct {
	Role     string   `json:"role"`
	Want     string   `json:"want"`
	SoThat   string   `json:"so_that"`
	Criteria []string `json:"criteria"`
}

// Result is the structured output of an elaboration run. Body is the
// rendered Markdown ready to drop into a GitHub issue; the individual
// sections are kept separately so callers can introspect, log, or reformat
// them without re-parsing the rendered string.
type Result struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Motivation  string `json:"motivation"`
	// Header is the source issue's "**Category**: … | **Scope**: …" line, carried
	// through so an elaboration that replaces the issue body does not drop the
	// header the draft-depth issue opened with. Empty when the source issue
	// carried no such line, in which case the rendered body simply starts at the
	// Description. It is derived from the source issue rather than produced by
	// Claude, so it is excluded from the serialized payload — the rendered Body
	// already carries it across a cache round-trip.
	Header string `json:"-"`
	// UserStories anchors clusters of acceptance criteria to who benefits and
	// why (role / want / so_that). Optional and proportional: omitted entirely
	// for purely mechanical or infrastructure work that serves no distinct
	// persona, never padded with a synthetic one.
	UserStories        []Story  `json:"user_stories,omitempty"`
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
	// Model is the resolved Claude model id (e.g. "claude-opus-5") that
	// produced this result. It is threaded per-run to the attribution footer
	// and excluded from the serialized payload.
	Model string `json:"-"`
}

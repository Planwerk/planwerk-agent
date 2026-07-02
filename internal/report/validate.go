package report

import (
	"fmt"
	"strings"
)

// ValidationRules returns the finding-schema rules Validate enforces, phrased as
// human-readable bullets for a schema-repair prompt. It is the single source for
// those rules: the claude package's validation-repair prompt builds its bullet
// list from this slice instead of hand-copying it. Keep this list and Validate
// in sync — update both together when a rule changes.
func ValidationRules() []string {
	return []string{
		`"title" must be a non-empty string.`,
		`"severity" must be one of BLOCKING, CRITICAL, WARNING, INFO.`,
		`"confidence" must be one of verified, likely, uncertain.`,
	}
}

// Validate reports the first schema violation in the finding: an empty Title,
// a Severity outside the enum, or a Confidence outside the enum. These are
// exactly the fields NormalizeActionability/NormalizeConfidence would otherwise
// silently default, so validating before normalization keeps schema drift from
// leaking inward as placeholder values. It returns nil when the finding is
// valid.
func (f Finding) Validate() error {
	if strings.TrimSpace(f.Title) == "" {
		return fmt.Errorf("title is empty")
	}
	if _, ok := severityOrder[f.Severity]; !ok {
		return fmt.Errorf("severity %q is not one of BLOCKING, CRITICAL, WARNING, INFO", f.Severity)
	}
	if _, ok := validConfidence[strings.ToLower(strings.TrimSpace(string(f.Confidence)))]; !ok {
		return fmt.Errorf("confidence %q is not one of verified, likely, uncertain", f.Confidence)
	}
	return nil
}

// Validate reports the first finding that violates the schema, wrapping the
// finding's index and title for context. An empty result (no findings) is
// valid.
func (r *ReviewResult) Validate() error {
	for i, f := range r.Findings {
		if err := f.Validate(); err != nil {
			return fmt.Errorf("finding %d (%q): %w", i, f.Title, err)
		}
	}
	return nil
}

package report

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Severity string

const (
	SeverityBlocking Severity = "BLOCKING"
	SeverityCritical Severity = "CRITICAL"
	SeverityWarning  Severity = "WARNING"
	SeverityInfo     Severity = "INFO"
)

var severityOrder = map[Severity]int{
	SeverityBlocking: 0,
	SeverityCritical: 1,
	SeverityWarning:  2,
	SeverityInfo:     3,
}

func ParseSeverity(s string) (Severity, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "BLOCKING":
		return SeverityBlocking, nil
	case "CRITICAL":
		return SeverityCritical, nil
	case "WARNING":
		return SeverityWarning, nil
	case "INFO":
		return SeverityInfo, nil
	default:
		return "", fmt.Errorf("unknown severity: %q", s)
	}
}

// UnmarshalJSON normalizes severity values during JSON parsing,
// so downstream code always sees uppercase canonical values.
func (s *Severity) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	// Normalize to uppercase; unknown values pass through for downstream handling.
	*s = Severity(strings.ToUpper(strings.TrimSpace(raw)))
	return nil
}

func (s Severity) MeetsMinimum(minSeverity Severity) bool {
	return severityOrder[s] <= severityOrder[minSeverity]
}

type Actionability string

const (
	ActionabilityAutoFix         Actionability = "auto-fix"
	ActionabilityNeedsDiscussion Actionability = "needs-discussion"
	ActionabilityArchitectural   Actionability = "architectural"
)

// FixClass indicates whether a finding can be auto-fixed or requires user input.
type FixClass string

const (
	FixClassAutoFix FixClass = "AUTO-FIX"
	FixClassAsk     FixClass = "ASK"
)

// Confidence indicates how certain the reviewer is about a finding.
type Confidence string

const (
	ConfidenceVerified  Confidence = "verified"
	ConfidenceLikely    Confidence = "likely"
	ConfidenceUncertain Confidence = "uncertain"
)

var validConfidence = map[string]Confidence{
	"verified":  ConfidenceVerified,
	"likely":    ConfidenceLikely,
	"uncertain": ConfidenceUncertain,
}

// NormalizeConfidence maps common variants to the canonical value.
// Unknown values default to uncertain.
func NormalizeConfidence(s string) Confidence {
	if c, ok := validConfidence[strings.ToLower(strings.TrimSpace(s))]; ok {
		return c
	}
	return ConfidenceUncertain
}

// DeriveFixClass maps an Actionability value to a FixClass.
// auto-fix → AUTO-FIX, everything else → ASK.
func DeriveFixClass(a Actionability) FixClass {
	if a == ActionabilityAutoFix {
		return FixClassAutoFix
	}
	return FixClassAsk
}

var validActionability = map[string]Actionability{
	"auto-fix":         ActionabilityAutoFix,
	"autofix":          ActionabilityAutoFix,
	"auto_fix":         ActionabilityAutoFix,
	"needs-discussion": ActionabilityNeedsDiscussion,
	"needs_discussion": ActionabilityNeedsDiscussion,
	"needsdiscussion":  ActionabilityNeedsDiscussion,
	"architectural":    ActionabilityArchitectural,
}

// NormalizeActionability maps common variants to the canonical value.
// Unknown values default to needs-discussion.
func NormalizeActionability(s string) Actionability {
	if a, ok := validActionability[strings.ToLower(strings.TrimSpace(s))]; ok {
		return a
	}
	return ActionabilityNeedsDiscussion
}

// FixOption is one of several alternative ways to address a finding.
// Reviewers attach options to non-auto-fix findings so the consumer can
// see the trade-offs side-by-side instead of receiving a single fix.
type FixOption struct {
	ID            string `json:"id"`                        // "A", "B", "C"
	Approach      string `json:"approach"`                  // one-sentence summary
	Pros          string `json:"pros,omitempty"`            // benefits, comma-separated or short sentence
	Cons          string `json:"cons,omitempty"`            // drawbacks
	Effort        string `json:"effort,omitempty"`          // LOW | MED | HIGH
	RiskIfSkipped string `json:"risk_if_skipped,omitempty"` // what happens if the option is not chosen
}

type Finding struct {
	ID                      string        `json:"id"`
	Severity                Severity      `json:"severity"`
	Title                   string        `json:"title"`
	File                    string        `json:"file"`
	Line                    int           `json:"line,omitempty"`
	LineEnd                 int           `json:"line_end,omitempty"`
	Pattern                 string        `json:"pattern,omitempty"`
	Actionability           Actionability `json:"actionability,omitempty"`
	FixClass                FixClass      `json:"fix_class,omitempty"`
	Confidence              Confidence    `json:"confidence,omitempty"`
	Problem                 string        `json:"problem"`
	Action                  string        `json:"action"`
	CodeSnippet             string        `json:"code_snippet,omitempty"`
	SuggestedFix            string        `json:"suggested_fix,omitempty"`
	FixOptions              []FixOption   `json:"fix_options,omitempty"`
	RecommendedOption       string        `json:"recommended_option,omitempty"`
	RecommendationReasoning string        `json:"recommendation_reasoning,omitempty"`
	RelatedTo               []string      `json:"related_to,omitempty"`
}

type ReviewResult struct {
	Findings       []Finding `json:"findings"`
	Summary        string    `json:"summary"`
	Recommendation string    `json:"recommendation"`
}

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

// ParseConfidence parses a user-supplied confidence threshold (e.g. the
// --min-confidence flag). Unlike NormalizeConfidence it rejects unknown
// values so a typo surfaces as an error instead of silently widening the
// filter.
func ParseConfidence(s string) (Confidence, error) {
	if c, ok := validConfidence[strings.ToLower(strings.TrimSpace(s))]; ok {
		return c, nil
	}
	return "", fmt.Errorf("unknown confidence: %q", s)
}

// confidenceRank orders confidence from strongest (0) to weakest. It drives
// both display ordering (verified findings first within a severity) and the
// --min-confidence filter. An unset/unknown confidence ranks with "likely":
// neither the best nor the worst, so an unannotated finding is never buried
// in the low-confidence section by default.
var confidenceRank = map[Confidence]int{
	ConfidenceVerified:  0,
	ConfidenceLikely:    1,
	ConfidenceUncertain: 2,
}

// Rank returns the ordering weight of c (0 = strongest). Unknown/empty values
// rank with "likely".
func (c Confidence) Rank() int {
	if r, ok := confidenceRank[c]; ok {
		return r
	}
	return 1
}

// MeetsMinimum reports whether c is at least as strong as minConfidence.
// An empty minConfidence imposes no threshold (every finding passes).
func (c Confidence) MeetsMinimum(minConfidence Confidence) bool {
	if minConfidence == "" {
		return true
	}
	return c.Rank() <= minConfidence.Rank()
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
	// ConfirmedBy lists the review passes that independently flagged this
	// finding (e.g. "review", "adversarial", "compliance"). When two or more
	// passes agree the finding's confidence is boosted one step and the
	// renderer marks it as cross-pass confirmed. It is provenance the model
	// never sets — the merge step assigns it.
	ConfirmedBy []string `json:"confirmed_by,omitempty"`
	// VerificationNote records why the claim-verification pass refuted a
	// BLOCKING/CRITICAL finding, paired with a demotion to uncertain confidence.
	// It is set only by the review pipeline's claim-verification step — the model
	// never populates it at structure time — and routes the demoted finding into
	// the Unverified section.
	VerificationNote string `json:"verification_note,omitempty"`
}

type ReviewResult struct {
	Findings       []Finding `json:"findings"`
	Summary        string    `json:"summary"`
	Recommendation string    `json:"recommendation"`
	// Model is the resolved Claude model id (e.g. "claude-opus-4-8") that
	// produced this result. It is threaded per-run to the attribution footer
	// and excluded from the serialized payload.
	Model string `json:"-"`
	// WikiRepo and WikiCommit record the target repo's GitHub Wiki and the
	// concrete commit its knowledge was resolved to, surfaced in the report
	// header so a review is reproducible against a fixed wiki state rather than
	// drifting with a moving wiki. Both are empty when no wiki was used. They
	// are threaded per-run from the resolved wiki and excluded from the cached
	// payload; WikiCommit is re-attached to the machine-readable data block.
	WikiRepo   string `json:"-"`
	WikiCommit string `json:"-"`
}

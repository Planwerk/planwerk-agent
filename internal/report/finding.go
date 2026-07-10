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
	// SnippetCheck records the quote-or-demote gate's per-finding decision:
	// SnippetCheckPassed when the finding's quoted code was located in the
	// changed files, or a "demoted: <reason>" string when it was not. It is set
	// only by the review pipeline's snippet-verification step — the model never
	// populates it. Unlike VerificationNote it does NOT route the finding
	// (Unverified is unchanged); it is a pure record of what the gate examined,
	// so a run where every finding passed is distinguishable from one where the
	// gate never ran (which leaves it empty).
	SnippetCheck string `json:"snippet_check,omitempty"`
	// ClaimCheck records the claim-verification gate's per-finding decision as a
	// machine token: ClaimCheckConfirmed, ClaimCheckRefuted, or
	// ClaimCheckNoVerdict (the gate ran but returned no verdict for this
	// finding — the fail-open case). It is set only by the review pipeline's
	// claim-verification step. The human-readable refutation reason still lives
	// in VerificationNote; this token does not duplicate it and does not route
	// the finding.
	ClaimCheck string `json:"claim_check,omitempty"`
}

// SnippetCheckPassed is the SnippetCheck value recorded on a finding whose
// quoted code the snippet gate located in the changed files. A demotion records
// a "demoted: <reason>" string instead.
const SnippetCheckPassed = "passed"

// ClaimCheck token values recorded by the claim-verification gate on each
// finding it examined.
const (
	ClaimCheckConfirmed = "confirmed"
	ClaimCheckRefuted   = "refuted"
	ClaimCheckNoVerdict = "no-verdict"
)

// Unverified reports whether the finding did not survive the hygiene stage and
// so belongs in the report's Unverified section rather than in its severity
// bucket. A low-confidence, low-severity finding is demoted so an uncertain nit
// never sits next to a verified bug. A BLOCKING/CRITICAL claim the verification
// pass explicitly refuted (uncertain + a VerificationNote) is demoted too — the
// counter-evidence makes it stronger than a merely-unverifiable finding, so it
// must not remain in the blocking section. A merely-uncertain BLOCKING/CRITICAL
// finding with no counter-evidence is NOT unverified: it is too important to
// bury, so it stays in its severity bucket.
//
// This is the single predicate that decides both where the renderer files a
// finding (see Categorize) and, in the implement command, whether an editing
// session may act on it — a finding that returns true is reported but never
// applied. Defining it once keeps the two from drifting apart.
func (f Finding) Unverified() bool {
	if f.Confidence != ConfidenceUncertain {
		return false
	}
	return f.VerificationNote != "" || f.Severity == SeverityWarning || f.Severity == SeverityInfo
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
	// Gates records what each demotion gate examined and demoted in this run.
	// Unlike the neighboring json:"-" fields it IS serialized: it rides the
	// cached result and the machine-readable data block so the refuted/examined
	// ratio can be computed from history rather than from a fresh run. A nil
	// per-gate entry means that gate never ran, which keeps a clean pass
	// distinguishable from a skipped gate.
	Gates *GateStats `json:"gates,omitempty"`
}

// GateStats aggregates the per-run counts of the review's two demotion gates.
// A nil per-gate field means that gate did not run this run.
type GateStats struct {
	Snippet *SnippetGateStats `json:"snippet,omitempty"`
	Claim   *ClaimGateStats   `json:"claim,omitempty"`
}

// SnippetGateStats records the quote-or-demote gate's per-run counts: how many
// findings it Examined and the Demoted subset whose quoted code it could not
// locate in the changed files. Both are recorded even when zero, so a gate that
// ran but demoted nothing is distinguishable from one that never ran (a nil
// SnippetGateStats).
type SnippetGateStats struct {
	Examined int `json:"examined"`
	Demoted  int `json:"demoted"`
}

// ClaimGateStats records the claim-verification gate's per-run counts: how many
// findings it Sent, how many Verdicts came back, and how many it Refuted. The
// refuted/sent ratio is the pass's own no-op signal. The verifier is asked to
// confirm unless it finds quoted counter-evidence, and it is handed the
// finding's claim along with the code — two nudges toward agreement — so a pass
// that refutes nothing across many runs is not verifying, it is agreeing, and
// belongs sharpened or deleted rather than left to look productive. Recording
// the counts on the run (not only in a log line that dies on exit) is what lets
// that ratio accumulate from real runs. Sent>0 with Verdicts=0 is the fail-open
// case: the gate ran but the verifier returned nothing.
type ClaimGateStats struct {
	Sent     int `json:"sent"`
	Verdicts int `json:"verdicts"`
	Refuted  int `json:"refuted"`
}

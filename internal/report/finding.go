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
	// It is set only by the finding-hygiene gates (review, implement's
	// self-review, audit), in their claim-verification step — the model never
	// populates it at structure time — and routes the demoted finding into the
	// Unverified section.
	VerificationNote string `json:"verification_note,omitempty"`
	// SnippetCheck records the quote-or-demote gate's per-finding decision:
	// SnippetCheckPassed when the finding's quoted code was located in the
	// changed files (anywhere in the checkout for an audit), a "verified: <how>"
	// string for a finding verified another way (its quote found outside the
	// changed files, in a review; its cited path absent from the checkout, in an
	// audit), or a "demoted: <reason>" string. It is set only by the
	// finding-hygiene gates (review, implement's self-review, audit), in their
	// snippet-verification step — the model never populates it. Unlike
	// VerificationNote it does NOT route the finding (Unverified is
	// unchanged); it is a pure record of what the gate examined, so a run where
	// every finding passed is distinguishable from one where the gate never ran
	// (which leaves it empty).
	SnippetCheck string `json:"snippet_check,omitempty"`
	// ClaimCheck records the claim-verification gate's per-finding decision as a
	// machine token: ClaimCheckConfirmed, ClaimCheckRefuted, or
	// ClaimCheckNoVerdict (the gate ran but returned no verdict for this
	// finding — the fail-open case). It is set only by the finding-hygiene gates
	// (review, implement's self-review, audit), in their claim-verification
	// step. The human-readable refutation reason still lives in
	// VerificationNote; this token does not duplicate it and does not route the
	// finding.
	ClaimCheck string `json:"claim_check,omitempty"`
	// LineCheck records the line anchor's per-finding decision
	// (hygiene.AnchorFindings): LineCheckAnchored, LineCheckReanchored,
	// LineCheckRelocated, or LineCheckUnanchored. It is set only by the
	// finding-hygiene stages (review, implement's self-review, audit), never by
	// the model, and is empty for a finding the anchor did not examine. An
	// audit never relocates a finding. It never moves a finding between report
	// sections; --inline posts no inline comment for a LineCheckUnanchored
	// finding, which stays in the summary comment, and its SARIF result has no
	// region (decision 139).
	LineCheck string `json:"line_check,omitempty"`
}

// SnippetCheckPassed is the SnippetCheck value recorded on a finding whose
// quoted code the snippet gate located in the changed files, or anywhere in the
// checkout for an audit. A demotion records a "demoted: <reason>" string
// instead.
const SnippetCheckPassed = "passed"

// ClaimCheck token values recorded by the claim-verification gate on each
// finding it examined.
const (
	ClaimCheckConfirmed = "confirmed"
	ClaimCheckRefuted   = "refuted"
	ClaimCheckNoVerdict = "no-verdict"
)

// LineCheck values recorded by the line anchor on each finding it examined:
// the model's range already lay on the quoted code (anchored), it was moved to
// the quoted code in the same file (reanchored) or in the one other changed
// file that holds it (relocated), or the quote pins no single place in the
// head content and the model's line was kept (unanchored).
const (
	LineCheckAnchored   = "anchored"
	LineCheckReanchored = "reanchored"
	LineCheckRelocated  = "relocated"
	LineCheckUnanchored = "unanchored"
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

// MeetsThresholds reports whether f has a known severity and passes the
// minSeverity and minConfidence thresholds. Categorize leaves out every finding
// that fails it, so such a finding appears in no section of the rendered
// report; the inline review applies the same test before it posts a comment.
// An empty minConfidence imposes no threshold; an empty minSeverity admits only
// BLOCKING, as Severity.MeetsMinimum does.
func (f Finding) MeetsThresholds(minSeverity Severity, minConfidence Confidence) bool {
	if _, ok := severityOrder[f.Severity]; !ok {
		return false
	}
	return f.Severity.MeetsMinimum(minSeverity) && f.Confidence.MeetsMinimum(minConfidence)
}

type ReviewResult struct {
	Findings       []Finding `json:"findings"`
	Summary        string    `json:"summary"`
	Recommendation string    `json:"recommendation"`
	// Model is the resolved Claude model id (e.g. "claude-opus-5-5") that
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
	// Gates records what each hygiene stage did in this run: what the line
	// anchor moved, what the two demotion gates examined and demoted, and what
	// the changed-file check, which records and never demotes, found. Unlike
	// the neighboring json:"-" fields it IS serialized: it rides the cached
	// result and the machine-readable data block so the refuted/examined ratio
	// can be computed from history rather than from a fresh run. A nil
	// per-stage entry means that stage never ran, which keeps a clean pass
	// distinguishable from a skipped stage.
	Gates *GateStats `json:"gates,omitempty"`
	// Passes records how each secondary pass the run requested ended: the
	// adversarial pass, the coverage map, the feature-compliance check, and each
	// domain specialist. It is serialized like Gates (the cached result, the data
	// block, --format json). nil means the run requested no secondary pass, or
	// the result predates pass recording. The primary review is never listed,
	// since its failure fails the run (decision 135).
	Passes []PassStatus `json:"passes,omitempty"`
	// ExcludedFiles lists the changed files --diff-exclusions left out of the
	// diff the review session read. It is serialized like Gates (the cached
	// result, the data block, --format json) and set by the pipeline, never by
	// a session. nil means exclusions were off, no changed file matched, or the
	// result predates the field (decision 137).
	ExcludedFiles []ExcludedFile `json:"excluded_files,omitempty"`
	// FilesReviewed is the finder's own account of the files its prompt's
	// Changed Files section listed (FinderOutput's files_reviewed): each one
	// reviewed, or skipped with a reason. hygiene.CheckFileLedger reads it into
	// Gates.Files. It rides the cached result, but the --format json output and
	// the data block carry the Gates.Files record rather than this list
	// (decision 138).
	FilesReviewed []FileReview `json:"files_reviewed,omitempty"`
}

// FileReview is one entry of a finder's files_reviewed account: a file its
// prompt listed, by its path as listed, with Status FileReviewed, or
// FileSkipped and the Reason the session gave for not reading it (decision
// 138).
type FileReview struct {
	File   string `json:"file"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// FileReview.Status and MissingFile.Status values. A finder answers reviewed
// or skipped; unreported marks a listed file the finder left out of its
// account, and appears on a MissingFile only.
const (
	FileReviewed   = "reviewed"
	FileSkipped    = "skipped"
	FileUnreported = "unreported"
)

// GateStats aggregates the per-run counts of the hygiene stages of a review or
// an audit, in stage order: the line anchor, the two demotion gates, and the
// changed-file check, which only a review runs. The anchor and the check
// record and never demote. A nil per-stage field means that stage did not run
// this run; a nil Files means the review prompt listed no changed files, and
// is always nil on an audit.
type GateStats struct {
	Anchor  *AnchorGateStats  `json:"anchor,omitempty"`
	Snippet *SnippetGateStats `json:"snippet,omitempty"`
	Claim   *ClaimGateStats   `json:"claim,omitempty"`
	Files   *FileGateStats    `json:"files,omitempty"`
}

// AnchorGateStats records the line anchor's per-run counts. Examined counts the
// findings that carry a file and a non-blank quote, in each pass before the
// merge, so a defect that several passes report counts once per pass.
// Reanchored, Relocated and Unanchored are disjoint subsets of it, so the
// findings confirmed on the model's range (LineCheckAnchored) are Examined
// minus the three. The counts are recorded even when zero, so an anchor that
// ran but moved nothing is distinguishable from one that never ran (a nil
// AnchorGateStats); Relocated is omitted when zero, like RecoveredOutsideDiff,
// since a move to another file is the exception (decision 139).
type AnchorGateStats struct {
	Examined   int `json:"examined"`
	Reanchored int `json:"reanchored"`
	Relocated  int `json:"relocated,omitempty"`
	Unanchored int `json:"unanchored"`
}

// SnippetGateStats records the quote-or-demote gate's per-run counts, for the
// demotion gates of a review or an audit: how many findings it Examined, the
// Demoted subset whose quoted code it could locate neither in the changed files
// nor anywhere else in the checkout, and the RecoveredOutsideDiff subset whose
// quoted code was absent from the changed files but found elsewhere in the
// checkout — a verified cross-file finding that is kept, not demoted. An audit,
// which has no changed files and searches the whole checkout, never sets
// RecoveredOutsideDiff. Examined and Demoted are recorded even when zero, so a
// gate that ran but demoted nothing is distinguishable from one that never ran
// (a nil SnippetGateStats); RecoveredOutsideDiff is omitted when zero since the
// fallback is the exception, not the norm.
type SnippetGateStats struct {
	Examined             int `json:"examined"`
	Demoted              int `json:"demoted"`
	RecoveredOutsideDiff int `json:"recovered_outside_diff,omitempty"`
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

// FileGateStats records the changed-file check: Changed is the number of files
// the review prompt listed, Reviewed the subset the review session reported
// reading, and Missing every other listed file, in listed order. The counts are
// the session's own claim, so Reviewed is an upper bound; the check moves no
// finding (decision 138).
type FileGateStats struct {
	Changed  int           `json:"changed"`
	Reviewed int           `json:"reviewed"`
	Missing  []MissingFile `json:"missing,omitempty"`
}

// MissingFile is a listed file the review session did not report reading:
// Status FileSkipped with the Reason the session gave, or FileUnreported when
// its account left the file out.
type MissingFile struct {
	File   string `json:"file"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

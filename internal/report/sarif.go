package report

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/planwerk/planwerk-agent/internal/attribution"
)

// The fixed values of the SARIF log RenderSARIF writes.
const (
	sarifSchemaURI      = "https://json.schemastore.org/sarif-2.1.0.json"
	sarifVersion        = "2.1.0"
	sarifToolName       = "planwerk-agent"
	sarifFingerprintKey = "planwerkFinding/v1"
	sarifFallbackRuleID = "finding"
	sarifLevelError     = "error"
	sarifLevelWarning   = "warning"
	sarifLevelNote      = "note"
)

// The sarif* types model the subset of SARIF 2.1.0 (OASIS) that RenderSARIF
// writes: one run whose results each carry one physical location, plus the
// run's invocation for the notifications that have no location. The slices a
// consumer iterates (rules, results) are always encoded as arrays, never null.
type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool        sarifTool         `json:"tool"`
	Invocations []sarifInvocation `json:"invocations"`
	Results     []sarifResult     `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	Version        string      `json:"version,omitempty"`
	InformationURI string      `json:"informationUri"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string       `json:"id"`
	ShortDescription sarifMessage `json:"shortDescription"`
}

type sarifResult struct {
	RuleID              string            `json:"ruleId"`
	Level               string            `json:"level"`
	Message             sarifMessage      `json:"message"`
	Locations           []sarifLocation   `json:"locations"`
	PartialFingerprints map[string]string `json:"partialFingerprints"`
	Properties          sarifProperties   `json:"properties"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysicalLocation `json:"physicalLocation"`
}

type sarifPhysicalLocation struct {
	ArtifactLocation sarifArtifactLocation `json:"artifactLocation"`
	Region           *sarifRegion          `json:"region,omitempty"`
}

type sarifArtifactLocation struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine int `json:"startLine"`
	EndLine   int `json:"endLine,omitempty"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifInvocation struct {
	ExecutionSuccessful        bool                `json:"executionSuccessful"`
	ToolExecutionNotifications []sarifNotification `json:"toolExecutionNotifications,omitempty"`
}

type sarifNotification struct {
	Level   string       `json:"level"`
	Message sarifMessage `json:"message"`
}

// sarifProperties is a result's property bag: the finding's own fields that
// SARIF has no slot for. Severity keeps the four levels that the result's
// level folds into three, and Unverified is always written, false included.
type sarifProperties struct {
	FindingID     string `json:"finding_id,omitempty"`
	Severity      string `json:"severity"`
	Confidence    string `json:"confidence,omitempty"`
	Actionability string `json:"actionability,omitempty"`
	Unverified    bool   `json:"unverified"`
}

// RenderSARIF writes the findings Categorize keeps under minSeverity and
// minConfidence, the set RenderJSON prints and in its order, as one SARIF
// 2.1.0 log for GitHub code scanning: one run of the tool planwerk-agent at
// version, one result per finding, and one rule per distinct ruleId. A finding
// Finding.Unverified reports becomes level note whatever its severity, so a
// refuted claim or an uncertain nit cannot fail a check as an error. A finding
// that names no file is not a result but a warning notification on the run's
// invocation, because code scanning places every result in a file. The log is
// the whole document, so the caller writes nothing else to the same writer
// (decision 134).
func (r *Renderer) RenderSARIF(result ReviewResult, minSeverity Severity, minConfidence Confidence, version string) error {
	cf := Categorize(result.Findings, minSeverity, minConfidence)
	rules := make([]sarifRule, 0)
	results := make([]sarifResult, 0, cf.Total())
	ruleListed := make(map[string]bool)
	fingerprintSeen := make(map[string]int)
	var fileless []string
	for _, f := range cf.All() {
		if strings.TrimSpace(f.File) == "" {
			fileless = append(fileless, strings.TrimSpace(f.ID+" "+f.Title))
			continue
		}
		ruleID := sarifRuleID(f.Pattern)
		if !ruleListed[ruleID] {
			ruleListed[ruleID] = true
			rules = append(rules, sarifRule{ID: ruleID, ShortDescription: sarifMessage{Text: sarifRuleDescription(ruleID, f.Pattern)}})
		}
		fingerprint := sarifFingerprint(f, ruleID)
		n := fingerprintSeen[fingerprint]
		fingerprintSeen[fingerprint] = n + 1
		if n > 0 {
			fingerprint += "#" + strconv.Itoa(n)
		}
		results = append(results, sarifResult{
			RuleID:              ruleID,
			Level:               sarifLevel(f),
			Message:             sarifMessage{Text: sarifMessageText(f)},
			Locations:           []sarifLocation{sarifLocationFor(f)},
			PartialFingerprints: map[string]string{sarifFingerprintKey: fingerprint},
			Properties: sarifProperties{
				FindingID:     f.ID,
				Severity:      string(f.Severity),
				Confidence:    string(f.Confidence),
				Actionability: string(f.Actionability),
				Unverified:    f.Unverified(),
			},
		})
	}

	doc := sarifLog{
		Schema:  sarifSchemaURI,
		Version: sarifVersion,
		Runs: []sarifRun{{
			Tool: sarifTool{Driver: sarifDriver{
				Name:           sarifToolName,
				Version:        strings.TrimSpace(version),
				InformationURI: attribution.RepoURL,
				Rules:          rules,
			}},
			Invocations: []sarifInvocation{sarifInvocationFor(fileless)},
			Results:     results,
		}},
	}
	enc := json.NewEncoder(r.w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// sarifInvocationFor builds the run's only invocation. executionSuccessful is
// always true, because a run whose review failed renders nothing. fileless
// holds a label for each finding that names no file; when there is one, the
// invocation carries a single warning notification that names them all.
func sarifInvocationFor(fileless []string) sarifInvocation {
	inv := sarifInvocation{ExecutionSuccessful: true}
	if len(fileless) > 0 {
		inv.ToolExecutionNotifications = []sarifNotification{{
			Level: sarifLevelWarning,
			Message: sarifMessage{Text: fmt.Sprintf("%d finding(s) name no file, so code scanning cannot show them as results: %s",
				len(fileless), strings.Join(fileless, "; "))},
		}}
	}
	return inv
}

// sarifLevel maps f to a SARIF level: note when Finding.Unverified reports it,
// otherwise error for BLOCKING and CRITICAL, warning for WARNING, and note for
// INFO or an unknown severity.
func sarifLevel(f Finding) string {
	if f.Unverified() {
		return sarifLevelNote
	}
	switch f.Severity {
	case SeverityBlocking, SeverityCritical:
		return sarifLevelError
	case SeverityWarning:
		return sarifLevelWarning
	default:
		return sarifLevelNote
	}
}

// sarifRuleID returns "pattern/<slug>" for a finding's pattern name: the name
// lowercased, with its letters and digits kept and every run of other
// characters between them turned into one '-', so "Missing context.Context
// parameter" gives "pattern/missing-context-context-parameter". A name with no
// letter or digit, the empty one included, gives sarifFallbackRuleID.
func sarifRuleID(pattern string) string {
	var slug strings.Builder
	gap := false
	for _, r := range strings.ToLower(pattern) {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			gap = true
			continue
		}
		if gap && slug.Len() > 0 {
			slug.WriteByte('-')
		}
		gap = false
		slug.WriteRune(r)
	}
	if slug.Len() == 0 {
		return sarifFallbackRuleID
	}
	return "pattern/" + slug.String()
}

// sarifRuleDescription is the shortDescription of the rule ruleID, which
// sarifRuleID derived from pattern: the trimmed pattern name, or a fixed text
// for sarifFallbackRuleID.
func sarifRuleDescription(ruleID, pattern string) string {
	if ruleID == sarifFallbackRuleID {
		return "A planwerk-agent finding that names no review pattern"
	}
	return strings.TrimSpace(pattern)
}

// sarifMessageText joins f's trimmed title and problem into a result message,
// title first, because code scanning shows the message's first sentence as the
// alert's title. A title that already ends a sentence is followed by a space,
// any other by ". ". When one of the two is empty the other stands alone, and
// when both are, the message names the finding's ID.
func sarifMessageText(f Finding) string {
	title := strings.TrimSpace(f.Title)
	problem := strings.TrimSpace(f.Problem)
	switch {
	case title == "" && problem == "":
		return strings.TrimSpace("planwerk-agent finding " + f.ID)
	case title == "":
		return problem
	case problem == "":
		return title
	case strings.HasSuffix(title, ".") || strings.HasSuffix(title, "!") || strings.HasSuffix(title, "?"):
		return title + " " + problem
	default:
		return title + ". " + problem
	}
}

// sarifArtifactURI turns a finding's repository-relative file into a relative
// URI reference: a leading "./" is dropped and the path is percent-escaped, so
// "./a b/c.go" gives "a%20b/c.go". A first segment that holds a colon gets the
// "./" back, so it cannot be read as a URI scheme.
func sarifArtifactURI(file string) string {
	return (&url.URL{Path: strings.TrimPrefix(strings.TrimSpace(file), "./")}).String()
}

// sarifLocationFor is f's physical location: its file, and a region only when
// it cites a line. The region ends at LineEnd only when LineEnd lies after
// Line.
func sarifLocationFor(f Finding) sarifLocation {
	loc := sarifLocation{PhysicalLocation: sarifPhysicalLocation{
		ArtifactLocation: sarifArtifactLocation{URI: sarifArtifactURI(f.File)},
	}}
	if f.Line > 0 {
		region := &sarifRegion{StartLine: f.Line}
		if f.LineEnd > f.Line {
			region.EndLine = f.LineEnd
		}
		loc.PhysicalLocation.Region = region
	}
	return loc
}

// sarifFingerprint is the hex SHA-256 of f's file, lowercased as SuppressionKey
// lowercases it and then escaped as an artifact URI, its ruleID, and the code
// it quotes, with whitespace collapsed as SuppressionKey collapses it, so code
// scanning keeps one alert while the quoted code moves or is re-indented. A
// finding that quotes nothing is anchored on its line instead. RenderSARIF
// appends "#n" to the n-th repeat of a fingerprint.
func sarifFingerprint(f Finding, ruleID string) string {
	anchor := normalizedSnippet(f.CodeSnippet)
	if anchor == "" {
		anchor = "line:" + strconv.Itoa(f.Line)
	}
	sum := sha256.Sum256([]byte(sarifArtifactURI(strings.ToLower(f.File)) + "|" + ruleID + "|" + anchor))
	return hex.EncodeToString(sum[:])
}

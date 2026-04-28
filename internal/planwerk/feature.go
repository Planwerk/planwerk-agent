// Package planwerk detects and parses Planwerk feature files from a repository.
package planwerk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// featureIDRe matches Planwerk-style feature IDs like "CC-0042". The text
// is uppercased before matching, so the character class needs only A-Z.
var featureIDRe = regexp.MustCompile(`[A-Z]{2,}-\d+`)

// Requirement is a single requirement from a Planwerk feature file.
type Requirement struct {
	ID          string     `json:"id"`
	Description string     `json:"description"`
	Priority    string     `json:"priority"`
	Rationale   string     `json:"rationale"`
	Scenarios   []Scenario `json:"scenarios"`
}

// Scenario is a BDD-style scenario within a requirement.
type Scenario struct {
	Name    string   `json:"name"`
	When    string   `json:"when"`
	Then    string   `json:"then"`
	AndThen []string `json:"and_then"`
}

// Story is a user story from a Planwerk feature file.
type Story struct {
	Title    string   `json:"title"`
	Role     string   `json:"role"`
	Want     string   `json:"want"`
	SoThat   string   `json:"so_that"`
	Criteria []string `json:"criteria"`
}

// TestSpecification is a planned test from the feature file.
type TestSpecification struct {
	TestFile     string `json:"test_file"`
	TestFunction string `json:"test_function"`
	Story        string `json:"story"`
	Expected     string `json:"expected"`
	RequirementID string `json:"requirement_id"`
}

// Task is a planned implementation task.
type Task struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Status       string   `json:"status"`
	Requirements []string `json:"requirements"`
}

// Feature represents a parsed Planwerk feature file.
type Feature struct {
	FeatureID          string              `json:"feature_id"`
	Title              string              `json:"title"`
	Slug               string              `json:"slug"`
	Status             string              `json:"status"`
	Description        string              `json:"description"`
	Stories            []Story             `json:"stories"`
	Requirements       []Requirement       `json:"requirements"`
	Tasks              []Task              `json:"tasks"`
	TestSpecifications []TestSpecification `json:"test_specifications"`
	FilePath           string              `json:"-"` // path to the feature file (not serialized)
}

// DetectFeature looks for a Planwerk feature file that matches the given PR.
// It searches .planwerk/features/, .planwerk/progress/, and .planwerk/completed/
// for feature files (the three lifecycle stages: planned, in-progress, done) and
// selects one by signal strength, in order:
//
//  1. branch name (e.g. "feature/CC-0042") — authoritative: if the branch
//     carries an explicit feature ID, that ID is the user's intent; either
//     the matching feature file is returned or nil (no fall-through).
//  2. PR title (e.g. "feat(CC-0042): ...") — authoritative, same semantics.
//  3. changed file paths under .planwerk/{features,progress,reviews,completed}/ —
//     fallback, only used when neither branch nor title carries a feature ID.
//  4. PR body — last-resort fallback, only accepted if exactly one candidate
//     is referenced (the body often contains cross-references to unrelated
//     features).
func DetectFeature(repoDir, prTitle, prBody, branchName string, changedFiles []string) (*Feature, error) {
	planwerkDir := filepath.Join(repoDir, ".planwerk")

	var candidates []*Feature
	for _, subdir := range []string{"features", "progress", "completed"} {
		dir := filepath.Join(planwerkDir, subdir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // directory may not exist
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			f, err := parseFeatureFile(path)
			if err != nil || f.FeatureID == "" {
				continue
			}
			f.FilePath = path
			candidates = append(candidates, f)
		}
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	// Stage 1: branch name — authoritative. If the branch carries a
	// feature ID, that ID is the user's declared intent; return the
	// matching candidate or nil, but do not fall through.
	if ids := extractFeatureIDs(branchName); len(ids) > 0 {
		return matchByIDs(candidates, ids), nil
	}

	// Stage 2: PR title — authoritative, same semantics as branch.
	if ids := extractFeatureIDs(prTitle); len(ids) > 0 {
		return matchByIDs(candidates, ids), nil
	}

	// Stage 3: changed file paths under .planwerk/ subtrees that track
	// feature work. Other paths are ignored to avoid noise from e.g. a
	// pattern doc that mentions an unrelated feature ID.
	if m := matchUnique(candidates, planwerkPathsText(changedFiles)); m != nil {
		return m, nil
	}

	// Stage 4: PR body — only accepted if exactly one candidate matches,
	// to avoid trapping on cross-references like "following CC-0050".
	if m := matchUnique(candidates, strings.ToUpper(prBody)); m != nil {
		return m, nil
	}

	return nil, nil
}

// extractFeatureIDs pulls feature-ID-looking tokens from text, uppercased
// and deduplicated in order of first appearance.
func extractFeatureIDs(text string) []string {
	if text == "" {
		return nil
	}
	matches := featureIDRe.FindAllString(strings.ToUpper(text), -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	ids := make([]string, 0, len(matches))
	for _, m := range matches {
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		ids = append(ids, m)
	}
	return ids
}

// matchByIDs returns the first candidate whose FeatureID equals any of the
// given IDs (case-insensitive), or nil if none match.
func matchByIDs(candidates []*Feature, ids []string) *Feature {
	for _, id := range ids {
		for _, f := range candidates {
			if strings.EqualFold(f.FeatureID, id) {
				return f
			}
		}
	}
	return nil
}

// matchUnique returns the single candidate whose FeatureID appears in text,
// or nil if zero or more than one candidate matches.
func matchUnique(candidates []*Feature, text string) *Feature {
	if text == "" {
		return nil
	}
	var found *Feature
	for _, f := range candidates {
		if strings.Contains(text, strings.ToUpper(f.FeatureID)) {
			if found != nil {
				return nil // ambiguous
			}
			found = f
		}
	}
	return found
}

// planwerkPathsText extracts the basenames of changed files that live under
// .planwerk/{features,progress,reviews,completed}/ and joins them into a
// single uppercase search string.
func planwerkPathsText(changedFiles []string) string {
	var parts []string
	for _, p := range changedFiles {
		p = filepath.ToSlash(p)
		if !strings.HasPrefix(p, ".planwerk/") {
			continue
		}
		rest := strings.TrimPrefix(p, ".planwerk/")
		slash := strings.IndexByte(rest, '/')
		if slash < 0 {
			continue
		}
		switch rest[:slash] {
		case "features", "progress", "reviews", "completed":
			parts = append(parts, filepath.Base(p))
		}
	}
	return strings.ToUpper(strings.Join(parts, " "))
}

func parseFeatureFile(path string) (*Feature, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f Feature
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// FormatForPrompt serializes the feature's requirements, stories, test specs,
// and tasks into a structured text block suitable for injection into a Claude prompt.
func (f *Feature) FormatForPrompt() string {
	var sb strings.Builder

	sb.WriteString("Feature: ")
	sb.WriteString(f.Title)
	sb.WriteString(" (")
	sb.WriteString(f.FeatureID)
	sb.WriteString(")\n\n")

	if f.Description != "" {
		sb.WriteString("Description:\n")
		sb.WriteString(f.Description)
		sb.WriteString("\n\n")
	}

	// User Stories with acceptance criteria
	if len(f.Stories) > 0 {
		sb.WriteString("## User Stories\n\n")
		for i, s := range f.Stories {
			sb.WriteString("### Story ")
			sb.WriteString(itoa(i + 1))
			sb.WriteString(": ")
			sb.WriteString(s.Title)
			sb.WriteString("\n")
			sb.WriteString("As a ")
			sb.WriteString(s.Role)
			sb.WriteString(", I want ")
			sb.WriteString(s.Want)
			sb.WriteString(", so that ")
			sb.WriteString(s.SoThat)
			sb.WriteString("\n\nAcceptance Criteria:\n")
			for _, c := range s.Criteria {
				sb.WriteString("- ")
				sb.WriteString(c)
				sb.WriteString("\n")
			}
			sb.WriteString("\n")
		}
	}

	// Requirements with scenarios
	if len(f.Requirements) > 0 {
		sb.WriteString("## Requirements\n\n")
		for _, r := range f.Requirements {
			sb.WriteString("### ")
			sb.WriteString(r.ID)
			sb.WriteString(" (")
			sb.WriteString(r.Priority)
			sb.WriteString("): ")
			sb.WriteString(r.Description)
			sb.WriteString("\n")
			if r.Rationale != "" {
				sb.WriteString("Rationale: ")
				sb.WriteString(r.Rationale)
				sb.WriteString("\n")
			}
			for _, s := range r.Scenarios {
				sb.WriteString("\n  Scenario: ")
				sb.WriteString(s.Name)
				sb.WriteString("\n  When: ")
				sb.WriteString(s.When)
				sb.WriteString("\n  Then: ")
				sb.WriteString(s.Then)
				for _, at := range s.AndThen {
					sb.WriteString("\n  And then: ")
					sb.WriteString(at)
				}
				sb.WriteString("\n")
			}
			sb.WriteString("\n")
		}
	}

	// Test specifications
	if len(f.TestSpecifications) > 0 {
		sb.WriteString("## Planned Test Specifications\n\n")
		for _, ts := range f.TestSpecifications {
			if ts.RequirementID == "" && ts.Story == "" {
				continue // skip discovered-during-implementation tests
			}
			sb.WriteString("- ")
			sb.WriteString(ts.TestFunction)
			sb.WriteString(" in ")
			sb.WriteString(ts.TestFile)
			if ts.RequirementID != "" {
				sb.WriteString(" [")
				sb.WriteString(ts.RequirementID)
				sb.WriteString("]")
			}
			sb.WriteString(": ")
			sb.WriteString(ts.Expected)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	// Tasks
	if len(f.Tasks) > 0 {
		sb.WriteString("## Implementation Tasks\n\n")
		for _, t := range f.Tasks {
			sb.WriteString("- ")
			sb.WriteString(t.ID)
			sb.WriteString(": ")
			sb.WriteString(t.Title)
			sb.WriteString(" (status: ")
			sb.WriteString(t.Status)
			sb.WriteString(")")
			if len(t.Requirements) > 0 {
				sb.WriteString(" [")
				sb.WriteString(strings.Join(t.Requirements, ", "))
				sb.WriteString("]")
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

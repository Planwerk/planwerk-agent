package gapanalysis

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/planwerk/planwerk-review/internal/planwerk"
)

// completedSubdir is the only Planwerk lifecycle directory the gap-analysis
// looks at: completed features are the ones the team has declared "done", so
// any unimplemented spec content there is a real gap.
const completedSubdir = "completed"

// LoadCompletedFeatures returns the parsed feature files under
// <repoDir>/.planwerk/completed/, optionally filtered by featureID or filePath.
//
// Filter semantics:
//   - featureID != "": match by Feature.FeatureID (case-insensitive). Returns
//     an empty slice without error if no match — the caller decides whether
//     that's a hard error.
//   - filePath != "": resolve to a single feature file. May be absolute, repo-
//     relative, or a bare basename; the file must live under .planwerk/completed/
//     to be accepted (we don't gap-analyze in-progress or pattern files).
//   - both empty: every parseable feature file in the directory.
//
// Files that fail to parse are skipped with a returned multi-error rather than
// silently dropped — a malformed file in completed/ is the user's bug, not ours.
func LoadCompletedFeatures(repoDir, featureID, filePath string) ([]*planwerk.Feature, error) {
	completedDir := filepath.Join(repoDir, ".planwerk", completedSubdir)

	if filePath != "" {
		f, err := loadSingleFile(repoDir, completedDir, filePath)
		if err != nil {
			return nil, err
		}
		if featureID != "" && !strings.EqualFold(f.FeatureID, featureID) {
			return nil, fmt.Errorf("feature file %s has feature_id %q, does not match --feature %q",
				filepath.Base(f.FilePath), f.FeatureID, featureID)
		}
		return []*planwerk.Feature{f}, nil
	}

	entries, err := os.ReadDir(completedDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no .planwerk/completed/ directory in %s — nothing to analyze", repoDir)
		}
		return nil, fmt.Errorf("reading %s: %w", completedDir, err)
	}

	var features []*planwerk.Feature
	var parseErrs []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(completedDir, e.Name())
		f, err := parseFeatureFile(path)
		if err != nil {
			parseErrs = append(parseErrs, fmt.Sprintf("%s: %v", e.Name(), err))
			continue
		}
		if f.FeatureID == "" {
			parseErrs = append(parseErrs, fmt.Sprintf("%s: feature_id is empty", e.Name()))
			continue
		}
		if featureID != "" && !strings.EqualFold(f.FeatureID, featureID) {
			continue
		}
		features = append(features, f)
	}

	sort.Slice(features, func(i, j int) bool {
		return features[i].FeatureID < features[j].FeatureID
	})

	if len(features) == 0 {
		if featureID != "" {
			return nil, fmt.Errorf("no completed feature with feature_id %q (parsed %d file(s), %d unparseable)",
				featureID, len(entries)-len(parseErrs), len(parseErrs))
		}
		if len(parseErrs) > 0 {
			return nil, fmt.Errorf("no parseable feature files in %s:\n  %s",
				completedDir, strings.Join(parseErrs, "\n  "))
		}
		return nil, fmt.Errorf("no feature files found in %s", completedDir)
	}
	return features, nil
}

func loadSingleFile(repoDir, completedDir, filePath string) (*planwerk.Feature, error) {
	abs := filePath
	if !filepath.IsAbs(abs) {
		// Try repo-relative first, then bare basename inside completedDir.
		candidate := filepath.Join(repoDir, filePath)
		if _, err := os.Stat(candidate); err == nil {
			abs = candidate
		} else {
			abs = filepath.Join(completedDir, filepath.Base(filePath))
		}
	}
	abs = filepath.Clean(abs)

	// Reject anything that does not live under .planwerk/completed/. We
	// gap-analyze ONLY completed specs by design — checking in-progress
	// or planned features would produce noise, not gaps.
	completedAbs, err := filepath.Abs(completedDir)
	if err != nil {
		return nil, fmt.Errorf("resolving completed dir: %w", err)
	}
	rel, err := filepath.Rel(completedAbs, abs)
	if err != nil || strings.HasPrefix(rel, "..") || strings.Contains(rel, string(filepath.Separator)) {
		return nil, fmt.Errorf("file %s is not inside %s — gap analysis only checks completed features",
			filePath, filepath.Join(".planwerk", completedSubdir))
	}

	f, err := parseFeatureFile(abs)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filepath.Base(abs), err)
	}
	if f.FeatureID == "" {
		return nil, fmt.Errorf("file %s has empty feature_id", filepath.Base(abs))
	}
	return f, nil
}

func parseFeatureFile(path string) (*planwerk.Feature, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f planwerk.Feature
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	f.FilePath = path
	return &f, nil
}

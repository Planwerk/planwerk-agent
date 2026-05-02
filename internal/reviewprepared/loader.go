package reviewprepared

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

// preparedSubdir is the lifecycle directory we scan. Prepared specs live
// under .planwerk/features/ alongside earlier-stage drafts; the status field
// disambiguates the two.
const preparedSubdir = "features"

// PreparedFeature pairs a parsed Feature with the raw bytes of the file it
// was loaded from. We hand the raw bytes to Claude so the model can preserve
// fields the Feature struct does not model (status_history, execution_history,
// affected_files, similar_patterns, ...) when emitting the improved JSON.
type PreparedFeature struct {
	Feature *planwerk.Feature
	Raw     []byte
}

// LoadPreparedFeatures returns every feature under <repoDir>/.planwerk/features/
// whose status is "prepared", optionally filtered by featureID or filePath.
//
// Files whose status is something else (draft, preparing, in_progress, ...)
// are silently skipped — the command's contract is "review prepared specs",
// not "review every JSON in the directory".
func LoadPreparedFeatures(repoDir, featureID, filePath string) ([]PreparedFeature, error) {
	preparedDir := filepath.Join(repoDir, ".planwerk", preparedSubdir)

	if filePath != "" {
		pf, err := loadSingleFile(repoDir, preparedDir, filePath)
		if err != nil {
			return nil, err
		}
		if featureID != "" && !strings.EqualFold(pf.Feature.FeatureID, featureID) {
			return nil, fmt.Errorf("feature file %s has feature_id %q, does not match --feature %q",
				filepath.Base(pf.Feature.FilePath), pf.Feature.FeatureID, featureID)
		}
		if !strings.EqualFold(pf.Feature.Status, PreparedStatus) {
			return nil, fmt.Errorf("feature %s has status %q, only %q features are reviewed",
				pf.Feature.FeatureID, pf.Feature.Status, PreparedStatus)
		}
		return []PreparedFeature{pf}, nil
	}

	entries, err := os.ReadDir(preparedDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no .planwerk/features/ directory in %s — nothing to review", repoDir)
		}
		return nil, fmt.Errorf("reading %s: %w", preparedDir, err)
	}

	var features []PreparedFeature
	var parseErrs []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(preparedDir, e.Name())
		pf, err := parsePreparedFile(path)
		if err != nil {
			parseErrs = append(parseErrs, fmt.Sprintf("%s: %v", e.Name(), err))
			continue
		}
		if pf.Feature.FeatureID == "" {
			parseErrs = append(parseErrs, fmt.Sprintf("%s: feature_id is empty", e.Name()))
			continue
		}
		if !strings.EqualFold(pf.Feature.Status, PreparedStatus) {
			continue
		}
		if featureID != "" && !strings.EqualFold(pf.Feature.FeatureID, featureID) {
			continue
		}
		features = append(features, pf)
	}

	sort.Slice(features, func(i, j int) bool {
		return features[i].Feature.FeatureID < features[j].Feature.FeatureID
	})

	if len(features) == 0 {
		if featureID != "" {
			return nil, fmt.Errorf("no prepared feature with feature_id %q in %s", featureID, preparedDir)
		}
		if len(parseErrs) > 0 {
			return nil, fmt.Errorf("no prepared feature files in %s:\n  %s",
				preparedDir, strings.Join(parseErrs, "\n  "))
		}
		return nil, fmt.Errorf("no feature files with status=%q found in %s", PreparedStatus, preparedDir)
	}
	return features, nil
}

func loadSingleFile(repoDir, preparedDir, filePath string) (PreparedFeature, error) {
	abs := filePath
	if !filepath.IsAbs(abs) {
		candidate := filepath.Join(repoDir, filePath)
		if _, err := os.Stat(candidate); err == nil {
			abs = candidate
		} else {
			abs = filepath.Join(preparedDir, filepath.Base(filePath))
		}
	}
	abs = filepath.Clean(abs)

	preparedAbs, err := filepath.Abs(preparedDir)
	if err != nil {
		return PreparedFeature{}, fmt.Errorf("resolving features dir: %w", err)
	}
	rel, err := filepath.Rel(preparedAbs, abs)
	if err != nil || strings.HasPrefix(rel, "..") || strings.Contains(rel, string(filepath.Separator)) {
		return PreparedFeature{}, fmt.Errorf("file %s is not inside %s — review-prepared only checks features under that directory",
			filePath, filepath.Join(".planwerk", preparedSubdir))
	}

	pf, err := parsePreparedFile(abs)
	if err != nil {
		return PreparedFeature{}, fmt.Errorf("parsing %s: %w", filepath.Base(abs), err)
	}
	if pf.Feature.FeatureID == "" {
		return PreparedFeature{}, fmt.Errorf("file %s has empty feature_id", filepath.Base(abs))
	}
	return pf, nil
}

func parsePreparedFile(path string) (PreparedFeature, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return PreparedFeature{}, err
	}
	var f planwerk.Feature
	if err := json.Unmarshal(data, &f); err != nil {
		return PreparedFeature{}, err
	}
	f.FilePath = path
	return PreparedFeature{Feature: &f, Raw: data}, nil
}

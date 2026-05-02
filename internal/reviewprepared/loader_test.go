package reviewprepared

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-review/internal/planwerk"
)

// writeFeature writes a feature JSON into <repoDir>/.planwerk/features/. Keeps
// each test focused on filter behavior instead of JSON layout.
func writeFeature(t *testing.T, repoDir, name string, f planwerk.Feature) string {
	t.Helper()
	dir := filepath.Join(repoDir, ".planwerk", preparedSubdir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, name)
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestLoadPreparedFeatures_StatusFilter(t *testing.T) {
	repo := t.TempDir()
	// Three lifecycle states; only "prepared" must be returned.
	writeFeature(t, repo, "PX-0001-draft.json", planwerk.Feature{FeatureID: "PX-0001", Status: "draft"})
	writeFeature(t, repo, "PX-0002-preparing.json", planwerk.Feature{FeatureID: "PX-0002", Status: "preparing"})
	writeFeature(t, repo, "PX-0003-prepared.json", planwerk.Feature{FeatureID: "PX-0003", Status: "prepared"})
	writeFeature(t, repo, "PX-0004-prepared.json", planwerk.Feature{FeatureID: "PX-0004", Status: "Prepared"}) // case-insensitive

	got, err := LoadPreparedFeatures(repo, "", "")
	if err != nil {
		t.Fatalf("LoadPreparedFeatures: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d features, want 2 (only prepared)", len(got))
	}
	if got[0].Feature.FeatureID != "PX-0003" || got[1].Feature.FeatureID != "PX-0004" {
		t.Errorf("expected sort by feature_id and prepared-only, got [%s, %s]",
			got[0].Feature.FeatureID, got[1].Feature.FeatureID)
	}
	// Raw bytes must be present so the Claude prompt can render them verbatim.
	if len(got[0].Raw) == 0 {
		t.Errorf("expected raw bytes to be loaded for %s", got[0].Feature.FeatureID)
	}
}

func TestLoadPreparedFeatures_FilterByID(t *testing.T) {
	repo := t.TempDir()
	writeFeature(t, repo, "PX-0001-prepared.json", planwerk.Feature{FeatureID: "PX-0001", Status: "prepared"})
	writeFeature(t, repo, "PX-0002-prepared.json", planwerk.Feature{FeatureID: "PX-0002", Status: "prepared"})

	got, err := LoadPreparedFeatures(repo, "px-0002", "")
	if err != nil {
		t.Fatalf("LoadPreparedFeatures: %v", err)
	}
	if len(got) != 1 || got[0].Feature.FeatureID != "PX-0002" {
		t.Errorf("expected single PX-0002, got %+v", got)
	}
}

func TestLoadPreparedFeatures_FilterByID_StatusMismatch(t *testing.T) {
	repo := t.TempDir()
	writeFeature(t, repo, "PX-0001-draft.json", planwerk.Feature{FeatureID: "PX-0001", Status: "draft"})

	_, err := LoadPreparedFeatures(repo, "PX-0001", "")
	if err == nil {
		t.Fatalf("expected error for non-prepared feature, got nil")
	}
}

func TestLoadPreparedFeatures_FilterByFile_RejectsOutsideFeatures(t *testing.T) {
	repo := t.TempDir()
	otherDir := filepath.Join(repo, ".planwerk", "completed")
	if err := os.MkdirAll(otherDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	outside := filepath.Join(otherDir, "PX-0001.json")
	data, _ := json.Marshal(planwerk.Feature{FeatureID: "PX-0001", Status: "prepared"})
	if err := os.WriteFile(outside, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := LoadPreparedFeatures(repo, "", outside)
	if err == nil {
		t.Fatalf("expected error for file outside features dir, got nil")
	}
	if !strings.Contains(err.Error(), "not inside") {
		t.Errorf("expected 'not inside' error, got %v", err)
	}
}

func TestLoadPreparedFeatures_NoneFound(t *testing.T) {
	repo := t.TempDir()
	writeFeature(t, repo, "PX-0001-draft.json", planwerk.Feature{FeatureID: "PX-0001", Status: "draft"})

	_, err := LoadPreparedFeatures(repo, "", "")
	if err == nil {
		t.Fatalf("expected error when no prepared features exist, got nil")
	}
}

func TestLoadPreparedFeatures_MissingDir(t *testing.T) {
	repo := t.TempDir()
	_, err := LoadPreparedFeatures(repo, "", "")
	if err == nil {
		t.Fatalf("expected error when .planwerk/features/ is missing, got nil")
	}
}

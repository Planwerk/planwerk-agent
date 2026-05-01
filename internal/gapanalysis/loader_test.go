package gapanalysis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-review/internal/planwerk"
)

// writeFeature writes a minimal feature JSON into <repoDir>/.planwerk/completed/.
// The helper keeps tests focused on filter behavior instead of JSON layout.
func writeFeature(t *testing.T, repoDir, name string, f planwerk.Feature) string {
	t.Helper()
	dir := filepath.Join(repoDir, ".planwerk", "completed")
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

func TestLoadCompletedFeatures_AllFiles(t *testing.T) {
	repo := t.TempDir()
	writeFeature(t, repo, "CC-0001-foo.json", planwerk.Feature{FeatureID: "CC-0001", Title: "Foo"})
	writeFeature(t, repo, "CC-0002-bar.json", planwerk.Feature{FeatureID: "CC-0002", Title: "Bar"})

	got, err := LoadCompletedFeatures(repo, "", "")
	if err != nil {
		t.Fatalf("LoadCompletedFeatures: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d features, want 2", len(got))
	}
	if got[0].FeatureID != "CC-0001" || got[1].FeatureID != "CC-0002" {
		t.Errorf("expected sort by feature_id, got [%s, %s]", got[0].FeatureID, got[1].FeatureID)
	}
}

func TestLoadCompletedFeatures_FilterByID(t *testing.T) {
	repo := t.TempDir()
	writeFeature(t, repo, "CC-0001-foo.json", planwerk.Feature{FeatureID: "CC-0001"})
	writeFeature(t, repo, "CC-0002-bar.json", planwerk.Feature{FeatureID: "CC-0002"})

	got, err := LoadCompletedFeatures(repo, "cc-0002", "")
	if err != nil {
		t.Fatalf("LoadCompletedFeatures: %v", err)
	}
	if len(got) != 1 || got[0].FeatureID != "CC-0002" {
		t.Errorf("expected single CC-0002, got %+v", got)
	}
}

func TestLoadCompletedFeatures_FilterByFile_Basename(t *testing.T) {
	repo := t.TempDir()
	writeFeature(t, repo, "CC-0042-thing.json", planwerk.Feature{FeatureID: "CC-0042", Title: "Thing"})
	writeFeature(t, repo, "CC-0099-other.json", planwerk.Feature{FeatureID: "CC-0099"})

	got, err := LoadCompletedFeatures(repo, "", "CC-0042-thing.json")
	if err != nil {
		t.Fatalf("LoadCompletedFeatures: %v", err)
	}
	if len(got) != 1 || got[0].FeatureID != "CC-0042" {
		t.Errorf("expected CC-0042, got %+v", got)
	}
}

func TestLoadCompletedFeatures_FilterByFile_RejectsOutsideCompleted(t *testing.T) {
	repo := t.TempDir()
	progressDir := filepath.Join(repo, ".planwerk", "progress")
	if err := os.MkdirAll(progressDir, 0o750); err != nil {
		t.Fatalf("mkdir progress: %v", err)
	}
	progressFile := filepath.Join(progressDir, "CC-0050-wip.json")
	if err := os.WriteFile(progressFile, []byte(`{"feature_id":"CC-0050"}`), 0o600); err != nil {
		t.Fatalf("write progress: %v", err)
	}
	writeFeature(t, repo, "CC-0001-real.json", planwerk.Feature{FeatureID: "CC-0001"})

	_, err := LoadCompletedFeatures(repo, "", progressFile)
	if err == nil || !strings.Contains(err.Error(), "not inside") {
		t.Fatalf("expected outside-completed rejection, got: %v", err)
	}
}

func TestLoadCompletedFeatures_FilterByFile_AbsolutePath(t *testing.T) {
	repo := t.TempDir()
	abs := writeFeature(t, repo, "CC-0007-abs.json", planwerk.Feature{FeatureID: "CC-0007"})

	got, err := LoadCompletedFeatures(repo, "", abs)
	if err != nil {
		t.Fatalf("LoadCompletedFeatures: %v", err)
	}
	if len(got) != 1 || got[0].FeatureID != "CC-0007" {
		t.Errorf("expected CC-0007, got %+v", got)
	}
}

func TestLoadCompletedFeatures_NoDir(t *testing.T) {
	repo := t.TempDir()
	_, err := LoadCompletedFeatures(repo, "", "")
	if err == nil || !strings.Contains(err.Error(), "no .planwerk/completed") {
		t.Fatalf("expected missing-dir error, got: %v", err)
	}
}

func TestLoadCompletedFeatures_FeatureIDNotFound(t *testing.T) {
	repo := t.TempDir()
	writeFeature(t, repo, "CC-0001-foo.json", planwerk.Feature{FeatureID: "CC-0001"})

	_, err := LoadCompletedFeatures(repo, "CC-9999", "")
	if err == nil || !strings.Contains(err.Error(), "CC-9999") {
		t.Fatalf("expected CC-9999 not-found error, got: %v", err)
	}
}

func TestLoadCompletedFeatures_FilterMismatch(t *testing.T) {
	// Both --feature and --file given but they disagree → hard error rather
	// than silently using one or the other.
	repo := t.TempDir()
	writeFeature(t, repo, "CC-0001-foo.json", planwerk.Feature{FeatureID: "CC-0001"})

	_, err := LoadCompletedFeatures(repo, "CC-0099", "CC-0001-foo.json")
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected mismatch error, got: %v", err)
	}
}

func TestLoadCompletedFeatures_SkipsMalformedJSON(t *testing.T) {
	repo := t.TempDir()
	completedDir := filepath.Join(repo, ".planwerk", "completed")
	if err := os.MkdirAll(completedDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(completedDir, "broken.json"), []byte("not json"), 0o600); err != nil {
		t.Fatalf("write broken: %v", err)
	}
	writeFeature(t, repo, "CC-0001-ok.json", planwerk.Feature{FeatureID: "CC-0001"})

	got, err := LoadCompletedFeatures(repo, "", "")
	if err != nil {
		t.Fatalf("LoadCompletedFeatures: %v", err)
	}
	if len(got) != 1 || got[0].FeatureID != "CC-0001" {
		t.Errorf("expected only the valid feature, got %+v", got)
	}
}

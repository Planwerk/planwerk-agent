package planwerk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectFeature_MatchesByTitle(t *testing.T) {
	t.Parallel()

	dir := setupFeatureDir(t, "features", "CC-0042-test.json", `{
		"feature_id": "CC-0042",
		"title": "Add resources field",
		"requirements": [{"id": "REQ-001", "description": "test", "priority": "SHALL"}]
	}`)

	f, err := DetectFeature(dir, "feat(CC-0042): Add resources field", "", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f == nil {
		t.Fatal("expected feature to be detected")
	}
	if f.FeatureID != "CC-0042" {
		t.Errorf("feature_id = %q, want %q", f.FeatureID, "CC-0042")
	}
}

func TestDetectFeature_MatchesByBranch(t *testing.T) {
	t.Parallel()

	dir := setupFeatureDir(t, "features", "CC-0042-test.json", `{
		"feature_id": "CC-0042",
		"title": "Add resources field"
	}`)

	f, err := DetectFeature(dir, "Some PR title", "", "feature/CC-0042", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f == nil {
		t.Fatal("expected feature to be detected by branch name")
	}
}

func TestDetectFeature_MatchesByBody(t *testing.T) {
	t.Parallel()

	dir := setupFeatureDir(t, "features", "CC-0042-test.json", `{
		"feature_id": "CC-0042",
		"title": "Add resources field"
	}`)

	f, err := DetectFeature(dir, "Some title", "Implements CC-0042 feature", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f == nil {
		t.Fatal("expected feature to be detected by PR body")
	}
}

func TestDetectFeature_CompletedDir(t *testing.T) {
	t.Parallel()

	dir := setupFeatureDir(t, "completed", "CC-0042-test.json", `{
		"feature_id": "CC-0042",
		"title": "Add resources field"
	}`)

	f, err := DetectFeature(dir, "feat(CC-0042): done", "", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f == nil {
		t.Fatal("expected feature to be detected in completed dir")
	}
}

// TestDetectFeature_ProgressDir reproduces the real PR plexsphere#117 scenario:
// the PR implements a feature whose canonical file has been renamed from
// .planwerk/features/ to .planwerk/progress/ as part of the lifecycle. The
// branch and title both carry the feature ID, so detection must follow the
// file into progress/ instead of returning nil.
func TestDetectFeature_ProgressDir(t *testing.T) {
	t.Parallel()

	dir := setupFeatureDir(t, "progress", "PX-0019-test.json", `{
		"feature_id": "PX-0019",
		"title": "Implement heartbeat handler"
	}`)

	f, err := DetectFeature(dir, "feat(PX-0019): heartbeat", "", "feature/PX-0019", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f == nil {
		t.Fatal("expected feature to be detected in progress dir")
	}
	if f.FeatureID != "PX-0019" {
		t.Errorf("feature_id = %q, want %q", f.FeatureID, "PX-0019")
	}
}

func TestDetectFeature_NoMatch(t *testing.T) {
	t.Parallel()

	dir := setupFeatureDir(t, "features", "CC-0042-test.json", `{
		"feature_id": "CC-0042",
		"title": "Add resources field"
	}`)

	f, err := DetectFeature(dir, "Unrelated PR", "No feature ref", "bugfix/something", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f != nil {
		t.Error("expected no feature to be detected")
	}
}

func TestDetectFeature_NoPlanwerkDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	f, err := DetectFeature(dir, "feat(CC-0042): test", "", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f != nil {
		t.Error("expected nil when .planwerk directory does not exist")
	}
}

func TestDetectFeature_CaseInsensitive(t *testing.T) {
	t.Parallel()

	dir := setupFeatureDir(t, "features", "cc-0042-test.json", `{
		"feature_id": "CC-0042",
		"title": "Add resources field"
	}`)

	f, err := DetectFeature(dir, "feat(cc-0042): lowercase ref", "", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f == nil {
		t.Fatal("expected case-insensitive match")
	}
}

// TestDetectFeature_BranchWinsOverBodyCrossRef reproduces the PR #167 scenario:
// two feature files on disk, branch points to CC-0055, but the PR body
// references CC-0050 ("following the pattern established by CC-0050"). The
// branch must win — returning CC-0050 would be wrong.
func TestDetectFeature_BranchWinsOverBodyCrossRef(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFeatureFile(t, dir, "features", "CC-0050-refactor-ci.json", `{
		"feature_id": "CC-0050",
		"title": "Refactor CI workflow into reusable scripts and actions"
	}`)
	writeFeatureFile(t, dir, "features", "CC-0055-refactor-build-images.json", `{
		"feature_id": "CC-0055",
		"title": "Refactor build-images workflow into reusable components"
	}`)

	body := "Refactor build-images.yaml, following the pattern established by CC-0050 for ci.yaml."
	f, err := DetectFeature(dir,
		"feat(CC-0055): Refactor build-images workflow into reusable components",
		body,
		"feature/CC-0055",
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f == nil || f.FeatureID != "CC-0055" {
		t.Fatalf("feature_id = %v, want CC-0055", f)
	}
}

// TestDetectFeature_MatchesByChangedFiles covers the case where neither the
// branch nor the title carry a feature ID, but the PR touches files under
// .planwerk/progress/ or .planwerk/reviews/ that carry the ID.
func TestDetectFeature_MatchesByChangedFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFeatureFile(t, dir, "features", "CC-0050-refactor-ci.json", `{
		"feature_id": "CC-0050",
		"title": "Refactor CI"
	}`)
	writeFeatureFile(t, dir, "features", "CC-0055-refactor-build-images.json", `{
		"feature_id": "CC-0055",
		"title": "Refactor build-images"
	}`)

	changed := []string{
		".github/workflows/build-images.yaml",
		".planwerk/progress/CC-0055-refactor-build-images.json",
		".planwerk/reviews/CC-0055-refactor-build-images-review-1.json",
	}
	f, err := DetectFeature(dir, "Some PR", "", "bugfix/thing", changed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f == nil || f.FeatureID != "CC-0055" {
		t.Fatalf("feature_id = %v, want CC-0055", f)
	}
}

// TestDetectFeature_AmbiguousBodyReturnsNil ensures that when only the body
// carries signals and it references multiple feature IDs, detection refuses
// to guess.
func TestDetectFeature_AmbiguousBodyReturnsNil(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFeatureFile(t, dir, "features", "CC-0050-a.json", `{"feature_id": "CC-0050", "title": "A"}`)
	writeFeatureFile(t, dir, "features", "CC-0055-b.json", `{"feature_id": "CC-0055", "title": "B"}`)

	body := "See CC-0050 and CC-0055 for context."
	f, err := DetectFeature(dir, "Some PR", body, "bugfix/thing", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f != nil {
		t.Errorf("expected nil on ambiguous body, got %q", f.FeatureID)
	}
}

// TestDetectFeature_BranchIDIsAuthoritative ensures that when a branch
// carries an explicit feature ID with no matching feature file on disk,
// detection returns nil rather than falling through to weaker signals
// (paths, body) that might pick an unrelated feature.
func TestDetectFeature_BranchIDIsAuthoritative(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFeatureFile(t, dir, "features", "CC-0055-b.json", `{"feature_id": "CC-0055", "title": "B"}`)

	// Branch references CC-0099 (no feature file). Changed paths reference
	// CC-0055 which does have a file, but that is not what the PR is about —
	// the branch stated CC-0099 as the work item, so detection must return
	// nil instead of silently substituting CC-0055.
	changed := []string{".planwerk/progress/CC-0055-b.json"}
	f, err := DetectFeature(dir, "Some PR", "", "feature/CC-0099", changed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f != nil {
		t.Errorf("expected nil (branch CC-0099 has no feature file), got %q", f.FeatureID)
	}
}

// TestDetectFeature_PR167_NewFeatureFileInDiff reproduces the real PR #167
// scenario end-to-end. The branch is feature/CC-0055 and the title uses
// feat(CC-0055), but CC-0055 has no feature file anywhere on the branch.
// The PR happens to add a new, unrelated feature file for CC-0056, and it
// also edits .planwerk/progress/CC-0055-*.json and .planwerk/reviews/
// CC-0055-*.json. Detection must return nil: substituting CC-0056 would be
// wrong because CC-0056 is a separate feature that just ships in the same PR.
func TestDetectFeature_PR167_NewFeatureFileInDiff(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// CC-0050 is the cross-referenced feature from the PR body (lives in
	// completed/ because it already shipped).
	writeFeatureFile(t, dir, "completed", "CC-0050-refactor-ci.json", `{
		"feature_id": "CC-0050",
		"title": "Refactor CI workflow into reusable scripts and actions"
	}`)
	// CC-0056 is the unrelated feature newly added by this same PR.
	writeFeatureFile(t, dir, "features", "CC-0056-implement-expand-migrate-contract.json", `{
		"feature_id": "CC-0056",
		"title": "Implement expand-migrate-contract DB migration strategy"
	}`)
	// CC-0055 intentionally has NO feature file — that is the real-world
	// state on the PR branch.

	body := "`.github/workflows/build-images.yaml` has grown... " +
		"following the pattern established by CC-0050 for ci.yaml."
	changed := []string{
		".github/workflows/build-images.yaml",
		".planwerk/features/CC-0056-implement-expand-migrate-contract.json",
		".planwerk/progress/CC-0055-refactor-build-images-workflow-into-reusable.json",
		".planwerk/reviews/CC-0055-refactor-build-images-workflow-into-reusable-review-1.json",
	}
	f, err := DetectFeature(dir,
		"feat(CC-0055): Refactor build-images workflow into reusable components",
		body,
		"feature/CC-0055",
		changed,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f != nil {
		t.Errorf("expected nil (CC-0055 has no feature file; CC-0056 is unrelated), got %q", f.FeatureID)
	}
}

func writeFeatureFile(t *testing.T, repoDir, subdir, filename, content string) {
	t.Helper()
	featureDir := filepath.Join(repoDir, ".planwerk", subdir)
	if err := os.MkdirAll(featureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(featureDir, filename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFormatForPrompt_ContainsAllSections(t *testing.T) {
	t.Parallel()

	f := &Feature{
		FeatureID: "CC-0042",
		Title:     "Add resources field",
		Stories: []Story{
			{Title: "Story 1", Role: "operator", Want: "resources", SoThat: "HPA works", Criteria: []string{"crit1", "crit2"}},
		},
		Requirements: []Requirement{
			{
				ID:          "REQ-001",
				Description: "Inject defaults",
				Priority:    "SHALL",
				Scenarios:   []Scenario{{Name: "nil defaults", When: "nil", Then: "defaults set"}},
			},
		},
		TestSpecifications: []TestSpecification{
			{TestFile: "test.go", TestFunction: "TestFoo", Expected: "bar", RequirementID: "REQ-001"},
		},
		Tasks: []Task{
			{ID: "1.1", Title: "Add field", Status: "done", Requirements: []string{"REQ-001"}},
		},
	}

	output := f.FormatForPrompt()

	for _, want := range []string{
		"CC-0042",
		"Add resources field",
		"Story 1",
		"operator",
		"crit1",
		"REQ-001",
		"SHALL",
		"Inject defaults",
		"nil defaults",
		"TestFoo",
		"test.go",
		"1.1",
		"Add field",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("FormatForPrompt output missing %q", want)
		}
	}
}

func TestFormatForPrompt_SkipsDiscoveredTests(t *testing.T) {
	t.Parallel()

	f := &Feature{
		FeatureID: "CC-0042",
		Title:     "Test",
		TestSpecifications: []TestSpecification{
			{TestFile: "test.go", TestFunction: "TestPlanned", Expected: "works", RequirementID: "REQ-001"},
			{TestFile: "test.go", TestFunction: "TestDiscovered", Expected: "Discovered during implementation"},
		},
	}

	output := f.FormatForPrompt()

	if !strings.Contains(output, "TestPlanned") {
		t.Error("should include planned test")
	}
	if strings.Contains(output, "TestDiscovered") {
		t.Error("should skip discovered-during-implementation test")
	}
}

func setupFeatureDir(t *testing.T, subdir, filename, content string) string {
	t.Helper()
	dir := t.TempDir()
	featureDir := filepath.Join(dir, ".planwerk", subdir)
	if err := os.MkdirAll(featureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(featureDir, filename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

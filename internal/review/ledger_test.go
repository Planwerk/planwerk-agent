package review

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// TestReviewLedger pins which changed files the review prompt lists: every
// file in git's order, minus what the run's diff left out and the .planwerk/
// planning artifacts, keeping the checklist, its appended parts and the review
// patterns, with control characters in a path made harmless (decision 138).
func TestReviewLedger(t *testing.T) {
	files := []github.FileChange{
		{Path: "main.go", Status: github.FileModified, Added: 3, Deleted: 1},
		{Path: ".planwerk/plan.md", Status: github.FileAdded, Added: 40},
		{Path: ".planwerk/checklist.md", Status: github.FileModified, Added: 1},
		{Path: ".planwerk/checklist.d/security.md", Status: github.FileAdded, Added: 5},
		{Path: ".planwerk/review_patterns/x.md", Status: github.FileAdded, Added: 9},
		{Path: ".planwerk/checklist.md.orig", Status: github.FileAdded, Added: 1},
		{Path: "go.sum", Status: github.FileModified, Added: 2, Deleted: 2},
		{Path: "vendor/lib/a.go", Status: github.FileModified, Added: 1},
		{Path: "evil\nname.go", OldPath: "old\tname.go", Status: github.FileRenamed, Added: 1},
		{Path: "a.go", Status: github.FileDeleted, Deleted: 4},
	}
	excluded := []report.ExcludedFile{
		{Path: "go.sum", Reason: report.ExcludedLockfile},
		{Path: "vendor/", Reason: report.ExcludedVendored, Files: 1},
	}
	want := []github.FileChange{
		{Path: "main.go", Status: github.FileModified, Added: 3, Deleted: 1},
		{Path: ".planwerk/checklist.md", Status: github.FileModified, Added: 1},
		{Path: ".planwerk/checklist.d/security.md", Status: github.FileAdded, Added: 5},
		{Path: ".planwerk/review_patterns/x.md", Status: github.FileAdded, Added: 9},
		{Path: "evil�name.go", OldPath: "old�name.go", Status: github.FileRenamed, Added: 1},
		{Path: "a.go", Status: github.FileDeleted, Deleted: 4},
	}
	if got := reviewLedger(files, excluded); !reflect.DeepEqual(got, want) {
		t.Errorf("reviewLedger =\n%+v\nwant\n%+v", got, want)
	}
	if files[8].Path != "evil\nname.go" {
		t.Errorf("reviewLedger rewrote its input: %q", files[8].Path)
	}

	// Without an excluded set, nothing but the planning artifacts goes.
	if got := ledgerPaths(reviewLedger(files, nil)); !slices.Contains(got, "go.sum") || !slices.Contains(got, "vendor/lib/a.go") || slices.Contains(got, ".planwerk/plan.md") {
		t.Errorf("reviewLedger(files, nil) lists %q", got)
	}
	if got := reviewLedger([]github.FileChange{{Path: ".planwerk/plan.md"}}, nil); got != nil {
		t.Errorf("a change made only of planning artifacts lists %+v, want nil", got)
	}
}

// TestLedgerPath pins the sanitizing: a control character or an invalid UTF-8
// byte becomes U+FFFD, and every other path, non-ASCII ones included, is kept
// as it is.
func TestLedgerPath(t *testing.T) {
	for in, want := range map[string]string{
		"internal/añ b.go": "internal/añ b.go",
		"a\nb.go":          "a�b.go",
		"a\x1b[31mb.go":    "a�[31mb.go",
		"a\xffb.go":        "a�b.go",
		"":                 "",
	} {
		if got := ledgerPath(in); got != want {
			t.Errorf("ledgerPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLedgerPaths(t *testing.T) {
	if got := ledgerPaths(nil); got != nil {
		t.Errorf("ledgerPaths(nil) = %q, want nil", got)
	}
	got := ledgerPaths([]github.FileChange{{Path: "b.go", OldPath: "a.go"}, {Path: "c.go"}})
	if want := []string{"b.go", "c.go"}; !slices.Equal(got, want) {
		t.Errorf("ledgerPaths = %q, want %q", got, want)
	}
}

// reviewSkillDoc is the /planwerk:review skill, which fills gates.files from
// its own ledger when asked for JSON (decision 151).
const reviewSkillDoc = "../../plugins/planwerk/skills/review/SKILL.md"

// TestReviewSkillCountsFilesAsTheLedgerDoes fails when the review skill's
// changed-file record stops meaning what reviewLedger's does: the skill must
// keep the .planwerk/ paths ledgerKeptPlanwerk keeps, and leave every other
// planning artifact out of gates.files, so that files.changed has one meaning
// whichever of the two wrote it.
func TestReviewSkillCountsFilesAsTheLedgerDoes(t *testing.T) {
	raw, err := os.ReadFile(reviewSkillDoc)
	if err != nil {
		t.Fatalf("reading %s: %v", reviewSkillDoc, err)
	}
	skill := strings.Join(strings.Fields(string(raw)), " ")
	for _, keep := range ledgerKeptPlanwerk {
		if !strings.Contains(skill, "`"+keep+"`") {
			t.Errorf("%s does not keep %s out of its planning artifacts; reviewLedger lists it", reviewSkillDoc, keep)
		}
	}
	if marker := "without the entries skipped as a planning artifact"; !strings.Contains(skill, marker) {
		t.Errorf("%s no longer says gates.files is filled %q; the command's files.changed leaves the planning artifacts out", reviewSkillDoc, marker)
	}
}

package review

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/planwerk/planwerk-agent/internal/cache"
	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/diffscope"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/github/githubtest"
	"github.com/planwerk/planwerk-agent/internal/hygiene"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// partitionFiles returns the Files of each partition of plan, in order.
func partitionFiles(plan []report.ReviewPartition) [][]string {
	out := make([][]string, len(plan))
	for i, p := range plan {
		out[i] = p.Files
	}
	return out
}

func TestPlanPartitions_DisabledOrSmall(t *testing.T) {
	files := []string{"a/1.go", "a/2.go", "b/1.go"}
	for _, maxFiles := range []int{0, -1} {
		if plan := planReviewPartitions(files, nil, maxFiles); plan != nil {
			t.Errorf("maxFiles %d: plan = %+v, want nil: the flag is off", maxFiles, plan)
		}
	}
	for _, maxFiles := range []int{3, 10} {
		if plan := planReviewPartitions(files, nil, maxFiles); plan != nil {
			t.Errorf("maxFiles %d: plan = %+v, want nil: %d files fit one session", maxFiles, plan, len(files))
		}
	}
}

func TestPlanPartitions_GroupsByDirectory(t *testing.T) {
	got := partitionFiles(planPartitions([]string{"b/2.go", "a/1.go", "b/1.go", "a/2.go"}, nil, 2))
	want := [][]string{{"a/1.go", "a/2.go"}, {"b/1.go", "b/2.go"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plan = %q, want %q", got, want)
	}
}

func TestPlanPartitions_CutsALargeDirectory(t *testing.T) {
	got := partitionFiles(planPartitions([]string{"a/1.go", "a/2.go", "a/3.go", "a/4.go", "a/5.go", "b/1.go"}, nil, 2))
	want := [][]string{{"a/1.go", "a/2.go"}, {"a/3.go", "a/4.go"}, {"a/5.go", "b/1.go"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plan = %q, want %q", got, want)
	}
}

// TestPlanPartitions_SplitsBySize: five files at four per partition make two
// partitions and a budget of 520 lines, so the 1000-line file runs alone and
// the rest of its directory starts the next partition.
func TestPlanPartitions_SplitsBySize(t *testing.T) {
	lines := map[string]int{"a/1.go": 1000, "a/2.go": 10, "a/3.go": 10, "a/4.go": 10, "b/1.go": 10}
	plan := planPartitions([]string{"a/1.go", "a/2.go", "a/3.go", "a/4.go", "b/1.go"}, lines, 4)
	want := []report.ReviewPartition{
		{Files: []string{"a/1.go"}, Lines: 1000},
		{Files: []string{"a/2.go", "a/3.go", "a/4.go", "b/1.go"}, Lines: 40},
	}
	if !reflect.DeepEqual(plan, want) {
		t.Errorf("plan = %+v, want %+v", plan, want)
	}
}

// TestPlanPartitions_CoversEveryFileOnceAndIsDeterministic locks the planner's
// guarantees over several shapes and every order of their files: each file
// lands in exactly one partition, no partition exceeds the file cap, and the
// plan depends only on the set of files.
func TestPlanPartitions_CoversEveryFileOnceAndIsDeterministic(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for _, tc := range []struct {
		name     string
		files    []string
		lines    map[string]int
		maxFiles int
	}{
		{"one directory", []string{"x/1.go", "x/2.go", "x/3.go", "x/4.go", "x/5.go", "x/6.go", "x/7.go"}, nil, 3},
		{"top level and nested", []string{"README.md", "go.mod", "cmd/main.go", "cmd/x/y.go", "internal/a/a.go", "internal/a/a_test.go", "internal/b/b.go"}, nil, 2},
		{"uneven sizes", []string{"a/1.go", "a/2.go", "b/1.go", "b/2.go", "c/1.go", "d/1.go"}, map[string]int{"a/1.go": 900, "b/2.go": 300, "c/1.go": 5}, 3},
		{"duplicates", []string{"a/1.go", "a/1.go", "a/2.go", "b/1.go", "b/1.go"}, nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := planPartitions(tc.files, tc.lines, tc.maxFiles)
			if len(first) < 2 {
				t.Fatalf("plan = %+v, want at least two partitions", first)
			}
			seen := map[string]int{}
			for _, p := range first {
				if len(p.Files) == 0 || len(p.Files) > tc.maxFiles {
					t.Errorf("partition %q holds %d files, want 1 to %d", p.Files, len(p.Files), tc.maxFiles)
				}
				for _, f := range p.Files {
					seen[f]++
				}
			}
			for _, f := range tc.files {
				if seen[f] != 1 {
					t.Errorf("%s lands in %d partitions, want exactly 1", f, seen[f])
				}
			}
			if len(seen) != len(slices.Compact(slices.Sorted(slices.Values(tc.files)))) {
				t.Errorf("the plan holds %d distinct files, want the input's", len(seen))
			}
			for range 20 {
				shuffled := slices.Clone(tc.files)
				rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
				if got := planPartitions(shuffled, tc.lines, tc.maxFiles); !reflect.DeepEqual(got, first) {
					t.Fatalf("plan of %q = %+v, want %+v for every order", shuffled, got, first)
				}
			}
		})
	}
}

// TestPlanReviewPartitions_CountsLinesFromTheFileChanges locks where the line
// budget comes from: each file's added plus deleted lines from the pull
// request's file changes, a binary file counting 0, and without the changes a
// plan by file count alone.
func TestPlanReviewPartitions_CountsLinesFromTheFileChanges(t *testing.T) {
	files := []string{"a/1.go", "a/2.go", "a/3.png", "b/1.go"}
	changes := []github.FileChange{
		{Path: "a/1.go", Status: github.FileModified, Added: 300, Deleted: 100},
		{Path: "a/2.go", Status: github.FileAdded, Added: 6},
		{Path: "a/3.png", Status: github.FileModified, Binary: true},
		{Path: "b/1.go", Status: github.FileModified, Added: 2, Deleted: 2},
	}
	want := []report.ReviewPartition{
		{Files: []string{"a/1.go"}, Lines: 400},
		{Files: []string{"a/2.go", "a/3.png", "b/1.go"}, Lines: 10},
	}
	if got := planReviewPartitions(files, changes, 3); !reflect.DeepEqual(got, want) {
		t.Errorf("plan = %+v, want %+v", got, want)
	}

	byCount := []report.ReviewPartition{
		{Files: []string{"a/1.go", "a/2.go", "a/3.png"}},
		{Files: []string{"b/1.go"}},
	}
	if got := planReviewPartitions(files, nil, 3); !reflect.DeepEqual(got, byCount) {
		t.Errorf("plan without file changes = %+v, want %+v", got, byCount)
	}
}

// TestFilesToPartition locks which changed files the plan splits: the raw
// paths of the file changes rather than DiffNames' names, which git may
// quote, less the files the diff left out and the planning artifacts, and
// DiffNames' names when there are no file changes (decision 142).
func TestFilesToPartition(t *testing.T) {
	changed := []string{`"b/q\"x.go"`, "go.sum", ".planwerk/features/x.md", ".planwerk/checklist.md", "a/1.go"}
	changes := []github.FileChange{{Path: `b/q"x.go`}, {Path: "go.sum"}, {Path: ".planwerk/features/x.md"}, {Path: ".planwerk/checklist.md"}, {Path: "a/1.go"}}
	excluded := []report.ExcludedFile{{Path: "go.sum", Reason: report.ExcludedLockfile}}

	if got, want := filesToPartition(changed, changes, excluded), []string{`b/q"x.go`, ".planwerk/checklist.md", "a/1.go"}; !slices.Equal(got, want) {
		t.Errorf("files = %q, want the raw paths %q", got, want)
	}
	if got, want := filesToPartition(changed, nil, excluded), []string{`"b/q\"x.go"`, ".planwerk/checklist.md", "a/1.go"}; !slices.Equal(got, want) {
		t.Errorf("files without file changes = %q, want DiffNames' %q", got, want)
	}
}

// TestPlanReviewPartitions_LeavesOutPlanningArtifacts: a planning artifact,
// which every session is told to ignore, counts toward neither the threshold
// nor the line budget and fills no partition, not even partition 1, which
// sorts first (decision 142).
func TestPlanReviewPartitions_LeavesOutPlanningArtifacts(t *testing.T) {
	changes := []github.FileChange{{Path: ".planwerk/features/x.md", Status: github.FileAdded, Added: 600}}
	for i := range 20 {
		changes = append(changes, github.FileChange{Path: fmt.Sprintf("internal/x/f%02d.go", i), Status: github.FileModified, Added: 30})
	}
	plan := planReviewPartitions(filesToPartition(nil, changes, nil), changes, 15)

	// 600 code lines over two partitions make a budget of 300, ten files each.
	if len(plan) != 2 {
		t.Fatalf("plan = %+v, want two partitions of the 20 code files", plan)
	}
	for i, p := range plan {
		if len(p.Files) != 10 || p.Lines != 300 || slices.ContainsFunc(p.Files, planningArtifact) {
			t.Errorf("partition %d = %+v, want ten code files of 300 lines and no planning artifact", i+1, p)
		}
	}

	small := []github.FileChange{{Path: ".planwerk/features/x.md"}, {Path: ".planwerk/plan.md"}}
	for i := range 14 {
		small = append(small, github.FileChange{Path: fmt.Sprintf("internal/x/f%02d.go", i)})
	}
	if plan := planReviewPartitions(filesToPartition(nil, small, nil), small, 15); plan != nil {
		t.Errorf("plan = %+v, want nil: 14 code files fit one session, whatever the planning artifacts", plan)
	}
}

func TestLedgerSubsets(t *testing.T) {
	ledger := []github.FileChange{
		{Path: "b/1.go", Status: github.FileModified},
		{Path: "a/1.go", Status: github.FileModified},
		{Path: "unplanned.go", Status: github.FileAdded},
		{Path: "a/2.go", Status: github.FileDeleted},
		{Path: ledgerPath("b/x\ny.go"), Status: github.FileAdded},
	}
	// The plan holds raw paths; the ledger holds them as ledgerPath sanitized them.
	plan := []report.ReviewPartition{{Files: []string{"a/1.go", "a/2.go"}}, {Files: []string{"b/1.go", "b/x\ny.go", ".planwerk/plan.md"}}}

	got := ledgerSubsets(ledger, plan)
	want := [][]github.FileChange{
		{ledger[1], ledger[2], ledger[3]}, // a file no partition holds goes to partition 1
		{ledger[0], ledger[4]},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("subsets = %+v, want %+v", got, want)
	}
	if n := largestSessionLedger(ledger, plan); n != 3 {
		t.Errorf("largestSessionLedger = %d, want 3", n)
	}
	if n := largestSessionLedger(ledger, nil); n != len(ledger) {
		t.Errorf("largestSessionLedger without a plan = %d, want the whole ledger's %d", n, len(ledger))
	}
	if got := ledgerSubsets(nil, plan); len(got) != 2 || got[0] != nil || got[1] != nil {
		t.Errorf("subsets of no ledger = %+v, want two empty ones", got)
	}
}

// TestPartitionContexts locks what each partition session is handed: its
// partition and the other partitions' files, its share of the ledger, and the
// zero Diff, with every other field shared (decision 142).
func TestPartitionContexts(t *testing.T) {
	scope := &patterns.FileScope{}
	rctx := claude.ReviewContext{
		BaseBranch:   "main",
		PRTitle:      "t",
		MaxFindings:  9,
		Diff:         diffscope.Diff{Dir: "/tmp/whole", File: "/tmp/whole/branch.diff", Range: "origin/main...HEAD", Lines: 10},
		ChangedFiles: []github.FileChange{{Path: "a/1.go"}, {Path: "b/1.go"}, {Path: "c/1.go"}},
		Scope:        scope,
		Catalog:      patterns.Catalog{Dir: "/tmp/catalog"},
	}
	plan := []report.ReviewPartition{{Files: []string{"a/1.go"}}, {Files: []string{"b/1.go"}}, {Files: []string{"c/1.go"}}}

	if got := partitionContexts(rctx, nil, rctx.ChangedFiles); got != nil {
		t.Errorf("contexts without a plan = %+v, want nil", got)
	}
	got := partitionContexts(rctx, plan, rctx.ChangedFiles)
	if len(got) != 3 {
		t.Fatalf("got %d contexts, want one per partition", len(got))
	}
	wantOthers := [][]string{{"b/1.go", "c/1.go"}, {"a/1.go", "c/1.go"}, {"a/1.go", "b/1.go"}}
	for i, c := range got {
		want := claude.PartitionScope{Index: i + 1, Count: 3, Files: plan[i].Files, Others: wantOthers[i]}
		if !reflect.DeepEqual(c.Partition, want) {
			t.Errorf("context %d: Partition = %+v, want %+v", i, c.Partition, want)
		}
		if !reflect.DeepEqual(c.ChangedFiles, []github.FileChange{rctx.ChangedFiles[i]}) {
			t.Errorf("context %d: ChangedFiles = %+v, want its own file", i, c.ChangedFiles)
		}
		if !reflect.DeepEqual(c.Diff, diffscope.Diff{}) {
			t.Errorf("context %d: Diff = %+v, want the zero Diff for the caller to replace", i, c.Diff)
		}
		if c.Scope != scope || c.Catalog.Dir != "/tmp/catalog" || c.PRTitle != "t" || c.MaxFindings != 9 {
			t.Errorf("context %d does not share the run's context: %+v", i, c)
		}
	}

	// A raw path is listed as the ledger lists it, so it keeps to one line.
	raw := []report.ReviewPartition{{Files: []string{"a/x\ny.go"}}, {Files: []string{"b/1.go"}}}
	listed := partitionContexts(rctx, raw, nil)
	if want := []string{"a/x�y.go"}; !slices.Equal(listed[0].Partition.Files, want) || !slices.Equal(listed[1].Partition.Others, want) {
		t.Errorf("Partition lists = %q and %q, want %q in both", listed[0].Partition.Files, listed[1].Partition.Others, want)
	}
	if raw[0].Files[0] != "a/x\ny.go" {
		t.Errorf("the plan's path became %q, want it raw for the partition's diff", raw[0].Files[0])
	}
}

// TestReviewPartitions_SkipsPartitionsNotStarted: when a partition fails, the
// sessions already running finish, and the ones still waiting for a slot
// never start (decision 142).
func TestReviewPartitions_SkipsPartitionsNotStarted(t *testing.T) {
	pr := fakePR(t, "acme", "widgets", 80, "sha-partition-skip")
	plan := make([]report.ReviewPartition, 2*maxConcurrentPartitions)
	for i := range plan {
		plan[i].Files = []string{fmt.Sprintf("d%d/x.go", i)}
	}
	var started sync.WaitGroup
	started.Add(maxConcurrentPartitions - 1)
	failed := make(chan struct{})
	claudeMock := &configurableClaude{
		review: func(_ string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			switch i := ctx.Partition.Index; {
			case i == 1:
				// Fail once every other session holding a slot is running.
				started.Wait()
				close(failed)
				return nil, errors.New("session crashed")
			case i <= maxConcurrentPartitions:
				started.Done()
				<-failed
				// Hold the slot while the group cancels on partition 1's error.
				time.Sleep(50 * time.Millisecond)
			}
			return &report.ReviewResult{Summary: "ok"}, nil
		},
	}

	_, err := (&Runner{Claude: claudeMock}).reviewPartitions(pr, plan, partitionContexts(claude.ReviewContext{BaseBranch: "main"}, plan, nil), nil)
	if want := fmt.Sprintf("partition 1 of %d", len(plan)); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("reviewPartitions error = %v, want one naming %s", err, want)
	}
	if claudeMock.reviewCalls != maxConcurrentPartitions {
		t.Errorf("Review calls = %d, want %d: the sessions running when partition 1 failed, and no later one", claudeMock.reviewCalls, maxConcurrentPartitions)
	}
}

func TestMergePartitions_StrongestVerdictAndJoinedSummaries(t *testing.T) {
	plan := []report.ReviewPartition{{Files: []string{"a.go"}, Lines: 3}, {Files: []string{"b.go"}, Lines: 4}, {Files: []string{"c.go"}}}
	results := []*report.ReviewResult{
		{Summary: "  First partition is clean.  ", Recommendation: "merge because the review found no issues", Model: ""},
		{
			Summary:        "Second partition leaks a handle.",
			Recommendation: "do not merge because Leaked handle in b.go closes nothing",
			Model:          "claude-opus-5-5",
			Findings: []report.Finding{
				{ID: "C-001", Severity: report.SeverityCritical, Title: "Leaked handle", File: "b.go", Line: 4},
				{ID: "I-001", Severity: report.SeverityInfo, Title: "Naming", File: "b.go", Line: 9},
			},
		},
		{Recommendation: "merge after fixes because Typo in c.go misleads", Findings: []report.Finding{{ID: "W-001", Severity: report.SeverityWarning, Title: "Typo", File: "c.go", Line: 1}}},
	}

	got := mergePartitions(results, plan)
	if want := "First partition is clean.\n\nSecond partition leaks a handle."; got.Summary != want {
		t.Errorf("Summary = %q, want %q", got.Summary, want)
	}
	if got.Recommendation != results[1].Recommendation {
		t.Errorf("Recommendation = %q, want the strongest verdict %q", got.Recommendation, results[1].Recommendation)
	}
	if got.Model != "claude-opus-5-5" {
		t.Errorf("Model = %q, want the first non-empty one", got.Model)
	}
	if len(got.Findings) != 3 {
		t.Errorf("got %d findings, want every partition's", len(got.Findings))
	}
	want := []report.ReviewPartition{{Files: []string{"a.go"}, Lines: 3, Findings: 0}, {Files: []string{"b.go"}, Lines: 4, Findings: 2}, {Files: []string{"c.go"}, Findings: 1}}
	if !reflect.DeepEqual(got.Partitions, want) {
		t.Errorf("Partitions = %+v, want %+v", got.Partitions, want)
	}
	if plan[1].Findings != 0 {
		t.Error("mergePartitions must record the counts on a copy of the plan")
	}
}

// TestMergePartitions_TieGoesToThePartitionWithFindings: a clean partition's
// zero-finding verdict ranks with every other merge, so on a tie the first
// partition that returned findings supplies the recommendation, and the report
// that lists them never ends "the review found no issues". Between partitions
// that both returned findings, the first keeps it (decision 142).
func TestMergePartitions_TieGoesToThePartitionWithFindings(t *testing.T) {
	plan := []report.ReviewPartition{{Files: []string{"a/a.go"}}, {Files: []string{"b/b.go"}}, {Files: []string{"c/c.go"}}}
	clean := func() *report.ReviewResult {
		return &report.ReviewResult{Recommendation: "merge because the review found no issues"}
	}
	withFinding := func(rec, file string) *report.ReviewResult {
		return &report.ReviewResult{Recommendation: rec, Findings: []report.Finding{{Severity: report.SeverityWarning, Title: "Unclear helper name", File: file, Line: 3}}}
	}

	second := "merge because Unclear helper name in b/b.go is cosmetic"
	if got := mergePartitions([]*report.ReviewResult{clean(), withFinding(second, "b/b.go"), clean()}, plan); got.Recommendation != second {
		t.Errorf("Recommendation = %q, want the verdict of the partition with findings %q", got.Recommendation, second)
	}
	third := "merge because Unclear helper name in c/c.go is cosmetic"
	if got := mergePartitions([]*report.ReviewResult{clean(), withFinding(second, "b/b.go"), withFinding(third, "c/c.go")}, plan); got.Recommendation != second {
		t.Errorf("Recommendation = %q, want the first verdict that came with findings %q", got.Recommendation, second)
	}
	if got := mergePartitions([]*report.ReviewResult{clean(), clean(), clean()}, plan); got.Recommendation != clean().Recommendation {
		t.Errorf("Recommendation = %q, want the clean verdict of a review that found nothing", got.Recommendation)
	}
	// A verdict the merge cannot read outranks a clean one whatever the
	// findings.
	offForm := "merge after fixing the leaked handle"
	if got := mergePartitions([]*report.ReviewResult{withFinding(second, "a/a.go"), withFinding(offForm, "b/b.go"), clean()}, plan); got.Recommendation != offForm {
		t.Errorf("Recommendation = %q, want the unreadable verdict %q over a clean merge", got.Recommendation, offForm)
	}
}

// TestVerdictRank locks the order mergePartitions keeps the strongest
// recommendation by, read past Markdown emphasis: a verdict it cannot read,
// such as a "merge" followed by anything but "because", ranks above a clean
// merge, so a doubtful partition never yields to one.
func TestVerdictRank(t *testing.T) {
	for rec, want := range map[string]int{
		"":                                     0,
		" ** ":                                 0,
		"Do not merge because X":               4,
		"**Do not merge** because X":           4,
		"**Do** not merge because X":           4,
		"Don't merge because X":                4,
		"don’t merge because X":                4,
		"merge after fixes because Y":          3,
		"_Merge after fixes_ because Y":        3,
		"merge  after\tfixes because Y":        3,
		"Recommendation: ship it":              2,
		"merge after fixing the leaked handle": 2,
		"Merge after fix because X":            2,
		"mergeable because X":                  2,
		"Recommendation: merge because X":      2,
		"  merge because nothing broke ":       1,
		"**Merge** because nothing broke":      1,
		"**Merge**: because X":                 1,
		"`merge`, because X":                   1,
		"merge\nbecause X":                     1,
		"Merge.":                               1,
	} {
		if got := verdictRank(rec); got != want {
			t.Errorf("verdictRank(%q) = %d, want %d", rec, got, want)
		}
	}
}

// TestMergePartitions_DuplicateGetsNoBoost: two partitions that report one
// defect are one pass, so the folded finding carries the review label alone
// and keeps its confidence (decision 142).
func TestMergePartitions_DuplicateGetsNoBoost(t *testing.T) {
	finding := func() report.Finding {
		return report.Finding{Severity: report.SeverityWarning, Title: "Unchecked error from Close", File: "a/x.go", Line: 12, Confidence: report.ConfidenceLikely, Problem: "p", Action: "a"}
	}
	plan := []report.ReviewPartition{{Files: []string{"a/x.go"}}, {Files: []string{"b/y.go"}}}
	got := mergePartitions([]*report.ReviewResult{{Findings: []report.Finding{finding()}}, {Findings: []report.Finding{finding()}}}, plan)

	if len(got.Findings) != 1 {
		t.Fatalf("got %d findings, want the duplicate folded into one: %+v", len(got.Findings), got.Findings)
	}
	f := got.Findings[0]
	if !slices.Equal(f.ConfirmedBy, []string{hygiene.PassReview}) {
		t.Errorf("confirmed_by = %v, want [%s] alone", f.ConfirmedBy, hygiene.PassReview)
	}
	if f.Confidence != report.ConfidenceLikely {
		t.Errorf("confidence = %q, want it unchanged", f.Confidence)
	}
}

// TestMergePartitions_KeepsTheAnchorCountsAndEveryAccount: the merge starts
// from the first result, so the line anchor's counts recorded on it survive,
// and it joins every partition's account of its files, which MergeResults
// does not carry; nil results are skipped (decision 142).
func TestMergePartitions_KeepsTheAnchorCountsAndEveryAccount(t *testing.T) {
	anchor := &report.AnchorGateStats{Examined: 4, Reanchored: 1}
	results := []*report.ReviewResult{
		{Gates: &report.GateStats{Anchor: anchor}, FilesReviewed: []report.FileReview{{File: "a.go", Status: report.FileReviewed}}},
		nil,
		{FilesReviewed: []report.FileReview{{File: "c.go", Status: report.FileSkipped, Reason: "generated"}}, Partitions: []report.ReviewPartition{{Files: []string{"forged"}}}},
	}
	plan := []report.ReviewPartition{{Files: []string{"a.go"}}, {Files: []string{"b.go"}}, {Files: []string{"c.go"}}}
	got := mergePartitions(results, plan)

	if got.Gates == nil || got.Gates.Anchor != anchor {
		t.Errorf("Gates = %+v, want the first partition's anchor counts", got.Gates)
	}
	want := []report.FileReview{{File: "a.go", Status: report.FileReviewed}, {File: "c.go", Status: report.FileSkipped, Reason: "generated"}}
	if !reflect.DeepEqual(got.FilesReviewed, want) {
		t.Errorf("FilesReviewed = %+v, want %+v", got.FilesReviewed, want)
	}
	if len(got.Partitions) != 3 || got.Partitions[2].Files[0] != "c.go" {
		t.Errorf("Partitions = %+v, want the plan, never a session's own", got.Partitions)
	}
	if empty := mergePartitions([]*report.ReviewResult{nil, nil}, plan[:2]); empty == nil || len(empty.Partitions) != 2 {
		t.Errorf("merging no results = %+v, want an empty result carrying the plan", empty)
	}
}

// partitionCalls records the contexts a partitioned run handed the review
// sessions, which run concurrently.
type partitionCalls struct {
	mu   sync.Mutex
	ctxs []claude.ReviewContext
}

func (p *partitionCalls) add(ctx claude.ReviewContext) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ctxs = append(p.ctxs, ctx)
}

// byIndex returns the recorded contexts ordered by partition number.
func (p *partitionCalls) byIndex() []claude.ReviewContext {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := slices.Clone(p.ctxs)
	slices.SortFunc(out, func(a, b claude.ReviewContext) int { return a.Partition.Index - b.Partition.Index })
	return out
}

// TestRun_PartitionFiles_OneSessionPerPartition locks the partitioned run end
// to end: one review session per partition, each told its partition and the
// rest, and a JSON report that carries the plan and every session's findings
// (decision 142).
func TestRun_PartitionFiles_OneSessionPerPartition(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 81, "sha-partitions")
	pr.ChangedFiles = []string{"c/1.go", "a/2.go", "b/1.go", "a/1.go", "b/2.go"}
	calls := &partitionCalls{}
	claudeMock := &configurableClaude{
		review: func(_ string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			calls.add(ctx)
			file := ctx.Partition.Files[0]
			return &report.ReviewResult{Summary: "partition " + file, Findings: []report.Finding{
				{Severity: report.SeverityWarning, Title: "Defect in " + file, File: file, Line: 1, Problem: "p", Action: "a"},
			}}, nil
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	opts := baseOpts()
	opts.Format = formatJSON
	opts.PartitionFiles = 2

	var out bytes.Buffer
	if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if claudeMock.reviewCalls != 3 {
		t.Fatalf("Review calls = %d, want one per partition (3)", claudeMock.reviewCalls)
	}
	for i, ctx := range calls.byIndex() {
		p := ctx.Partition
		if p.Index != i+1 || p.Count != 3 {
			t.Errorf("session %d: Partition = %d of %d, want %d of 3", i, p.Index, p.Count, i+1)
		}
		all := append(slices.Clone(p.Files), p.Others...)
		slices.Sort(all)
		if want := slices.Sorted(slices.Values(pr.ChangedFiles)); !slices.Equal(all, want) {
			t.Errorf("session %d: Files %q + Others %q = %q, want every changed file once", i, p.Files, p.Others, all)
		}
	}

	var got report.ReviewResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decoding the JSON output: %v\n%s", err, out.String())
	}
	want := []report.ReviewPartition{
		{Files: []string{"a/1.go", "a/2.go"}, Findings: 1},
		{Files: []string{"b/1.go", "b/2.go"}, Findings: 1},
		{Files: []string{"c/1.go"}, Findings: 1},
	}
	if !reflect.DeepEqual(got.Partitions, want) {
		t.Errorf("JSON partitions = %+v, want %+v", got.Partitions, want)
	}
	if len(got.Findings) != 3 {
		t.Errorf("JSON carries %d findings, want every partition's: %+v", len(got.Findings), got.Findings)
	}
	if got.Summary != "partition a/1.go\n\npartition b/1.go\n\npartition c/1.go" {
		t.Errorf("Summary = %q, want the partitions' summaries in partition order", got.Summary)
	}
}

// TestRun_PartitionFiles_BelowThresholdKeepsOneSession: a pull request with
// no more changed files than the flag is reviewed as before, by one session
// that is told of no partition (decision 142).
func TestRun_PartitionFiles_BelowThresholdKeepsOneSession(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 82, "sha-one-session")
	pr.ChangedFiles = []string{"a/1.go", "b/1.go"}
	var handed claude.PartitionScope
	claudeMock := &configurableClaude{
		review: func(_ string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			handed = ctx.Partition
			return &report.ReviewResult{Summary: "ok", Partitions: []report.ReviewPartition{{Files: []string{"forged"}}}}, nil
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	opts := baseOpts()
	opts.Format = formatJSON
	opts.PartitionFiles = 2

	var out bytes.Buffer
	if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if claudeMock.reviewCalls != 1 {
		t.Errorf("Review calls = %d, want 1", claudeMock.reviewCalls)
	}
	if !reflect.DeepEqual(handed, claude.PartitionScope{}) {
		t.Errorf("ReviewContext.Partition = %+v, want the zero scope", handed)
	}
	if strings.Contains(out.String(), `"partitions"`) {
		t.Errorf("an unpartitioned review must carry no partitions, not even a session's own:\n%s", out.String())
	}
}

// TestRun_PartitionFiles_FailedPartitionFailsTheRun: a review that lost a
// partition would claim coverage it does not have, so the run fails, names
// the partition, and caches nothing (decision 142).
func TestRun_PartitionFiles_FailedPartitionFailsTheRun(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 83, "sha-partition-fails")
	pr.ChangedFiles = []string{"a/1.go", "b/1.go", "c/1.go"}
	claudeMock := &configurableClaude{
		review: func(_ string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			if ctx.Partition.Index == 2 {
				return nil, errors.New("session timed out")
			}
			return &report.ReviewResult{Summary: "ok"}, nil
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	opts := baseOpts()
	opts.PartitionFiles = 1

	err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&bytes.Buffer{}, opts)
	if err == nil || !strings.Contains(err.Error(), "partition 2 of 3") || !strings.Contains(err.Error(), "session timed out") {
		t.Fatalf("Run error = %v, want one naming partition 2 of 3 and its cause", err)
	}
	if _, ok := readCachedResult(t, cache.Key(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, "partition-files=1", baseOptsPatternFlag())); ok {
		t.Error("a run that lost a partition must not be cached")
	}
}

// TestRun_PartitionFiles_SecondaryPassesStayWholePR: the adversarial pass and
// the specialists run once each over the whole pull request, and the
// partitions alone are enough to run the file-less dedup (decision 142).
func TestRun_PartitionFiles_SecondaryPassesStayWholePR(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	files := []string{"api/handler.go", "api/routes.go", "internal/a.go", "internal/b.go", "cmd/main.go"}
	review := func(_ string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
		return &report.ReviewResult{Summary: "ok", Findings: []report.Finding{
			{Severity: report.SeverityWarning, Title: "Missing Requirement: retries", Problem: "p", Action: "a"},
		}}, nil
	}

	pr := fakePR(t, "acme", "widgets", 84, "sha-partition-secondaries")
	pr.ChangedFiles = files
	claudeMock := &configurableClaude{review: review, adversarial: func(_, _ string) (*report.ReviewResult, error) { return &report.ReviewResult{}, nil }}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	opts := baseOpts()
	opts.PartitionFiles = 2
	opts.Thorough = true
	opts.Specialists = true
	if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if claudeMock.reviewCalls != 3 {
		t.Errorf("Review calls = %d, want 3 partitions", claudeMock.reviewCalls)
	}
	if claudeMock.adversarialCalls != 1 {
		t.Errorf("AdversarialReview calls = %d, want 1 for the whole pull request", claudeMock.adversarialCalls)
	}
	if int(claudeMock.specialistCalls) != len(claude.Specialists) {
		t.Errorf("specialist calls = %d, want one per specialist (%d)", claudeMock.specialistCalls, len(claude.Specialists))
	}

	onlyPartitions := &configurableClaude{review: review}
	pr2 := fakePR(t, "acme", "widgets", 85, "sha-partition-dedup")
	pr2.ChangedFiles = files
	gh2 := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr2, nil }}
	opts2 := baseOpts()
	opts2.PartitionFiles = 2
	if err := (&Runner{Claude: onlyPartitions, GitHub: gh2}).Run(&bytes.Buffer{}, opts2); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if onlyPartitions.dedupCalls != 1 {
		t.Errorf("DedupFindings calls = %d, want 1: three partitions each reported the file-less finding", onlyPartitions.dedupCalls)
	}
}

// TestRun_PartitionFiles_CacheKeyCarriesTheFlag: the flag decides which
// sessions run, so a partitioned run caches under a key of its own (decision
// 142).
func TestRun_PartitionFiles_CacheKeyCarriesTheFlag(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 86, "sha-partition-key")
	pr.ChangedFiles = []string{"a/1.go", "b/1.go", "c/1.go"}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	opts := baseOpts()
	opts.PartitionFiles = 2
	if err := (&Runner{Claude: &configurableClaude{}, GitHub: gh}).Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	cached, ok := readCachedResult(t, cache.Key(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, "partition-files=2", baseOptsPatternFlag()))
	if !ok {
		t.Fatal("expected the run cached under a key carrying partition-files=2")
	}
	if len(cached.Partitions) != 2 {
		t.Errorf("cached partitions = %+v, want the two-partition plan", cached.Partitions)
	}
	if _, ok := readCachedResult(t, cache.Key(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, baseOptsPatternFlag())); ok {
		t.Error("a partitioned run must not be cached under the unpartitioned key")
	}
}

// TestRun_PartitionFiles_AnchorsBeforeTheMerge: two partitions report one
// defect on different lines; both are placed on the quoted code before the
// partitions merge, so they fold into one finding, and the anchor's counts
// cover both partitions (decision 139, decision 142).
func TestRun_PartitionFiles_AnchorsBeforeTheMerge(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	pr := fakePR(t, "acme", "widgets", 87, "sha-partition-anchor")
	pr.ChangedFiles = []string{"main.go", "util/x.go"}
	src := "package main\n\nimport \"fmt\"\n\nfunc serve() {\n\tif err := run(); err != nil {\n\t\tfmt.Println(err)\n\t}\n}\n"
	for name, body := range map[string]string{"main.go": src, "util/x.go": "package util\n"} {
		path := filepath.Join(pr.Dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const snippet = "if err := run(); err != nil {\n\tfmt.Println(err)"
	claudeMock := &configurableClaude{
		review: func(_ string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			line := 2 // the first partition miscounts
			if ctx.Partition.Index == 2 {
				line = 6
			}
			return &report.ReviewResult{Findings: []report.Finding{
				{Severity: report.SeverityWarning, Title: "Server error is printed, not returned", File: "main.go", Line: line, Problem: "p", Action: "a", Confidence: report.ConfidenceLikely, CodeSnippet: snippet},
			}}, nil
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	opts := baseOpts()
	opts.PartitionFiles = 1
	if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	cached, ok := readCachedResult(t, cache.Key(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, "partition-files=1", baseOptsPatternFlag()))
	if !ok {
		t.Fatal("expected a cached result")
	}
	if len(cached.Findings) != 1 {
		t.Fatalf("got %d findings, want the two partitions' findings merged into one: %+v", len(cached.Findings), cached.Findings)
	}
	if f := cached.Findings[0]; f.Line != 6 || f.LineEnd != 7 || f.Confidence != report.ConfidenceLikely {
		t.Errorf("merged finding at %d-%d (%q), want 6-7 at its own confidence", f.Line, f.LineEnd, f.Confidence)
	}
	if cached.Gates == nil || cached.Gates.Anchor == nil || cached.Gates.Anchor.Examined != 2 {
		t.Errorf("anchor stats = %+v, want both partitions' findings examined", cached.Gates)
	}
}

// partitionRepo returns a pull request clone whose origin/main commit writes
// base and whose head commit on top of it writes head and removes remove, the
// maps keyed by slash-separated path. core.quotePath is on in the clone, so
// DiffNames quotes a non-ASCII name whatever the machine's own setting.
func partitionRepo(t *testing.T, base, head map[string]string, remove ...string) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(files map[string]string) {
		t.Helper()
		for name, content := range files {
			p := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
	}
	git("init", "-q")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "tester")
	git("config", "core.quotePath", "true")
	write(base)
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	git("update-ref", "refs/remotes/origin/main", git("rev-parse", "HEAD"))
	write(head)
	for _, name := range remove {
		git("rm", "-q", name)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "head")
	return dir
}

// partitionCheckout returns a pull request clone whose head, on top of
// origin/main, changes a/x.go and b/y.go, renames old.go to c/new.go with a
// one-line edit, and changes go.sum.
func partitionCheckout(t *testing.T) string {
	t.Helper()
	var old strings.Builder
	old.WriteString("package c\n\n")
	for i := range 20 {
		fmt.Fprintf(&old, "var V%d = %d\n", i, i)
	}
	return partitionRepo(t,
		map[string]string{"a/x.go": "package a\n", "b/y.go": "package b\n", "old.go": old.String(), "go.sum": "a v1 h1:x\n"},
		map[string]string{"a/x.go": "package a\n\nvar X = 1\n", "b/y.go": "package b\n\nvar Y = 1\n", "go.sum": "a v2 h1:y\n", "c/new.go": old.String() + "var Added = 1\n"},
		"old.go")
}

// TestRun_PartitionFiles_EachSessionReadsItsOwnDiff locks what a partition
// session reads: a diff of its own files, with a rename paired as in the
// run's diff, removed when the session returns; the run's excluded files
// named on it; its share of the changed-file list; and a file record over the
// whole list (decision 142).
func TestRun_PartitionFiles_EachSessionReadsItsOwnDiff(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	dir := partitionCheckout(t)
	changed, err := (github.Client{}).DiffNames(dir, "main")
	if err != nil {
		t.Fatalf("DiffNames: %v", err)
	}
	fileChanges, err := (github.Client{}).DiffFileChanges(dir, "main")
	if err != nil {
		t.Fatalf("DiffFileChanges: %v", err)
	}
	pr := fakePR(t, "acme", "widgets", 88, "sha-partition-diff")
	pr.Dir, pr.ChangedFiles, pr.FileChanges = dir, changed, fileChanges

	type seen struct {
		diff   diffscope.Diff
		body   string
		listed []string
	}
	var (
		mu       sync.Mutex
		sessions = map[int]seen{}
	)
	claudeMock := &configurableClaude{
		review: func(_ string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			data, err := os.ReadFile(ctx.Diff.File)
			if err != nil {
				t.Errorf("partition %d: the diff must be readable while the session runs: %v", ctx.Partition.Index, err)
			}
			var account []report.FileReview
			for _, f := range ctx.ChangedFiles {
				account = append(account, report.FileReview{File: f.Path, Status: report.FileReviewed})
			}
			mu.Lock()
			sessions[ctx.Partition.Index] = seen{diff: ctx.Diff, body: string(data), listed: ledgerPaths(ctx.ChangedFiles)}
			mu.Unlock()
			return &report.ReviewResult{Summary: "ok", FilesReviewed: account}, nil
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	opts := baseOpts()
	opts.Format = formatJSON
	opts.DiffExclusions = true
	opts.PartitionFiles = 1

	var out bytes.Buffer
	if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	excluded := []report.ExcludedFile{{Path: "go.sum", Reason: report.ExcludedLockfile}}
	for i, want := range []struct {
		file     string
		has, not []string
	}{
		{"a/x.go", []string{"diff --git a/a/x.go b/a/x.go\n", "+var X = 1\n"}, []string{"b/y.go", "c/new.go", "go.sum"}},
		{"b/y.go", []string{"diff --git a/b/y.go b/b/y.go\n", "+var Y = 1\n"}, []string{"a/x.go", "c/new.go", "go.sum"}},
		{"c/new.go", []string{"rename from old.go\n", "rename to c/new.go\n", "+var Added = 1\n"}, []string{"a/x.go", "b/y.go", "go.sum", "+var V0 = 0\n"}},
	} {
		s, ok := sessions[i+1]
		if !ok {
			t.Fatalf("partition %d never ran; sessions = %v", i+1, sessions)
		}
		for _, h := range want.has {
			if !strings.Contains(s.body, h) {
				t.Errorf("partition %d (%s): the diff lacks %q:\n%s", i+1, want.file, h, s.body)
			}
		}
		for _, n := range want.not {
			if strings.Contains(s.body, n) {
				t.Errorf("partition %d (%s): the diff holds %q: another file, or a rename shown as a whole-file addition:\n%s", i+1, want.file, n, s.body)
			}
		}
		if !slices.Equal(s.diff.Excluded, excluded) {
			t.Errorf("partition %d: Diff.Excluded = %+v, want the run's %+v", i+1, s.diff.Excluded, excluded)
		}
		if !slices.Equal(s.listed, []string{want.file}) {
			t.Errorf("partition %d: ChangedFiles lists %q, want its own %q", i+1, s.listed, want.file)
		}
		if _, err := os.Stat(s.diff.Dir); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("partition %d: after Run, stat %s = %v, want not exist", i+1, s.diff.Dir, err)
		}
	}

	var got report.ReviewResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decoding the JSON output: %v\n%s", err, out.String())
	}
	if !slices.Equal(got.ExcludedFiles, excluded) {
		t.Errorf("JSON ExcludedFiles = %+v, want the run's %+v", got.ExcludedFiles, excluded)
	}
	if got.Gates == nil || got.Gates.Files == nil || !reflect.DeepEqual(*got.Gates.Files, report.FileGateStats{Changed: 3, Reviewed: 3}) {
		t.Errorf("JSON gates.files = %+v, want every listed file reviewed across the partitions", got.Gates)
	}
	if wantLines := []int{2, 2, 1}; len(got.Partitions) != 3 || got.Partitions[0].Lines != wantLines[0] || got.Partitions[1].Lines != wantLines[1] || got.Partitions[2].Lines != wantLines[2] {
		t.Errorf("JSON partitions = %+v, want the files' added plus deleted lines %v", got.Partitions, wantLines)
	}
}

// TestRun_PartitionFiles_QuotedPathsReadTheirDiff: DiffNames lists a name
// with a double quote, or with a byte above 0x7F under core.quotePath,
// C-quoted, which matches no file as a pathspec. The plan takes the file
// changes' raw paths, so every partition's diff holds its own file's changes
// and its session lists that file (decision 142).
func TestRun_PartitionFiles_QuotedPathsReadTheirDiff(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	files := []string{"a/ä.go", `b/q"x.go`, "c/plain.go"}
	base, head := map[string]string{}, map[string]string{}
	for _, f := range files {
		pkg := f[:1]
		base[f] = "package " + pkg + "\n"
		head[f] = "package " + pkg + "\n\nvar " + strings.ToUpper(pkg) + " = 1\n"
	}
	dir := partitionRepo(t, base, head)
	changed, err := (github.Client{}).DiffNames(dir, "main")
	if err != nil {
		t.Fatalf("DiffNames: %v", err)
	}
	if !slices.Contains(changed, `"b/q\"x.go"`) {
		t.Fatalf("DiffNames = %q, want git's quoted name for b/q\"x.go, the case under test", changed)
	}
	fileChanges, err := (github.Client{}).DiffFileChanges(dir, "main")
	if err != nil {
		t.Fatalf("DiffFileChanges: %v", err)
	}
	pr := fakePR(t, "acme", "widgets", 90, "sha-partition-quoted")
	pr.Dir, pr.ChangedFiles, pr.FileChanges = dir, changed, fileChanges

	type seen struct {
		body   string
		listed []string
	}
	var (
		mu       sync.Mutex
		sessions = map[int]seen{}
	)
	claudeMock := &configurableClaude{
		review: func(_ string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
			data, err := os.ReadFile(ctx.Diff.File)
			if err != nil {
				t.Errorf("partition %d: reading its diff: %v", ctx.Partition.Index, err)
			}
			var account []report.FileReview
			for _, f := range ctx.ChangedFiles {
				account = append(account, report.FileReview{File: f.Path, Status: report.FileReviewed})
			}
			mu.Lock()
			sessions[ctx.Partition.Index] = seen{body: string(data), listed: ledgerPaths(ctx.ChangedFiles)}
			mu.Unlock()
			return &report.ReviewResult{Summary: "ok", FilesReviewed: account}, nil
		},
	}
	gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
	opts := baseOpts()
	opts.Format = formatJSON
	opts.PartitionFiles = 1

	var out bytes.Buffer
	if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	for i, file := range files {
		s := sessions[i+1]
		if want := "+var " + strings.ToUpper(file[:1]) + " = 1\n"; !strings.Contains(s.body, want) {
			t.Errorf("partition %d (%s): the diff lacks %q:\n%s", i+1, file, want, s.body)
		}
		if !slices.Equal(s.listed, []string{file}) {
			t.Errorf("partition %d: ChangedFiles lists %q, want its own %q", i+1, s.listed, file)
		}
	}

	var got report.ReviewResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decoding the JSON output: %v\n%s", err, out.String())
	}
	want := []report.ReviewPartition{{Files: files[:1], Lines: 2}, {Files: files[1:2], Lines: 2}, {Files: files[2:], Lines: 2}}
	if !reflect.DeepEqual(got.Partitions, want) {
		t.Errorf("JSON partitions = %+v, want %+v", got.Partitions, want)
	}
}

// TestRun_PartitionFiles_LedgerCapAppliesPerSession: each session lists only
// its own share of the changed files, so a pull request whose list exceeds
// maxLedgerFiles keeps it when no session's share does, and loses it when one
// does (decision 138, decision 142).
func TestRun_PartitionFiles_LedgerCapAppliesPerSession(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	const total = maxLedgerFiles + 50
	for _, tc := range []struct {
		maxFiles, added int
		wantListed      bool
	}{
		{maxFiles: total / 2, added: 1, wantListed: true},
		{maxFiles: maxLedgerFiles + 40, added: 0, wantListed: false},
	} {
		pr := fakePR(t, "acme", "widgets", 89, fmt.Sprintf("sha-ledger-cap-%d", tc.maxFiles))
		for i := range total {
			p := fmt.Sprintf("gen/f%03d.go", i)
			pr.ChangedFiles = append(pr.ChangedFiles, p)
			pr.FileChanges = append(pr.FileChanges, github.FileChange{Path: p, Status: github.FileAdded, Added: tc.added})
		}
		var (
			mu     sync.Mutex
			listed []int
		)
		claudeMock := &configurableClaude{
			review: func(_ string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
				var account []report.FileReview
				for _, f := range ctx.ChangedFiles {
					account = append(account, report.FileReview{File: f.Path, Status: report.FileReviewed})
				}
				mu.Lock()
				listed = append(listed, len(ctx.ChangedFiles))
				mu.Unlock()
				return &report.ReviewResult{Summary: "ok", FilesReviewed: account}, nil
			},
		}
		gh := &githubtest.Fake{FetchAndCheckoutFn: func(ref string) (*github.PR, error) { return pr, nil }}
		opts := baseOpts()
		opts.PartitionFiles = tc.maxFiles
		if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&bytes.Buffer{}, opts); err != nil {
			t.Fatalf("maxFiles %d: Run returned error: %v", tc.maxFiles, err)
		}
		slices.Sort(listed)
		cached, ok := readCachedResult(t, cache.Key(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, fmt.Sprintf("partition-files=%d", tc.maxFiles), baseOptsPatternFlag()))
		if !ok {
			t.Fatalf("maxFiles %d: expected a cached result", tc.maxFiles)
		}
		if tc.wantListed {
			if !slices.Equal(listed, []int{total / 2, total / 2}) {
				t.Errorf("maxFiles %d: the sessions listed %v files, want %d each", tc.maxFiles, listed, total/2)
			}
			if cached.Gates == nil || cached.Gates.Files == nil || !reflect.DeepEqual(*cached.Gates.Files, report.FileGateStats{Changed: total, Reviewed: total}) {
				t.Errorf("maxFiles %d: gates.files = %+v, want all %d listed files reviewed", tc.maxFiles, cached.Gates, total)
			}
			continue
		}
		if !slices.Equal(listed, []int{0, 0}) {
			t.Errorf("maxFiles %d: the sessions listed %v files, want none: one share exceeds the cap", tc.maxFiles, listed)
		}
		if cached.Gates != nil && cached.Gates.Files != nil {
			t.Errorf("maxFiles %d: gates.files = %+v, want none", tc.maxFiles, cached.Gates.Files)
		}
	}
}

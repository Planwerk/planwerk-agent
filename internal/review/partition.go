package review

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/diffscope"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/hygiene"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// maxConcurrentPartitions is how many partition sessions of a partitioned
// review run at once. With the adversarial pass, the six specialists, the
// coverage map and the compliance check running beside them, it keeps a
// partitioned run's concurrent sessions near what an unpartitioned
// --thorough --specialists run starts (decision 142).
const maxConcurrentPartitions = 4

// filesToPartition returns the changed files a partitioned review splits
// among its sessions, in a slice of its own: the paths of changes, which git
// printed raw with -z, or the names DiffNames listed (changed) when changes is
// nil; either list minus every path the run's diff left out (excluded) and the
// .planwerk/ planning artifacts reviewLedger drops, which the review prompt has
// every session ignore. A partition's diff takes these paths as literal
// pathspecs, and a name DiffNames printed C-quoted, or trimmed of a leading or
// trailing space, matches no file. A planning artifact counts toward neither
// the --partition-files threshold nor the line budget, and lies in no
// partition's list (decision 142).
func filesToPartition(changed []string, changes []github.FileChange, excluded []report.ExcludedFile) []string {
	if changes != nil {
		changed = make([]string, len(changes))
		for i, c := range changes {
			changed[i] = c.Path
		}
	}
	var files []string
	for _, f := range diffscope.Reviewable(changed, excluded) {
		if !planningArtifact(f) {
			files = append(files, f)
		}
	}
	return files
}

// planReviewPartitions returns the partitions a review of files is split into
// under --partition-files maxFiles, or nil when the review runs as one
// session: maxFiles <= 0, or files holds no more than maxFiles. files are the
// changed files the sessions split (filesToPartition); changes supplies their
// added and deleted line counts, and when it is nil, because git could not
// list them, the plan is made by file count alone (decision 142).
func planReviewPartitions(files []string, changes []github.FileChange, maxFiles int) []report.ReviewPartition {
	if maxFiles <= 0 || len(files) <= maxFiles {
		return nil
	}
	var lines map[string]int
	if changes == nil {
		slog.Warn("the changed lines could not be counted; partitioning the review by file count alone")
	} else {
		lines = make(map[string]int, len(changes))
		for _, c := range changes {
			lines[c.Path] = c.Added + c.Deleted
		}
	}
	return planPartitions(files, lines, maxFiles)
}

// planPartitions splits files into partitions of at most maxFiles files and at
// most a line budget, the files' total of lines spread evenly over
// ceil(files/maxFiles) partitions; a file lines does not hold counts 0, and a
// total of 0 sets no budget. It sorts the files by directory, then path, and
// drops duplicates. It cuts them into runs of one directory each, starting a
// new run where a file would break either cap, so a file over the budget runs
// alone. Then it packs the runs in order, appending a run to the last
// partition while the whole run fits in it and starting a new one otherwise.
// Every file lands in exactly one partition, and the result depends on the
// set of files, not their order. It returns nil when fewer than two
// partitions result (decision 142).
func planPartitions(files []string, lines map[string]int, maxFiles int) []report.ReviewPartition {
	if maxFiles <= 0 {
		return nil
	}
	sorted := slices.Clone(files)
	slices.SortFunc(sorted, func(a, b string) int {
		return cmp.Or(strings.Compare(path.Dir(a), path.Dir(b)), strings.Compare(a, b))
	})
	sorted = slices.Compact(sorted)
	if len(sorted) == 0 {
		return nil
	}

	total := 0
	for _, f := range sorted {
		total += lines[f]
	}
	count := (len(sorted) + maxFiles - 1) / maxFiles
	budget := (total + count - 1) / count
	fits := func(files, lines, addFiles, addLines int) bool {
		return files+addFiles <= maxFiles && (budget == 0 || lines+addLines <= budget)
	}

	var runs []report.ReviewPartition
	for _, f := range sorted {
		n := lines[f]
		if last := len(runs) - 1; last >= 0 && path.Dir(runs[last].Files[0]) == path.Dir(f) && fits(len(runs[last].Files), runs[last].Lines, 1, n) {
			runs[last].Files = append(runs[last].Files, f)
			runs[last].Lines += n
			continue
		}
		runs = append(runs, report.ReviewPartition{Files: []string{f}, Lines: n})
	}

	var plan []report.ReviewPartition
	for _, run := range runs {
		if last := len(plan) - 1; last >= 0 && fits(len(plan[last].Files), plan[last].Lines, len(run.Files), run.Lines) {
			plan[last].Files = append(plan[last].Files, run.Files...)
			plan[last].Lines += run.Lines
			continue
		}
		plan = append(plan, run)
	}
	if len(plan) < 2 {
		return nil
	}
	return plan
}

// otherPartitionFiles returns the files of every partition of plan but the
// i-th, in plan order.
func otherPartitionFiles(plan []report.ReviewPartition, i int) []string {
	var others []string
	for j, p := range plan {
		if j != i {
			others = append(others, p.Files...)
		}
	}
	return others
}

// ledgerSubsets splits the review's changed-file ledger by plan: entry i holds,
// in ledger order, the ledger files that partition i reviews, matched on the
// plan's paths as ledgerPath sanitizes them, as the ledger's are. A ledger
// file no partition holds goes to partition 1, which reports what no
// partition owns, so every ledger file is listed to one session. Without a
// plan it returns the whole ledger as one subset (decision 142).
func ledgerSubsets(ledger []github.FileChange, plan []report.ReviewPartition) [][]github.FileChange {
	if len(plan) == 0 {
		return [][]github.FileChange{ledger}
	}
	owner := make(map[string]int)
	for i, p := range plan {
		for _, f := range p.Files {
			owner[ledgerPath(f)] = i
		}
	}
	subsets := make([][]github.FileChange, len(plan))
	for _, f := range ledger {
		i := owner[f.Path] // 0, partition 1, for a file no partition holds
		subsets[i] = append(subsets[i], f)
	}
	return subsets
}

// largestSessionLedger returns how many ledger files the session that lists
// the most of them would list: the whole ledger without a plan, the largest
// partition's share of it with one. maxLedgerFiles caps this count, since each
// session lists only its own share (decision 138, decision 142).
func largestSessionLedger(ledger []github.FileChange, plan []report.ReviewPartition) int {
	largest := 0
	for _, s := range ledgerSubsets(ledger, plan) {
		largest = max(largest, len(s))
	}
	return largest
}

// partitionContexts returns one review context per partition of plan, in plan
// order: rctx with Partition naming the partition and the files the other
// partitions review, ChangedFiles set to the partition's share of ledger
// (ledgerSubsets), and the zero Diff, which the caller replaces with the
// partition's own diff (partitionDiff). Partition's file lists go through
// ledgerPath, like the ledger, so a path git printed raw cannot break its line
// of the prompt's list; the diff is written from the plan's own paths. Every
// other field, the pattern Scope and Catalog included, is the one all
// sessions share. It returns nil without a plan, when the review runs as one
// session on rctx. reviewPartitions and the review's dry run share it, so a
// preview shows the sessions a run starts (decision 142).
func partitionContexts(rctx claude.ReviewContext, plan []report.ReviewPartition, ledger []github.FileChange) []claude.ReviewContext {
	if len(plan) == 0 {
		return nil
	}
	subsets := ledgerSubsets(ledger, plan)
	contexts := make([]claude.ReviewContext, len(plan))
	for i, p := range plan {
		c := rctx
		c.Diff = diffscope.Diff{}
		c.ChangedFiles = subsets[i]
		c.Partition = claude.PartitionScope{Index: i + 1, Count: len(plan), Files: listedPaths(p.Files), Others: listedPaths(otherPartitionFiles(plan, i))}
		contexts[i] = c
	}
	return contexts
}

// listedPaths returns each of paths through ledgerPath, in a slice of its own.
func listedPaths(paths []string) []string {
	listed := make([]string, len(paths))
	for i, p := range paths {
		listed[i] = ledgerPath(p)
	}
	return listed
}

// partitionDiff writes the diff a partition session reads: the pull request's
// diff (origin/<base>...HEAD) limited to files, the partition's paths as
// filesToPartition gave them, plus the base-side path of each of them
// pr.FileChanges lists as renamed or copied, so the diff pairs a rename as the
// run's diff does instead of showing the whole file as added. It leaves
// nothing out itself, since files holds no file the run's diff left out; a
// written Diff's Excluded is excluded, the run's set, so the prompt names those
// files as an unpartitioned session's does. A failed write yields the zero
// Diff, whose prompt keeps the git command, and a no-op cleanup. The caller
// removes the diff with the cleanup once the session returns (decision 142).
func partitionDiff(pr *github.PR, files []string, excluded []report.ExcludedFile) (diffscope.Diff, func()) {
	paths := slices.Clone(files)
	for _, c := range pr.FileChanges {
		if c.OldPath != "" && slices.Contains(files, c.Path) {
			paths = append(paths, c.OldPath)
		}
	}
	d, cleanup := diffscope.MaterializeOrWarn(diffscope.Request{
		RepoDir: pr.Dir,
		Revs:    []string{"origin/" + pr.BaseBranch + "...HEAD"},
		Paths:   paths,
	})
	if d.File != "" {
		d.Excluded = excluded
	}
	return d, cleanup
}

// reviewPartitions runs one primary-review session per context of sessions,
// which partitionContexts built from plan, at most maxConcurrentPartitions at
// a time, each reading the diff of its partition of plan (partitionDiff),
// which is removed when the session returns. It returns the results in
// partition order, unmerged, so the line anchor sees each partition's findings
// before the merge does. A failed session fails the review with an error
// naming its partition, and the sessions not started by then are skipped
// (decision 142).
func (r *Runner) reviewPartitions(pr *github.PR, plan []report.ReviewPartition, sessions []claude.ReviewContext, excluded []report.ExcludedFile) ([]*report.ReviewResult, error) {
	results := make([]*report.ReviewResult, len(sessions))
	g, ctx := errgroup.WithContext(context.Background())
	g.SetLimit(maxConcurrentPartitions)
	for i, rctx := range sessions {
		g.Go(func() error {
			if ctx.Err() != nil {
				return nil
			}
			diff, cleanup := partitionDiff(pr, plan[i].Files, excluded)
			defer cleanup()
			rctx.Diff = diff
			res, err := r.Claude.Review(pr.Dir, rctx)
			if err != nil {
				return fmt.Errorf("partition %d of %d: %w", rctx.Partition.Index, rctx.Partition.Count, err)
			}
			results[i] = res
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return results, nil
}

// mergePartitions folds the results of a partitioned review into one, in
// partition order, starting from the first non-nil result so the line
// anchor's counts recorded on it carry over. Each partition's findings are
// tagged hygiene.PassReview before hygiene.MergeResults folds them, so a
// defect two partitions report merges into one finding without the
// multi-pass confidence boost. It keeps the first non-empty Model, joins the
// non-empty summaries with a blank line, and keeps the strongest recommendation
// (verdictRank). On a tie it keeps the first that came with findings, else the
// first, so a clean partition's "merge because the review found no issues"
// never ends a report that lists another partition's findings. It appends
// every partition's FilesReviewed, which MergeResults does not carry, and
// records plan, with each partition's finding count before the merge, as
// Partitions (decision 142).
func mergePartitions(results []*report.ReviewResult, plan []report.ReviewPartition) *report.ReviewResult {
	parts := slices.Clone(plan)
	var (
		merged         *report.ReviewResult
		summaries      []string
		recommendation string
		recRank        int  // verdictRank(recommendation)
		recFound       bool // whether recommendation's partition returned findings
		filesReviewed  []report.FileReview
	)
	for i, res := range results {
		if res == nil {
			continue
		}
		if i < len(parts) {
			parts[i].Findings = len(res.Findings)
		}
		hygiene.TagPass(res, hygiene.PassReview)
		if s := strings.TrimSpace(res.Summary); s != "" {
			summaries = append(summaries, s)
		}
		if rank := verdictRank(res.Recommendation); rank > recRank || (rank == recRank && !recFound && len(res.Findings) > 0) {
			recommendation, recRank, recFound = res.Recommendation, rank, len(res.Findings) > 0
		}
		filesReviewed = append(filesReviewed, res.FilesReviewed...)
		if merged == nil {
			merged = res
			continue
		}
		if merged.Model == "" {
			merged.Model = res.Model
		}
		merged = hygiene.MergeResults(merged, res)
	}
	if merged == nil {
		merged = &report.ReviewResult{}
	}
	merged.Summary = strings.Join(summaries, "\n\n")
	merged.Recommendation = recommendation
	merged.FilesReviewed = filesReviewed
	merged.Partitions = parts
	return merged
}

// verdictRank orders the verdicts a review's recommendation opens with, as the
// review prompt asks for them, read without Markdown emphasis and with each
// whitespace run as one space: "do not merge" (or "don't merge") ranks 4,
// "merge after fixes" 3, and "merge" 1, the last only when the verb stands
// alone before "because" or the end, punctuation aside. Any other text ranks
// 2, "merge after fixing the handle" among it, above "merge", so a verdict the
// merge cannot read never yields to a clean one, and an empty recommendation
// ranks 0, so a partition that recommends anything outranks one that
// recommends nothing.
func verdictRank(rec string) int {
	rec = strings.Join(strings.Fields(strings.ToLower(verdictEmphasis.Replace(rec))), " ")
	switch {
	case rec == "":
		return 0
	case strings.HasPrefix(rec, "do not merge"), strings.HasPrefix(rec, "don't merge"), strings.HasPrefix(rec, "don’t merge"):
		return 4
	case strings.HasPrefix(rec, "merge after fixes"):
		return 3
	}
	if verb, _, _ := strings.Cut(rec, " because"); strings.TrimRight(verb, ".,:;!") == "merge" {
		return 1
	}
	return 2
}

// verdictEmphasis drops the Markdown emphasis verdictRank reads past: a
// session may bold or code-format the verdict, or only its verb.
var verdictEmphasis = strings.NewReplacer("*", "", "_", "", "`", "")

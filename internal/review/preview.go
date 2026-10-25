package review

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"text/tabwriter"

	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
	"github.com/planwerk/planwerk-agent/internal/search"
)

// The values of Preview.Cache: an entry a review would be served from, none,
// or a run under --no-cache, which reads no entry.
const (
	previewCacheHit  = "hit"
	previewCacheMiss = "miss"
	previewCacheOff  = "off"
)

// The values of Preview.Post: where the review would be posted.
const (
	previewPostNone    = "none"
	previewPostComment = "comment"
	previewPostInline  = "inline"
)

// formatJSON is the --format value that renders the review, and the dry-run
// report, as JSON.
const formatJSON = "json"

// specialistsOff is the Reason of every specialist row without --specialists,
// which the text form collapses into one row.
const specialistsOff = "--specialists is off"

// Preview is the report of review --dry-run: what a review of the pull
// request would run, as the review's own preparation found it, before any
// session starts. It is printed as text, or as one JSON object under
// --format json (decision 143).
type Preview struct {
	PR          string          `json:"pr"` // owner/repo#N
	Title       string          `json:"title"`
	HeadSHA     string          `json:"head_sha"`
	HeadBranch  string          `json:"head_branch"`
	BaseBranch  string          `json:"base_branch"`
	CacheKey    string          `json:"cache_key"`
	Cache       string          `json:"cache"` // hit | miss | off
	WikiRepo    string          `json:"wiki_repo,omitempty"`
	WikiCommit  string          `json:"wiki_commit,omitempty"`
	MemoryPages int             `json:"memory_pages"`
	Brain       bool            `json:"brain"`
	TechTags    []string        `json:"tech_tags"` // never null
	Patterns    PreviewPatterns `json:"patterns"`
	// ScopePatterns is the review prompt's split of the catalog under
	// --scope-patterns; nil without the flag.
	ScopePatterns *PreviewScope `json:"scope_patterns,omitempty"`
	Feature       string        `json:"feature,omitempty"` // the detected feature file's FeatureID
	ChangedFiles  []string      `json:"changed_files"`     // never null
	// ExcludedFiles is what --diff-exclusions leaves out of the diff the
	// sessions read (decision 137).
	ExcludedFiles []report.ExcludedFile `json:"excluded_files,omitempty"`
	// Partitions is the --partition-files plan, one entry per primary-review
	// session; empty when the review runs as one session (decision 142).
	Partitions []PreviewPartition `json:"partitions,omitempty"`
	Passes     []PreviewPass      `json:"passes"`
	Post       string             `json:"post"` // none | comment | inline
}

// PreviewPatterns counts the loaded catalog by category, before any scoping.
// MaxPatterns is the --max-patterns cap each prompt applies; 0 is none.
type PreviewPatterns struct {
	Loaded          int `json:"loaded"`
	DesignPrinciple int `json:"design_principle"`
	Technology      int `json:"technology"`
	Review          int `json:"review"`
	General         int `json:"general"`
	MaxPatterns     int `json:"max_patterns"`
}

// PreviewScope is how --scope-patterns splits the catalog for the review
// prompt: Inline bodies it carries and Indexed patterns it only lists, both
// before --max-patterns. Without a changed-file list or a written catalog
// every body is inline (decision 141).
type PreviewScope struct {
	Inline  int `json:"inline"`
	Indexed int `json:"indexed"`
}

// PreviewPartition is one partition of the --partition-files plan: the
// changed files its session reviews and their added plus deleted lines.
type PreviewPartition struct {
	Files []string `json:"files"`
	Lines int      `json:"lines"`
}

// PreviewPass is one finder session a review could start. Pass is the session
// label a run records the pass's usage under (usage.passes[].pass). Run says
// whether the run starts it; a pass that does not run has a Reason. A running
// pass has the pattern bodies its prompt carries and the prompt's size in
// bytes. Partition numbers the primary-review sessions of a partitioned
// review, which all run under the review label, and is 0 otherwise.
type PreviewPass struct {
	Pass        string `json:"pass"`
	Partition   int    `json:"partition,omitempty"`
	Run         bool   `json:"run"`
	Reason      string `json:"reason,omitempty"`
	Patterns    int    `json:"patterns"`
	PromptBytes int    `json:"prompt_bytes"`
}

// buildPreview returns the dry run's report of in, which prepareReview
// prepared for pr. It also writes the diffs a partitioned run writes for its
// partition sessions, so each review prompt is sized as a run sends it, and
// removes them before it returns. Every candidate pass gets a row, in a fixed
// order: the primary-review sessions, then the adversarial pass, the coverage
// map, the compliance check and each registered specialist. The specialists
// run as the adaptive gate decides for pr.ChangedFiles (decision 143).
func buildPreview(opts Options, pr *github.PR, wiki patterns.ResolvedWiki, surface search.Surface, cacheKey, cacheState string, in *preparedReview) Preview {
	reviews := []claude.ReviewContext{in.reviewCtx}
	if len(in.sessions) > 0 {
		reviews = make([]claude.ReviewContext, len(in.sessions))
		var cleanups []func()
		defer func() {
			for _, cleanup := range cleanups {
				cleanup()
			}
		}()
		for i, rctx := range in.sessions {
			d, cleanup := partitionDiff(pr, in.plan[i].Files, in.diff.Excluded)
			cleanups = append(cleanups, cleanup)
			rctx.Diff = d
			reviews[i] = rctx
		}
	}

	var specialists []claude.Specialist
	if opts.Specialists {
		for _, sp := range claude.Specialists {
			if sp.ShouldRun(pr.ChangedFiles) {
				specialists = append(specialists, sp)
			}
		}
	}
	prompts := claude.ReviewPassPrompts(claude.ReviewPasses{
		Reviews:     reviews,
		BaseBranch:  pr.BaseBranch,
		Diff:        in.diff,
		Patterns:    in.pats,
		MaxPatterns: opts.MaxPatterns,
		Adversarial: opts.Thorough,
		Coverage:    opts.CoverageMap,
		Feature:     in.feature,
		Specialists: specialists,
	})

	runRow := func(pp claude.PassPrompt) PreviewPass {
		return PreviewPass{Pass: pp.Label, Run: true, Patterns: pp.Patterns, PromptBytes: len(pp.Prompt)}
	}
	var passes []PreviewPass
	for i, pp := range prompts[:len(reviews)] {
		row := runRow(pp)
		if len(reviews) > 1 {
			row.Partition = i + 1
		}
		passes = append(passes, row)
	}
	ran := make(map[string]claude.PassPrompt)
	for _, pp := range prompts[len(reviews):] {
		ran[pp.Label] = pp
	}
	row := func(label, reason string) PreviewPass {
		if pp, ok := ran[label]; ok {
			return runRow(pp)
		}
		return PreviewPass{Pass: label, Reason: reason}
	}
	passes = append(passes,
		row(claude.PassLabelAdversarial, "--thorough is off"),
		row(claude.PassLabelCoverage, "--coverage-map is off"),
		row(claude.PassLabelCompliance, "no Planwerk feature file matches the pull request"),
	)
	for _, sp := range claude.Specialists {
		reason := specialistsOff
		if opts.Specialists {
			reason = "the diff changes no " + sp.Relevance.String()
		}
		passes = append(passes, row(sp.PassLabel(), reason))
	}

	design, technology, reviewCount, general := patterns.CountByCategory(in.pats)
	p := Preview{
		PR:          fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
		Title:       pr.Title,
		HeadSHA:     pr.HeadSHA,
		HeadBranch:  pr.HeadBranch,
		BaseBranch:  pr.BaseBranch,
		CacheKey:    cacheKey,
		Cache:       cacheState,
		WikiRepo:    wiki.Repo,
		WikiCommit:  wiki.CommitSHA,
		MemoryPages: len(wiki.MemoryPages),
		Brain:       surface.Enabled(),
		TechTags:    nonNil(in.techTags),
		Patterns: PreviewPatterns{
			Loaded:          len(in.pats),
			DesignPrinciple: design,
			Technology:      technology,
			Review:          reviewCount,
			General:         general,
			MaxPatterns:     max(opts.MaxPatterns, 0),
		},
		ChangedFiles:  nonNil(pr.ChangedFiles),
		ExcludedFiles: in.diff.Excluded,
		Passes:        passes,
		Post:          previewPostNone,
	}
	if in.feature != nil {
		p.Feature = in.feature.FeatureID
	}
	if opts.ScopePatterns {
		p.ScopePatterns = &PreviewScope{Inline: len(in.pats)}
		if s := in.reviewCtx.Scope; s != nil {
			p.ScopePatterns.Inline, p.ScopePatterns.Indexed = len(s.Inline), len(s.Rest)
		}
	}
	for _, part := range in.plan {
		p.Partitions = append(p.Partitions, PreviewPartition{Files: part.Files, Lines: part.Lines})
	}
	switch {
	case opts.InlineReview:
		p.Post = previewPostInline
	case opts.PostReview:
		p.Post = previewPostComment
	}
	return p
}

// nonNil returns s, or an empty slice for nil, so the JSON field reads [].
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// writePreview writes p to w: one indented JSON object for formatJSON, the
// text form for every other format.
func writePreview(w io.Writer, format string, p Preview) error {
	if format == formatJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(p)
	}
	return writePreviewText(w, p)
}

// writePreviewText writes the text form of p: a header line, one aligned line
// per fact, the changed and excluded files and the partitions as indented
// lists, one row per pass, and a closing line on what is not sized. The paths,
// and the title and feature ID the pull request supplies, go through
// ledgerPath, so a control character in one cannot break a line or add one.
func writePreviewText(w io.Writer, p Preview) error {
	var b strings.Builder
	fmt.Fprintf(&b, "[dry-run] Review of %s: no Claude session, no cache write, nothing posted.\n\n", p.PR)
	line := func(label, value string) { fmt.Fprintf(&b, "%-14s%s\n", label+":", value) }

	if p.Title != "" {
		line("PR", p.PR+": "+ledgerPath(p.Title))
	} else {
		line("PR", p.PR)
	}
	line("Head", fmt.Sprintf("%s (%s into %s)", report.ShortSHA(p.HeadSHA), p.HeadBranch, p.BaseBranch))
	switch p.Cache {
	case previewCacheHit:
		line("Cache", "hit, key "+p.CacheKey+": a review would serve the cached result and start no session; --no-cache runs the passes below")
	case previewCacheMiss:
		line("Cache", "miss, key "+p.CacheKey)
	default:
		line("Cache", "off (--no-cache)")
	}
	if p.WikiRepo != "" {
		line("Wiki", fmt.Sprintf("%s at %s, %s", p.WikiRepo, report.ShortSHA(p.WikiCommit), countOf(p.MemoryPages, "memory page")))
	}
	if p.Brain {
		line("Brain", "the review session may search the local mirror")
	}
	if len(p.TechTags) > 0 {
		line("Technologies", strings.Join(p.TechTags, ", "))
	} else {
		line("Technologies", "none detected")
	}
	pats := fmt.Sprintf("%d loaded (%d design-principle, %d technology, %d review, %d general)",
		p.Patterns.Loaded, p.Patterns.DesignPrinciple, p.Patterns.Technology, p.Patterns.Review, p.Patterns.General)
	if p.Patterns.MaxPatterns > 0 {
		pats += fmt.Sprintf("; --max-patterns %d caps each prompt", p.Patterns.MaxPatterns)
	}
	line("Patterns", pats)
	if s := p.ScopePatterns; s != nil {
		line("Scope", fmt.Sprintf("--scope-patterns: the review carries %s and indexes %d", countOf(s.Inline, "pattern body"), s.Indexed))
	}
	if p.Feature != "" {
		line("Feature", ledgerPath(p.Feature))
	}
	line("Post", p.Post)

	if len(p.ChangedFiles) == 0 {
		b.WriteString("\nChanged files: (none listed; the specialist gate fails open)\n")
	} else {
		fmt.Fprintf(&b, "\nChanged files (%d):\n", len(p.ChangedFiles))
		for _, f := range p.ChangedFiles {
			b.WriteString("  " + ledgerPath(f) + "\n")
		}
	}
	if len(p.ExcludedFiles) > 0 {
		fmt.Fprintf(&b, "\nExcluded files (%d):\n", len(p.ExcludedFiles))
		for _, e := range p.ExcludedFiles {
			b.WriteString("  " + ledgerPath(e.Path) + " (" + e.Label() + ")\n")
		}
	}
	if len(p.Partitions) > 0 {
		fmt.Fprintf(&b, "\nPartitions (%d):\n", len(p.Partitions))
		for i, part := range p.Partitions {
			fmt.Fprintf(&b, "  %d: %s, %s\n", i+1, countOf(len(part.Files), "file"), countOf(part.Lines, "line"))
			for _, f := range part.Files {
				b.WriteString("     " + ledgerPath(f) + "\n")
			}
		}
	}

	b.WriteString("\nPasses:\n")
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	collapsed := false
	for _, ps := range p.Passes {
		if ps.Reason == specialistsOff {
			if !collapsed {
				_, _ = fmt.Fprintf(tw, "  specialists\tskip\t%s\n", specialistsOff)
				collapsed = true
			}
			continue
		}
		name := ps.Pass
		if ps.Partition > 0 {
			name = fmt.Sprintf("%s (partition %d of %d)", ps.Pass, ps.Partition, len(p.Partitions))
		}
		if ps.Run {
			_, _ = fmt.Fprintf(tw, "  %s\trun\t%s\t%s\n", name, countOf(ps.Patterns, "pattern"), formatBytes(ps.PromptBytes))
			continue
		}
		_, _ = fmt.Fprintf(tw, "  %s\tskip\t%s\n", name, ps.Reason)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	b.WriteString("\nThe sizes are prompt bytes, not tokens. The passes that work on the findings (dedup, claim verification, capture) depend on the findings and are not sized.\n")

	_, err := io.WriteString(w, b.String())
	return err
}

// countOf renders n with noun, plural for every n but 1: "1 file", "3 files".
// A noun ending in "y" takes "ies", as "body" does.
func countOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	if strings.HasSuffix(noun, "y") {
		return fmt.Sprintf("%d %sies", n, strings.TrimSuffix(noun, "y"))
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// formatBytes renders a prompt size: "N B" under 1024 bytes, "N.N KB" in
// units of 1024 bytes from there.
func formatBytes(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f KB", float64(n)/1024)
}

// printReviewPrompt writes the prompt of the review session in, or of the
// first partition's session for a partitioned review, which it logs; the
// dry run lists every partition (decision 143).
func printReviewPrompt(w io.Writer, in *preparedReview) error {
	ctx := in.reviewCtx
	if len(in.sessions) > 0 {
		slog.Info("printing the first review partition; --dry-run lists them all", "partitions", len(in.sessions))
		ctx = in.sessions[0]
	}
	return writePrompt(w, claude.BuildReviewPrompt(ctx))
}

// writePrompt writes prompt to w, ending it with a newline when it lacks one.
func writePrompt(w io.Writer, prompt string) error {
	if _, err := io.WriteString(w, prompt); err != nil {
		return fmt.Errorf("writing prompt: %w", err)
	}
	if !strings.HasSuffix(prompt, "\n") {
		_, _ = fmt.Fprintln(w)
	}
	return nil
}

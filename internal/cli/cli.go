package cli

import (
	"time"

	"github.com/planwerk/planwerk-review/internal/audit"
	"github.com/planwerk/planwerk-review/internal/draft"
	"github.com/planwerk/planwerk-review/internal/elaborate"
	"github.com/planwerk/planwerk-review/internal/fix"
	"github.com/planwerk/planwerk-review/internal/gapanalysis"
	"github.com/planwerk/planwerk-review/internal/implement"
	"github.com/planwerk/planwerk-review/internal/meta"
	"github.com/planwerk/planwerk-review/internal/prompt"
	"github.com/planwerk/planwerk-review/internal/propose"
	"github.com/planwerk/planwerk-review/internal/rebase"
	"github.com/planwerk/planwerk-review/internal/report"
	"github.com/planwerk/planwerk-review/internal/review"
	"github.com/planwerk/planwerk-review/internal/reviewprepared"
)

// Config holds configuration for the review command.
type Config struct {
	PRRef           string
	PatternDirs     []string
	MinSeverity     report.Severity
	MinConfidence   report.Confidence
	NoRepoPatterns  bool
	NoLocalPatterns bool
	NoCache         bool
	ClearCache      bool
	ClearCacheScope string // restrict --clear-cache to a single command (review/propose/audit); empty = all
	CacheStats      bool
	CacheInspect    string // cache key to print contents + metadata for
	CacheMaxAge     time.Duration
	Format          string
	PostReview      bool
	InlineReview    bool
	Thorough        bool
	Specialists     bool
	CoverageMap     bool
	MaxPatterns     int
	MaxFindings     int
	Local           bool
	Force           bool
}

func (c Config) ToReviewOptions(version string) review.Options {
	return review.Options{
		PRRef:           c.PRRef,
		PatternDirs:     c.PatternDirs,
		NoRepoPatterns:  c.NoRepoPatterns,
		NoLocalPatterns: c.NoLocalPatterns,
		NoCache:         c.NoCache,
		MinSeverity:     c.MinSeverity,
		MinConfidence:   c.MinConfidence,
		Format:          c.Format,
		Version:         version,
		PostReview:      c.PostReview,
		InlineReview:    c.InlineReview,
		Thorough:        c.Thorough,
		Specialists:     c.Specialists,
		CoverageMap:     c.CoverageMap,
		MaxPatterns:     c.MaxPatterns,
		MaxFindings:     c.MaxFindings,
		CacheMaxAge:     c.CacheMaxAge,
		Local:           c.Local,
		Force:           c.Force,
	}
}

// ProposeConfig holds configuration for the propose command.
type ProposeConfig struct {
	RepoRef         string
	PatternDirs     []string
	NoRepoPatterns  bool
	NoLocalPatterns bool
	NoCache         bool
	Format          string // "markdown", "json", "issues"
	MaxPatterns     int
	CreateIssues    bool
	NoIssueDedupe   bool
	CacheMaxAge     time.Duration
	Local           bool
	Force           bool
}

func (c ProposeConfig) ToProposeOptions(version string) propose.Options {
	return propose.Options{
		RepoRef:         c.RepoRef,
		PatternDirs:     c.PatternDirs,
		NoRepoPatterns:  c.NoRepoPatterns,
		NoLocalPatterns: c.NoLocalPatterns,
		NoCache:         c.NoCache,
		Format:          c.Format,
		Version:         version,
		MaxPatterns:     c.MaxPatterns,
		CreateIssues:    c.CreateIssues,
		NoIssueDedupe:   c.NoIssueDedupe,
		CacheMaxAge:     c.CacheMaxAge,
		Local:           c.Local,
		Force:           c.Force,
	}
}

// AuditConfig holds configuration for the audit command.
type AuditConfig struct {
	RepoRef          string
	PatternDirs      []string
	NoRepoPatterns   bool
	NoLocalPatterns  bool
	NoCache          bool
	MinSeverity      report.Severity
	MinConfidence    report.Confidence
	Format           string // "markdown" or "json"
	MaxPatterns      int
	MaxFindings      int
	CreateIssues     bool
	IssueMinSeverity report.Severity
	NoIssueDedupe    bool
	CacheMaxAge      time.Duration
	Local            bool
	Force            bool
}

// ElaborateConfig holds configuration for the elaborate command.
type ElaborateConfig struct {
	IssueRef            string
	PatternDirs         []string
	NoRepoPatterns      bool
	NoLocalPatterns     bool
	NoCache             bool
	Format              string // "markdown" or "json"
	MaxPatterns         int
	UpdateIssue         bool // overwrite the issue body with the elaboration
	PostComment         bool // post the elaboration as a new issue comment
	Review              bool // run the reviewer gate + refine loop before output
	MaxReviewIterations int  // cap on refine iterations (<=0 uses the package default)
	CacheMaxAge         time.Duration
	Local               bool
	Force               bool
}

func (c ElaborateConfig) ToElaborateOptions(version string) elaborate.Options {
	mode := elaborate.UpdateNone
	switch {
	case c.UpdateIssue:
		mode = elaborate.UpdateReplace
	case c.PostComment:
		mode = elaborate.UpdateComment
	}
	return elaborate.Options{
		IssueRef:            c.IssueRef,
		PatternDirs:         c.PatternDirs,
		NoRepoPatterns:      c.NoRepoPatterns,
		NoLocalPatterns:     c.NoLocalPatterns,
		NoCache:             c.NoCache,
		Format:              c.Format,
		Version:             version,
		MaxPatterns:         c.MaxPatterns,
		UpdateMode:          mode,
		Review:              c.Review,
		MaxReviewIterations: c.MaxReviewIterations,
		CacheMaxAge:         c.CacheMaxAge,
		Local:               c.Local,
		Force:               c.Force,
	}
}

// DraftConfig holds configuration for the draft command.
type DraftConfig struct {
	RepoRef         string
	Seed            string
	Local           bool
	NoInteractive   bool
	DryRun          bool
	NoCreate        bool // alias of DryRun: render without filing
	Labels          []string
	Format          string // "markdown" or "json"
	PrintPrompt     bool
	PrintBarePrompt bool
}

// ToDraftOptions maps the CLI config to draft.Options. --no-create is an alias
// of --dry-run, so either one renders the draft without filing it.
func (c DraftConfig) ToDraftOptions(version string) draft.Options {
	return draft.Options{
		RepoRef:         c.RepoRef,
		Seed:            c.Seed,
		Local:           c.Local,
		NoInteractive:   c.NoInteractive,
		DryRun:          c.DryRun || c.NoCreate,
		Labels:          c.Labels,
		Format:          c.Format,
		PrintPrompt:     c.PrintPrompt,
		PrintBarePrompt: c.PrintBarePrompt,
		Version:         version,
	}
}

// MetaConfig holds configuration for the meta command.
type MetaConfig struct {
	IssueRef string
	Format   string // "markdown" or "json"
	Labels   []string
	DryRun   bool
	NoCreate bool // alias of DryRun: render the split without filing
}

// ToMetaOptions maps the CLI config to meta.Options. --no-create is an alias of
// --dry-run, so either one renders the planned split without filing it.
func (c MetaConfig) ToMetaOptions(version string) meta.Options {
	return meta.Options{
		IssueRef: c.IssueRef,
		Format:   c.Format,
		Labels:   c.Labels,
		DryRun:   c.DryRun || c.NoCreate,
		Version:  version,
	}
}

// FixConfig holds configuration for the fix command.
type FixConfig struct {
	PRRef           string
	PollInterval    time.Duration
	MaxIterations   int
	Interactive     bool
	DryRun          bool
	PrintPrompt     bool
	PrintBarePrompt bool
	NoFixComment    bool

	PatternDirs     []string
	NoRepoPatterns  bool
	NoLocalPatterns bool
	MaxPatterns     int
	Local           bool
	Force           bool
}

func (c FixConfig) ToFixOptions(version string) fix.Options {
	return fix.Options{
		PRRef:           c.PRRef,
		PollInterval:    c.PollInterval,
		MaxIterations:   c.MaxIterations,
		Interactive:     c.Interactive,
		DryRun:          c.DryRun,
		PrintPrompt:     c.PrintPrompt,
		NoFixComment:    c.NoFixComment,
		Version:         version,
		PatternDirs:     c.PatternDirs,
		NoRepoPatterns:  c.NoRepoPatterns,
		NoLocalPatterns: c.NoLocalPatterns,
		MaxPatterns:     c.MaxPatterns,
		Local:           c.Local,
		Force:           c.Force,
	}
}

// RebaseConfig holds configuration for the rebase command.
type RebaseConfig struct {
	PRRef             string
	Onto              string
	Push              bool
	ApplyAdjustments  bool
	MaxIterations     int
	NoAnalysis        bool
	NoAnalysisComment bool
	DryRun            bool
	PrintPrompt       bool
	PrintBarePrompt   bool

	PatternDirs     []string
	NoRepoPatterns  bool
	NoLocalPatterns bool
	MaxPatterns     int
	Local           bool
	Force           bool
}

// ToRebaseOptions maps the CLI config to rebase.Options. PrintBarePrompt is
// handled at the cmd layer (it selects a different entry point), so it is not
// carried into Options — mirroring FixConfig.
func (c RebaseConfig) ToRebaseOptions(version string) rebase.Options {
	return rebase.Options{
		PRRef:             c.PRRef,
		Onto:              c.Onto,
		Push:              c.Push,
		ApplyAdjustments:  c.ApplyAdjustments,
		MaxIterations:     c.MaxIterations,
		NoAnalysis:        c.NoAnalysis,
		NoAnalysisComment: c.NoAnalysisComment,
		DryRun:            c.DryRun,
		PrintPrompt:       c.PrintPrompt,
		Version:           version,
		PatternDirs:       c.PatternDirs,
		NoRepoPatterns:    c.NoRepoPatterns,
		NoLocalPatterns:   c.NoLocalPatterns,
		MaxPatterns:       c.MaxPatterns,
		Local:             c.Local,
		Force:             c.Force,
	}
}

// ImplementConfig holds configuration for the implement command.
type ImplementConfig struct {
	IssueRef        string
	DryRun          bool
	PrintPrompt     bool
	PrintBarePrompt bool
	PrintPlanPrompt bool
	NoPlan          bool
	NoPlanComment   bool
	NoReportComment bool
	Verify          bool

	PatternDirs     []string
	NoRepoPatterns  bool
	NoLocalPatterns bool
	MaxPatterns     int
	Local           bool
	Force           bool
}

func (c ImplementConfig) ToImplementOptions(version string) implement.Options {
	return implement.Options{
		IssueRef:        c.IssueRef,
		DryRun:          c.DryRun,
		PrintPrompt:     c.PrintPrompt,
		PrintBarePrompt: c.PrintBarePrompt,
		PrintPlanPrompt: c.PrintPlanPrompt,
		NoPlan:          c.NoPlan,
		NoPlanComment:   c.NoPlanComment,
		NoReportComment: c.NoReportComment,
		Verify:          c.Verify,
		Version:         version,
		PatternDirs:     c.PatternDirs,
		NoRepoPatterns:  c.NoRepoPatterns,
		NoLocalPatterns: c.NoLocalPatterns,
		MaxPatterns:     c.MaxPatterns,
		Local:           c.Local,
		Force:           c.Force,
	}
}

// PromptConfig holds configuration for the prompt command.
type PromptConfig struct {
	IssueRef string
	Mode     string // "auto" | "fix" | "implement"
}

func (c PromptConfig) ToPromptOptions(version string) prompt.Options {
	mode := prompt.ModeAuto
	switch c.Mode {
	case "fix":
		mode = prompt.ModeFix
	case "implement":
		mode = prompt.ModeImplement
	}
	return prompt.Options{
		IssueRef: c.IssueRef,
		Mode:     mode,
		Version:  version,
	}
}

// GapAnalysisConfig holds configuration for the gap-analysis command.
type GapAnalysisConfig struct {
	RepoRef         string
	PatternDirs     []string
	NoRepoPatterns  bool
	NoLocalPatterns bool
	NoCache         bool
	Format          string // "markdown" or "json"
	MaxPatterns     int

	FeatureID string
	FilePath  string

	CreateIssues  bool
	NoIssueDedupe bool
	CacheMaxAge   time.Duration
	Local         bool
	Force         bool
}

func (c GapAnalysisConfig) ToGapAnalysisOptions(version string) gapanalysis.Options {
	return gapanalysis.Options{
		RepoRef:         c.RepoRef,
		PatternDirs:     c.PatternDirs,
		NoRepoPatterns:  c.NoRepoPatterns,
		NoLocalPatterns: c.NoLocalPatterns,
		NoCache:         c.NoCache,
		Format:          c.Format,
		Version:         version,
		MaxPatterns:     c.MaxPatterns,
		FeatureID:       c.FeatureID,
		FilePath:        c.FilePath,
		CreateIssues:    c.CreateIssues,
		NoIssueDedupe:   c.NoIssueDedupe,
		CacheMaxAge:     c.CacheMaxAge,
		Local:           c.Local,
		Force:           c.Force,
	}
}

// ReviewPreparedConfig holds configuration for the review-prepared command.
type ReviewPreparedConfig struct {
	RepoRef         string
	PatternDirs     []string
	NoRepoPatterns  bool
	NoLocalPatterns bool
	NoCache         bool
	Format          string // "markdown" or "json"
	MaxPatterns     int
	MinSeverity     report.Severity

	FeatureID string
	FilePath  string

	CreatePR bool
	PRBranch string
	PRBase   string

	CacheMaxAge time.Duration
	Local       bool
	Force       bool
}

func (c ReviewPreparedConfig) ToReviewPreparedOptions(version string) reviewprepared.Options {
	return reviewprepared.Options{
		RepoRef:         c.RepoRef,
		PatternDirs:     c.PatternDirs,
		NoRepoPatterns:  c.NoRepoPatterns,
		NoLocalPatterns: c.NoLocalPatterns,
		NoCache:         c.NoCache,
		Format:          c.Format,
		Version:         version,
		MaxPatterns:     c.MaxPatterns,
		MinSeverity:     c.MinSeverity,
		FeatureID:       c.FeatureID,
		FilePath:        c.FilePath,
		CreatePR:        c.CreatePR,
		PRBranch:        c.PRBranch,
		PRBase:          c.PRBase,
		CacheMaxAge:     c.CacheMaxAge,
		Local:           c.Local,
		Force:           c.Force,
	}
}

func (c AuditConfig) ToAuditOptions(version string) audit.Options {
	return audit.Options{
		RepoRef:          c.RepoRef,
		PatternDirs:      c.PatternDirs,
		NoRepoPatterns:   c.NoRepoPatterns,
		NoLocalPatterns:  c.NoLocalPatterns,
		NoCache:          c.NoCache,
		MinSeverity:      c.MinSeverity,
		MinConfidence:    c.MinConfidence,
		Format:           c.Format,
		Version:          version,
		MaxPatterns:      c.MaxPatterns,
		MaxFindings:      c.MaxFindings,
		CreateIssues:     c.CreateIssues,
		IssueMinSeverity: c.IssueMinSeverity,
		NoIssueDedupe:    c.NoIssueDedupe,
		CacheMaxAge:      c.CacheMaxAge,
		Local:            c.Local,
		Force:            c.Force,
	}
}

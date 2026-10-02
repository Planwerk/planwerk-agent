package brain

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/planwerk/planwerk-agent/internal/capture"
	"github.com/planwerk/planwerk-agent/internal/detect"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
	"github.com/planwerk/planwerk-agent/internal/workspace"
)

// maxPageBytes is the largest page the bootstrap keeps, measured as the wiki
// holds it, with its provenance marker: the cap patterns.LoadMemoryPages reads
// a memory page under, so a kept page is one a later session can read.
const maxPageBytes = patterns.MaxMemoryPageBytes

// The reasons the bootstrap itself rejects a proposed page for. Every other
// reason is the reviewer's.
const (
	reasonInvalidPath   = "invalid path"
	reasonDuplicatePath = "duplicate path"
	reasonNoVerdict     = "no review verdict"
	reasonNoReason      = "rejected without a reason"
	reasonReviseNoBody  = "revise verdict without a page"
	reasonEmpty         = "empty page"
)

// reasonTooLarge names the cap in KiB.
var reasonTooLarge = fmt.Sprintf("page exceeds %d KiB", maxPageBytes>>10)

// The steps of a unit, as the error of a stopped run names them.
const (
	stepContent  = "content"
	stepAnalysis = "analysis"
	stepReview   = "review"
	stepApply    = "apply"
)

// A proposed page is new only under a name the wiki, `brain memory`, and a
// shell all take as it is: the rule of commandLineSafe restricted to lowercase
// ASCII. A proposal that names an existing page keeps that page's name.
var (
	memoryPathRe  = regexp.MustCompile(`^memory/[a-z0-9][a-z0-9._-]*\.md$`)
	patternPathRe = regexp.MustCompile(`^review_patterns/[a-z0-9][a-z0-9._-]*\.md$`)
)

// AnalyzeFn runs the analysis session of one unit in the checkout at dir and
// returns the pages it proposes. A nil result proposes nothing.
type AnalyzeFn func(dir string, ctx UnitContext) (*capture.CaptureResult, error)

// ReviewFn runs the review session of one unit's proposals in the checkout at
// dir and returns one verdict per proposed page. A nil result gives none.
type ReviewFn func(dir string, ctx ReviewContext) (*ReviewResult, error)

// BootstrapOptions configures one `brain bootstrap` run.
type BootstrapOptions struct {
	RepoRef string
	// StateDir is the state directory; empty means StateDirName in the working
	// directory.
	StateDir string
	// DryRun lists the units and stops: no session, no wiki clone, no state
	// write.
	DryRun bool
	// WriteWiki pushes the changed pages once no unit remains, after a
	// confirmation at a terminal.
	WriteWiki bool
	// MaxUnits stops the run after this many units; 0 processes every
	// remaining unit.
	MaxUnits int
	// DecisionDocs replaces the discovery of decision documents with these
	// repository-relative paths; NoDecisionDocs reads none.
	DecisionDocs   []string
	NoDecisionDocs bool
	Wiki           patterns.WikiOptions
	Remote         patterns.RemoteOptions
}

// Bootstrapper runs `brain bootstrap` over injected seams, so the run can be
// exercised without GitHub, a wiki, a terminal, or a Claude session. A nil
// seam takes its production default; Analyze and Review have none.
type Bootstrapper struct {
	Source  Source              // nil means APISource over github.Client
	GitHub  BootstrapGitHub     // nil means github.Client{}
	Analyze AnalyzeFn           // the analysis session of one unit
	Review  ReviewFn            // the review session of one unit's proposals
	Usage   func() report.Usage // the Claude usage so far; nil counts nothing
	Writer  capture.WikiWriter  // nil means capture.DefaultWikiWriter{}
	In      io.Reader           // the confirmation is read from it; nil means os.Stdin
	IsTTY   func() bool         // nil means workspace.IsStdinTTY
	Now     func() time.Time    // nil means time.Now
}

// withDefaults returns a copy of b with every nil seam set to its default.
func (b *Bootstrapper) withDefaults() *Bootstrapper {
	c := *b
	if c.GitHub == nil {
		c.GitHub = github.Client{}
	}
	if c.Source == nil {
		c.Source = APISource{GitHub: github.Client{}}
	}
	if c.Usage == nil {
		c.Usage = func() report.Usage { return report.Usage{} }
	}
	if c.Writer == nil {
		c.Writer = capture.DefaultWikiWriter{}
	}
	if c.In == nil {
		c.In = os.Stdin
	}
	if c.IsTTY == nil {
		c.IsTTY = workspace.IsStdinTTY
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return &c
}

// bootstrapRun is the state of one Run.
type bootstrapRun struct {
	*Bootstrapper
	w    io.Writer
	opts BootstrapOptions

	owner, name string
	repo        string // "owner/name"
	dir         string // the state directory
	pagesDir    string // the absolute path of its pages directory
	cloneDir    string // the checkout the sessions run in
	state       *State
	patterns    []patterns.Pattern

	// lastUsage is the snapshot the next usage delta is taken against, and
	// runUsage the sum of this run's deltas.
	lastUsage report.Usage
	runUsage  report.Usage
	// analysisModel and reviewModel are the model ids the latest session of
	// each kind reported.
	analysisModel string
	reviewModel   string
	// rejected lists the proposals rejected in this run, with their unit.
	rejected []unitRejection
}

// unitRejection is a rejected proposal with the key of its unit.
type unitRejection struct {
	unit string
	Rejection
}

// Run bootstraps the project memory of opts.RepoRef from its history. It
// lists the history, clones the repository, groups the history into units,
// and skips the units the state already covers. A dry run prints the remaining
// units and stops. Otherwise the working set is refreshed from the wiki and
// every remaining unit is analyzed and reviewed in order, with the state saved
// after each one, so a run that stops continues with the unit it stopped at.
// The run ends with a report and, under opts.WriteWiki, the gated push.
func (b *Bootstrapper) Run(w io.Writer, opts BootstrapOptions) error {
	owner, name, err := github.ParseRepoRef(opts.RepoRef)
	if err != nil {
		return fmt.Errorf("parsing repo ref: %w", err)
	}
	r := &bootstrapRun{Bootstrapper: b.withDefaults(), w: printableWriter{w}, opts: opts, owner: owner, name: name, repo: owner + "/" + name, dir: opts.StateDir}
	if r.dir == "" {
		r.dir = StateDirName
	}

	if r.state, err = LoadState(r.dir, r.repo); err != nil {
		return err
	}
	listing, err := r.Source.List(owner, name)
	if err != nil {
		return fmt.Errorf("listing the history of %s: %w", r.repo, err)
	}
	clone, err := r.GitHub.CloneRepo(opts.RepoRef)
	if err != nil {
		return fmt.Errorf("cloning repository: %w", err)
	}
	defer clone.Cleanup()
	r.cloneDir = clone.Dir

	docPaths, err := resolveDecisionDocs(clone.Dir, opts.DecisionDocs, opts.NoDecisionDocs)
	if err != nil {
		return err
	}
	units, skippedBots := BuildUnits(r.repo, listing, loadDocChunks(clone.Dir, docPaths))
	remaining := r.remaining(units)
	r.printUnitSummary(units, len(remaining), skippedBots)
	if opts.DryRun {
		for _, u := range remaining {
			r.printf("%s  %s\n", u.unit.Key, foldSpace(u.unit.Title))
		}
		return nil
	}

	if err := r.prepare(len(remaining) > 0); err != nil {
		return err
	}
	todo := remaining
	if opts.MaxUnits > 0 && len(todo) > opts.MaxUnits {
		todo = todo[:opts.MaxUnits]
	}
	if err := r.processUnits(todo, len(units)); err != nil {
		return err
	}
	if err := r.printReport(); err != nil {
		return err
	}
	return r.write(len(remaining) - len(todo))
}

// numberedUnit is a unit with its 1-based number in the full unit list, as
// the progress line prints it ("[3/196]").
type numberedUnit struct {
	unit   Unit
	number int
}

// remaining returns the units the state does not cover, in order: the ones
// without a record, and the issue units that hold a pull request or a closer
// commit their record does not name (UnitRecord.covers).
func (r *bootstrapRun) remaining(units []Unit) []numberedUnit {
	done := make(map[string]UnitRecord, len(r.state.Units))
	for _, rec := range r.state.Units {
		done[rec.Key] = rec
	}
	var out []numberedUnit
	for i, u := range units {
		if rec, ok := done[u.Key]; !ok || !rec.covers(u) {
			out = append(out, numberedUnit{unit: u, number: i + 1})
		}
	}
	return out
}

// printableWriter drops control characters other than newline and tab from
// what is written through it. Unit titles, page paths, and rejection reasons
// are text from outside the tool, and the run prints them to a terminal.
type printableWriter struct {
	w io.Writer
}

func (p printableWriter) Write(b []byte) (int, error) {
	if _, err := io.WriteString(p.w, printable(string(b))); err != nil {
		return 0, err
	}
	return len(b), nil
}

// printf writes one formatted piece of the run's output.
func (r *bootstrapRun) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(r.w, format, args...)
}

// printUnitSummary writes the two lines that count the units.
func (r *bootstrapRun) printUnitSummary(units []Unit, remaining, skippedBots int) {
	kinds := make(map[string]int)
	for _, u := range units {
		kinds[u.Kind]++
	}
	r.printf("Units: %d total, %d processed, %d remaining\n", len(units), len(units)-remaining, remaining)
	r.printf("  %d issues, %d pull requests, %d commit ranges, %d decision document chunks; %d bot-authored pull requests skipped\n",
		kinds[KindIssue], kinds[KindPR], kinds[KindCommits], kinds[KindDoc], skippedBots)
}

// prepare readies a real run: it reconciles the state with the page files,
// refreshes the working set from the wiki, saves the state (which creates the
// state directory on a first run), and, when there are units to process,
// loads the pattern catalog a proposal is deduplicated against.
func (r *bootstrapRun) prepare(haveUnits bool) error {
	if err := r.state.Reconcile(r.dir); err != nil {
		return err
	}
	if err := r.state.Refresh(r.dir, r.Writer, r.wikiRepo(), r.opts.Wiki.Ref); err != nil {
		return err
	}
	if err := r.state.Save(r.dir); err != nil {
		return err
	}
	pagesDir, err := filepath.Abs(filepath.Join(r.dir, pagesDirName))
	if err != nil {
		return fmt.Errorf("resolving the pages directory: %w", err)
	}
	r.pagesDir = pagesDir
	if haveUnits {
		r.patterns = patterns.LoadForRepoOrWarn(patterns.RepoLoadOptions{RepoDir: r.cloneDir, Tags: detect.Technologies(r.cloneDir), Remote: r.opts.Remote})
	}
	return nil
}

// wikiRepo is the wiki of the run: the configured one, else the target
// repository's.
func (r *bootstrapRun) wikiRepo() string {
	if r.opts.Wiki.Repo != "" {
		return r.opts.Wiki.Repo
	}
	return r.repo
}

// processUnits processes units in order and saves the state after each one.
// The first unit that fails ends the run: what it spent is added to the state,
// the state is saved, and the error names the unit and the step.
func (r *bootstrapRun) processUnits(units []numberedUnit, total int) error {
	r.lastUsage = r.Usage()
	for _, nu := range units {
		u := nu.unit
		rec, step, err := r.processUnit(u)
		r.addUsage()
		if err == nil {
			// An issue unit that ran again replaces its earlier record.
			r.state.Units = append(slices.DeleteFunc(r.state.Units, func(old UnitRecord) bool { return old.Key == rec.Key }), rec)
			if err = r.state.Save(r.dir); err != nil {
				step = stepApply
			}
		}
		if err != nil {
			return r.stop(u, step, err)
		}
		r.printf("[%d/%d] %s: %d proposed, %d accepted, %d rejected\n", nu.number, total, u.Key, rec.Proposed, rec.Accepted, len(rec.Rejected))
	}
	return nil
}

// stop ends the run at unit u: it saves the state, says where the run stopped
// when the state could be saved, and returns the error of the unit.
func (r *bootstrapRun) stop(u Unit, step string, err error) error {
	err = fmt.Errorf("unit %s: %s: %w", u.Key, step, err)
	if saveErr := r.state.Save(r.dir); saveErr != nil {
		return errors.Join(err, saveErr)
	}
	r.printf("Stopped at unit %s. The state is saved in %s; run the same command again to continue.\n", u.Key, r.dir)
	return err
}

// addUsage adds the Claude usage since the last snapshot to the state and to
// the run's own total.
func (r *bootstrapRun) addUsage() {
	now := r.Usage()
	delta := now.Sub(r.lastUsage)
	r.lastUsage = now
	r.state.Usage = r.state.Usage.Add(delta)
	r.runUsage = r.runUsage.Add(delta)
}

// processUnit runs one unit: it reads the unit's content, has the analysis
// propose pages, has the review judge the valid ones, records the unit as the
// source of the accepted pages, and writes their bodies into the working set.
// On an error it returns the step that failed.
func (r *bootstrapRun) processUnit(u Unit) (rec UnitRecord, step string, err error) {
	items, omitted, err := unitItems(r.Source, r.GitHub, r.cloneDir, r.owner, r.name, u)
	if err != nil {
		return rec, stepContent, err
	}
	ctx := UnitContext{
		RepoName: r.repo,
		Unit:     u,
		Items:    items,
		Omitted:  omitted,
		PagesDir: r.pagesDir,
		Index:    workingSetIndex(r.pagesDir),
		Patterns: r.patterns,
	}

	result, err := r.Analyze(r.cloneDir, ctx)
	if err != nil {
		return rec, stepAnalysis, err
	}
	var proposed []capture.ProposedPage
	if result != nil {
		r.analysisModel = cmp.Or(result.Model, r.analysisModel)
		// The kind is the list the structuring put the page in, never the
		// model's own "kind" field.
		for i := range result.Patterns {
			result.Patterns[i].Kind = capture.KindPattern
		}
		for i := range result.Memory {
			result.Memory[i].Kind = capture.KindMemory
		}
		proposed = result.AllPages()
	}
	kept, rejected := r.validProposals(proposed)

	var accepted []capture.ProposedPage
	if len(kept) > 0 {
		review, err := r.Review(r.cloneDir, ReviewContext{UnitContext: ctx, Proposed: kept})
		if err != nil {
			return rec, stepReview, err
		}
		if review != nil {
			r.reviewModel = cmp.Or(review.Model, r.reviewModel)
		}
		var more []Rejection
		accepted, more = resolveVerdicts(kept, review, u.Source)
		rejected = append(rejected, more...)
	}

	// The state names the unit as the source of its pages before a page file
	// changes. A run that stops between the two then leaves an entry without a
	// file, which Reconcile drops, or a page under the source of the unit that
	// wrote it, and never a changed page under the source of an earlier unit.
	for _, p := range accepted {
		page := r.state.Pages[p.Path]
		page.Source = u.Source
		r.state.Pages[p.Path] = page
	}
	if len(accepted) > 0 {
		if err := r.state.Save(r.dir); err != nil {
			return rec, stepApply, err
		}
	}
	for _, p := range accepted {
		if err := writePage(r.dir, p.Path, p.Body); err != nil {
			return rec, stepApply, err
		}
	}
	for _, rej := range rejected {
		r.rejected = append(r.rejected, unitRejection{unit: u.Key, Rejection: rej})
	}
	if rejected == nil {
		rejected = []Rejection{}
	}
	rec = UnitRecord{
		Key:        u.Key,
		Title:      u.Title,
		Proposed:   len(proposed),
		Accepted:   len(accepted),
		Rejected:   rejected,
		FinishedAt: r.Now().UTC().Format(time.RFC3339),
	}
	if u.Kind == KindIssue {
		rec.PRs, rec.Commits = u.PRs, u.Commits
	}
	return rec, "", nil
}

// validProposals splits the analysis's proposals into the ones the review
// sees and the ones rejected for their path. A proposal is kept when its path
// names a page of the working set, or is a new memory page or review pattern
// under a valid name; a second proposal for a path of the same unit is a
// duplicate. IsUpdate is set from the working set, never taken from the model.
func (r *bootstrapRun) validProposals(proposed []capture.ProposedPage) (kept []capture.ProposedPage, rejected []Rejection) {
	seen := make(map[string]bool, len(proposed))
	for _, p := range proposed {
		_, exists := r.state.Pages[p.Path]
		validNew := (p.Kind == capture.KindMemory && memoryPathRe.MatchString(p.Path)) ||
			(p.Kind == capture.KindPattern && patternPathRe.MatchString(p.Path))
		switch {
		case !exists && !validNew:
			rejected = append(rejected, Rejection{Path: p.Path, Reason: reasonInvalidPath})
		case seen[p.Path]:
			rejected = append(rejected, Rejection{Path: p.Path, Reason: reasonDuplicatePath})
		default:
			seen[p.Path] = true
			p.IsUpdate = exists
			kept = append(kept, p)
		}
	}
	return kept, rejected
}

// resolveVerdicts applies the review to the proposals it saw. An accept keeps
// the proposed body and a revise with a body keeps the reviewer's. A reject, a
// revise without a body, an unknown verdict, and a missing verdict reject the
// page (rejectionReason). A kept body is stored without a provenance marker,
// and one that is empty, or larger than maxPageBytes once it carries the
// marker for source, is rejected. A nil review rejects every proposal.
func resolveVerdicts(proposed []capture.ProposedPage, review *ReviewResult, source string) (accepted []capture.ProposedPage, rejected []Rejection) {
	verdicts := make(map[string]ReviewedPage)
	if review != nil {
		paths := make(map[string]bool, len(proposed))
		for _, p := range proposed {
			paths[p.Path] = true
		}
		for _, v := range review.Pages {
			if !paths[v.Path] {
				// The path is a session's text: quote it for the terminal.
				slog.Warn("ignoring a review verdict for a page that was not proposed", "path", strconv.Quote(v.Path))
				continue
			}
			if _, dup := verdicts[v.Path]; !dup {
				verdicts[v.Path] = v
			}
		}
	}

	for _, p := range proposed {
		v, ok := verdicts[p.Path]
		verdict := strings.ToLower(strings.TrimSpace(v.Verdict))
		switch {
		case ok && verdict == VerdictAccept:
			// The proposed body stands.
		case ok && verdict == VerdictRevise && strings.TrimSpace(v.Body) != "":
			p.Body = v.Body
		default:
			rejected = append(rejected, Rejection{Path: p.Path, Reason: rejectionReason(verdict, v.Reason)})
			continue
		}
		p.Body = normalizePage(capture.StripMarker(p.Body))
		switch {
		case strings.TrimSpace(p.Body) == "":
			rejected = append(rejected, Rejection{Path: p.Path, Reason: reasonEmpty})
		case len(capture.RenderWithSource(p.Body, source)) > maxPageBytes:
			rejected = append(rejected, Rejection{Path: p.Path, Reason: reasonTooLarge})
		default:
			accepted = append(accepted, p)
		}
	}
	return accepted, rejected
}

// rejectionReason is the reason recorded for a page whose verdict did not keep
// it. A reject carries the reviewer's reason. A revise that reached this point
// came without a page, and its reason was written for the corrected page, so
// it is not passed on. Any other verdict, and a missing one, is no verdict.
func rejectionReason(verdict, reason string) string {
	reason = strings.TrimSpace(reason)
	switch verdict {
	case VerdictReject:
		return cmp.Or(reason, reasonNoReason)
	case VerdictRevise:
		return reasonReviseNoBody
	default:
		return cmp.Or(reason, reasonNoVerdict)
	}
}

// pageStatus is one page of the working set as the report and the write see
// it: its path, its entry, its normalized text, and whether that text differs
// from the wiki version it is based on.
type pageStatus struct {
	path  string
	entry PageState
	body  string
	dirty bool
}

// pageStatuses reads every page of the working set, in path order.
func (r *bootstrapRun) pageStatuses() ([]pageStatus, error) {
	paths := make([]string, 0, len(r.state.Pages))
	for p := range r.state.Pages {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	pages := make([]pageStatus, 0, len(paths))
	for _, p := range paths {
		body, err := readPage(r.dir, p)
		if err != nil {
			return nil, err
		}
		entry := r.state.Pages[p]
		pages = append(pages, pageStatus{path: p, entry: entry, body: body, dirty: hashPage(body) != entry.BaseSHA256})
	}
	return pages, nil
}

// printReport writes the run report: the page counts, one line per changed
// page, per proposal rejected in this run, and per diverged page, the models
// that ran, and the usage of this run and of all runs.
func (r *bootstrapRun) printReport() error {
	pages, err := r.pageStatuses()
	if err != nil {
		return err
	}
	var newPages, updated, unchanged, diverged int
	var changed, divergedLines strings.Builder
	for _, p := range pages {
		switch {
		case p.entry.Diverged:
			diverged++
			fmt.Fprintf(&divergedLines, "- `%s` diverged: the wiki page and the local file both changed\n", p.path)
		case !p.dirty:
			unchanged++
		default:
			verb := "update"
			if p.entry.BaseSHA256 == "" {
				verb = "new"
				newPages++
			} else {
				updated++
			}
			fmt.Fprintf(&changed, "- `%s` (%s) from %s\n", p.path, verb, cmp.Or(p.entry.Source, "a local edit"))
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Pages: %d new, %d updated, %d unchanged, %d diverged\n", newPages, updated, unchanged, diverged)
	sb.WriteString(changed.String())
	for _, rej := range r.rejected {
		// The path and the reason are a session's text: keep each on its line.
		fmt.Fprintf(&sb, "- `%s` (%s): %s\n", foldSpace(rej.Path), rej.unit, foldSpace(rej.Reason))
	}
	sb.WriteString(divergedLines.String())
	fmt.Fprintf(&sb, "Models: analysis %s, review %s\n", cmp.Or(r.analysisModel, "-"), cmp.Or(r.reviewModel, "-"))
	fmt.Fprintf(&sb, "Usage this run: %s\n", usageLine(r.runUsage))
	fmt.Fprintf(&sb, "Usage all runs: %s\n", usageLine(r.state.Usage))
	r.printf("%s", sb.String())
	return nil
}

// usageLine renders a usage for the run report.
func usageLine(u report.Usage) string {
	return fmt.Sprintf("%d input tokens, %d output tokens, %d calls, est. $%.2f", u.InputTokens, u.OutputTokens, u.Calls, u.CostUSD)
}

// write ends the run: the propose-only note without opts.WriteWiki, a note
// while units remain, and otherwise the gated push of every dirty page that
// is not diverged. The push needs a confirmation at a terminal, so a run
// whose stdin is not one is refused before anything is listed.
func (r *bootstrapRun) write(unitsLeft int) error {
	if !r.opts.WriteWiki {
		r.printf("Propose-only: nothing was written to the wiki. The pages are under %s; run again with --write-wiki to push them.\n", filepath.Join(r.dir, pagesDirName))
		return nil
	}
	if unitsLeft > 0 {
		noun := "units remain"
		if unitsLeft == 1 {
			noun = "unit remains"
		}
		r.printf("%d %s; the wiki write runs once every unit is processed.\n", unitsLeft, noun)
		return nil
	}

	pages, err := r.pageStatuses()
	if err != nil {
		return err
	}
	var writes []capture.PageWrite
	for _, p := range pages {
		if !p.dirty || p.entry.Diverged {
			continue
		}
		// A dirty page without a source is a wiki page without a marker that
		// the operator edited by hand: it is pushed as it is, without a marker
		// that would name a unit.
		content := p.body
		if p.entry.Source != "" {
			content = capture.RenderWithSource(p.body, p.entry.Source)
		}
		writes = append(writes, capture.PageWrite{Path: p.path, Content: content, IsUpdate: p.entry.BaseSHA256 != ""})
	}
	if len(writes) == 0 {
		r.printf("Nothing to write: every page matches the wiki.\n")
		return nil
	}
	// The base hashes and the wiki commit of the state belong to the wiki it
	// was refreshed from. The push goes to the wiki of this run and to no
	// other, whatever state.json names.
	if wiki := r.wikiRepo(); r.state.WikiRepo != "" && !strings.EqualFold(r.state.WikiRepo, wiki) {
		return fmt.Errorf("the pages in %s were last refreshed from the %q wiki, and this run writes to the %s wiki; run again once that wiki can be cloned, or delete the directory to start over", r.dir, r.state.WikiRepo, wiki)
	}
	if !r.IsTTY() {
		return fmt.Errorf("refusing to write to the wiki: brain bootstrap pushes only after a confirmation at a terminal, and stdin is not a TTY")
	}

	written, err := capture.WritePages(r.w, r.In, r.IsTTY, r.Writer, capture.WriteRequest{
		Flag:       "--write-wiki",
		WikiRepo:   r.wikiRepo(),
		WikiCommit: r.state.WikiCommit,
		Ref:        r.opts.Wiki.Ref,
		Pages:      writes,
		CommitMsg:  r.commitMsg,
	})
	if err != nil {
		return err
	}
	if len(written) == 0 {
		return nil
	}
	for _, p := range pages {
		if slices.Contains(written, p.path) {
			entry := p.entry
			entry.BaseSHA256 = hashPage(p.body)
			r.state.Pages[p.path] = entry
		}
	}
	return r.state.Save(r.dir)
}

// commitMsg renders the commit message of the push.
func (r *bootstrapRun) commitMsg(written []capture.PageWrite) string {
	count := fmt.Sprintf("%d pages", len(written))
	if len(written) == 1 {
		count = "1 page"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Bootstrap %s\n\nWritten by planwerk-agent brain bootstrap from the history of %s:\n", count, r.repo)
	for _, p := range written {
		fmt.Fprintf(&sb, "- %s\n", p.Path)
	}
	return sb.String()
}

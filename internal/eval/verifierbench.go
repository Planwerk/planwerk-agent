package eval

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/planwerk/planwerk-agent/internal/hygiene"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// verifierBatchMax caps the comments one verifier call judges, so a pull
// request with many sampled comments is split over several calls instead of
// one session that reads every claim at once.
const verifierBatchMax = 5

// verifierTitleRunes bounds the title aacrFinding takes from a comment's first
// line; the whole comment is the finding's problem.
const verifierTitleRunes = 100

// The AACR-Bench categories the verifier bench draws from: the claims the
// severity ladder rates BLOCKING or CRITICAL, the only ones the claim gate
// sends (hygiene.VerifyClaims).
const (
	aacrCategoryDefect   = "Code Defect"
	aacrCategorySecurity = "Security Vulnerability"
)

// VerifierSample is one AACR-Bench comment the verifier bench sent: its pull
// request and revision, its lines, its expert label, category and context,
// and what the verifier made of it. Outcome is the decision
// hygiene.ClassifyVerdict reads from Verdict (hygiene.VerdictConfirmed,
// VerdictRefuted or VerdictUnverifiable), or report.ClaimCheckNoVerdict when
// the call returned no verdict for the comment. Verdict, Evidence and Reason
// are the verifier's own words.
type VerifierSample struct {
	PRURL     string `json:"pr_url"`
	Commit    string `json:"commit"`
	File      string `json:"file"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Label     int    `json:"label"`
	Category  string `json:"category"`
	Context   string `json:"context"`
	Outcome   string `json:"outcome"`
	Verdict   string `json:"verdict,omitempty"`
	Evidence  string `json:"evidence,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// SkippedComment is a sampled comment that never reached the verifier, and
// why: its revision could not be checked out, or its lines could not be read.
type SkippedComment struct {
	PRURL     string `json:"pr_url"`
	File      string `json:"file"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Label     int    `json:"label"`
	Reason    string `json:"reason"`
}

// VerdictTally counts the outcomes of the Sent comments. RefuteRate and
// UnverifiableRate are Refuted and Unverifiable over Sent, nil when Sent is 0.
type VerdictTally struct {
	Sent             int      `json:"sent"`
	Confirmed        int      `json:"confirmed"`
	Refuted          int      `json:"refuted"`
	Unverifiable     int      `json:"unverifiable"`
	NoVerdict        int      `json:"no_verdict"`
	RefuteRate       *float64 `json:"refute_rate"`
	UnverifiableRate *float64 `json:"unverifiable_rate"`
}

// LabelTally is the tally of the comments with one expert label: 0 for a
// comment experts rejected, 1 for one they confirmed.
type LabelTally struct {
	Label int `json:"label"`
	VerdictTally
}

// VerifierReport is what one verifier bench measured: the comments drawn per
// label, the rows the draw found not eligible, the tiers, the tally per label
// (label 0, then label 1) and over all comments, the comments skipped and
// why, every comment sent with its verdict, and the usage of the verifier's
// calls. Skipped and Samples are never nil, so the JSON carries [] rather
// than null. RunVerifierBench fills all but PerLabel, Excluded, Tiers and
// Usage, which the caller sets (decision 148).
type VerifierReport struct {
	PerLabel int              `json:"per_label"`
	Excluded int              `json:"excluded"`
	Tiers    Tiers            `json:"tiers"`
	Labels   []LabelTally     `json:"labels"`
	All      VerdictTally     `json:"all"`
	Skipped  []SkippedComment `json:"skipped"`
	Samples  []VerifierSample `json:"samples"`
	Usage    report.Usage     `json:"usage"`
}

// CheckoutFn checks commit of the pull request at prURL out into a fresh
// directory and returns it with the cleanup that removes it. planwerk-eval's
// is CheckoutAACRHead bound to the -aacr-cache root.
type CheckoutFn func(prURL, commit string) (dir string, cleanup func(), err error)

// SampleVerifierComments draws up to perLabel comments of each expert label
// from rows, label 0 first. A row is eligible when it sits on the right side,
// names a path, has an ordered line range from line 1 on, carries label 0 or
// 1, and is a Code Defect or Security Vulnerability. Each label's eligible
// rows are ordered by the SHA-256 of their pull request URL, path, lines and
// text, so the draw is the same whatever order rows come in, and a larger
// perLabel draws the smaller one's picks first. excluded counts the rows that
// are not eligible. rows is left as it is (decision 148).
func SampleVerifierComments(rows []AACRRow, perLabel int) (sample []AACRRow, excluded int) {
	type keyed struct {
		key [sha256.Size]byte
		row AACRRow
	}
	var pools [2][]keyed
	for _, r := range rows {
		if !verifierEligible(r) {
			excluded++
			continue
		}
		pools[r.Label] = append(pools[r.Label], keyed{verifierKey(r), r})
	}
	for _, pool := range pools {
		slices.SortStableFunc(pool, func(a, b keyed) int { return bytes.Compare(a.key[:], b.key[:]) })
		for _, k := range pool[:max(0, min(perLabel, len(pool)))] {
			sample = append(sample, k.row)
		}
	}
	return sample, excluded
}

// verifierEligible reports whether r is a comment the verifier bench may
// draw: one the claim gate would see, on lines the revision has.
func verifierEligible(r AACRRow) bool {
	return r.Side == aacrRight && r.Path != "" && r.FromLine >= 1 && r.ToLine >= r.FromLine &&
		(r.Label == 0 || r.Label == 1) &&
		(r.Category == aacrCategoryDefect || r.Category == aacrCategorySecurity)
}

// verifierKey is the SHA-256 SampleVerifierComments orders a label's rows by.
func verifierKey(r AACRRow) [sha256.Size]byte {
	return sha256.Sum256([]byte(strings.Join([]string{
		r.PRURL, r.Path, strconv.Itoa(r.FromLine), strconv.Itoa(r.ToLine), r.Note,
	}, "\x00")))
}

// aacrFinding turns comment c into the finding the verifier judges: CRITICAL,
// so it is a claim the gate would send, titled by the comment's first line,
// with the comment as the problem and snippet, its lines at the revision, as
// the quoted code. It sets nothing else and never reads the label.
func aacrFinding(c AACRRow, snippet string) report.Finding {
	return report.Finding{
		Severity:    report.SeverityCritical,
		Title:       noteTitle(c.Note),
		File:        c.Path,
		Line:        c.FromLine,
		LineEnd:     c.ToLine,
		Problem:     strings.TrimSpace(c.Note),
		CodeSnippet: snippet,
	}
}

// noteTitle returns the first non-blank line of note, trimmed, and cut to
// verifierTitleRunes runes, the last of them an ellipsis, when it is longer.
func noteTitle(note string) string {
	for line := range strings.SplitSeq(note, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if utf8.RuneCountInString(line) > verifierTitleRunes {
			return string([]rune(line)[:verifierTitleRunes-1]) + "…"
		}
		return line
	}
	return ""
}

// readSnippet returns lines from through to, inclusive and counted from 1, of
// the slash path in the checkout at dir, joined by \n and without the \r of a
// CRLF ending. It reads through os.Root, so a path or symlink that leaves the
// checkout is refused. A range that does not start at line 1 or later, or
// that runs past the file's end, is an error.
func readSnippet(dir, path string, from, to int) (string, error) {
	if from < 1 || to < from {
		return "", fmt.Errorf("lines %d-%d of %s are no range", from, to, path)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	data, err := root.ReadFile(filepath.FromSlash(path))
	if err != nil {
		return "", err
	}
	var lines []string
	if len(data) > 0 {
		lines = strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	}
	if to > len(lines) {
		return "", fmt.Errorf("lines %d-%d past the end of %s (%d lines)", from, to, path, len(lines))
	}
	picked := lines[from-1 : to]
	for i, line := range picked {
		picked[i] = strings.TrimSuffix(line, "\r")
	}
	return strings.Join(picked, "\n"), nil
}

// revision is the pull request and commit a group of sampled comments sits
// on, and the indices of those comments in the sample.
type revision struct {
	prURL, commit string
	idx           []int
}

// groupByRevision groups the indices of sample by pull request and commit, in
// order of first appearance.
func groupByRevision(sample []AACRRow) []revision {
	var groups []revision
	at := make(map[[2]string]int)
	for i, c := range sample {
		k := [2]string{c.PRURL, c.TargetCommit}
		g, ok := at[k]
		if !ok {
			g = len(groups)
			at[k] = g
			groups = append(groups, revision{prURL: c.PRURL, commit: c.TargetCommit})
		}
		groups[g].idx = append(groups[g].idx, i)
	}
	return groups
}

// RunVerifierBench sends each comment of sample to verify as the CRITICAL
// finding aacrFinding builds, quoting its lines at the comment's revision,
// and tallies the verdicts per expert label (decision 148). It checks each
// pull request's revision out once, through checkout, and sends that pull
// request's comments at most verifierBatchMax per call. A revision checkout
// cannot provide, or lines its file does not have, skip the comment with the
// reason in Skipped. A verify error ends the bench with an error naming the
// pull request, after the checkout's cleanup ran: a failed call counted as no
// verdict would read as a verifier that abstains. verify is called as is, so a
// caller that wants a retry wraps it. Each verdict is classified by
// hygiene.ClassifyVerdict, the gate's own classifier; a comment the call
// returned no verdict for is report.ClaimCheckNoVerdict, and a verdict whose
// index lies outside its call's batch is ignored. It writes the plan, and a
// line per checkout and per call, to progress. When no comment reached the
// verifier it returns an error. The caller fills PerLabel, Excluded, Tiers
// and Usage.
func RunVerifierBench(sample []AACRRow, checkout CheckoutFn, verify hygiene.VerifyClaimsFn, progress io.Writer) (VerifierReport, error) {
	groups := groupByRevision(sample)
	b := verifierBench{
		sample:   sample,
		samples:  make([]VerifierSample, len(sample)),
		sent:     make([]bool, len(sample)),
		skipped:  []SkippedComment{},
		verify:   verify,
		progress: progress,
	}
	for i, c := range sample {
		b.samples[i] = VerifierSample{
			PRURL: c.PRURL, Commit: c.TargetCommit, File: c.Path, LineStart: c.FromLine, LineEnd: c.ToLine,
			Label: c.Label, Category: c.Category, Context: c.Context,
		}
	}
	for _, g := range groups {
		b.calls += (len(g.idx) + verifierBatchMax - 1) / verifierBatchMax
	}
	_, _ = fmt.Fprintf(progress, "verifier bench: %d comments over %d pull requests, up to %d verifier calls\n", len(sample), len(groups), b.calls)

	for _, g := range groups {
		if err := b.run(g, checkout); err != nil {
			return VerifierReport{}, err
		}
	}

	rep := VerifierReport{Skipped: b.skipped, Samples: []VerifierSample{}}
	for i, s := range b.samples {
		if b.sent[i] {
			rep.Samples = append(rep.Samples, s)
		}
	}
	rep.Labels, rep.All = tallyVerdicts(rep.Samples)
	if rep.All.Sent == 0 {
		if len(rep.Skipped) > 0 {
			s := rep.Skipped[0]
			return VerifierReport{}, fmt.Errorf("no AACR-Bench comment reached the verifier: %d skipped, the first %s %s:%d-%d: %s",
				len(rep.Skipped), s.PRURL, s.File, s.LineStart, s.LineEnd, s.Reason)
		}
		return VerifierReport{}, errors.New("no AACR-Bench comment reached the verifier")
	}
	return rep, nil
}

// verifierBench is the state of one RunVerifierBench: the sample, its
// samples and which of them were sent, the comments skipped so far, and the
// calls made out of the calls planned.
type verifierBench struct {
	sample      []AACRRow
	samples     []VerifierSample
	sent        []bool
	skipped     []SkippedComment
	verify      hygiene.VerifyClaimsFn
	progress    io.Writer
	call, calls int
}

// run checks g's revision out, reads each comment's lines, and sends the
// comments it could read in batches of at most verifierBatchMax. A checkout
// error skips every comment of g; only a verify error is returned.
func (b *verifierBench) run(g revision, checkout CheckoutFn) error {
	_, _ = fmt.Fprintf(b.progress, "checking out %s at %s ...\n", g.prURL, g.commit)
	dir, cleanup, err := checkout(g.prURL, g.commit)
	if err != nil {
		for _, i := range g.idx {
			b.skip(i, "checkout: "+err.Error())
		}
		return nil
	}
	defer cleanup()

	var idx []int
	var findings []report.Finding
	for _, i := range g.idx {
		c := b.sample[i]
		snippet, err := readSnippet(dir, c.Path, c.FromLine, c.ToLine)
		if err != nil {
			b.skip(i, err.Error())
			continue
		}
		idx = append(idx, i)
		findings = append(findings, aacrFinding(c, snippet))
	}
	for start := 0; start < len(idx); start += verifierBatchMax {
		end := min(start+verifierBatchMax, len(idx))
		b.call++
		_, _ = fmt.Fprintf(b.progress, "verifying %s (%d comments, call %d/%d) ...\n", g.prURL, end-start, b.call, b.calls)
		verdicts, err := b.verify(dir, findings[start:end])
		if err != nil {
			return fmt.Errorf("verifying %s at %s: %w", g.prURL, g.commit, err)
		}
		applyVerdicts(b.samples, idx[start:end], verdicts)
		for _, i := range idx[start:end] {
			b.sent[i] = true
		}
	}
	return nil
}

// skip records sample i as skipped for reason.
func (b *verifierBench) skip(i int, reason string) {
	c := b.sample[i]
	b.skipped = append(b.skipped, SkippedComment{
		PRURL: c.PRURL, File: c.Path, LineStart: c.FromLine, LineEnd: c.ToLine, Label: c.Label, Reason: reason,
	})
}

// applyVerdicts records one call's verdicts on the samples it sent, where
// chunk[j] is the index into samples of the call's finding j. Every sample of
// the chunk starts as report.ClaimCheckNoVerdict; a verdict for finding j sets
// its Outcome by hygiene.ClassifyVerdict and copies the verifier's words. When
// a call returns several verdicts for one finding, the sample keeps the outcome
// hygiene.VerifyClaims acts on, whatever their order (outcomeRank): a
// refutation stands, as the gate's demotion does, and among equal outcomes
// the last verdict's words stand, as the gate's note does. An index outside
// the chunk is ignored.
func applyVerdicts(samples []VerifierSample, chunk []int, verdicts []hygiene.ClaimVerdict) {
	for _, i := range chunk {
		samples[i].Outcome = report.ClaimCheckNoVerdict
	}
	for _, v := range verdicts {
		if v.Index < 0 || v.Index >= len(chunk) {
			continue
		}
		s := &samples[chunk[v.Index]]
		outcome := hygiene.ClassifyVerdict(v.Verdict)
		if outcomeRank(outcome) < outcomeRank(s.Outcome) {
			continue
		}
		s.Outcome = outcome
		s.Verdict, s.Evidence, s.Reason = v.Verdict, v.Evidence, v.Reason
	}
}

// outcomeRank orders the outcomes of one finding's verdicts by which one
// hygiene.VerifyClaims acts on when it gets several: a refutation demotes the
// finding whatever follows it, a confirmation records over an unverifiable
// verdict, which changes nothing, and any verdict counts over none.
func outcomeRank(outcome string) int {
	switch outcome {
	case hygiene.VerdictRefuted:
		return 3
	case hygiene.VerdictConfirmed:
		return 2
	case hygiene.VerdictUnverifiable:
		return 1
	}
	return 0
}

// tallyVerdicts counts the outcomes of samples per label, label 0 then label
// 1, and over all of them.
func tallyVerdicts(samples []VerifierSample) (labels []LabelTally, all VerdictTally) {
	labels = []LabelTally{{Label: 0}, {Label: 1}}
	for _, s := range samples {
		all.count(s.Outcome)
		if s.Label == 0 || s.Label == 1 {
			labels[s.Label].count(s.Outcome)
		}
	}
	for i := range labels {
		labels[i].rates()
	}
	all.rates()
	return labels, all
}

// count adds one sent comment with outcome to t.
func (t *VerdictTally) count(outcome string) {
	t.Sent++
	switch outcome {
	case hygiene.VerdictConfirmed:
		t.Confirmed++
	case hygiene.VerdictRefuted:
		t.Refuted++
	case hygiene.VerdictUnverifiable:
		t.Unverifiable++
	default:
		t.NoVerdict++
	}
}

// rates sets t's rates from its counts, or leaves them nil when nothing was
// sent.
func (t *VerdictTally) rates() {
	if t.Sent == 0 {
		return
	}
	refuted, unverifiable := float64(t.Refuted)/float64(t.Sent), float64(t.Unverifiable)/float64(t.Sent)
	t.RefuteRate, t.UnverifiableRate = &refuted, &unverifiable
}

// aacrLabelName names an expert label in the verifier table.
func aacrLabelName(label int) string {
	if label == 0 {
		return "expert-rejected"
	}
	return "expert-confirmed"
}

// RenderVerifierTable writes rep as an aligned text table: one row per label
// and an ALL row, with the rates as percentages ("n/a" when nothing was sent),
// the two footnotes that say how to read them, then the SAMPLE line with one
// SKIPPED line per skipped comment, the TIERS line and the COST line.
func RenderVerifierTable(w io.Writer, rep VerifierReport) {
	const header = "%-18s  %4s  %9s  %7s  %12s  %10s  %8s  %13s\n"
	const row = "%-18s  %4d  %9d  %7d  %12d  %10d  %8s  %13s\n"
	writeTally := func(name string, t VerdictTally) {
		_, _ = fmt.Fprintf(w, row, name, t.Sent, t.Confirmed, t.Refuted, t.Unverifiable, t.NoVerdict, pct(t.RefuteRate), pct(t.UnverifiableRate))
	}
	head := fmt.Sprintf(header, "LABEL", "SENT", "CONFIRMED", "REFUTED", "UNVERIFIABLE", "NO-VERDICT", "REFUTED%", "UNVERIFIABLE%")
	_, _ = io.WriteString(w, head)
	for _, l := range rep.Labels {
		writeTally(fmt.Sprintf("%d %s", l.Label, aacrLabelName(l.Label)), l.VerdictTally)
	}
	// The rule is as wide as the header row, its newline aside.
	_, _ = fmt.Fprintln(w, strings.Repeat("-", len(head)-1))
	writeTally("ALL", rep.All)
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "REFUTED% = refuted/sent: the share the gate would demote. UNVERIFIABLE% = unverifiable/sent.")
	_, _ = fmt.Fprintln(w, "A verifier that tells the labels apart refutes label 0 far more often than label 1.")

	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "SAMPLE: %d per label; %d rows not eligible; %d skipped\n", rep.PerLabel, rep.Excluded, len(rep.Skipped))
	for _, s := range rep.Skipped {
		_, _ = fmt.Fprintf(w, "  SKIPPED %s %s:%d-%d (label %d): %s\n", s.PRURL, s.File, s.LineStart, s.LineEnd, s.Label, s.Reason)
	}
	_, _ = fmt.Fprintf(w, "TIERS: %s (the verifier runs on the finder tier)\n", rep.Tiers)
	_, _ = fmt.Fprintf(w, "COST: %d calls, %d prompt tokens, %d output tokens, $%.2f\n",
		rep.Usage.Calls, promptTokens(rep.Usage), rep.Usage.OutputTokens, rep.Usage.CostUSD)
}

// RenderVerifierJSON writes rep as indented JSON, as RenderJSON writes a
// review eval's report. An undefined rate serializes as null.
func RenderVerifierJSON(w io.Writer, rep VerifierReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

// CheckoutAACRHead checks commit of the AACR-Bench pull request at prURL out
// into a fresh temp directory and returns it with the cleanup that removes
// it. Bound to the -aacr-cache root, it is the CheckoutFn planwerk-eval
// -verifier-bench runs. It keeps the commit in the pull request's store under
// root, the one PrepareAACR fills (aacrStorePath), creating the store when it
// is missing and fetching the commit at depth 1, under aacrVerifierRef, only
// when the store lacks it. prURL must be a GitHub pull request URL and commit
// a full SHA, so no dataset value reaches git as an option.
func CheckoutAACRHead(root, prURL, commit string) (dir string, cleanup func(), err error) {
	owner, repo, number, err := parseAACRURL(prURL)
	if err != nil {
		return "", nil, err
	}
	if !commitRe.MatchString(commit) {
		return "", nil, fmt.Errorf("%q is not a full commit SHA", commit)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", nil, fmt.Errorf("AACR-Bench cache %s: %w", root, err)
	}
	return checkoutAACRCommit(aacrStorePath(abs, owner, repo, number), aacrCloneURL(owner, repo), strings.ToLower(commit))
}

// aacrVerifierRef is the ref a store keeps the revision the verifier bench
// fetched under, so the commit stays reachable. It is not the ref PrepareAACR
// fetches the revision into: git fetch skips a ref that already holds the
// commit it asks for, unless every ref does, so PrepareAACR's first fetch
// would deepen the base alone, leave the revision at the bench's depth of 1,
// and find the merge base only in a deeper round.
const aacrVerifierRef = "refs/planwerk-eval/verifier"

// checkoutAACRCommit is CheckoutAACRHead on an absolute store path and a
// clone URL, so a test can fetch from a file:// repository.
func checkoutAACRCommit(store, cloneURL, commit string) (dir string, cleanup func(), err error) {
	if err := initAACRStore(store); err != nil {
		return "", nil, err
	}
	if _, err := gitRun(store, gitTimeout, nil, "cat-file", "-e", commit+"^{commit}"); err != nil {
		if _, err := gitRun(store, aacrGitTimeout, []string{"GIT_TERMINAL_PROMPT=0"},
			"fetch", "--no-tags", "--depth=1", cloneURL, "+"+commit+":"+aacrVerifierRef); err != nil {
			return "", nil, err
		}
	}
	dir, err = os.MkdirTemp("", "planwerk-eval-verifier-*")
	if err != nil {
		return "", nil, fmt.Errorf("temp checkout: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	if err := checkoutAACRTree(dir, store, commit); err != nil {
		cleanup()
		return "", nil, err
	}
	return dir, cleanup, nil
}

// checkoutAACRTree builds a repository in dir that reads store through
// alternates and checks commit's tree out on main, in a commit of its own:
// the store is shallow, and the new repository, which has no record of where
// the history was cut, would fail on the revision's missing parents.
func checkoutAACRTree(dir, store, commit string) error {
	if err := initAACRRepo(dir, store); err != nil {
		return err
	}
	head, err := gitRun(dir, gitTimeout, nil, "commit-tree", "--no-gpg-sign", "-m", "revision", commit+"^{tree}")
	if err != nil {
		return err
	}
	if err := git(dir, "update-ref", "refs/heads/"+evalBaseBranch, head); err != nil {
		return err
	}
	// As in setupAACRRepo: the smudge filter would fetch from a remote the
	// repository does not have.
	_, err = gitRun(dir, aacrGitTimeout, []string{"GIT_LFS_SKIP_SMUDGE=1"}, "reset", "-q", "--hard")
	return err
}

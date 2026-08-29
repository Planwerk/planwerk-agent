// Package githubtest is the one test double for github.Client. A Fake carries
// the same method set as the client, so it satisfies every command package's
// narrow GitHubClient interface the way github.Client{} does, and the command
// packages no longer each keep a fake of their own.
//
// Every operation has three layers. A hook (<Method>Fn) scripts it completely
// when set. Otherwise a scripted value (Issue, PR, Checks, ...) or its error
// field answers, with a zero value that stands for "nothing there" so a test
// only sets what it exercises. Every call is recorded regardless, so a test
// asserts what happened through Count, Calls, or the typed accessors.
package githubtest

import (
	"fmt"
	"sync"

	"github.com/planwerk/planwerk-agent/internal/github"
)

// Call is one recorded invocation: the method, its arguments in order, and
// the error it returned.
type Call struct {
	Method string
	Args   []any
	Err    error
}

// MergeCall is one recorded MergePR invocation.
type MergeCall struct {
	Number  int
	Method  string
	HeadSHA string
}

// Fake scripts and records the GitHub and git operations. The zero value is
// usable; set only the fields the test needs.
type Fake struct {
	mu    sync.Mutex
	calls []Call

	// Dir is the checkout directory the clone and checkout operations report
	// when the PR or Repo template leaves it empty.
	Dir string

	// PR is the template FetchAndCheckout and OpenLocalPR return: a copy with
	// Owner, Repo and Number filled from the ref, Dir defaulted from Dir, and
	// Local set by OpenLocalPR. FetchErr fails both. When only
	// FetchAndCheckoutFn is set, OpenLocalPR answers through it with Local set,
	// and UseLocalRepo does the same with CloneRepoFn.
	PR       github.PR
	FetchErr error

	// Repo is the template CloneRepo and UseLocalRepo return, filled the same
	// way. CloneErr fails both.
	Repo     github.Repo
	CloneErr error

	// Issue is what GetIssue returns, with Owner, Name and Number set from the
	// arguments; nil yields an issue carrying only those three. IssueErr fails
	// it.
	Issue    *github.Issue
	IssueErr error

	// Relations is what GetIssueRelations returns; nil yields an empty
	// neighbourhood (the issue stands alone). RelationsErr fails it.
	Relations    *github.IssueRelations
	RelationsErr error

	// IssueComments is what ListIssueComments returns (oldest first, as gh
	// does); nil means no comments. IssueCommentsErr fails it.
	IssueComments    []github.IssueComment
	IssueCommentsErr error

	// CommentErr fails AddIssueComment, AddPRComment and PostPRComment.
	CommentErr error

	// BranchRef is what CurrentBranchRef returns; BranchRefErr fails it.
	BranchRef    *github.BranchRef
	BranchRefErr error

	// HeadSHAErr fails HeadSHA, which otherwise returns "sha1", "sha2", ... per
	// call so a test can tell which commit a later step scoped itself to.
	HeadSHAErr error

	// ChangedFiles is what DiffNames returns; ChangedFilesErr fails it.
	ChangedFiles    []string
	ChangedFilesErr error

	// ResumeState and ProgressState are what PrepareResume and
	// CurrentFeatureProgress return; nil means nothing to resume. Their Err
	// fields fail them.
	ResumeState   *github.ResumeState
	ResumeErr     error
	ProgressState *github.ResumeState
	ProgressErr   error

	// PushErr fails PushHead and ForceWithLeasePush.
	PushErr error

	// Checks is the sequence ListChecks answers with, one entry per call,
	// repeating the last entry once exhausted; empty yields no runs. ChecksErr
	// fails it.
	Checks    [][]github.CheckRun
	ChecksErr error

	// Logs is what FailedRunLogs returns; LogsErr fails it.
	Logs    string
	LogsErr error

	// HeadSHAs is the sequence BranchHeadSHA answers with, repeating the last
	// entry once exhausted; empty yields "".
	HeadSHAs []string

	// PullErr fails PullFFOnly.
	PullErr error

	// Threads is what FetchReviewThreads returns; ThreadsErr fails it.
	Threads    []github.ReviewThread
	ThreadsErr error

	// ReplyErr fails AddReviewThreadReply; ResolveErr fails ResolveReviewThread.
	ReplyErr   error
	ResolveErr error

	// MergeBaseSHA is what MergeBase returns.
	MergeBaseSHA string

	// RebaseStates is the sequence StartRebase and RebaseContinue consume in
	// order, repeating the last once exhausted; empty yields a Done state.
	RebaseStates []github.RebaseState
	rebaseIdx    int

	// DefaultHead is what DefaultBranchHEAD returns; empty yields "head-sha".
	DefaultHead string

	// Hooks script an operation completely when set.
	ListChecksFn             func(owner, name, sha string) ([]github.CheckRun, error)
	FailedRunLogsFn          func(owner, name string, runID int64) (string, error)
	PostPRCommentFn          func(owner, repo string, number int, body string) (string, error)
	AddPRCommentFn           func(owner, repo string, number int, body string) (string, error)
	AddReviewThreadReplyFn   func(threadID, body string) (string, error)
	ResolveReviewThreadFn    func(threadID string) error
	FetchReviewCommentFn     func(owner, repo string, number int) (string, bool, error)
	ListAllIssuesFn          func(owner, name string) ([]github.ExistingIssue, error)
	ListAllIssuesWithLimitFn func(owner, name string, limit int) ([]github.ExistingIssue, error)
	AddIssueDependencyFn     func(owner, name string, blockedNumber, blockerNumber int) error
	BlockedByIssuesFn        func(owner, name string, number int) ([]github.Issue, error)
	FetchDiffFn              func(owner, repo string, number int) (string, error)
	OpenImprovementPRFn      func(repo *github.Repo, opts github.ImprovementPROptions) (string, error)
	SearchIssuesFn           func(owner, name, query string) ([]string, error)
	CreateIssueFn            func(owner, name, title, body string) (string, error)
	CreateIssueWithLabelsFn  func(owner, name, title, body string, labels []string) (string, error)
	GetIssueFn               func(owner, name string, number int) (*github.Issue, error)
	ListIssueCommentsFn      func(owner, name string, number int) ([]github.IssueComment, error)
	EditIssueBodyFn          func(owner, name string, number int, body string) error
	CloseIssueFn             func(owner, name string, number int) error
	AddIssueCommentFn        func(owner, name string, number int, body string) (string, error)
	OpenLocalPRFn            func(ref string, opts github.LocalOptions) (*github.PR, error)
	UseLocalRepoFn           func(ref string, opts github.LocalOptions) (*github.Repo, error)
	PullFFOnlyFn             func(dir, branch string) error
	PRMergeabilityFn         func(owner, name string, number int) (*github.Mergeability, error)
	MarkPRReadyFn            func(owner, name string, number int) error
	MergePRFn                func(owner, name string, number int, method, headSHA string) error
	CurrentBranchRefFn       func(dir string) (*github.BranchRef, error)
	HeadSHAFn                func(dir string) (string, error)
	FetchAndCheckoutFn       func(ref string) (*github.PR, error)
	DiffNamesFn              func(dir, baseBranch string) ([]string, error)
	MergeBaseFn              func(dir, ref1, ref2 string) (string, error)
	CommitsInRangeFn         func(dir, rangeExpr string) ([]github.Commit, error)
	FetchBranchFn            func(dir, branch string) error
	StartRebaseFn            func(dir, onto string) (github.RebaseState, error)
	RebaseContinueFn         func(dir string) (github.RebaseState, error)
	RebaseAbortFn            func(dir string) error
	ResetHardFn              func(dir, ref string) error
	ConflictedFilesFn        func(dir string) ([]string, error)
	ForceWithLeasePushFn     func(dir, branch string) error
	PushHeadFn               func(dir, branch string) error
	GetIssueRelationsFn      func(owner, name string, number int) (*github.IssueRelations, error)
	CloneRepoFn              func(ref string) (*github.Repo, error)
	BranchHeadSHAFn          func(owner, repo, branch string) (string, error)
	DefaultBranchHEADFn      func(owner, repo string) (string, error)
	PrepareResumeFn          func(dir string, issueNumber int) (*github.ResumeState, error)
	CurrentFeatureProgressFn func(dir string) (*github.ResumeState, error)
	FetchReviewThreadsFn     func(owner, repo string, number int) ([]github.ReviewThread, error)
	SubmitPRReviewFn         func(owner, repo string, number int, commitSHA, body string, comments []github.ReviewComment) (string, error)
	AddSubIssueFn            func(owner, name string, parentNumber, childNumber int) error
}

// record appends one call and returns its index, so the recorded error can be
// filled in once the operation has run.
func (f *Fake) record(method string, args ...any) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, Call{Method: method, Args: args})
	return len(f.calls) - 1
}

func (f *Fake) setErr(i int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[i].Err = err
}

// Count reports how many times method was called, failures included.
func (f *Fake) Count(method string) int {
	return len(f.Calls(method))
}

// Calls returns the recorded invocations of method, in order.
func (f *Fake) Calls(method string) []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Call
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// succeeded returns the argument at index idx of every successful call of the
// given methods, in call order.
func (f *Fake) succeeded(idx int, methods ...string) []any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []any
	for _, c := range f.calls {
		if c.Err != nil {
			continue
		}
		for _, m := range methods {
			if c.Method == m {
				out = append(out, c.Args[idx])
				break
			}
		}
	}
	return out
}

func strings(vals []any) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		out = append(out, v.(string))
	}
	return out
}

func ints(vals []any) []int {
	out := make([]int, 0, len(vals))
	for _, v := range vals {
		out = append(out, v.(int))
	}
	return out
}

// Comments returns the bodies posted through AddIssueComment, AddPRComment
// and PostPRComment that succeeded, in order.
func (f *Fake) Comments() []string {
	return strings(f.succeeded(3, "AddIssueComment", "AddPRComment", "PostPRComment"))
}

// Replies returns the thread IDs AddReviewThreadReply succeeded on, in order.
func (f *Fake) Replies() []string { return strings(f.succeeded(0, "AddReviewThreadReply")) }

// Resolved returns the thread IDs ResolveReviewThread succeeded on, in order.
func (f *Fake) Resolved() []string { return strings(f.succeeded(0, "ResolveReviewThread")) }

// Readied returns the PR numbers MarkPRReady succeeded on, in order.
func (f *Fake) Readied() []int { return ints(f.succeeded(2, "MarkPRReady")) }

// Closed returns the issue numbers CloseIssue succeeded on, in order.
func (f *Fake) Closed() []int { return ints(f.succeeded(2, "CloseIssue")) }

// Pushed returns the branches PushHead succeeded on, in order.
func (f *Fake) Pushed() []string { return strings(f.succeeded(1, "PushHead")) }

// Edits returns the bodies EditIssueBody succeeded with, in order.
func (f *Fake) Edits() []string { return strings(f.succeeded(3, "EditIssueBody")) }

// Merged returns the successful MergePR invocations, in order.
func (f *Fake) Merged() []MergeCall {
	var out []MergeCall
	for _, c := range f.Calls("MergePR") {
		if c.Err == nil {
			out = append(out, MergeCall{Number: c.Args[2].(int), Method: c.Args[3].(string), HeadSHA: c.Args[4].(string)})
		}
	}
	return out
}

// ImprovementPRs returns the options every successful OpenImprovementPR call
// received, in order.
func (f *Fake) ImprovementPRs() []github.ImprovementPROptions {
	var out []github.ImprovementPROptions
	for _, v := range f.succeeded(1, "OpenImprovementPR") {
		out = append(out, v.(github.ImprovementPROptions))
	}
	return out
}

// pick returns the entry of a per-call sequence for the n-th call (1-based),
// repeating the last entry once the sequence is exhausted.
func pick[T any](seq []T, n int) (T, bool) {
	var zero T
	if len(seq) == 0 {
		return zero, false
	}
	if n > len(seq) {
		n = len(seq)
	}
	return seq[n-1], true
}

// --- checkout -------------------------------------------------------------

func (f *Fake) FetchAndCheckout(ref string) (*github.PR, error) {
	i := f.record("FetchAndCheckout", ref)
	if f.FetchAndCheckoutFn != nil {
		pr, err := f.FetchAndCheckoutFn(ref)
		f.setErr(i, err)
		return pr, err
	}
	pr, err := f.makePR(ref, false)
	f.setErr(i, err)
	return pr, err
}

func (f *Fake) OpenLocalPR(ref string, opts github.LocalOptions) (*github.PR, error) {
	i := f.record("OpenLocalPR", ref, opts)
	if f.OpenLocalPRFn != nil {
		pr, err := f.OpenLocalPRFn(ref, opts)
		f.setErr(i, err)
		return pr, err
	}
	if f.FetchAndCheckoutFn != nil {
		pr, err := f.FetchAndCheckoutFn(ref)
		if err == nil {
			pr.Local = true
		}
		f.setErr(i, err)
		return pr, err
	}
	pr, err := f.makePR(ref, true)
	f.setErr(i, err)
	return pr, err
}

func (f *Fake) makePR(ref string, local bool) (*github.PR, error) {
	if f.FetchErr != nil {
		return nil, f.FetchErr
	}
	pr := f.PR
	if ref != "" {
		owner, repo, number, err := github.ParseRef(ref)
		if err != nil {
			return nil, err
		}
		pr.Owner, pr.Repo, pr.Number = owner, repo, number
	}
	if pr.Dir == "" {
		pr.Dir = f.Dir
	}
	pr.Local = local
	return &pr, nil
}

func (f *Fake) CloneRepo(ref string) (*github.Repo, error) {
	i := f.record("CloneRepo", ref)
	if f.CloneRepoFn != nil {
		repo, err := f.CloneRepoFn(ref)
		f.setErr(i, err)
		return repo, err
	}
	repo, err := f.makeRepo(ref, false)
	f.setErr(i, err)
	return repo, err
}

func (f *Fake) UseLocalRepo(ref string, opts github.LocalOptions) (*github.Repo, error) {
	i := f.record("UseLocalRepo", ref, opts)
	if f.UseLocalRepoFn != nil {
		repo, err := f.UseLocalRepoFn(ref, opts)
		f.setErr(i, err)
		return repo, err
	}
	if f.CloneRepoFn != nil {
		repo, err := f.CloneRepoFn(ref)
		if err == nil {
			repo.Local = true
		}
		f.setErr(i, err)
		return repo, err
	}
	repo, err := f.makeRepo(ref, true)
	f.setErr(i, err)
	return repo, err
}

func (f *Fake) makeRepo(ref string, local bool) (*github.Repo, error) {
	if f.CloneErr != nil {
		return nil, f.CloneErr
	}
	repo := f.Repo
	if ref != "" {
		owner, name, err := github.ParseRepoRef(ref)
		if err != nil {
			return nil, err
		}
		repo.Owner, repo.Name = owner, name
	}
	if repo.Dir == "" {
		repo.Dir = f.Dir
	}
	repo.Local = local
	return &repo, nil
}

func (f *Fake) PullFFOnly(dir, branch string) error {
	i := f.record("PullFFOnly", dir, branch)
	err := f.PullErr
	if f.PullFFOnlyFn != nil {
		err = f.PullFFOnlyFn(dir, branch)
	}
	f.setErr(i, err)
	return err
}

// --- issues ---------------------------------------------------------------

func (f *Fake) GetIssue(owner, name string, number int) (*github.Issue, error) {
	i := f.record("GetIssue", owner, name, number)
	if f.GetIssueFn != nil {
		iss, err := f.GetIssueFn(owner, name, number)
		f.setErr(i, err)
		return iss, err
	}
	if f.IssueErr != nil {
		f.setErr(i, f.IssueErr)
		return nil, f.IssueErr
	}
	iss := github.Issue{}
	if f.Issue != nil {
		iss = *f.Issue
	}
	iss.Owner, iss.Name, iss.Number = owner, name, number
	return &iss, nil
}

func (f *Fake) GetIssueRelations(owner, name string, number int) (*github.IssueRelations, error) {
	i := f.record("GetIssueRelations", owner, name, number)
	if f.GetIssueRelationsFn != nil {
		rel, err := f.GetIssueRelationsFn(owner, name, number)
		f.setErr(i, err)
		return rel, err
	}
	if f.RelationsErr != nil {
		f.setErr(i, f.RelationsErr)
		return nil, f.RelationsErr
	}
	if f.Relations == nil {
		return &github.IssueRelations{}, nil
	}
	return f.Relations, nil
}

func (f *Fake) ListIssueComments(owner, name string, number int) ([]github.IssueComment, error) {
	i := f.record("ListIssueComments", owner, name, number)
	if f.ListIssueCommentsFn != nil {
		cs, err := f.ListIssueCommentsFn(owner, name, number)
		f.setErr(i, err)
		return cs, err
	}
	f.setErr(i, f.IssueCommentsErr)
	if f.IssueCommentsErr != nil {
		return nil, f.IssueCommentsErr
	}
	return f.IssueComments, nil
}

func (f *Fake) EditIssueBody(owner, name string, number int, body string) error {
	i := f.record("EditIssueBody", owner, name, number, body)
	var err error
	if f.EditIssueBodyFn != nil {
		err = f.EditIssueBodyFn(owner, name, number, body)
	}
	f.setErr(i, err)
	return err
}

func (f *Fake) CloseIssue(owner, name string, number int) error {
	i := f.record("CloseIssue", owner, name, number)
	var err error
	if f.CloseIssueFn != nil {
		err = f.CloseIssueFn(owner, name, number)
	}
	f.setErr(i, err)
	return err
}

func (f *Fake) AddIssueComment(owner, name string, number int, body string) (string, error) {
	i := f.record("AddIssueComment", owner, name, number, body)
	if f.AddIssueCommentFn != nil {
		url, err := f.AddIssueCommentFn(owner, name, number, body)
		f.setErr(i, err)
		return url, err
	}
	if f.CommentErr != nil {
		f.setErr(i, f.CommentErr)
		return "", f.CommentErr
	}
	return fmt.Sprintf("https://github.com/%s/%s/issues/%d#issuecomment-1", owner, name, number), nil
}

func (f *Fake) ListAllIssues(owner, name string) ([]github.ExistingIssue, error) {
	i := f.record("ListAllIssues", owner, name)
	if f.ListAllIssuesFn != nil {
		iss, err := f.ListAllIssuesFn(owner, name)
		f.setErr(i, err)
		return iss, err
	}
	return nil, nil
}

func (f *Fake) ListAllIssuesWithLimit(owner, name string, limit int) ([]github.ExistingIssue, error) {
	i := f.record("ListAllIssuesWithLimit", owner, name, limit)
	if f.ListAllIssuesWithLimitFn != nil {
		iss, err := f.ListAllIssuesWithLimitFn(owner, name, limit)
		f.setErr(i, err)
		return iss, err
	}
	return nil, nil
}

func (f *Fake) SearchIssues(owner, name, query string) ([]string, error) {
	i := f.record("SearchIssues", owner, name, query)
	if f.SearchIssuesFn != nil {
		urls, err := f.SearchIssuesFn(owner, name, query)
		f.setErr(i, err)
		return urls, err
	}
	return nil, nil
}

func (f *Fake) CreateIssue(owner, name, title, body string) (string, error) {
	i := f.record("CreateIssue", owner, name, title, body)
	if f.CreateIssueFn != nil {
		url, err := f.CreateIssueFn(owner, name, title, body)
		f.setErr(i, err)
		return url, err
	}
	return fmt.Sprintf("https://github.com/%s/%s/issues/1", owner, name), nil
}

func (f *Fake) CreateIssueWithLabels(owner, name, title, body string, labels []string) (string, error) {
	i := f.record("CreateIssueWithLabels", owner, name, title, body, labels)
	if f.CreateIssueWithLabelsFn != nil {
		url, err := f.CreateIssueWithLabelsFn(owner, name, title, body, labels)
		f.setErr(i, err)
		return url, err
	}
	return fmt.Sprintf("https://github.com/%s/%s/issues/1", owner, name), nil
}

func (f *Fake) AddIssueDependency(owner, name string, blockedNumber, blockerNumber int) error {
	i := f.record("AddIssueDependency", owner, name, blockedNumber, blockerNumber)
	var err error
	if f.AddIssueDependencyFn != nil {
		err = f.AddIssueDependencyFn(owner, name, blockedNumber, blockerNumber)
	}
	f.setErr(i, err)
	return err
}

func (f *Fake) BlockedByIssues(owner, name string, number int) ([]github.Issue, error) {
	i := f.record("BlockedByIssues", owner, name, number)
	if f.BlockedByIssuesFn != nil {
		iss, err := f.BlockedByIssuesFn(owner, name, number)
		f.setErr(i, err)
		return iss, err
	}
	return nil, nil
}

func (f *Fake) AddSubIssue(owner, name string, parentNumber, childNumber int) error {
	i := f.record("AddSubIssue", owner, name, parentNumber, childNumber)
	var err error
	if f.AddSubIssueFn != nil {
		err = f.AddSubIssueFn(owner, name, parentNumber, childNumber)
	}
	f.setErr(i, err)
	return err
}

// --- pull requests --------------------------------------------------------

func (f *Fake) PostPRComment(owner, repo string, number int, body string) (string, error) {
	i := f.record("PostPRComment", owner, repo, number, body)
	if f.PostPRCommentFn != nil {
		url, err := f.PostPRCommentFn(owner, repo, number, body)
		f.setErr(i, err)
		return url, err
	}
	if f.CommentErr != nil {
		f.setErr(i, f.CommentErr)
		return "", f.CommentErr
	}
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d#issuecomment-1", owner, repo, number), nil
}

func (f *Fake) AddPRComment(owner, repo string, number int, body string) (string, error) {
	i := f.record("AddPRComment", owner, repo, number, body)
	if f.AddPRCommentFn != nil {
		url, err := f.AddPRCommentFn(owner, repo, number, body)
		f.setErr(i, err)
		return url, err
	}
	if f.CommentErr != nil {
		f.setErr(i, f.CommentErr)
		return "", f.CommentErr
	}
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d#issuecomment-1", owner, repo, number), nil
}

func (f *Fake) FetchReviewComment(owner, repo string, number int) (string, bool, error) {
	i := f.record("FetchReviewComment", owner, repo, number)
	if f.FetchReviewCommentFn != nil {
		body, found, err := f.FetchReviewCommentFn(owner, repo, number)
		f.setErr(i, err)
		return body, found, err
	}
	return "", false, nil
}

func (f *Fake) FetchDiff(owner, repo string, number int) (string, error) {
	i := f.record("FetchDiff", owner, repo, number)
	if f.FetchDiffFn != nil {
		diff, err := f.FetchDiffFn(owner, repo, number)
		f.setErr(i, err)
		return diff, err
	}
	return "", nil
}

func (f *Fake) SubmitPRReview(owner, repo string, number int, commitSHA, body string, comments []github.ReviewComment) (string, error) {
	i := f.record("SubmitPRReview", owner, repo, number, commitSHA, body, comments)
	if f.SubmitPRReviewFn != nil {
		url, err := f.SubmitPRReviewFn(owner, repo, number, commitSHA, body, comments)
		f.setErr(i, err)
		return url, err
	}
	return "", nil
}

func (f *Fake) FetchReviewThreads(owner, repo string, number int) ([]github.ReviewThread, error) {
	i := f.record("FetchReviewThreads", owner, repo, number)
	if f.FetchReviewThreadsFn != nil {
		ts, err := f.FetchReviewThreadsFn(owner, repo, number)
		f.setErr(i, err)
		return ts, err
	}
	f.setErr(i, f.ThreadsErr)
	if f.ThreadsErr != nil {
		return nil, f.ThreadsErr
	}
	return f.Threads, nil
}

func (f *Fake) AddReviewThreadReply(threadID, body string) (string, error) {
	i := f.record("AddReviewThreadReply", threadID, body)
	if f.AddReviewThreadReplyFn != nil {
		url, err := f.AddReviewThreadReplyFn(threadID, body)
		f.setErr(i, err)
		return url, err
	}
	if f.ReplyErr != nil {
		f.setErr(i, f.ReplyErr)
		return "", f.ReplyErr
	}
	return "https://github.com/o/r/pull/1#discussion_r1", nil
}

func (f *Fake) ResolveReviewThread(threadID string) error {
	i := f.record("ResolveReviewThread", threadID)
	err := f.ResolveErr
	if f.ResolveReviewThreadFn != nil {
		err = f.ResolveReviewThreadFn(threadID)
	}
	f.setErr(i, err)
	return err
}

func (f *Fake) PRMergeability(owner, name string, number int) (*github.Mergeability, error) {
	i := f.record("PRMergeability", owner, name, number)
	if f.PRMergeabilityFn != nil {
		m, err := f.PRMergeabilityFn(owner, name, number)
		f.setErr(i, err)
		return m, err
	}
	return &github.Mergeability{Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN"}, nil
}

func (f *Fake) MarkPRReady(owner, name string, number int) error {
	i := f.record("MarkPRReady", owner, name, number)
	var err error
	if f.MarkPRReadyFn != nil {
		err = f.MarkPRReadyFn(owner, name, number)
	}
	f.setErr(i, err)
	return err
}

func (f *Fake) MergePR(owner, name string, number int, method, headSHA string) error {
	i := f.record("MergePR", owner, name, number, method, headSHA)
	var err error
	if f.MergePRFn != nil {
		err = f.MergePRFn(owner, name, number, method, headSHA)
	}
	f.setErr(i, err)
	return err
}

func (f *Fake) OpenImprovementPR(repo *github.Repo, opts github.ImprovementPROptions) (string, error) {
	i := f.record("OpenImprovementPR", repo, opts)
	if f.OpenImprovementPRFn != nil {
		url, err := f.OpenImprovementPRFn(repo, opts)
		f.setErr(i, err)
		return url, err
	}
	return "https://github.com/o/r/pull/1", nil
}

// --- checks ---------------------------------------------------------------

func (f *Fake) ListChecks(owner, name, sha string) ([]github.CheckRun, error) {
	i := f.record("ListChecks", owner, name, sha)
	if f.ListChecksFn != nil {
		runs, err := f.ListChecksFn(owner, name, sha)
		f.setErr(i, err)
		return runs, err
	}
	if f.ChecksErr != nil {
		f.setErr(i, f.ChecksErr)
		return nil, f.ChecksErr
	}
	runs, _ := pick(f.Checks, f.Count("ListChecks"))
	return runs, nil
}

func (f *Fake) FailedRunLogs(owner, name string, runID int64) (string, error) {
	i := f.record("FailedRunLogs", owner, name, runID)
	if f.FailedRunLogsFn != nil {
		logs, err := f.FailedRunLogsFn(owner, name, runID)
		f.setErr(i, err)
		return logs, err
	}
	f.setErr(i, f.LogsErr)
	return f.Logs, f.LogsErr
}

func (f *Fake) BranchHeadSHA(owner, repo, branch string) (string, error) {
	i := f.record("BranchHeadSHA", owner, repo, branch)
	if f.BranchHeadSHAFn != nil {
		sha, err := f.BranchHeadSHAFn(owner, repo, branch)
		f.setErr(i, err)
		return sha, err
	}
	sha, _ := pick(f.HeadSHAs, f.Count("BranchHeadSHA"))
	return sha, nil
}

func (f *Fake) DefaultBranchHEAD(owner, repo string) (string, error) {
	i := f.record("DefaultBranchHEAD", owner, repo)
	if f.DefaultBranchHEADFn != nil {
		sha, err := f.DefaultBranchHEADFn(owner, repo)
		f.setErr(i, err)
		return sha, err
	}
	if f.DefaultHead != "" {
		return f.DefaultHead, nil
	}
	return "head-sha", nil
}

// --- local git ------------------------------------------------------------

func (f *Fake) CurrentBranchRef(dir string) (*github.BranchRef, error) {
	i := f.record("CurrentBranchRef", dir)
	if f.CurrentBranchRefFn != nil {
		ref, err := f.CurrentBranchRefFn(dir)
		f.setErr(i, err)
		return ref, err
	}
	f.setErr(i, f.BranchRefErr)
	if f.BranchRefErr != nil {
		return nil, f.BranchRefErr
	}
	return f.BranchRef, nil
}

func (f *Fake) HeadSHA(dir string) (string, error) {
	i := f.record("HeadSHA", dir)
	if f.HeadSHAFn != nil {
		sha, err := f.HeadSHAFn(dir)
		f.setErr(i, err)
		return sha, err
	}
	if f.HeadSHAErr != nil {
		f.setErr(i, f.HeadSHAErr)
		return "", f.HeadSHAErr
	}
	return fmt.Sprintf("sha%d", f.Count("HeadSHA")), nil
}

func (f *Fake) DiffNames(dir, baseBranch string) ([]string, error) {
	i := f.record("DiffNames", dir, baseBranch)
	if f.DiffNamesFn != nil {
		files, err := f.DiffNamesFn(dir, baseBranch)
		f.setErr(i, err)
		return files, err
	}
	f.setErr(i, f.ChangedFilesErr)
	if f.ChangedFilesErr != nil {
		return nil, f.ChangedFilesErr
	}
	return f.ChangedFiles, nil
}

func (f *Fake) MergeBase(dir, ref1, ref2 string) (string, error) {
	i := f.record("MergeBase", dir, ref1, ref2)
	if f.MergeBaseFn != nil {
		base, err := f.MergeBaseFn(dir, ref1, ref2)
		f.setErr(i, err)
		return base, err
	}
	return f.MergeBaseSHA, nil
}

func (f *Fake) CommitsInRange(dir, rangeExpr string) ([]github.Commit, error) {
	i := f.record("CommitsInRange", dir, rangeExpr)
	if f.CommitsInRangeFn != nil {
		commits, err := f.CommitsInRangeFn(dir, rangeExpr)
		f.setErr(i, err)
		return commits, err
	}
	return nil, nil
}

func (f *Fake) FetchBranch(dir, branch string) error {
	i := f.record("FetchBranch", dir, branch)
	var err error
	if f.FetchBranchFn != nil {
		err = f.FetchBranchFn(dir, branch)
	}
	f.setErr(i, err)
	return err
}

func (f *Fake) nextRebaseState() github.RebaseState {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rebaseIdx++
	state, ok := pick(f.RebaseStates, f.rebaseIdx)
	if !ok {
		return github.RebaseState{Done: true}
	}
	return state
}

func (f *Fake) StartRebase(dir, onto string) (github.RebaseState, error) {
	i := f.record("StartRebase", dir, onto)
	if f.StartRebaseFn != nil {
		state, err := f.StartRebaseFn(dir, onto)
		f.setErr(i, err)
		return state, err
	}
	return f.nextRebaseState(), nil
}

func (f *Fake) RebaseContinue(dir string) (github.RebaseState, error) {
	i := f.record("RebaseContinue", dir)
	if f.RebaseContinueFn != nil {
		state, err := f.RebaseContinueFn(dir)
		f.setErr(i, err)
		return state, err
	}
	return f.nextRebaseState(), nil
}

func (f *Fake) RebaseAbort(dir string) error {
	i := f.record("RebaseAbort", dir)
	var err error
	if f.RebaseAbortFn != nil {
		err = f.RebaseAbortFn(dir)
	}
	f.setErr(i, err)
	return err
}

func (f *Fake) ResetHard(dir, ref string) error {
	i := f.record("ResetHard", dir, ref)
	var err error
	if f.ResetHardFn != nil {
		err = f.ResetHardFn(dir, ref)
	}
	f.setErr(i, err)
	return err
}

func (f *Fake) ConflictedFiles(dir string) ([]string, error) {
	i := f.record("ConflictedFiles", dir)
	if f.ConflictedFilesFn != nil {
		files, err := f.ConflictedFilesFn(dir)
		f.setErr(i, err)
		return files, err
	}
	return nil, nil
}

func (f *Fake) ForceWithLeasePush(dir, branch string) error {
	i := f.record("ForceWithLeasePush", dir, branch)
	err := f.PushErr
	if f.ForceWithLeasePushFn != nil {
		err = f.ForceWithLeasePushFn(dir, branch)
	}
	f.setErr(i, err)
	return err
}

func (f *Fake) PushHead(dir, branch string) error {
	i := f.record("PushHead", dir, branch)
	err := f.PushErr
	if f.PushHeadFn != nil {
		err = f.PushHeadFn(dir, branch)
	}
	f.setErr(i, err)
	return err
}

func (f *Fake) PrepareResume(dir string, issueNumber int) (*github.ResumeState, error) {
	i := f.record("PrepareResume", dir, issueNumber)
	if f.PrepareResumeFn != nil {
		state, err := f.PrepareResumeFn(dir, issueNumber)
		f.setErr(i, err)
		return state, err
	}
	f.setErr(i, f.ResumeErr)
	return f.ResumeState, f.ResumeErr
}

func (f *Fake) CurrentFeatureProgress(dir string) (*github.ResumeState, error) {
	i := f.record("CurrentFeatureProgress", dir)
	if f.CurrentFeatureProgressFn != nil {
		state, err := f.CurrentFeatureProgressFn(dir)
		f.setErr(i, err)
		return state, err
	}
	f.setErr(i, f.ProgressErr)
	return f.ProgressState, f.ProgressErr
}

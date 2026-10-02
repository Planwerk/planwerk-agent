package githubtest

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/github"
)

// TestFake_CarriesEveryClientMethod locks the contract that makes one fake
// serve every consumer interface: *Fake has each method github.Client has,
// with the same signature.
func TestFake_CarriesEveryClientMethod(t *testing.T) {
	client := reflect.TypeOf(github.Client{})
	fake := reflect.TypeOf(&Fake{})
	for i := 0; i < client.NumMethod(); i++ {
		m := client.Method(i)
		fm, ok := fake.MethodByName(m.Name)
		if !ok {
			t.Errorf("Fake lacks %s", m.Name)
			continue
		}
		// Method(i).Type carries the receiver as the first parameter; compare
		// the rest.
		if got, want := fm.Type.NumIn(), m.Type.NumIn(); got != want {
			t.Errorf("%s: Fake takes %d parameters, Client %d", m.Name, got-1, want-1)
			continue
		}
		for p := 1; p < m.Type.NumIn(); p++ {
			if fm.Type.In(p) != m.Type.In(p) {
				t.Errorf("%s: parameter %d is %v on Fake, %v on Client", m.Name, p, fm.Type.In(p), m.Type.In(p))
			}
		}
		if got, want := fm.Type.NumOut(), m.Type.NumOut(); got != want {
			t.Errorf("%s: Fake returns %d values, Client %d", m.Name, got, want)
			continue
		}
		for r := 0; r < m.Type.NumOut(); r++ {
			if fm.Type.Out(r) != m.Type.Out(r) {
				t.Errorf("%s: result %d is %v on Fake, %v on Client", m.Name, r, fm.Type.Out(r), m.Type.Out(r))
			}
		}
	}
}

func TestFake_CheckoutDefaultsFillFromRef(t *testing.T) {
	f := &Fake{Dir: "/tmp/clone", PR: github.PR{Title: "demo", HeadBranch: "feat/x"}}
	pr, err := f.FetchAndCheckout("acme/widgets#7")
	if err != nil {
		t.Fatalf("FetchAndCheckout: %v", err)
	}
	if pr.Owner != "acme" || pr.Repo != "widgets" || pr.Number != 7 || pr.Dir != "/tmp/clone" || pr.Local || pr.Title != "demo" {
		t.Errorf("FetchAndCheckout = %+v", pr)
	}
	local, err := f.OpenLocalPR("", github.LocalOptions{})
	if err != nil {
		t.Fatalf("OpenLocalPR: %v", err)
	}
	if !local.Local || local.HeadBranch != "feat/x" {
		t.Errorf("OpenLocalPR = %+v", local)
	}
	repo, err := f.UseLocalRepo("acme/widgets", github.LocalOptions{})
	if err != nil {
		t.Fatalf("UseLocalRepo: %v", err)
	}
	if repo.Owner != "acme" || repo.Name != "widgets" || repo.Dir != "/tmp/clone" || !repo.Local {
		t.Errorf("UseLocalRepo = %+v", repo)
	}
	if f.Count("FetchAndCheckout") != 1 || f.Count("OpenLocalPR") != 1 || f.Count("UseLocalRepo") != 1 {
		t.Errorf("counts: fetch=%d local=%d use=%d", f.Count("FetchAndCheckout"), f.Count("OpenLocalPR"), f.Count("UseLocalRepo"))
	}
}

func TestFake_RecordsSuccessfulPostsOnly(t *testing.T) {
	f := &Fake{}
	if _, err := f.AddIssueComment("o", "r", 1, "first"); err != nil {
		t.Fatal(err)
	}
	f.CommentErr = errors.New("down")
	if _, err := f.AddPRComment("o", "r", 1, "second"); err == nil {
		t.Fatal("want the scripted error")
	}
	if got := f.Comments(); len(got) != 1 || got[0] != "first" {
		t.Errorf("Comments() = %v, want [first]", got)
	}
	if f.Count("AddPRComment") != 1 {
		t.Errorf("failed calls must still count, got %d", f.Count("AddPRComment"))
	}
}

func TestFake_SequencesRepeatTheirLastEntry(t *testing.T) {
	f := &Fake{HeadSHAs: []string{"a", "b"}, RebaseStates: []github.RebaseState{{Conflicted: true}}}
	var got []string
	for range 3 {
		sha, _ := f.BranchHeadSHA("o", "r", "main")
		got = append(got, sha)
	}
	if got[0] != "a" || got[1] != "b" || got[2] != "b" {
		t.Errorf("BranchHeadSHA sequence = %v", got)
	}
	if s, _ := f.StartRebase("d", "main"); !s.Conflicted {
		t.Error("first rebase state should be the scripted one")
	}
	if s, _ := f.RebaseContinue("d"); !s.Conflicted {
		t.Error("an exhausted sequence repeats its last state")
	}
	if s, _ := (&Fake{}).StartRebase("d", "main"); !s.Done {
		t.Error("no scripted states means the rebase is done")
	}
}

func TestFake_HooksWinOverDefaults(t *testing.T) {
	f := &Fake{Issue: &github.Issue{Title: "default"}}
	f.GetIssueFn = func(_, _ string, _ int) (*github.Issue, error) { return nil, errors.New("scripted") }
	if _, err := f.GetIssue("o", "r", 1); err == nil || err.Error() != "scripted" {
		t.Errorf("hook not used: %v", err)
	}
	f.GetIssueFn = nil
	iss, err := f.GetIssue("o", "r", 2)
	if err != nil || iss.Title != "default" || iss.Number != 2 || iss.Owner != "o" {
		t.Errorf("default issue = %+v, %v", iss, err)
	}
}

func TestFake_HistoryReadersAnswerFromTheirFields(t *testing.T) {
	f := &Fake{
		History:        []github.HistoryCommit{{SHA: "c1"}},
		MergedPRsErr:   errors.New("rate limited"),
		CommitMessages: map[string]string{"c1": "Subject"},
	}
	if commits, err := f.DefaultBranchHistory("o", "r"); err != nil || len(commits) != 1 || commits[0].SHA != "c1" {
		t.Errorf("DefaultBranchHistory = %+v, %v", commits, err)
	}
	if _, err := f.ListMergedPRs("o", "r"); err == nil || err.Error() != "rate limited" {
		t.Errorf("ListMergedPRs err = %v, want the scripted error", err)
	}
	if issues, err := f.ListClosedIssues("o", "r"); err != nil || issues != nil {
		t.Errorf("ListClosedIssues = %+v, %v, want an empty listing", issues, err)
	}
	if msg, err := f.CommitMessage("d", "c1"); err != nil || msg != "Subject" {
		t.Errorf("CommitMessage = %q, %v", msg, err)
	}
	if msg, err := f.CommitMessage("d", "unknown"); err != nil || msg != "" {
		t.Errorf("CommitMessage of an unscripted SHA = %q, %v, want \"\"", msg, err)
	}
	f.CommitMessageFn = func(_, sha string) (string, error) { return "", errors.New("no commit " + sha) }
	if _, err := f.CommitMessage("d", "c1"); err == nil || err.Error() != "no commit c1" {
		t.Errorf("hook not used: %v", err)
	}
	if f.Count("CommitMessage") != 3 || f.Calls("ListMergedPRs")[0].Err == nil {
		t.Errorf("calls not recorded: %d CommitMessage calls, ListMergedPRs err %v", f.Count("CommitMessage"), f.Calls("ListMergedPRs")[0].Err)
	}
}

func TestFake_ItemReadersAnswerFromTheirFields(t *testing.T) {
	f := &Fake{
		UpdatedItems: []github.UpdatedItem{{Number: 7, UpdatedAt: "2026-03-01T10:00:00Z"}},
		Items:        map[int]*github.Item{9: {Kind: github.ItemKindPull, Title: "Add a flag"}},
		History:      []github.HistoryCommit{{SHA: "c1"}, {SHA: "c2"}, {SHA: "c3"}},
	}

	// The scripted listing is returned whatever since is.
	if items, err := f.ListUpdatedItems("o", "r", "2026-01-01T00:00:00Z"); err != nil || len(items) != 1 || items[0].Number != 7 {
		t.Errorf("ListUpdatedItems = %+v, %v", items, err)
	}
	if it, err := f.GetItem("o", "r", 9); err != nil || it.Number != 9 || it.Kind != github.ItemKindPull || it.Title != "Add a flag" {
		t.Errorf("GetItem = %+v, %v", it, err)
	}
	// A number without a template is an issue carrying only the number.
	if it, err := f.GetItem("o", "r", 4); err != nil || it.Number != 4 || it.Kind != github.ItemKindIssue || it.Title != "" {
		t.Errorf("GetItem of an unscripted number = %+v, %v", it, err)
	}
	if f.Items[9].Number != 0 {
		t.Error("GetItem must return a copy and leave the template alone")
	}
	// One page holds the whole history, newest first, until a page size is set.
	var pages [][]string
	collect := func(stopAt string) func(page []github.HistoryCommit, total int) bool {
		pages = nil
		return func(page []github.HistoryCommit, total int) bool {
			if total != 3 {
				t.Errorf("total = %d, want the 3 commits of History", total)
			}
			var shas []string
			for _, c := range page {
				shas = append(shas, c.SHA)
			}
			pages = append(pages, shas)
			return slices.Contains(shas, stopAt)
		}
	}
	if commits, err := f.DefaultBranchHistoryUntil("o", "r", collect("")); err != nil || len(commits) != 3 || commits[0].SHA != "c1" ||
		!slices.EqualFunc(pages, [][]string{{"c3", "c2", "c1"}}, slices.Equal[[]string]) {
		t.Errorf("DefaultBranchHistoryUntil = %+v, %v over the pages %v, want the whole history in one page", commits, err, pages)
	}
	f.HistoryPageSize = 2
	if commits, err := f.DefaultBranchHistoryUntil("o", "r", collect("")); err != nil || len(commits) != 3 || commits[0].SHA != "c1" ||
		!slices.EqualFunc(pages, [][]string{{"c3", "c2"}, {"c1"}}, slices.Equal[[]string]) {
		t.Errorf("DefaultBranchHistoryUntil = %+v, %v over the pages %v, want the whole history in two pages", commits, err, pages)
	}
	// The read ends at the page done accepts and returns the pages it read.
	if commits, err := f.DefaultBranchHistoryUntil("o", "r", collect("c2")); err != nil || len(commits) != 2 || commits[0].SHA != "c2" || len(pages) != 1 {
		t.Errorf("DefaultBranchHistoryUntil = %+v, %v over the pages %v, want c2 and c3 from the first page", commits, err, pages)
	}

	f.UpdatedItemsErr = errors.New("rate limited")
	f.ItemErr = errors.New("gone")
	f.HistoryErr = errors.New("down")
	if _, err := f.ListUpdatedItems("o", "r", ""); err == nil || err.Error() != "rate limited" {
		t.Errorf("ListUpdatedItems err = %v, want the scripted error", err)
	}
	if _, err := f.GetItem("o", "r", 9); err == nil || err.Error() != "gone" {
		t.Errorf("GetItem err = %v, want the scripted error", err)
	}
	if _, err := f.DefaultBranchHistoryUntil("o", "r", collect("")); err == nil || err.Error() != "down" || len(pages) != 0 {
		t.Errorf("DefaultBranchHistoryUntil err = %v after %d pages, want the scripted error and no page", err, len(pages))
	}

	f.ListUpdatedItemsFn = func(_, _, since string) ([]github.UpdatedItem, error) {
		return []github.UpdatedItem{{Number: 1, UpdatedAt: since}}, nil
	}
	f.GetItemFn = func(_, _ string, number int) (*github.Item, error) {
		return &github.Item{Number: number, Title: "hook"}, nil
	}
	f.DefaultBranchHistoryUntilFn = func(_, name string, _ func([]github.HistoryCommit, int) bool) ([]github.HistoryCommit, error) {
		return []github.HistoryCommit{{SHA: "head-of-" + name}}, nil
	}
	if items, err := f.ListUpdatedItems("o", "r", "t0"); err != nil || len(items) != 1 || items[0].UpdatedAt != "t0" {
		t.Errorf("ListUpdatedItems hook not used: %+v, %v", items, err)
	}
	if it, err := f.GetItem("o", "r", 5); err != nil || it.Title != "hook" {
		t.Errorf("GetItem hook not used: %+v, %v", it, err)
	}
	if commits, err := f.DefaultBranchHistoryUntil("o", "r", collect("")); err != nil || len(commits) != 1 || commits[0].SHA != "head-of-r" {
		t.Errorf("DefaultBranchHistoryUntil hook not used: %+v, %v", commits, err)
	}

	if f.Count("ListUpdatedItems") != 3 || f.Count("GetItem") != 4 || f.Count("DefaultBranchHistoryUntil") != 5 {
		t.Errorf("calls not recorded: %d listings, %d items, %d histories", f.Count("ListUpdatedItems"), f.Count("GetItem"), f.Count("DefaultBranchHistoryUntil"))
	}
	if got := f.Calls("GetItem")[0].Args; len(got) != 3 || got[2] != 9 {
		t.Errorf("GetItem args = %v, want owner, name, and the number", got)
	}
	if f.Calls("ListUpdatedItems")[1].Err == nil {
		t.Error("a failed call must record its error")
	}
}

func TestFake_ThreadReadersFillTheNumber(t *testing.T) {
	f := &Fake{
		IssueThread: &github.IssueThread{Title: "issue"},
		Threads:     []github.ReviewThread{{ID: "RT_1"}},
	}
	iss, err := f.GetIssueThread("o", "r", 4)
	if err != nil || iss.Number != 4 || iss.Title != "issue" {
		t.Errorf("GetIssueThread = %+v, %v", iss, err)
	}
	// No template: the pull request carries its number and the scripted
	// review threads.
	pr, err := f.GetPRThread("o", "r", 5)
	if err != nil || pr.Number != 5 || len(pr.ReviewThreads) != 1 {
		t.Errorf("GetPRThread = %+v, %v", pr, err)
	}
	f.PRThreadErr = errors.New("gone")
	if _, err := f.GetPRThread("o", "r", 5); err == nil || err.Error() != "gone" {
		t.Errorf("GetPRThread err = %v, want the scripted error", err)
	}
	f.GetIssueThreadFn = func(_, _ string, _ int) (*github.IssueThread, error) { return nil, errors.New("scripted") }
	if _, err := f.GetIssueThread("o", "r", 4); err == nil || err.Error() != "scripted" {
		t.Errorf("hook not used: %v", err)
	}
}

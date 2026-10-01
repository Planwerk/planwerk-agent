package githubtest

import (
	"errors"
	"reflect"
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

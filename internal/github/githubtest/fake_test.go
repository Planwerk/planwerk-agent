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

package draft

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-review/internal/github"
)

type fakeClaude struct {
	questions      []string
	questionsErr   error
	questionsCalls int
	draftFn        func(ctx Context) (*Result, error)
	draftCalls     int
	lastCtx        Context
}

func (f *fakeClaude) GenerateQuestions(seed string) ([]string, error) {
	f.questionsCalls++
	return f.questions, f.questionsErr
}

func (f *fakeClaude) Draft(ctx Context) (*Result, error) {
	f.draftCalls++
	f.lastCtx = ctx
	if f.draftFn != nil {
		return f.draftFn(ctx)
	}
	return &Result{Title: "Drafted Title", Description: "desc", Motivation: "motiv", Scope: "Medium"}, nil
}

type recorder struct {
	createCalls  int
	createOwner  string
	createName   string
	createTitle  string
	createBody   string
	createLabels []string
	searchCalls  int
}

// fakeCapturer is a deterministic Capturer: it returns canned answers in order
// and records each prompt. It mirrors the real composer by rendering the prompt
// to out (stderr in production) so prompt-channel assertions hold and stdout
// stays clean. An exhausted answer list yields "" (an empty submission).
type fakeCapturer struct {
	answers []string
	calls   int
	prompts []string
}

func (f *fakeCapturer) Capture(prompt string, _ io.Reader, out io.Writer) (string, error) {
	f.prompts = append(f.prompts, prompt)
	_, _ = io.WriteString(out, prompt+"\n")
	ans := ""
	if f.calls < len(f.answers) {
		ans = f.answers[f.calls]
	}
	f.calls++
	return ans, nil
}

// newTestRunner builds a Runner with deterministic fakes. searchMatches is what
// the duplicate searcher returns; in (the reader contents) feeds the Q&A loop
// and the create confirmation.
func newTestRunner(cl ClaudeDrafter, in string, isTTY bool, rec *recorder, searchMatches []string) *Runner {
	return &Runner{
		Claude: cl,
		Create: func(owner, name, title, body string, labels []string) (string, error) {
			rec.createCalls++
			rec.createOwner, rec.createName = owner, name
			rec.createTitle, rec.createBody, rec.createLabels = title, body, labels
			return "https://github.com/acme/widgets/issues/1", nil
		},
		Search: func(owner, name, query string) ([]string, error) {
			rec.searchCalls++
			return searchMatches, nil
		},
		DetectOrigin:    func() (string, string, error) { return "acme", "widgets", nil },
		BuildPrompt:     func(ctx Context) string { return "DRAFT PROMPT seed=" + ctx.Seed },
		BuildBarePrompt: func(seed string) string { return "BARE PROMPT seed=" + seed },
		In:              strings.NewReader(in),
		Prompt:          io.Discard,
		IsTTY:           func() bool { return isTTY },
		// Default to an empty composer: interactive tests override it with
		// canned answers; non-interactive / non-TTY tests never reach it.
		Capture: &fakeCapturer{},
	}
}

func baseOpts() Options {
	return Options{RepoRef: "acme/widgets", Seed: "add a dark mode toggle", Format: "markdown", Version: "test"}
}

func TestRun_InteractiveQA_DraftsAndCreates(t *testing.T) {
	cl := &fakeClaude{questions: []string{"Who benefits?", "Any hard constraints?"}}
	rec := &recorder{}
	// On a TTY the Q&A answers come from the composer; stdin carries only the
	// "y" for the create confirmation.
	r := newTestRunner(cl, "y\n", true, rec, nil)
	fc := &fakeCapturer{answers: []string{"end users", "none"}}
	r.Capture = fc

	var out bytes.Buffer
	if err := r.Run(&out, baseOpts()); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if cl.questionsCalls != 1 {
		t.Errorf("GenerateQuestions calls = %d, want 1", cl.questionsCalls)
	}
	if fc.calls != 2 {
		t.Errorf("composer calls = %d, want 2 (one per question)", fc.calls)
	}
	if len(cl.lastCtx.Answers) != 2 {
		t.Fatalf("Draft answers = %d, want 2: %+v", len(cl.lastCtx.Answers), cl.lastCtx.Answers)
	}
	if cl.lastCtx.Answers[0].Question != "Who benefits?" || cl.lastCtx.Answers[0].Answer != "end users" {
		t.Errorf("first Q&A = %+v, want Who benefits?/end users", cl.lastCtx.Answers[0])
	}
	if rec.createCalls != 1 {
		t.Fatalf("create calls = %d, want 1", rec.createCalls)
	}
	if !strings.Contains(rec.createBody, "## Description") {
		t.Errorf("created body missing Description section:\n%s", rec.createBody)
	}
}

func TestRun_NoInteractive_SkipsQuestions(t *testing.T) {
	cl := &fakeClaude{questions: []string{"should not be asked"}}
	rec := &recorder{}
	r := newTestRunner(cl, "", false, rec, nil)

	opts := baseOpts()
	opts.NoInteractive = true
	opts.DryRun = true // avoid the create confirmation read

	if err := r.Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if cl.questionsCalls != 0 {
		t.Errorf("GenerateQuestions calls = %d, want 0 with --no-interactive", cl.questionsCalls)
	}
	if cl.draftCalls != 1 {
		t.Errorf("Draft calls = %d, want 1", cl.draftCalls)
	}
	if len(cl.lastCtx.Answers) != 0 {
		t.Errorf("Draft answers = %d, want 0", len(cl.lastCtx.Answers))
	}
}

func TestRun_DryRun_FilesNothing(t *testing.T) {
	cl := &fakeClaude{}
	rec := &recorder{}
	r := newTestRunner(cl, "", true, rec, nil)

	opts := baseOpts()
	opts.NoInteractive = true
	opts.DryRun = true

	var out bytes.Buffer
	if err := r.Run(&out, opts); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if rec.createCalls != 0 {
		t.Errorf("create calls = %d, want 0 in dry-run", rec.createCalls)
	}
	got := out.String()
	for _, want := range []string{"Draft issue for acme/widgets", "## Description", "## Motivation"} {
		if !strings.Contains(got, want) {
			t.Errorf("dry-run output missing %q:\n%s", want, got)
		}
	}
}

func TestRun_FormatJSON_EmitsValidJSON(t *testing.T) {
	cl := &fakeClaude{}
	rec := &recorder{}
	r := newTestRunner(cl, "", true, rec, nil)

	opts := baseOpts()
	opts.NoInteractive = true
	opts.Format = formatJSON

	var out bytes.Buffer
	if err := r.Run(&out, opts); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if rec.createCalls != 0 {
		t.Errorf("create calls = %d, want 0 for --format json", rec.createCalls)
	}
	var result Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("output is not valid Result JSON: %v\n%s", err, out.String())
	}
	if result.Title != "Drafted Title" || !strings.Contains(result.Body, "## Description") {
		t.Errorf("decoded result missing fields: %+v", result)
	}
}

func TestRun_FormatJSON_PromptsDoNotPolluteOutput(t *testing.T) {
	cl := &fakeClaude{questions: []string{"Who benefits?"}}
	rec := &recorder{}
	r := newTestRunner(cl, "", true, rec, nil)
	// Capture the interactive prompts separately from the JSON output. The
	// composer renders to this writer (stderr in production), never to stdout.
	var prompts bytes.Buffer
	r.Prompt = &prompts
	r.Capture = &fakeCapturer{answers: []string{"end users"}}

	opts := baseOpts()
	opts.Format = formatJSON // interactive Q&A still runs

	var out bytes.Buffer
	if err := r.Run(&out, opts); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	var result Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not clean JSON (Q&A prompts leaked?): %v\n%s", err, out.String())
	}
	if !strings.Contains(prompts.String(), "Who benefits?") {
		t.Errorf("Q&A prompt should go to the prompt writer, got:\n%s", prompts.String())
	}
}

func TestRun_Local_InfersOrigin(t *testing.T) {
	cl := &fakeClaude{}
	rec := &recorder{}
	r := newTestRunner(cl, "y\n", true, rec, nil)

	opts := baseOpts()
	opts.RepoRef = "" // no explicit ref under --local
	opts.Local = true
	opts.NoInteractive = true

	if err := r.Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if rec.createCalls != 1 {
		t.Fatalf("create calls = %d, want 1", rec.createCalls)
	}
	if rec.createOwner != "acme" || rec.createName != "widgets" {
		t.Errorf("create target = %s/%s, want acme/widgets", rec.createOwner, rec.createName)
	}
}

func TestRun_Local_RefMismatchAborts(t *testing.T) {
	cl := &fakeClaude{}
	rec := &recorder{}
	r := newTestRunner(cl, "", true, rec, nil)

	opts := baseOpts()
	opts.RepoRef = "other/repo" // disagrees with origin acme/widgets
	opts.Local = true

	err := r.Run(&bytes.Buffer{}, opts)
	if err == nil || !errors.Is(err, github.ErrOriginMismatch) {
		t.Fatalf("expected ErrOriginMismatch, got %v", err)
	}
	if cl.draftCalls != 0 || rec.createCalls != 0 {
		t.Errorf("nothing should be drafted/created on mismatch")
	}
}

func TestRun_NonLocal_EmptyRefErrors(t *testing.T) {
	cl := &fakeClaude{}
	rec := &recorder{}
	r := newTestRunner(cl, "", true, rec, nil)

	opts := baseOpts()
	opts.RepoRef = ""

	if err := r.Run(&bytes.Buffer{}, opts); err == nil {
		t.Fatal("expected an error for an empty non-local repo ref")
	}
	if cl.draftCalls != 0 {
		t.Errorf("Draft must not run when the repo ref is invalid")
	}
}

func TestRun_DuplicateWarning_Confirms(t *testing.T) {
	cl := &fakeClaude{}
	rec := &recorder{}
	// "y" to create, then "y" to the duplicate confirmation.
	r := newTestRunner(cl, "y\ny\n", true, rec, []string{"Existing dark mode issue\thttps://example/1"})

	opts := baseOpts()
	opts.NoInteractive = true

	var out bytes.Buffer
	if err := r.Run(&out, opts); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if rec.searchCalls != 1 {
		t.Errorf("search calls = %d, want 1", rec.searchCalls)
	}
	if !strings.Contains(out.String(), "Possible duplicate") {
		t.Errorf("expected a duplicate warning, got:\n%s", out.String())
	}
	if rec.createCalls != 1 {
		t.Errorf("create calls = %d, want 1 after confirming through the duplicate warning", rec.createCalls)
	}
}

func TestRun_Quit_NoCreate(t *testing.T) {
	cl := &fakeClaude{}
	rec := &recorder{}
	r := newTestRunner(cl, "q\n", true, rec, nil)

	opts := baseOpts()
	opts.NoInteractive = true

	var out bytes.Buffer
	if err := r.Run(&out, opts); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if rec.createCalls != 0 {
		t.Errorf("create calls = %d, want 0 after quitting", rec.createCalls)
	}
	if !strings.Contains(out.String(), "Aborted") {
		t.Errorf("expected an abort message, got:\n%s", out.String())
	}
}

func TestRun_NonTTY_NoSeed_Aborts(t *testing.T) {
	cl := &fakeClaude{}
	rec := &recorder{}
	r := newTestRunner(cl, "", false, rec, nil)

	opts := baseOpts()
	opts.Seed = ""

	err := r.Run(&bytes.Buffer{}, opts)
	if err == nil || !strings.Contains(err.Error(), "stdin is not a TTY") {
		t.Fatalf("expected a non-TTY abort, got %v", err)
	}
	if cl.draftCalls != 0 || rec.createCalls != 0 {
		t.Errorf("nothing should run when the seed cannot be captured")
	}
}

func TestRun_NoInteractive_NoSeed_Aborts(t *testing.T) {
	cl := &fakeClaude{}
	rec := &recorder{}
	r := newTestRunner(cl, "", true, rec, nil)

	opts := baseOpts()
	opts.Seed = ""
	opts.NoInteractive = true

	err := r.Run(&bytes.Buffer{}, opts)
	if err == nil || !strings.Contains(err.Error(), "--no-interactive") {
		t.Fatalf("expected a --no-interactive abort, got %v", err)
	}
	if cl.draftCalls != 0 {
		t.Errorf("Draft must not run without a seed")
	}
}

func TestRun_InteractiveSeed_UsesComposer(t *testing.T) {
	cl := &fakeClaude{} // no questions, so only the seed is captured
	rec := &recorder{}
	r := newTestRunner(cl, "", true, rec, nil)
	fc := &fakeCapturer{answers: []string{"a multi\nline idea"}}
	r.Capture = fc

	opts := baseOpts()
	opts.Seed = ""     // force interactive capture
	opts.DryRun = true // render without filing, no confirmation read

	if err := r.Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if fc.calls != 1 {
		t.Fatalf("composer calls = %d, want 1 (seed)", fc.calls)
	}
	if cl.lastCtx.Seed != "a multi\nline idea" {
		t.Errorf("drafted seed = %q, want the multi-line composer value", cl.lastCtx.Seed)
	}
}

func TestRun_InteractiveSeed_EmptyAborts(t *testing.T) {
	cl := &fakeClaude{}
	rec := &recorder{}
	r := newTestRunner(cl, "", true, rec, nil)
	// The composer trims to empty (an empty submission), which must abort just
	// like the single-line path does.
	r.Capture = &fakeCapturer{answers: []string{""}}

	opts := baseOpts()
	opts.Seed = ""

	err := r.Run(&bytes.Buffer{}, opts)
	if err == nil || !strings.Contains(err.Error(), "no idea provided") {
		t.Fatalf("expected a no-idea-provided abort, got %v", err)
	}
	if cl.draftCalls != 0 {
		t.Errorf("Draft must not run when the composer yields no idea")
	}
}

func TestRun_NonTTY_SkipsComposer(t *testing.T) {
	cl := &fakeClaude{questions: []string{"Who benefits?", "Any constraints?"}}
	rec := &recorder{}
	// Not a TTY, interactive (no --no-interactive): the Q&A must read lines from
	// stdin and the composer must never engage.
	r := newTestRunner(cl, "answer1\nanswer2\ny\n", false, rec, nil)
	fc := &fakeCapturer{answers: []string{"SENTINEL — must not be used"}}
	r.Capture = fc

	var out bytes.Buffer
	if err := r.Run(&out, baseOpts()); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if fc.calls != 0 {
		t.Fatalf("composer engaged on a non-TTY (calls = %d), want 0", fc.calls)
	}
	if len(cl.lastCtx.Answers) != 2 ||
		cl.lastCtx.Answers[0].Answer != "answer1" || cl.lastCtx.Answers[1].Answer != "answer2" {
		t.Errorf("answers = %+v, want the piped answer1/answer2", cl.lastCtx.Answers)
	}
	if rec.createCalls != 1 {
		t.Errorf("create calls = %d, want 1", rec.createCalls)
	}
}

func TestRun_PrintPrompt(t *testing.T) {
	cl := &fakeClaude{}
	rec := &recorder{}
	r := newTestRunner(cl, "", false, rec, nil)

	opts := baseOpts()
	opts.PrintPrompt = true

	var out bytes.Buffer
	if err := r.Run(&out, opts); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if out.String() != "DRAFT PROMPT seed=add a dark mode toggle" {
		t.Errorf("unexpected prompt output: %q", out.String())
	}
	if cl.draftCalls != 0 || cl.questionsCalls != 0 || rec.createCalls != 0 || rec.searchCalls != 0 {
		t.Errorf("print mode must not touch Claude or GitHub")
	}
}

func TestRun_PrintBarePrompt(t *testing.T) {
	cl := &fakeClaude{}
	rec := &recorder{}
	r := newTestRunner(cl, "", false, rec, nil)

	opts := baseOpts()
	opts.PrintBarePrompt = true

	var out bytes.Buffer
	if err := r.Run(&out, opts); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if out.String() != "BARE PROMPT seed=add a dark mode toggle" {
		t.Errorf("unexpected bare prompt output: %q", out.String())
	}
}

func TestRun_PrintPrompt_NoSeedErrors(t *testing.T) {
	cl := &fakeClaude{}
	rec := &recorder{}
	r := newTestRunner(cl, "", false, rec, nil)

	opts := baseOpts()
	opts.Seed = ""
	opts.PrintPrompt = true

	if err := r.Run(&bytes.Buffer{}, opts); err == nil {
		t.Fatal("expected an error when print mode has no idea")
	}
}

package draft

// QA is a single clarifying question and the user's answer collected during
// the interactive draft loop.
type QA struct {
	Question string
	Answer   string
}

// Context is the input for the Claude draft prompt: the one-line seed idea and
// the answers gathered in the clarifying Q&A loop. Answers is empty when the
// loop is skipped (--no-interactive).
type Context struct {
	Seed    string
	Answers []QA
}

// ClaudeDrafter drives the two Claude calls the draft pipeline needs: one to
// generate clarifying questions from the seed, one to draft the issue from the
// seed plus answers. The draft package depends on this interface so tests can
// inject a deterministic fake.
type ClaudeDrafter interface {
	GenerateQuestions(seed string) ([]string, error)
	Draft(ctx Context) (*Result, error)
}

// QuestionsFn is the bare-function form of the question generator the CLI
// passes in. It is adapted to ClaudeDrafter via claudeFnAdapter.
type QuestionsFn func(seed string) ([]string, error)

// DraftFn is the bare-function form of the issue drafter.
type DraftFn func(ctx Context) (*Result, error)

// PromptBuildFn renders the draft prompt for a context without invoking Claude
// (--print-prompt mode).
type PromptBuildFn func(ctx Context) string

// BarePromptBuildFn renders a portable, self-contained draft prompt from the
// seed alone (--print-bare-prompt mode).
type BarePromptBuildFn func(seed string) string

// claudeFnAdapter adapts a QuestionsFn + DraftFn pair to ClaudeDrafter.
type claudeFnAdapter struct {
	questions QuestionsFn
	draft     DraftFn
}

func (a claudeFnAdapter) GenerateQuestions(seed string) ([]string, error) {
	return a.questions(seed)
}

func (a claudeFnAdapter) Draft(ctx Context) (*Result, error) {
	return a.draft(ctx)
}

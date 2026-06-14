// Package inputeditor provides the draft subcommand's interactive multi-line
// capture component: a Claude-Code-style inline composer built on bubbletea
// and bubbles/textarea, with a Ctrl-E escape hatch to $VISUAL/$EDITOR/vi.
//
// It renders to the writer it is given (stderr in production) so the captured
// text never pollutes stdout, and it engages only on an interactive terminal —
// the draft runner gates on stdin and stderr both being TTYs before calling it.
package inputeditor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ErrCanceled is returned by Capture when the user cancels the composer
// (Ctrl-C or Esc) instead of submitting.
var ErrCanceled = errors.New("composer canceled")

// composerHint is the footer line describing the active keys.
const composerHint = "enter: newline · ctrl+d: submit · ctrl+e: $EDITOR · ctrl+c: cancel"

// composer layout bounds: the textarea width tracks the terminal but is capped
// so a wide window does not produce an unwieldy line length.
const (
	maxComposerWidth = 80
	minComposerWidth = 20
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true)
	boxStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	hintStyle   = lipgloss.NewStyle().Faint(true)
	statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

// Editor is the production capture component. It satisfies the draft.Capturer
// interface so the draft runner can inject a fake in tests.
type Editor struct{}

// New returns a ready-to-use Editor.
func New() *Editor { return &Editor{} }

// Capture opens the multi-line composer titled with prompt, reading key input
// from in and rendering to out. It returns the trimmed buffer when the user
// submits (Ctrl-D), or ErrCanceled when the user cancels (Ctrl-C/Esc).
func (e *Editor) Capture(prompt string, in io.Reader, out io.Writer) (string, error) {
	final, err := tea.NewProgram(newModel(prompt), tea.WithInput(in), tea.WithOutput(out)).Run()
	if err != nil {
		return "", fmt.Errorf("running composer: %w", err)
	}
	fm, ok := final.(model)
	if !ok {
		return "", fmt.Errorf("composer returned unexpected model %T", final)
	}
	return captureResult(fm)
}

// captureResult maps a finished composer model onto the Capture contract:
// ErrCanceled when canceled, otherwise the trimmed buffer.
func captureResult(m model) (string, error) {
	if m.canceled {
		return "", ErrCanceled
	}
	return strings.TrimSpace(m.textarea.Value()), nil
}

// editorFinishedMsg reports the outcome of the $EDITOR handoff back to the
// model: the new buffer contents on success, or err when the editor could not
// run or its file could not be read.
type editorFinishedMsg struct {
	content string
	err     error
}

// model is the bubbletea model wrapping the textarea and the editor handoff.
type model struct {
	prompt   string
	textarea textarea.Model
	canceled bool
	status   string
}

// newModel builds a focused composer model titled with prompt.
func newModel(prompt string) model {
	ta := textarea.New()
	ta.Placeholder = "Describe your idea. Enter starts a new line."
	ta.ShowLineNumbers = false
	ta.Focus()
	return model{prompt: prompt, textarea: ta}
}

// Init starts the cursor blink.
func (m model) Init() tea.Cmd { return textarea.Blink }

// Update handles the composer's own keys (submit, cancel, editor handoff) and
// the editor-finished message, delegating everything else to the textarea.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// These keys are intercepted before the textarea sees them, so the
		// textarea's default ctrl+d (delete) and ctrl+e (line end) bindings do
		// not fire.
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEscape:
			m.canceled = true
			return m, tea.Quit
		case tea.KeyCtrlD:
			return m, tea.Quit
		case tea.KeyCtrlE:
			return m, m.openEditor()
		}
	case editorFinishedMsg:
		if msg.err != nil {
			// Keep the buffer the user already typed; surface the failure on a
			// status line rather than aborting the whole draft.
			m.status = "editor failed: " + msg.err.Error() + " (buffer kept)"
		} else {
			m.textarea.SetValue(msg.content)
			m.status = ""
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.textarea.SetWidth(composerWidth(msg.Width))
		return m, nil
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

// View renders the titled border box, the key hint, and any status line.
func (m model) View() string {
	out := titleStyle.Render(m.prompt) + "\n" +
		boxStyle.Render(m.textarea.View()) + "\n" +
		hintStyle.Render(composerHint)
	if m.status != "" {
		out += "\n" + statusStyle.Render(m.status)
	}
	return out + "\n"
}

// openEditor seeds a temp file with the current buffer and hands off to the
// resolved editor via tea.ExecProcess, which releases and restores the
// terminal's raw mode around the external program.
func (m model) openEditor() tea.Cmd {
	path, err := writeDraftTemp(m.textarea.Value())
	if err != nil {
		return func() tea.Msg { return editorFinishedMsg{err: err} }
	}
	return tea.ExecProcess(editorCommand(path), func(runErr error) tea.Msg {
		if runErr != nil {
			_ = os.Remove(path)
			return editorFinishedMsg{err: runErr}
		}
		content, readErr := readDraftTemp(path)
		return editorFinishedMsg{content: content, err: readErr}
	})
}

// editorCommand builds the command that opens the resolved editor on path. It
// runs through "sh -c" so $VISUAL/$EDITOR values that carry flags (e.g.
// "code --wait") work, matching how git launches an editor. The path is passed
// as a positional argument, never interpolated into the script.
func editorCommand(path string) *exec.Cmd {
	return exec.Command("sh", "-c", resolveEditor()+` "$@"`, "sh", path)
}

// composerWidth caps the textarea width so a wide terminal does not stretch
// lines, while keeping a sane minimum on narrow ones.
func composerWidth(termWidth int) int {
	w := termWidth - 4 // border + horizontal padding
	if w > maxComposerWidth {
		w = maxComposerWidth
	}
	if w < minComposerWidth {
		w = minComposerWidth
	}
	return w
}

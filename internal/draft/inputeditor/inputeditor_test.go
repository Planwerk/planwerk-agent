package inputeditor

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// send delivers msg to the model and returns the concrete model back so tests
// can chain key presses and inspect state.
func send(t *testing.T, m model, msg tea.Msg) (model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	nm, ok := next.(model)
	if !ok {
		t.Fatalf("Update returned %T, want model", next)
	}
	return nm, cmd
}

// typeRunes feeds a string to the model as a single key message, mirroring how
// bubbletea delivers typed (or pasted) input.
func typeRunes(t *testing.T, m model, s string) model {
	t.Helper()
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	return m
}

// isQuit reports whether cmd is the tea.Quit command.
func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestComposer_TypesMultipleLinesAndSubmits(t *testing.T) {
	m := newModel("Describe your feature idea")
	m = typeRunes(t, m, "first line")
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = typeRunes(t, m, "second line")

	m, cmd := send(t, m, tea.KeyMsg{Type: tea.KeyCtrlD})
	if !isQuit(cmd) {
		t.Fatal("ctrl+d should submit (return tea.Quit)")
	}

	got, err := captureResult(m)
	if err != nil {
		t.Fatalf("captureResult error: %v", err)
	}
	if want := "first line\nsecond line"; got != want {
		t.Errorf("captured value = %q, want %q", got, want)
	}
}

func TestComposer_EditsEarlierLine(t *testing.T) {
	m := newModel("Describe your feature idea")
	m = typeRunes(t, m, "lineA")
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = typeRunes(t, m, "lineB")

	// Move the cursor up to the first line and append to it.
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyUp})
	m = typeRunes(t, m, "!")

	got, err := captureResult(m)
	if err != nil {
		t.Fatalf("captureResult error: %v", err)
	}
	if want := "lineA!\nlineB"; got != want {
		t.Errorf("edited value = %q, want %q", got, want)
	}
}

func TestComposer_EmptySubmissionYieldsEmptyString(t *testing.T) {
	m := newModel("Describe your feature idea")
	m, cmd := send(t, m, tea.KeyMsg{Type: tea.KeyCtrlD})
	if !isQuit(cmd) {
		t.Fatal("ctrl+d should submit (return tea.Quit)")
	}
	got, err := captureResult(m)
	if err != nil {
		t.Fatalf("captureResult error: %v", err)
	}
	if got != "" {
		t.Errorf("empty submission value = %q, want \"\"", got)
	}
}

func TestComposer_WhitespaceOnlySubmissionIsTrimmed(t *testing.T) {
	m := newModel("Describe your feature idea")
	m = typeRunes(t, m, "   ")
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	got, err := captureResult(m)
	if err != nil {
		t.Fatalf("captureResult error: %v", err)
	}
	if got != "" {
		t.Errorf("whitespace-only value = %q, want \"\" after trim", got)
	}
}

func TestComposer_CancelReturnsErrCanceled(t *testing.T) {
	for _, key := range []tea.KeyType{tea.KeyCtrlC, tea.KeyEscape} {
		m := newModel("Describe your feature idea")
		m = typeRunes(t, m, "discarded")
		m, cmd := send(t, m, tea.KeyMsg{Type: key})
		if !isQuit(cmd) {
			t.Fatalf("key %v should quit", key)
		}
		if _, err := captureResult(m); !errors.Is(err, ErrCanceled) {
			t.Errorf("key %v: captureResult err = %v, want ErrCanceled", key, err)
		}
	}
}

func TestComposer_EditorSuccessReplacesBuffer(t *testing.T) {
	m := newModel("Describe your feature idea")
	m = typeRunes(t, m, "before")

	m, _ = send(t, m, editorFinishedMsg{content: "edited in $EDITOR"})

	got, err := captureResult(m)
	if err != nil {
		t.Fatalf("captureResult error: %v", err)
	}
	if want := "edited in $EDITOR"; got != want {
		t.Errorf("buffer after editor = %q, want %q", got, want)
	}
	if m.status != "" {
		t.Errorf("status = %q, want empty after a successful edit", m.status)
	}
}

func TestComposer_EditorErrorKeepsBuffer(t *testing.T) {
	m := newModel("Describe your feature idea")
	m = typeRunes(t, m, "kept buffer")

	m, _ = send(t, m, editorFinishedMsg{err: errors.New("exit status 1")})

	got, err := captureResult(m)
	if err != nil {
		t.Fatalf("captureResult error: %v", err)
	}
	if want := "kept buffer"; got != want {
		t.Errorf("buffer after editor error = %q, want %q (must be kept)", got, want)
	}
	if m.status == "" {
		t.Error("expected a status line reporting the editor failure")
	}
}

func TestComposerWidth(t *testing.T) {
	tests := []struct {
		name string
		term int
		want int
	}{
		{"caps wide terminals", 200, maxComposerWidth},
		{"tracks medium terminals", 64, 60},
		{"floors narrow terminals", 10, minComposerWidth},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := composerWidth(tt.term); got != tt.want {
				t.Errorf("composerWidth(%d) = %d, want %d", tt.term, got, tt.want)
			}
		})
	}
}

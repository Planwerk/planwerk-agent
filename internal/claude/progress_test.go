package claude

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// safeBuffer is a bytes.Buffer guarded by a mutex so the progress goroutine
// and the test goroutine can access it without a data race.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// captureLogger returns a text-format logger that writes into the returned
// buffer, so a test can pass it to the code under test and assert on the
// rendered records.
func captureLogger() (*slog.Logger, *safeBuffer) {
	buf := &safeBuffer{}
	return slog.New(slog.NewTextHandler(buf, nil)), buf
}

func TestStartProgressTo_EmitsLabeledElapsedUpdates(t *testing.T) {
	var buf safeBuffer
	stop := startProgressTo(&buf, "review", 10*time.Millisecond)
	// Wait for at least two ticks so we see periodic updates.
	time.Sleep(35 * time.Millisecond)
	stop()

	out := buf.String()
	if !strings.Contains(out, "[review]") {
		t.Errorf("output should contain the label, got: %q", out)
	}
	if !strings.Contains(out, "elapsed:") {
		t.Errorf("output should contain elapsed marker, got: %q", out)
	}
	if !strings.Contains(out, "still running") {
		t.Errorf("output should describe running state, got: %q", out)
	}
	if strings.Count(out, "\n") < 2 {
		t.Errorf("expected at least two updates on separate lines, got: %q", out)
	}
}

func TestStartProgressTo_StopIsSynchronousAndSilentAfter(t *testing.T) {
	var buf safeBuffer
	stop := startProgressTo(&buf, "review", 10*time.Millisecond)
	time.Sleep(25 * time.Millisecond)
	stop()
	afterStop := buf.String()

	// After stop returns, no more output may appear. Give the goroutine a
	// chance to misbehave, then assert that the buffer is unchanged.
	time.Sleep(30 * time.Millisecond)
	if buf.String() != afterStop {
		t.Errorf("progress goroutine wrote after stop() returned:\nbefore: %q\nafter:  %q", afterStop, buf.String())
	}
}

func TestStartProgressLogged_EmitsSlogHeartbeat(t *testing.T) {
	logger, buf := captureLogger()
	stop := startProgressLogged(logger, "review", 10*time.Millisecond)
	time.Sleep(35 * time.Millisecond)
	stop()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	heartbeats := 0
	for _, line := range lines {
		if !strings.Contains(line, `msg="claude still running"`) {
			t.Errorf("unexpected log record: %q", line)
			continue
		}
		if !strings.Contains(line, "label=review") || !strings.Contains(line, "elapsed=") {
			t.Errorf("heartbeat does not carry the label and elapsed attributes: %q", line)
		}
		heartbeats++
	}
	if heartbeats < 2 {
		t.Errorf("expected at least two heartbeat records, got %d:\n%s", heartbeats, buf.String())
	}
}

// TestStartProgressLogged_StopBeforeFirstTickIsSilent covers a call that ends
// before the first heartbeat is due: stop must return without waiting for a
// tick, and nothing may be logged.
func TestStartProgressLogged_StopBeforeFirstTickIsSilent(t *testing.T) {
	logger, buf := captureLogger()
	stop := startProgressLogged(logger, "review", time.Hour)

	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stop() did not return before the first heartbeat was due")
	}
	if got := buf.String(); got != "" {
		t.Errorf("logged before the first tick: %q", got)
	}
}

// Ensure startProgressTo accepts an io.Writer (compile-time check).
var _ io.Writer = (*safeBuffer)(nil)

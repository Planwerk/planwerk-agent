package claude

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"
)

// progressInterval is how often elapsed-time updates are printed while a
// Claude Code invocation is in flight.
const progressInterval = 15 * time.Second

// progressMu serializes writes to the TTY progress sink so that concurrent
// Claude invocations do not interleave bytes within a single update line.
var progressMu sync.Mutex

// stderrIsTerminal reports whether os.Stderr refers to a character device
// (i.e., an interactive terminal).
func stderrIsTerminal() bool {
	fi, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// startProgress prints a periodic elapsed-time message while a Claude call
// is running. It returns a stop function that synchronously halts the
// goroutine; callers should `defer stop()` immediately after starting.
//
// On an interactive terminal the update is written as a plain line on
// stderr so long-running work is visible without scraping log levels.
// When stderr is a pipe or file (CI, `2>log.txt`), the heartbeat is
// emitted through slog at info level instead so structured log streams
// still record progress — previously this case was silent, which made
// 15+ minute Claude invocations look hung in CI logs.
func startProgress(label string) func() {
	if stderrIsTerminal() {
		return startProgressTo(os.Stderr, label, progressInterval)
	}
	return startProgressLogged(slog.Default(), label, progressInterval)
}

// startProgressTo is the testable core of the TTY path. It writes updates
// to w at the given interval until the returned stop function is called.
func startProgressTo(w io.Writer, label string, interval time.Duration) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	start := time.Now()

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				elapsed := time.Since(start).Round(time.Second)
				progressMu.Lock()
				_, _ = fmt.Fprintf(w, "  [%s] still running... (elapsed: %s)\n", label, elapsed)
				progressMu.Unlock()
			}
		}
	}()

	return func() {
		close(stop)
		<-done
	}
}

// startProgressLogged is the non-TTY path: heartbeats go through logger at
// info level so they appear in structured log output (text or JSON) rather
// than being suppressed.
func startProgressLogged(logger *slog.Logger, label string, interval time.Duration) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	start := time.Now()

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				elapsed := time.Since(start).Round(time.Second)
				logger.Info("claude still running",
					"label", label,
					"elapsed", elapsed.String())
			}
		}
	}()

	return func() {
		close(stop)
		<-done
	}
}

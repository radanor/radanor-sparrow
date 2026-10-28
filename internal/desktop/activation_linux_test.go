//go:build linux

package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	sparrowlog "github.com/radanor/radanor-shield/internal/logging"
)

// L2 turns a second launch into a request to the supervisor that already owns the
// instance. These tests cover the parts that decide whether a user gets their
// dashboard back without a second monitoring core, a second gateway or a second
// window: the request/acknowledgement protocol, the one-window rule, and the
// browser fallback when the window cannot be shown again. None of them needs GTK,
// a display or a gateway, which is why the boundary is tested here.

// syncBuffer is a log sink the watcher goroutine can write to while the test reads
// it, so the tests stay race-detector clean.
type syncBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buffer.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buffer.String()
}

// shortenActivationPoll makes the supervisor watch for a request as fast as a test
// can wait for it. The value is read when the watcher starts, so a test sets it
// before starting one.
func shortenActivationPoll(t *testing.T, interval time.Duration) {
	t.Helper()
	original := desktopActivationPoll
	desktopActivationPoll = interval
	t.Cleanup(func() { desktopActivationPoll = original })
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if condition() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	return len(strings.Fields(string(content)))
}

// presentingHelper is a stand-in window component that reports readiness like the
// real one, stays alive as a presented window, and records every start so a test
// can see how many windows the supervisor actually opened. `exec` keeps the
// recorded process the one that must be ended.
func presentingHelper(t *testing.T, starts, pids string) string {
	t.Helper()
	return fakeHelper(t, fmt.Sprintf(`printf 'start\n' >> %q
printf '%%s\n' "$$" >> %q
printf 'ready\n' >&3
exec sleep 60`, starts, pids))
}

func killRecordedHelpers(t *testing.T, pids string) {
	t.Helper()
	content, err := os.ReadFile(pids)
	if err != nil {
		return
	}
	for _, field := range strings.Fields(string(content)) {
		if pid, err := strconv.Atoi(field); err == nil {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

func firstRecordedPID(t *testing.T, pids string) int {
	t.Helper()
	content, err := os.ReadFile(pids)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(strings.Fields(string(content))[0]))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

// TestSecondLaunchHandsTheRequestToTheRunningSupervisor covers the protocol: the
// launch creates one request in the private runtime directory and reports success
// only once the running instance has taken it.
func TestSecondLaunchHandsTheRequestToTheRunningSupervisor(t *testing.T) {
	t.Setenv("RADANOR_SPARROW_WINDOW_HELPER", fakeHelper(t, `exec sleep 60`))
	runtimeDir := t.TempDir()
	taken := make(chan struct{})
	go func() {
		for {
			if consumeActivationRequest(runtimeDir) {
				close(taken)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	if err := requestDesktopActivation(runtimeDir); err != nil {
		t.Fatalf("an answered request must succeed: %v", err)
	}
	select {
	case <-taken:
	case <-time.After(5 * time.Second):
		t.Fatal("the request was reported as taken but was never consumed")
	}
	if consumeActivationRequest(runtimeDir) {
		t.Error("a taken request was still present")
	}
}

// TestAnUnwatchedWindowRequestKeepsTheBrowserPath: an instance that has the
// component but does not watch for requests — an older build — must leave the
// launch on its existing browser path rather than hanging.
func TestAnUnwatchedWindowRequestKeepsTheBrowserPath(t *testing.T) {
	original := desktopActivationWait
	desktopActivationWait = 300 * time.Millisecond
	t.Cleanup(func() { desktopActivationWait = original })
	t.Setenv("RADANOR_SPARROW_WINDOW_HELPER", fakeHelper(t, `exec sleep 60`))

	runtimeDir := t.TempDir()
	err := requestDesktopActivation(runtimeDir)
	if err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("error = %v, want the unanswered-request reason", err)
	}
	// A request nobody took is a private file in the private runtime directory: it
	// carries no session value, and it is not readable by anyone else.
	info, statErr := os.Stat(activationRequestPath(runtimeDir))
	if statErr != nil {
		t.Fatalf("the request file should remain for the next supervisor: %v", statErr)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("request file mode = %#o, want 0600", mode)
	}
	content, readErr := os.ReadFile(activationRequestPath(runtimeDir))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(content), "ticket") || strings.Contains(string(content), "http") {
		t.Errorf("the request file carries more than a trigger: %q", content)
	}
}

// TestALaunchWithNoWindowComponentDoesNotWait is the fast path for a machine that
// has no component installed: there is nothing to ask the running instance for, so
// the launch must not spend the request timeout waiting for an answer.
func TestALaunchWithNoWindowComponentDoesNotWait(t *testing.T) {
	t.Setenv("RADANOR_SPARROW_WINDOW_HELPER", filepath.Join(t.TempDir(), windowHelperName))
	original := desktopActivationWait
	desktopActivationWait = time.Minute // a return faster than this cannot be the timeout
	t.Cleanup(func() { desktopActivationWait = original })

	runtimeDir := t.TempDir()
	started := time.Now()
	err := requestDesktopActivation(runtimeDir)
	if err == nil {
		t.Fatal("a launch with no component must report that it could not ask for a window")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Errorf("the launch waited %s for a window component that is not installed", elapsed)
	}
	if _, statErr := os.Stat(activationRequestPath(runtimeDir)); !os.IsNotExist(statErr) {
		t.Error("a request was left behind although no component could answer it")
	}
}

// TestSecondLaunchUsesTheWindowRequestInsteadOfTheBrowser drives the shared reopen
// path: with the request answered there is nothing else to do, so the launch exits
// without reading the discovery record, without a ticket and without a browser.
func TestSecondLaunchUsesTheWindowRequestInsteadOfTheBrowser(t *testing.T) {
	t.Setenv("RADANOR_SPARROW_WINDOW_HELPER", fakeHelper(t, `exec sleep 60`))
	runtimeDir := t.TempDir()
	taken := make(chan struct{})
	go func() {
		for {
			if consumeActivationRequest(runtimeDir) {
				close(taken)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	if err := reopenDashboard(context.Background(), runtimeDir, false); err != nil {
		t.Fatalf("a second launch that was answered must succeed: %v", err)
	}
	select {
	case <-taken:
	case <-time.After(5 * time.Second):
		t.Fatal("the second launch returned without its request being taken")
	}
}

// TestActivationRequestDoesNotStartASecondWindow is the duplicate rule: while a
// window is on screen or being started, an activation request is answered by
// leaving that window alone. Once the user closes it, the next request opens a new
// one, still without touching monitoring.
func TestActivationRequestDoesNotStartASecondWindow(t *testing.T) {
	dir := t.TempDir()
	starts := filepath.Join(dir, "starts")
	pids := filepath.Join(dir, "pids")
	t.Cleanup(func() { killRecordedHelpers(t, pids) })

	session := &windowSession{helper: presentingHelper(t, starts, pids), runtimeDir: dir, stateDir: dir}
	if err := session.open(context.Background()); err != nil {
		t.Fatalf("the first window must open: %v", err)
	}
	if err := session.open(context.Background()); !errors.Is(err, errWindowAlreadyOpen) {
		t.Fatalf("error = %v, want the already-open refusal", err)
	}
	if got := countLines(t, starts); got != 1 {
		t.Fatalf("the component was started %d times while a window was open, want 1", got)
	}

	// Closing the window ends only the component. The supervisor must notice that
	// the window is gone and be able to present the dashboard again.
	if err := syscall.Kill(firstRecordedPID(t, pids), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the closed window to be forgotten", func() bool { return !session.presenting() })
	if err := session.open(context.Background()); err != nil {
		t.Fatalf("the window must open again after the user closed it: %v", err)
	}
	if got := countLines(t, starts); got != 2 {
		t.Fatalf("the component was started %d times after the window closed, want 2", got)
	}
}

// TestWatcherServesASecondLaunchAndFallsBackWhenItCannot is the whole supervisor
// side of L2 in one place: the watcher takes the request, opens the window when it
// can, and when it cannot it reports the reason and keeps the existing
// authenticated browser path instead of failing the instance.
func TestWatcherServesASecondLaunchAndFallsBackWhenItCannot(t *testing.T) {
	shortenActivationPoll(t, 20*time.Millisecond)

	t.Run("opens the window again and never a second one", func(t *testing.T) {
		dir := t.TempDir()
		starts := filepath.Join(dir, "starts")
		pids := filepath.Join(dir, "pids")
		t.Cleanup(func() { killRecordedHelpers(t, pids) })

		helper := presentingHelper(t, starts, pids)
		// The launch side resolves the component beside the executable, so the
		// stand-in has to be reachable that way too.
		t.Setenv("RADANOR_SPARROW_WINDOW_HELPER", helper)
		session := &windowSession{helper: helper, runtimeDir: dir, stateDir: dir}
		if err := session.open(context.Background()); err != nil {
			t.Fatalf("the first window must open: %v", err)
		}
		var fallbacks int32
		var log syncBuffer
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go session.watchDesktopActivations(ctx, sparrowlog.New(&log, slog.LevelInfo), func() error {
			atomic.AddInt32(&fallbacks, 1)
			return nil
		})

		// A second launch while the window is open must leave that window alone.
		if err := requestDesktopActivation(dir); err != nil {
			t.Fatalf("the request must be answered: %v", err)
		}
		waitFor(t, "the request to be reported as served", func() bool {
			return strings.Contains(log.String(), "already open")
		})
		if got := countLines(t, starts); got != 1 {
			t.Fatalf("a second window was started (%d components); the open one must be left alone", got)
		}
		if atomic.LoadInt32(&fallbacks) != 0 {
			t.Error("the browser path was used while a window was already open")
		}

		// Close the window, then ask again: this is the user's reopen.
		if err := syscall.Kill(firstRecordedPID(t, pids), syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the closed window to be forgotten", func() bool { return !session.presenting() })
		if err := requestDesktopActivation(dir); err != nil {
			t.Fatalf("the reopen request must be answered: %v", err)
		}
		waitFor(t, "the reopened window", func() bool { return countLines(t, starts) == 2 })
		waitFor(t, "the reopen to be reported", func() bool {
			return strings.Contains(log.String(), "showing it again")
		})
		if atomic.LoadInt32(&fallbacks) != 0 {
			t.Error("the browser path was used while the window could be shown again")
		}
	})

	t.Run("reports the reason and keeps the browser path", func(t *testing.T) {
		dir := t.TempDir()
		session := &windowSession{helper: filepath.Join(dir, windowHelperName), runtimeDir: dir, stateDir: dir}
		t.Setenv("RADANOR_SPARROW_WINDOW_HELPER", fakeHelper(t, `exec sleep 60`))
		fallbacks := make(chan struct{}, 1)
		var log syncBuffer
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go session.watchDesktopActivations(ctx, sparrowlog.New(&log, slog.LevelInfo), func() error {
			select {
			case fallbacks <- struct{}{}:
			default:
			}
			return nil
		})
		if err := requestDesktopActivation(dir); err != nil {
			t.Fatalf("a request is taken even when the window cannot be shown: %v", err)
		}
		select {
		case <-fallbacks:
		case <-time.After(10 * time.Second):
			t.Fatal("the supervisor neither showed the window nor opened the browser")
		}
		if !strings.Contains(log.String(), "could not be shown again") || !strings.Contains(log.String(), "cannot start the native window component") {
			t.Errorf("the fallback was not explained with its reason: %s", log.String())
		}
		if strings.Contains(log.String(), "ticket") || strings.Contains(log.String(), "http://") {
			t.Errorf("the fallback line leaked an address or a ticket: %s", log.String())
		}
	})
}

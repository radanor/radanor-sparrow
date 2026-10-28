//go:build linux

package desktop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type containment struct{}

func newContainment() (containment, error)     { return containment{}, nil }
func (containment) attach(_ *os.Process) error { return nil }
func (containment) kill(process *os.Process)   { _ = syscall.Kill(-process.Pid, syscall.SIGKILL) }
func (containment) close()                     {}
func configureChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}
func openBrowser(url string) error {
	cmd := exec.Command("xdg-open", url)
	if err := cmd.Start(); err != nil {
		return err
	}
	// Some associations keep xdg-open attached to the browser. Observe quick
	// failures, but never make monitoring shutdown wait for a browser to close.
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()
	select {
	case err := <-finished:
		return err
	case <-time.After(time.Second):
		return nil
	}
}

// activateDesktopWindow reports that no window can be raised by this process.
// The Linux window is a separate process owned by the running supervisor, so
// bringing the dashboard back is a request to that supervisor
// (requestDesktopActivation) rather than a window-manager lookup here.
func activateDesktopWindow() bool { return false }

const (
	// windowHelperName is the presentation component, installed beside the
	// supervisor. Everything here is Linux-only: the Windows window lives inside
	// this same binary and is unaffected.
	windowHelperName = "radanor-sparrow-window"
	// windowHelperReadyEnv names the inherited descriptor the helper reports on.
	windowHelperReadyEnv = "RADANOR_SPARROW_WINDOW_READY_FD"
	// windowHelperStatusLimit bounds what the supervisor is willing to read from
	// the helper, so a misbehaving component cannot flood the application log.
	windowHelperStatusLimit = 1 << 10
	// windowHelperReasonLimit bounds one logged reason.
	windowHelperReasonLimit = 200
)

// windowHelperTimeout is the time the helper has to report that the dashboard is
// on screen, including its own probe of the GUI runtime. It is a variable so the
// tests can shorten it.
var windowHelperTimeout = 30 * time.Second

// windowHelperStopGrace is how long the component has to dispose its window and
// WebKit's own processes after SIGTERM before it is stopped with its group. It is
// a variable so the tests can shorten it.
var windowHelperStopGrace = 2 * time.Second

// launchDesktopDashboard presents the authenticated dashboard. On Linux it
// prefers the native GTK/WebKitGTK window and keeps the browser path as the
// fallback, which is the whole reason the window is a separate process: this
// binary stays CGO-free and therefore cannot be destroyed by a missing library.
// the private engineering record records that decision.
//
// After the window is up, the supervisor keeps the ability to present it again:
// closing the window ends only the component, monitoring continues, and a second
// launch is served here (see watchDesktopActivations) instead of starting a
// second core. That is why the window's whole lifetime belongs to this process.
//
// The returned function ends the component this supervisor presented and waits
// until the component is reaped. The caller runs it before this process exits, so
// a window the supervisor owned is never left to the kernel's reparenting: see
// windowSession.shutdown.
func launchDesktopDashboard(ctx context.Context, cancel context.CancelFunc, origin, runtimeDir, stateDir string, secret []byte, logger *slog.Logger) (func(), error) {
	helper, found := windowHelperPath()
	if !found {
		logger.Warn("the native window component is not installed; opening the authenticated dashboard in the default browser")
		return func() {}, launchDashboard(ctx, origin, runtimeDir, secret, openBrowser)
	}
	windows := &windowSession{helper: helper, runtimeDir: runtimeDir, stateDir: stateDir, cancel: cancel}
	// A launch that finds this instance already running asks it for the window
	// rather than starting anything of its own, so this supervisor answers such
	// requests for as long as it lives — including when it could not show a window
	// itself, where the answer is the same authenticated browser path the user is
	// already being given. Without that, a machine with GTK missing would make
	// every later launch wait for an answer that never comes.
	go windows.watchDesktopActivations(ctx, logger, func() error {
		return launchDashboard(ctx, origin, runtimeDir, secret, openBrowser)
	})
	if err := windows.open(ctx); err != nil && !errors.Is(err, errWindowAlreadyOpen) {
		// The reason is the helper's own bounded sentence. It never contains the
		// origin, the launch ticket or a session value.
		logger.Warn("the native window could not be shown; opening the authenticated dashboard in the default browser", "reason", err.Error())
		// The watcher stays up for a later request, so the shutdown is returned
		// here too: a window it presents must be reaped by this process as well.
		return windows.shutdown, launchDashboard(ctx, origin, runtimeDir, secret, openBrowser)
	}
	// The window is up and the dashboard is loaded. It ends when the user closes
	// it, when this supervisor stops it on the way out, or — for a component that
	// dies without either signal — when Pdeathsig reaches it at process exit.
	return windows.shutdown, nil
}

// errWindowAlreadyOpen says that the window the requester asked for is the window
// that is already on screen. It is not a failure, and it must never cause a
// second window or a browser tab.
var errWindowAlreadyOpen = errors.New("the dashboard window is already open")

// windowSession is the supervisor's record of the one native window it may
// present: which component to start, and whether a window is on screen right now.
//
// There is exactly one owner of the window's lifetime, and it is the supervisor.
// A window it did not start is a window nobody would reap, could outlive
// monitoring, or could disappear together with the process that asked for it —
// which is why a second launch hands its request here instead of running the
// component itself.
type windowSession struct {
	helper     string
	runtimeDir string
	stateDir   string
	cancel     context.CancelFunc

	mu      sync.Mutex
	live    <-chan struct{}
	window  *windowComponent
	pending bool
	// settled is closed when the start in flight has finished, so a shutdown
	// that coincides with a second launch can wait for that start instead of
	// leaving its component to be killed by the kernel at process exit.
	settled chan struct{}
}

// presenting reports whether a window is on screen or is being started, and
// forgets a window whose component has already exited.
func (s *windowSession) presenting() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending {
		return true
	}
	if s.live == nil {
		return false
	}
	select {
	case <-s.live:
		s.live = nil
		s.window = nil
		return false
	default:
		return true
	}
}

// open starts the window component and waits for its verdict. It refuses to
// present a second window while one is on screen or being started.
func (s *windowSession) open(ctx context.Context) error {
	if s.presenting() {
		return errWindowAlreadyOpen
	}
	s.mu.Lock()
	if s.pending {
		s.mu.Unlock()
		return errWindowAlreadyOpen
	}
	s.pending = true
	settled := make(chan struct{})
	s.settled = settled
	s.mu.Unlock()

	window, err := startWindowHelper(ctx, s.helper, s.runtimeDir, s.stateDir)

	s.mu.Lock()
	s.pending = false
	s.settled = nil
	if err == nil {
		s.window = window
		s.live = window.exited
	}
	s.mu.Unlock()
	close(settled)
	return err
}

// shutdown ends the window this supervisor presented and waits until the
// component has been reaped by this process.
//
// The supervisor is the component's parent for the component's whole life, so
// exiting without this wait hands the component to whichever process the kernel
// reparents it to — and its last state before that is one the process table still
// shows, which is a zombie process the application would be leaving behind. Waiting
// here also stops the component the way a window close does, so WebKit disposes
// its own processes while their UI process is alive instead of being killed
// underneath it.
//
// It is safe to call more than once and when no window was ever opened.
func (s *windowSession) shutdown() {
	s.mu.Lock()
	settled := s.settled
	s.mu.Unlock()
	if settled != nil && s.cancel != nil {
		// Monitoring is ending while a second launch is having its window
		// started. The start is bounded by the same context and reaps its own
		// component when it is cancelled, so wait for it to settle, then stop
		// whatever it presented.
		s.cancel()
		select {
		case <-settled:
		case <-time.After(windowHelperStopGrace):
		}
	}
	s.mu.Lock()
	window := s.window
	s.window, s.live = nil, nil
	s.mu.Unlock()
	stopWindowComponent(window)
}

// watchDesktopActivations serves a launch that found the instance already running:
// it presents the dashboard window again, through the same component and the same
// authenticated IPC ticket, without touching the monitoring core.
//
// The request arrives as one trigger in the private runtime directory (see
// requestDesktopActivation). The supervisor stays the only process that starts a
// window, so a second launch can never leave an unreaped component behind, and a
// request that arrives while the window is already on screen is answered by
// leaving that window alone.
func (s *windowSession) watchDesktopActivations(ctx context.Context, logger *slog.Logger, fallback func() error) {
	ticker := time.NewTicker(desktopActivationPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if !consumeActivationRequest(s.runtimeDir) {
			continue
		}
		switch err := s.open(ctx); {
		case err == nil:
			logger.Info("a second launch asked for the dashboard window; showing it again")
		case errors.Is(err, errWindowAlreadyOpen):
			logger.Info("a second launch asked for the dashboard window; it is already open")
		default:
			logger.Warn("the dashboard window could not be shown again; opening the authenticated dashboard in the default browser", "reason", err.Error())
			if fallbackErr := fallback(); fallbackErr != nil {
				logger.Warn("the authenticated dashboard could not be opened in the default browser", "reason", fallbackErr.Error())
			}
		}
	}
}

// windowHelperPath finds the presentation component: beside this executable, as
// the build scripts install it. RADANOR_SPARROW_WINDOW_HELPER overrides the
// location for development and diagnostics; it can only point at a file this
// user can execute, which is the same trust the browser path already places in
// PATH.
func windowHelperPath() (string, bool) {
	if override := os.Getenv("RADANOR_SPARROW_WINDOW_HELPER"); override != "" {
		return usableWindowHelper(override)
	}
	executable, err := os.Executable()
	if err != nil {
		return "", false
	}
	return windowHelperPathFor(executable)
}

func windowHelperPathFor(executable string) (string, bool) {
	return usableWindowHelper(filepath.Join(filepath.Dir(executable), windowHelperName))
}

func usableWindowHelper(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return "", false
	}
	return path, true
}

// windowHelperCommand is the exact contract with the helper: where the private
// directories are, and the descriptor to report the outcome on. The dashboard
// URL and the launch ticket are deliberately not passed: the helper obtains them
// itself through the same authenticated IPC the rest of the product uses, so
// neither can appear in a process listing.
func windowHelperCommand(helper, runtimeDir, stateDir string, status *os.File) *exec.Cmd {
	cmd := exec.Command(helper, "-runtime-dir", runtimeDir, "-state-dir", stateDir)
	cmd.Env = append(os.Environ(), windowHelperReadyEnv+"=3")
	cmd.ExtraFiles = []*os.File{status}
	// The helper writes its diagnostics to its own private log, so nothing it
	// prints lands in the application log or on a terminal.
	cmd.Stdout, cmd.Stderr = nil, nil
	configureChild(cmd)
	return cmd
}

// runWindowHelper starts the helper and waits for its verdict. It is the
// supervisor's startup path; the caller that needs to own the component afterwards
// uses startWindowHelper directly.
func runWindowHelper(ctx context.Context, helper, runtimeDir, stateDir string) error {
	_, err := startWindowHelper(ctx, helper, runtimeDir, stateDir)
	return err
}

// windowComponent is a helper that reported readiness: the process, and the
// channel the goroutine that reaps it closes when it has exited.
type windowComponent struct {
	cmd    *exec.Cmd
	exited <-chan struct{}
}

// startWindowHelper starts the helper and waits for its verdict: "ready" once the
// authenticated dashboard is on screen, or a bounded reason why it is not. A
// helper that never answers is stopped, so a broken component cannot hold the
// product's startup.
//
// On success it returns the running component, whose exit channel is closed when
// it has been reaped, which is how the supervisor learns that the window it was
// presenting is gone — the state a later window request is answered from.
func startWindowHelper(ctx context.Context, helper, runtimeDir, stateDir string) (*windowComponent, error) {
	statusRead, statusWrite, err := os.Pipe()
	if err != nil {
		return nil, errors.New("cannot create the window readiness channel")
	}
	defer statusRead.Close()
	cmd := windowHelperCommand(helper, runtimeDir, stateDir, statusWrite)
	if err := cmd.Start(); err != nil {
		_ = statusWrite.Close()
		return nil, errors.New("cannot start the native window component")
	}
	// The helper holds the only writer from here: EOF means it exited without a
	// verdict.
	_ = statusWrite.Close()
	outcome := make(chan string, 1)
	go func() {
		buffer := make([]byte, windowHelperStatusLimit+1)
		read, _ := statusRead.Read(buffer)
		outcome <- string(buffer[:read])
	}()

	timer := time.NewTimer(windowHelperTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		stopWindowHelper(cmd)
		return nil, errors.New("the application is exiting")
	case <-timer.C:
		stopWindowHelper(cmd)
		return nil, errors.New("the native window component did not report readiness in time")
	case message := <-outcome:
		verdict := strings.TrimSpace(message)
		switch {
		case verdict == "ready":
			// The helper is still running, presenting the window, and the
			// supervisor stays up while it does. Reap it when it exits: a window
			// the user closes must not leave a zombie process behind, and this
			// process is the helper's parent for the rest of its life. The same
			// goroutine reports the exit, so the supervisor knows the window is
			// gone and may present another.
			exited := make(chan struct{})
			go func() { _ = cmd.Wait(); close(exited) }()
			return &windowComponent{cmd: cmd, exited: exited}, nil
		case verdict == "":
			stopWindowHelper(cmd)
			return nil, errors.New("the native window component exited before reporting readiness")
		case len(verdict) > windowHelperReasonLimit:
			stopWindowHelper(cmd)
			return nil, errors.New("the native window component reported an oversized reason: " + verdict[:windowHelperReasonLimit])
		default:
			stopWindowHelper(cmd)
			return nil, errors.New(helperReason(verdict))
		}
	}
}

// helperReason turns the helper's reported line into the reason the supervisor
// logs. The line is "error: <reason>" on the wire; the prefix is the protocol,
// not part of the sentence, and the reason is already bounded and scrubbed of any
// address by the helper.
func helperReason(verdict string) string {
	reason := strings.TrimPrefix(verdict, "error: ")
	if strings.TrimSpace(reason) == "" {
		return "the native window component reported no reason"
	}
	return reason
}

// stopWindowHelper ends the helper and everything in its process group, which
// includes WebKit's own web process. It is the start-up failure path: a component
// that could not present the window has nothing left to dispose gracefully.
func stopWindowHelper(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(windowHelperStopGrace):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}
}

// stopWindowComponent ends a component that presented a window and waits until
// this process has reaped it.
//
// The exit channel belongs to the goroutine that already called Wait, so this
// waits on that reaper instead of calling Wait a second time. When it returns, the
// component is gone from the process table rather than sitting in it as a zombie,
// which is what makes "the supervisor has exited" a complete answer to "may this
// instance have left a process behind".
func stopWindowComponent(window *windowComponent) {
	if window == nil || window.cmd == nil || window.cmd.Process == nil {
		return
	}
	process := window.cmd.Process
	// SIGTERM to the component itself, not to its process group: this is the
	// signal the helper already handles by closing its window, so WebKit tears
	// down its own processes instead of losing its UI process under them. The
	// group is what gets escalated below if that does not finish in time.
	_ = process.Signal(syscall.SIGTERM)
	if window.exited == nil {
		return
	}
	select {
	case <-window.exited:
		return
	case <-time.After(windowHelperStopGrace):
	}
	_ = syscall.Kill(-process.Pid, syscall.SIGKILL)
	<-window.exited
}

func ShowError(err error) { fmt.Fprintln(os.Stderr, "Radanor Sparrow:", err) }

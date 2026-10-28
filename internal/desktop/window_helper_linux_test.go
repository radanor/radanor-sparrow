//go:build linux

package desktop

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	sparrowlog "github.com/radanor/radanor-shield/internal/logging"
)

// The helper is a separate process, so its whole contract with the supervisor is
// one bounded line on an inherited descriptor and four command-line arguments.
// These tests exercise that handshake with a stand-in helper: they need no GTK,
// no display and no gateway, which is exactly why the boundary is worth pinning
// here rather than only on a host with a desktop session.
func fakeHelper(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), windowHelperName)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestSupervisorPassesOnlyPrivateDirectoriesToTheHelper is the strongest form of
// the "no ticket on the command line" rule: it records what the helper actually
// received. A process listing is world-readable, so the dashboard address and the
// single-use launch ticket must never be there.
func TestSupervisorPassesOnlyPrivateDirectoriesToTheHelper(t *testing.T) {
	argvFile := filepath.Join(t.TempDir(), "argv")
	envFile := filepath.Join(t.TempDir(), "fd")
	t.Setenv("RADANOR_TEST_ARGV_FILE", argvFile)
	t.Setenv("RADANOR_TEST_FD_FILE", envFile)
	helper := fakeHelper(t, `printf '%s\n' "$@" > "$RADANOR_TEST_ARGV_FILE"
printf '%s\n' "$RADANOR_SPARROW_WINDOW_READY_FD" > "$RADANOR_TEST_FD_FILE"
printf 'ready\n' >&3`)

	runtimeDir := "/private/runtime"
	stateDir := "/private/state"
	if err := runWindowHelper(context.Background(), helper, runtimeDir, stateDir); err != nil {
		t.Fatalf("a ready helper must satisfy the supervisor: %v", err)
	}

	argv := strings.Fields(readFileString(t, argvFile))
	want := []string{"-runtime-dir", runtimeDir, "-state-dir", stateDir}
	if strings.Join(argv, " ") != strings.Join(want, " ") {
		t.Errorf("helper argv = %q, want %q", argv, want)
	}
	for _, argument := range argv {
		if strings.Contains(argument, "http") || strings.Contains(argument, "ticket") {
			t.Errorf("helper argv contains an address or a ticket: %q", argument)
		}
	}
	if got := strings.TrimSpace(readFileString(t, envFile)); got != "3" {
		t.Errorf("helper readiness descriptor = %q, want 3", got)
	}
}

func TestSupervisorReportsTheHelperReasonWithoutTheProtocolPrefix(t *testing.T) {
	helper := fakeHelper(t, `printf 'error: the dashboard did not load\n' >&3`)
	err := runWindowHelper(context.Background(), helper, "/private/runtime", "/private/state")
	if err == nil {
		t.Fatal("a helper that reported a failure must not satisfy the supervisor")
	}
	if got, want := err.Error(), "the dashboard did not load"; got != want {
		t.Errorf("supervisor reason = %q, want %q", got, want)
	}
}

func TestSupervisorFallsBackWhenTheHelperExitsWithoutAVerdict(t *testing.T) {
	helper := fakeHelper(t, `exit 0`)
	err := runWindowHelper(context.Background(), helper, "/private/runtime", "/private/state")
	if err == nil || !strings.Contains(err.Error(), "exited before reporting readiness") {
		t.Fatalf("error = %v, want the missing-verdict reason", err)
	}
}

func TestSupervisorRefusesAnOversizedReason(t *testing.T) {
	helper := fakeHelper(t, `printf 'error: %s\n' "$(head -c 4096 /dev/zero | tr '\0' 'x')" >&3`)
	err := runWindowHelper(context.Background(), helper, "/private/runtime", "/private/state")
	if err == nil || !strings.Contains(err.Error(), "oversized reason") {
		t.Fatalf("error = %v, want an oversized-reason refusal", err)
	}
	if len(err.Error()) > windowHelperReasonLimit+64 {
		t.Errorf("the refusal itself is unbounded: %d bytes", len(err.Error()))
	}
}

// TestSupervisorStopsAHelperThatNeverReports keeps a broken component from
// holding the product's startup: the helper is ended, not left running invisibly.
func TestSupervisorStopsAHelperThatNeverReports(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	t.Setenv("RADANOR_TEST_PID_FILE", pidFile)
	helper := fakeHelper(t, `printf '%s\n' "$$" > "$RADANOR_TEST_PID_FILE"
exec sleep 30`)

	original := windowHelperTimeout
	windowHelperTimeout = 300 * time.Millisecond
	defer func() { windowHelperTimeout = original }()

	err := runWindowHelper(context.Background(), helper, "/private/runtime", "/private/state")
	if err == nil || !strings.Contains(err.Error(), "did not report readiness in time") {
		t.Fatalf("error = %v, want the timeout reason", err)
	}
	pid := waitForRecordedPID(t, pidFile)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the supervisor left the unresponsive helper (pid %d) running", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitForRecordedPID reads the process a stand-in helper recorded for itself. The
// helper writes it before it reports readiness, so this only waits for the file
// system to catch up; it never turns a missing record into a pass.
func waitForRecordedPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if pid, err := strconv.Atoi(strings.TrimSpace(readFileString(t, path))); err == nil {
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatalf("the stand-in helper never recorded its process in %s", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestSupervisorReapsTheHelperAfterTheDashboardIsLoaded: the supervisor stays up
// while the helper presents the window, and the helper is its child, so closing
// the window must not leave a zombie process behind for the rest of the session.
func TestSupervisorReapsTheHelperAfterTheDashboardIsLoaded(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	t.Setenv("RADANOR_TEST_PID_FILE", pidFile)
	// The record is written before the readiness line, so the assertion below never
	// depends on which of the helper's two writes lands first.
	helper := fakeHelper(t, `printf '%s\n' "$$" > "$RADANOR_TEST_PID_FILE"
printf 'ready\n' >&3
sleep 1`)

	if err := runWindowHelper(context.Background(), helper, "/private/runtime", "/private/state"); err != nil {
		t.Fatalf("a ready helper must satisfy the supervisor: %v", err)
	}
	pid := waitForRecordedPID(t, pidFile)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid))); err != nil {
			return // gone, not a zombie
		}
		if time.Now().After(deadline) {
			state, _ := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the exited helper was never reaped; process state: %s", summarizeState(string(state)))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestSupervisorReportsAHelperItCannotStart(t *testing.T) {
	missing := filepath.Join(t.TempDir(), windowHelperName)
	err := runWindowHelper(context.Background(), missing, "/private/runtime", "/private/state")
	if err == nil || !strings.Contains(err.Error(), "cannot start the native window component") {
		t.Fatalf("error = %v, want the cannot-start reason", err)
	}
}

// TestWindowHelperMustBeAnExecutableBesideTheApplication is the fallback rule: a
// component that is absent, a directory or not executable means the browser path,
// never a failed launch.
func TestWindowHelperMustBeAnExecutableBesideTheApplication(t *testing.T) {
	dir := t.TempDir()
	application := filepath.Join(dir, "radanor-sparrow")
	notExecutable := filepath.Join(dir, windowHelperName)
	if err := os.WriteFile(notExecutable, []byte("not a program"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, found := windowHelperPathFor(application); found {
		t.Error("a non-executable file beside the application was accepted as the window component")
	}
	if _, found := windowHelperPathFor(filepath.Join(dir, "elsewhere", "radanor-sparrow")); found {
		t.Error("the component was found in a directory that has none")
	}
	if err := os.Chmod(notExecutable, 0o755); err != nil {
		t.Fatal(err)
	}
	found, ok := windowHelperPathFor(application)
	if !ok || found != notExecutable {
		t.Errorf("windowHelperPathFor = %q, %t; want the executable beside the application", found, ok)
	}
	if _, found := usableWindowHelper(dir); found {
		t.Error("a directory was accepted as the window component")
	}
	t.Setenv("RADANOR_SPARROW_WINDOW_HELPER", notExecutable)
	if override, ok := windowHelperPath(); !ok || override != notExecutable {
		t.Errorf("the diagnostic override was ignored: %q, %t", override, ok)
	}
}

// TestMissingWindowComponentUsesTheBrowserFallback covers the decision, not the
// browser: with no component installed the supervisor logs the fallback and takes
// the browser path. The ticket exchange itself cannot complete here because there
// is no gateway on the socket, which is the expected outcome of this test.
func TestMissingWindowComponentUsesTheBrowserFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RADANOR_SPARROW_WINDOW_HELPER", filepath.Join(dir, "absent"))
	var log bytes.Buffer
	logger := sparrowlog.New(&log, slog.LevelInfo)

	stop, err := launchDesktopDashboard(context.Background(), func() {}, "http://127.0.0.1:41234", dir, dir, []byte("secret"), logger)
	if stop != nil {
		defer stop()
	}
	if err == nil {
		t.Fatal("no gateway answered, so the browser path cannot have succeeded")
	}
	if !strings.Contains(log.String(), "the native window component is not installed") {
		t.Errorf("the fallback was not explained: %s", log.String())
	}
}

// openTestWindow starts a stand-in component through the session the supervisor
// uses, so the shutdown tests cover the real lifecycle rather than a copy of it.
func openTestWindow(t *testing.T, helper string) *windowSession {
	t.Helper()
	session := &windowSession{helper: helper, runtimeDir: t.TempDir(), stateDir: t.TempDir(), cancel: func() {}}
	if err := session.open(context.Background()); err != nil {
		t.Fatalf("a ready helper must open a window: %v", err)
	}
	t.Cleanup(session.shutdown)
	return session
}

// TestSupervisorReapsTheWindowComponentBeforeItExits pins the shutdown property
// the smoke test observes from outside: once the supervisor has stopped its
// window, the component is gone from the process table rather than left as a
// zombie waiting for the kernel to reparent and reap it. A component that is still
// running when the supervisor exits is exactly what "FAIL no zombie process is
// left" reported.
//
// The component handles SIGTERM before it reports readiness. A component that
// installs its handler after the readiness line is a different test: the exit read
// below would then be the kernel's default action on the signal rather than the
// component's own, and the graceful path this test is about would go untested.
func TestSupervisorReapsTheWindowComponentBeforeItExits(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	t.Setenv("RADANOR_TEST_PID_FILE", pidFile)
	helper := fakeHelper(t, `printf '%s\n' "$$" > "$RADANOR_TEST_PID_FILE"
trap 'exit 0' TERM
printf 'ready\n' >&3
while :; do sleep 0.05; done`)
	session := openTestWindow(t, helper)
	pid := waitForRecordedPID(t, pidFile)
	component := componentOf(session)
	assertAlive(t, pid)

	started := time.Now()
	session.shutdown()
	elapsed := time.Since(started)

	// Checked immediately, with no waiting: a bounded wait here would let a
	// component that the supervisor abandoned look like one it reaped.
	if state := processState(pid); state != "gone" {
		t.Fatalf("the window component (pid %d) is still in the process table after shutdown: state %s", pid, state)
	}
	// It left through its own handler, which is what makes this the graceful half
	// of the contract: no escalation was needed, and the reaping above is of a
	// component that answered the signal rather than of one the kernel killed.
	if status := waitStatus(component); status != "exit status 0" {
		t.Errorf("the component did not exit through its handler on SIGTERM: %s", status)
	}
	if elapsed >= windowHelperStopGrace {
		t.Errorf("shutdown waited %s for a component that exited on the signal; the grace period belongs to a component that does not stop", elapsed)
	}
}

// TestWindowShutdownEscalatesForAComponentThatIgnoresTermination keeps the exit
// bounded: the supervisor must not hang on a component that will not stop, must
// give it the whole grace period before escalating, must end it with SIGKILL, and
// must still reap it itself before returning.
//
// The stand-in component ignores SIGTERM and only then reports readiness, so the
// process a supervisor has read a readiness line from is already one that cannot
// be stopped by SIGTERM. Reporting readiness first is the race this test was
// failing with: the supervisor's SIGTERM then kills the component outright,
// shutdown returns in tens of microseconds, and the test measures the stand-in's
// own setup rather than the escalation. The readiness line cannot be read before
// the disposition is set now, and the assertions below verify both halves before
// shutdown begins, so a recurrence says which one broke instead of only reporting
// a duration.
func TestWindowShutdownEscalatesForAComponentThatIgnoresTermination(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	t.Setenv("RADANOR_TEST_PID_FILE", pidFile)
	// exec replaces the shell without touching ignored signals, so the component
	// stays one process that cannot be stopped by SIGTERM and has no children of
	// its own: the escalation below is measured against the process itself.
	helper := fakeHelper(t, `printf '%s\n' "$$" > "$RADANOR_TEST_PID_FILE"
trap '' TERM
printf 'ready\n' >&3
exec sleep 300`)

	original := windowHelperStopGrace
	windowHelperStopGrace = 300 * time.Millisecond
	defer func() { windowHelperStopGrace = original }()

	session := openTestWindow(t, helper)
	pid := waitForRecordedPID(t, pidFile)
	component := componentOf(session)
	assertComponentIgnoresTermination(t, pid)

	started := time.Now()
	session.shutdown()
	elapsed := time.Since(started)

	if elapsed < windowHelperStopGrace {
		t.Errorf("shutdown returned after %s, before the grace period the component was given (%s): component %s, %s",
			elapsed, windowHelperStopGrace, processIdentity(pid), waitStatus(component))
	}
	if elapsed > 10*time.Second {
		t.Errorf("shutdown took %s; a component that ignores SIGTERM must not hold the exit", elapsed)
	}
	if state := processState(pid); state != "gone" {
		t.Fatalf("the unresponsive window component (pid %d) is still in the process table: state %s", pid, state)
	}
	// SIGKILL, not the SIGTERM the supervisor opened with: the reaped component is
	// evidence that escalation ran. "signal: terminated" here would mean the first
	// signal ended it, which is the graceful path and not this test.
	if status := waitStatus(component); status != "signal: killed" {
		t.Errorf("the unresponsive component did not end through escalation: %s", status)
	}
}

// TestWindowShutdownIsSafeWithoutAWindow covers the launch paths without a native
// window — no component installed, or the browser fallback — which must still be
// able to shut down without a component and without waiting for one.
func TestWindowShutdownIsSafeWithoutAWindow(t *testing.T) {
	session := &windowSession{helper: "/nonexistent", cancel: func() {}}
	started := time.Now()
	session.shutdown()
	session.shutdown()
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("shutting down without a window took %s", elapsed)
	}
}

// assertComponentIgnoresTermination proves, before shutdown is initiated, that the
// component is alive and that SIGTERM cannot end it. Both halves are asserted, and
// behaviourally as well as by disposition: the kernel's own record in
// /proc/<pid>/status is what a discarded signal is decided by, and sending one
// then finding the component still there is that record being used.
func assertComponentIgnoresTermination(t *testing.T, pid int) {
	t.Helper()
	assertAlive(t, pid)
	ignored, readable := sigtermIgnored(pid)
	if !readable {
		t.Fatalf("the stand-in component's signal dispositions are not readable: %s", processIdentity(pid))
	}
	if !ignored {
		t.Fatalf("the stand-in component does not ignore SIGTERM, so this test would measure its death by the first signal instead of the escalation: %s",
			processIdentity(pid))
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatalf("cannot signal the stand-in component: %v", err)
	}
	assertAlive(t, pid)
}

// assertAlive fails unless the process is there and running: a zombie is not a
// component that is presenting anything, so it does not count as alive here.
func assertAlive(t *testing.T, pid int) {
	t.Helper()
	switch state := processState(pid); state {
	case "gone":
		t.Fatalf("the stand-in component (pid %d) is not running before shutdown: %s", pid, processIdentity(pid))
	case "Z":
		t.Fatalf("the stand-in component (pid %d) is a zombie before shutdown: %s", pid, processIdentity(pid))
	}
}

// componentOf returns the component a session currently owns. The shutdown clears
// it, so a test that wants to read how the component ended must hold it first.
func componentOf(session *windowSession) *windowComponent {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.window
}

// waitStatus reports how a component that has been reaped ended, in the form
// os/exec records it: "exit status 0" for a component that answered the signal and
// left, "signal: terminated" for one the kernel killed on the first SIGTERM, and
// "signal: killed" for one ended by the escalation.
func waitStatus(component *windowComponent) string {
	if component == nil || component.cmd == nil {
		return "no component"
	}
	if component.cmd.ProcessState == nil {
		return "not reaped"
	}
	return component.cmd.ProcessState.String()
}

// processIdentity describes a process from the kernel's own records, for a failure
// message: pid, state, parent and name, or the fact that it is gone. It prints
// nothing from the instance's environment.
func processIdentity(pid int) string {
	state, ok := procStatusField(pid, "State")
	if !ok {
		return fmt.Sprintf("pid=%d gone", pid)
	}
	parent, _ := procStatusField(pid, "PPid")
	name, _ := procStatusField(pid, "Name")
	return fmt.Sprintf("pid=%d state=%s ppid=%s name=%s", pid, state, parent, name)
}

// sigtermIgnored reports whether the kernel has SIGTERM in the process's set of
// ignored signals, and whether that could be read at all.
func sigtermIgnored(pid int) (bool, bool) {
	raw, ok := procStatusField(pid, "SigIgn")
	if !ok {
		return false, false
	}
	mask, err := strconv.ParseUint(raw, 16, 64)
	if err != nil {
		return false, false
	}
	// SIGTERM is signal 15, so its bit is 15-1.
	return mask&(1<<(syscall.SIGTERM-1)) != 0, true
}

// procStatusField reads one field of /proc/<pid>/status, without the label.
func procStatusField(pid int, field string) (string, bool) {
	content, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return "", false
	}
	prefix := field + ":"
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix)), true
		}
	}
	return "", false
}

// processState reports the kernel's one-letter state for a process, or "gone"
// when the entry no longer exists. A zombie is "Z", which is the state the failed
// smoke check reported.
func processState(pid int) string {
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "gone"
	}
	return summarizeState(string(stat))
}

// summarizeState turns /proc/<pid>/stat into its one-letter process state.
func summarizeState(stat string) string {
	fields := strings.Fields(stat)
	if len(fields) == 0 {
		return "unreadable"
	}
	if len(fields) >= 3 {
		return fields[2]
	}
	return "unknown"
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

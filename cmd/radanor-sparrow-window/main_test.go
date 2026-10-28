//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The sandbox preflight decides whether a window is shown at all, so it is tested
// on the two states that matter for this product: bubblewrap that cannot be used,
// which must be reported as a reason instead of becoming a library abort, and
// bubblewrap that works, which must not block the window.
func TestSandboxPreflightReportsBubblewrapThatIsMissingOrNotExecutable(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "bwrap")
	notExecutable := filepath.Join(dir, "bwrap-not-executable")
	if err := os.WriteFile(notExecutable, []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"absent": missing, "not executable": notExecutable} {
		t.Run(name, func(t *testing.T) {
			restore := bubblewrapPath
			t.Cleanup(func() { bubblewrapPath = restore })
			bubblewrapPath = path
			err := sandboxUnavailable()
			if err == nil {
				t.Fatalf("preflight accepted %s bubblewrap at %s", name, path)
			}
			if !strings.Contains(err.Error(), "bubblewrap") || !strings.Contains(err.Error(), path) {
				t.Errorf("reason does not name the unusable bubblewrap: %q", err)
			}
			if reason := bound(err.Error()); reason == "" || len(reason) > 200 {
				t.Errorf("reason is not one bounded line: %q", reason)
			}
		})
	}
}

// A host can carry bubblewrap and still refuse to create a namespace for this
// user. WebKitGTK aborts the process on that, so the preflight has to catch it and
// report the library's own words.
func TestSandboxPreflightReportsBubblewrapThatCannotCreateASandbox(t *testing.T) {
	// State the policy, so the expected message does not depend on the host the
	// suite runs on: a 24.04 runner has the restriction enabled.
	policyFile(t, "0")
	dir := t.TempDir()
	path := filepath.Join(dir, "bwrap")
	script := "#!/bin/sh\necho 'bwrap: Creating new namespace failed: Operation not permitted' >&2\nexit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	restore := bubblewrapPath
	t.Cleanup(func() { bubblewrapPath = restore })
	bubblewrapPath = path
	err := sandboxUnavailable()
	if err == nil {
		t.Fatal("preflight accepted a bubblewrap that cannot create a namespace")
	}
	if !strings.Contains(err.Error(), "Operation not permitted") {
		t.Errorf("reason does not carry the sandbox failure: %q", err)
	}
	if strings.Contains(bound(err.Error()), "http") {
		t.Errorf("reported reason still looks like an address: %q", bound(err.Error()))
	}
}

// policyFile states the host's unprivileged-user-namespace policy for one test.
// The preflight reads the same file the kernel exposes, so this is the state a
// 24.04 host is in rather than a mock of the code under test.
func policyFile(t *testing.T, value string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "apparmor_restrict_unprivileged_userns")
	if err := os.WriteFile(path, []byte(value+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := apparmorUserNamespaceControl
	t.Cleanup(func() { apparmorUserNamespaceControl = restore })
	apparmorUserNamespaceControl = path
}

// bubblewrapStubThatFailsWith writes an executable bubblewrap stand-in that prints
// the given message to stderr and exits 1.
func bubblewrapStubThatFailsWith(t *testing.T, message string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bwrap")
	script := "#!/bin/sh\nprintf '%s\\n' " + "'" + message + "'" + " >&2\nexit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// Ubuntu 24.04 denies bubblewrap the write to its namespace uid map, and the
// reported reason has to lead an administrator to the policy rather than to an
// unexplained permission error. This is the message the packaged application
// produces there.
func TestSandboxPreflightNamesTheAppArmorUserNamespaceRestriction(t *testing.T) {
	policyFile(t, "1")
	restore := bubblewrapPath
	t.Cleanup(func() { bubblewrapPath = restore })
	bubblewrapPath = bubblewrapStubThatFailsWith(t, "bwrap: setting up uid map: Permission denied")

	err := sandboxUnavailable()
	if err == nil {
		t.Fatal("preflight accepted bubblewrap under the AppArmor restriction")
	}
	reason := bound(err.Error())
	if !strings.Contains(reason, "AppArmor") {
		t.Errorf("reason does not name AppArmor: %q", reason)
	}
	if !strings.Contains(reason, "kernel.apparmor_restrict_unprivileged_userns") {
		t.Errorf("reason does not name the setting to review: %q", reason)
	}
	if !strings.Contains(reason, "browser") {
		t.Errorf("reason does not offer the fallback: %q", reason)
	}
	if len(reason) > 200 {
		t.Errorf("reason is %d bytes, want at most 200: %q", len(reason), reason)
	}
}

// A host without the restriction must keep the bubblewrap-specific message: the
// AppArmor sentence would send an administrator looking for a policy that is not
// there.
func TestSandboxPreflightDoesNotBlameAppArmorWithoutTheRestriction(t *testing.T) {
	policyFile(t, "0")
	restore := bubblewrapPath
	t.Cleanup(func() { bubblewrapPath = restore })
	bubblewrapPath = bubblewrapStubThatFailsWith(t, "bwrap: setting up uid map: Permission denied")

	err := sandboxUnavailable()
	if err == nil {
		t.Fatal("preflight accepted bubblewrap that cannot create a namespace")
	}
	if strings.Contains(err.Error(), "AppArmor blocks") {
		t.Errorf("restriction is off, but the reason blames AppArmor: %q", err)
	}
	if !strings.Contains(err.Error(), "bubblewrap") {
		t.Errorf("reason does not identify the component: %q", err)
	}
}

// A restriction can be enabled while bubblewrap fails for an unrelated reason.
// Naming AppArmor then would be a wrong diagnosis, so the permission markers decide.
func TestSandboxPreflightDoesNotBlameAppArmorForAnUnrelatedFailure(t *testing.T) {
	policyFile(t, "1")
	restore := bubblewrapPath
	t.Cleanup(func() { bubblewrapPath = restore })
	bubblewrapPath = bubblewrapStubThatFailsWith(t, "bwrap: execvp /bin/true: No such file or directory")

	err := sandboxUnavailable()
	if err == nil {
		t.Fatal("preflight accepted a bubblewrap that failed")
	}
	if strings.Contains(err.Error(), "AppArmor blocks") {
		t.Errorf("unrelated failure was blamed on AppArmor: %q", err)
	}
	if !strings.Contains(err.Error(), "No such file or directory") {
		t.Errorf("reason does not carry the sandbox failure: %q", err)
	}
}

// Where the distribution already grants bubblewrap the permission, the same
// kernel policy must not keep the window from opening: the probe is the decision.
func TestSandboxPreflightAcceptsAWorkingBubblewrapUnderTheRestriction(t *testing.T) {
	policyFile(t, "1")
	dir := t.TempDir()
	path := filepath.Join(dir, "bwrap")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	restore := bubblewrapPath
	t.Cleanup(func() { bubblewrapPath = restore })
	bubblewrapPath = path
	if err := sandboxUnavailable(); err != nil {
		t.Fatalf("preflight rejected bubblewrap that works under the restriction: %v", err)
	}
}

func TestSandboxPreflightAcceptsAWorkingBubblewrap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bwrap")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	restore := bubblewrapPath
	t.Cleanup(func() { bubblewrapPath = restore })
	bubblewrapPath = path
	if err := sandboxUnavailable(); err != nil {
		t.Fatalf("preflight rejected a working bubblewrap: %v", err)
	}
}

// The preflight runs the helper with the arguments the sandbox itself needs, so
// an argument mistake would silently disable the native window. This pins them.
func TestSandboxProbeUsesReadOnlyMountAndTheGivenCommand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bwrap")
	args := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + args + "\nexit 0\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	restore, restoreProbe := bubblewrapPath, bubblewrapProbe
	t.Cleanup(func() { bubblewrapPath, bubblewrapProbe = restore, restoreProbe })
	bubblewrapPath, bubblewrapProbe = path, "/bin/true"
	if err := sandboxUnavailable(); err != nil {
		t.Fatalf("preflight failed against a conforming stub: %v", err)
	}
	recorded, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	want := "--ro-bind\n/\n/\n--proc\n/proc\n--\n/bin/true\n"
	if string(recorded) != want {
		t.Errorf("probe arguments = %q, want %q", recorded, want)
	}
}

// The supervisor writes the reason it receives straight into the application
// log, so the reporting path has to survive the one message that matters: a
// library error that quotes the address it failed to load.
func TestReportedReasonsCannotCarryTheAuthenticatedAddress(t *testing.T) {
	cases := map[string]string{
		"a load failure":     "Unable to load page http://127.0.0.1:41234/#ticket=9f8e7d6c5b4a failed",
		"a connection error": "Could not connect to http://[::1]:41234/: Connection refused",
		"a WebKit message":   "http://127.0.0.1:41234/#ticket=deadbeef did not finish loading",
	}
	for name, reason := range cases {
		got := bound(reason)
		for _, secret := range []string{"http", "127.0.0.1", "::1", "ticket", "41234"} {
			if strings.Contains(got, secret) {
				t.Errorf("%s: reported reason still contains %q: %q", name, secret, got)
			}
		}
		if got == "" {
			t.Errorf("%s: reported reason became empty", name)
		}
	}
}

func TestReportedReasonsStayOneShortLine(t *testing.T) {
	got := bound("the window failed\n\tbecause of something\nvery long that keeps going " + strings.Repeat("x", 400))
	if strings.ContainsAny(got, "\n\r\t") {
		t.Errorf("reported reason is not one line: %q", got)
	}
	if len(got) > 200 {
		t.Errorf("reported reason is %d bytes, want at most 200", len(got))
	}
}

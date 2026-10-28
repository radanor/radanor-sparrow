//go:build linux && cgo && gtk

package gtkwindow

// The package's other tests read source and pure functions; this one starts the
// real window and measures what the operating system and WebKit actually did.
// It is the evidence for the L3 claims: the dashboard is rendered in a confined
// process, and the page inside it cannot leave the dashboard.
//
// It needs a display and WebKitGTK, so it skips where either is absent - a build
// machine without a session must not fail the suite - and it never uses the
// user's own WebKit data directory: the view gets a temporary one.
//
// Everything is measured from one window. GTK is initialized on the thread that
// runs the loop and the main loop is not re-entered, because a second gtk_main in
// the same process after the first loop has ended is not a supported pattern.
// The page reports its own experiments over loopback, which also avoids asking
// GTK for anything from the test's thread while the loop is running.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestNativeWindowConfinesTheDashboard is the measured claim. In one window it
// checks, in order, that the renderer is in WebKit's sandbox, that the page
// cannot open a popup, read a local file, reach an external host, or navigate
// away from the dashboard.
func TestNativeWindowConfinesTheDashboard(t *testing.T) {
	if testing.Short() {
		t.Skip("the native window needs a display and WebKitGTK; skipped in -short")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if !Available() {
		t.Skip("no usable GTK/WebKitGTK display on this host; the sandbox can only be measured with a live renderer")
	}

	reports := make(chan string, 16)
	server := httptest.NewServer(reportHandler(t, reports, dashboardPage()))
	defer server.Close()

	run := make(chan error, 1)
	loadFailure := make(chan string, 1)
	loaded := make(chan struct{})
	var loadedOnce sync.Once
	go func() {
		run <- Run(Options{
			URL:     server.URL + "/",
			Title:   "sandbox probe",
			DataDir: filepath.Join(t.TempDir(), "webkit"),
			Allow:   SameOrigin(server.URL),
			// Every finished load reports here, including the same-origin
			// redirect navigation below; only the first one is readiness.
			Loaded: func() { loadedOnce.Do(func() { close(loaded) }) },
			Failed: func(reason string) { loadFailure <- reason },
			Closed: func() {},
		})
	}()
	// WebKit disposes its subprocesses through the UI process's exit, so this
	// test does not assert that they are gone while the test binary itself keeps
	// running; the supervisor-level lifecycle run measures that with the helper's
	// real exit (no leftover process, zombie or WebKit child).
	defer func() {
		Quit()
		select {
		case <-run:
		case <-time.After(20 * time.Second):
		}
	}()
	select {
	case reason := <-loadFailure:
		t.Fatalf("the dashboard did not load: %s", reason)
	case <-loaded:
	case <-time.After(45 * time.Second):
		t.Fatal("the dashboard did not load within 45s")
	}

	// --- the renderer process itself ---
	renderer, wrapper := findRenderer(t, os.Getpid())
	if renderer == 0 {
		t.Fatal("no WebKit web process was found among the window's descendants, so the dashboard cannot be running in a sandboxed renderer")
	}
	if wrapper == 0 {
		t.Error("the renderer has no bubblewrap ancestor; WebKitGTK started it without its process sandbox")
	}

	status := readProcFile(t, renderer, "status")
	for _, want := range []struct{ field, value, why string }{
		{"Seccomp", "2", "the renderer is under a seccomp filter"},
		{"NoNewPrivs", "1", "the renderer cannot gain privileges"},
		{"CapEff", "0000000000000000", "the renderer holds no capabilities"},
	} {
		if got := statusField(status, want.field); got != want.value {
			t.Errorf("renderer %s = %q, want %q (%s)", want.field, got, want.value, want.why)
		}
	}

	for _, namespace := range []string{"user", "pid", "mnt", "net", "uts"} {
		ours, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(os.Getpid()), "ns", namespace))
		if err != nil {
			t.Fatalf("cannot read this process's %s namespace: %v", namespace, err)
		}
		theirs, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(renderer), "ns", namespace))
		if err != nil {
			t.Fatalf("cannot read the renderer's %s namespace: %v", namespace, err)
		}
		if ours == theirs {
			t.Errorf("the renderer shares this process's %s namespace (%s); it is not confined", namespace, ours)
		}
	}

	// The renderer runs as one unmapped-to-root uid: that is the unprivileged
	// user namespace bubblewrap created for it.
	uid := strconv.Itoa(os.Getuid())
	mapping := strings.Fields(readProcFile(t, renderer, "uid_map"))
	if len(mapping) != 3 || mapping[2] != "1" || mapping[1] != uid || (mapping[0] != uid && mapping[0] != "0") {
		t.Errorf("renderer uid_map = %q, want a single id mapped to uid %s", strings.Join(mapping, " "), uid)
	}

	// --- what the page inside that renderer could do ---
	observed := map[string]string{}
	deadline := time.After(30 * time.Second)
	for len(observed) < 7 {
		select {
		case report := <-reports:
			key, value, found := strings.Cut(report, "=")
			if found {
				observed[key] = value
			}
		case <-deadline:
			t.Fatalf("the page did not report every experiment (missing %d): %v", 7-len(observed), observed)
		}
	}
	if observed["popup"] != "blocked" {
		t.Errorf("window.open returned %q, want the popup blocked", observed["popup"])
	}
	if observed["file"] != "denied" {
		t.Errorf("a local file read from the page was %q, want denied", observed["file"])
	}
	if observed["external"] != "refused" {
		t.Errorf("an external fetch from the page was %q, want refused", observed["external"])
	}
	if got, want := observed["still-here"], server.URL+"/"; got != want {
		t.Errorf("after requesting a top-level external navigation the page is at %q, want %q", got, want)
	}
	if got, want := observed["after-file"], server.URL+"/"; got != want {
		t.Errorf("after requesting a top-level file navigation the page is at %q, want %q", got, want)
	}
	// The URL may be the redirecting path or the dashboard root; what matters is
	// that the redirect target was refused and the page stayed on the origin.
	if got := observed["after-redirect"]; !strings.HasPrefix(got, server.URL+"/") {
		t.Errorf("a server redirect to an external host left the page at %q, want the dashboard origin %q", got, server.URL)
	}
}

// dashboardPage is the test dashboard. Its script runs inside the sandboxed
// renderer and reports what the window let it do, which is the only vantage
// point that proves the policy from the page's side. Every navigation attempt is
// scheduled after the report that precedes it, so a report that never arrives is
// itself evidence that the page left the dashboard.
func dashboardPage() string {
	return `<!doctype html><meta charset="utf-8"><title>dashboard</title>
<body><h1>dashboard</h1>
<script>
const report = (key, value) => fetch('/report?key=' + key + '&value=' + encodeURIComponent(value)).catch(() => {});
addEventListener('load', () => {
  report('loaded', document.title);
  const popup = window.open('https://example.com/popup', '_blank');
  report('popup', popup === null ? 'blocked' : 'opened');
  try {
    const request = new XMLHttpRequest();
    request.open('GET', 'file:///etc/passwd', false);
    request.send();
    report('file', 'read');
  } catch (error) {
    report('file', 'denied');
  }
  report('still-here', location.href);
  // Order matters: a full document navigation would cancel a pending fetch, so
  // the external request is made last, after every navigation attempt has been
  // observed (all of which are refused, so this document stays alive).
  setTimeout(() => {
    location.href = 'https://example.com/top-level';
  }, 100);
  setTimeout(() => {
    location.href = '/redirect';
    setTimeout(() => report('after-redirect', location.href), 900);
  }, 300);
  setTimeout(() => {
    location.href = 'file:///etc/passwd';
    setTimeout(() => report('after-file', location.href), 800);
  }, 1500);
  setTimeout(() => {
    fetch('https://example.com/exfiltration').then(
      () => report('external', 'reached'),
      () => report('external', 'refused'),
    );
  }, 2600);
});
</script></body>`
}

// reportHandler serves the page and records the page's reports in channel.
func reportHandler(t *testing.T, channel chan<- string, page string) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	})
	// A same-origin address that answers with a redirect to another host: the
	// navigation policy has to refuse the redirect target, not just direct loads.
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/redirected", http.StatusFound)
	})
	mux.HandleFunc("/report", func(w http.ResponseWriter, r *http.Request) {
		select {
		case channel <- r.URL.Query().Get("key") + "=" + r.URL.Query().Get("value"):
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

// findRenderer locates the live WebKit web process among this process's
// descendants and the bubblewrap process that started it. Returning zero means
// the sandbox is not in use.
func findRenderer(t *testing.T, root int) (renderer, wrapper int) {
	t.Helper()
	parents := processParents(t)
	descendants := map[int]bool{root: true}
	for changed := true; changed; {
		changed = false
		for pid, parent := range parents {
			if descendants[parent] && !descendants[pid] {
				descendants[pid] = true
				changed = true
			}
		}
	}
	for pid := range descendants {
		if pid == root {
			continue
		}
		// The program is argv[0]. A bubblewrap wrapper carries the whole
		// sandboxed command line, including WebKitWebProcess, so matching the
		// substring would confuse the wrapper with the process it started.
		fields := strings.Fields(readProcFile(t, pid, "cmdline"))
		if len(fields) == 0 {
			continue
		}
		switch program := filepath.Base(fields[0]); {
		case strings.HasPrefix(program, "WebKitWebProcess"):
			renderer = pid
		case program == "bwrap", program == "bubblewrap":
			wrapper = pid
		}
	}
	return renderer, wrapper
}

// processParents reads the parent of every process, which is how the test knows
// which WebKit processes belong to the window it started rather than to another
// application on the same desktop.
func processParents(t *testing.T) map[int]int {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatalf("cannot read /proc: %v", err)
	}
	parents := make(map[int]int, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue
		}
		// The command name is parenthesised and may contain spaces, so the
		// parent is the field after the closing parenthesis.
		closing := strings.LastIndex(string(stat), ")")
		if closing < 0 {
			continue
		}
		fields := strings.Fields(string(stat)[closing+1:])
		if len(fields) < 2 {
			continue
		}
		parent, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		parents[pid] = parent
	}
	return parents
}

// readProcFile reads one file of a process's /proc entry, with NULs in cmdline
// turned into spaces so it is searchable.
func readProcFile(t *testing.T, pid int, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), name))
	if err != nil {
		t.Fatalf("cannot read /proc/%d/%s: %v", pid, name, err)
	}
	return strings.ReplaceAll(string(content), "\x00", " ")
}

// statusField returns one "Name:\tvalue" field of a /proc status file.
func statusField(status, name string) string {
	for _, line := range strings.Split(status, "\n") {
		key, value, found := strings.Cut(line, ":")
		if found && key == name {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

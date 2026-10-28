//go:build linux

// Command radanor-sparrow-window is the Linux presentation component: it opens
// the native Radanor Sparrow window and shows the authenticated dashboard in it.
//
// It is a separate process on purpose. The monitoring core and the supervisor
// stay CGO-free and static, and a machine with no GTK or WebKitGTK still runs
// the product — the supervisor starts this helper only if it is installed, and
// falls back to the authenticated browser when it cannot run.
//
// The helper is a client of the existing security boundary, not a new one. It
// reads the published loopback origin, validates it with the same rule the
// supervisor uses, takes the existing single-use launch ticket over the
// authenticated IPC socket, and shows that URL. It registers no JavaScript-to-
// native bridge: the dashboard can ask for nothing but navigation, and only into
// its own origin. Nothing it prints contains the origin, the ticket or a
// session value.
//
// It reports exactly one bounded line to the supervisor that started it,
// through the inherited file descriptor named by
// RADANOR_SPARROW_WINDOW_READY_FD: "ready" once the dashboard has finished
// loading, or "error: <reason>". The supervisor logs that reason and opens the
// browser fallback.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/radanor/radanor-shield/internal/branding"
	"github.com/radanor/radanor-shield/internal/config"
	"github.com/radanor/radanor-shield/internal/desktop"
	"github.com/radanor/radanor-shield/internal/gateway"
	"github.com/radanor/radanor-shield/internal/gtkwindow"
	"github.com/radanor/radanor-shield/internal/ipc"
	sparrowlog "github.com/radanor/radanor-shield/internal/logging"
	"github.com/radanor/radanor-shield/internal/privatefs"
)

func main() {
	runtimeDir := flag.String("runtime-dir", "", "private runtime directory that holds the loopback origin and the IPC secret")
	stateDir := flag.String("state-dir", "", "private state directory that holds the window's own log and view data")
	originTimeout := flag.Duration("origin-timeout", 20*time.Second, "how long to wait for the supervisor to publish the loopback origin")
	loadTimeout := flag.Duration("load-timeout", 20*time.Second, "how long to wait for the dashboard to finish loading")
	flag.Parse()

	status := openStatus()
	code := run(*runtimeDir, *stateDir, *originTimeout, *loadTimeout, status)
	os.Exit(code)
}

func run(runtimeDir, stateDir string, originTimeout, loadTimeout time.Duration, status *statusReporter) int {
	if gateway.ValidateNotElevated() != nil {
		return status.fail("Radanor Sparrow must run as a normal user, without root")
	}
	cfg, err := config.Load()
	if err != nil {
		return status.fail("cannot load the application configuration")
	}
	// Flags exist so the supervisor can point the helper at an isolated profile;
	// the defaults are the user's own private directories.
	if runtimeDir != "" {
		cfg.RuntimeDir = filepath.Clean(runtimeDir)
	}
	if stateDir != "" {
		cfg.StateDir = filepath.Clean(stateDir)
	}
	if err := config.EnsurePrivateDirs(cfg); err != nil {
		return status.fail("cannot protect the private application directories")
	}

	logger, closeLog, err := openWindowLog(cfg.StateDir)
	if err != nil {
		return status.fail("cannot open the private window log")
	}
	defer closeLog()

	// A GUI probe before anything else: a machine without a usable display must
	// fail with one clear sentence, because the supervisor is going to offer the
	// authenticated browser instead. The sentence keeps its prefix, which operators
	// and the smoke test match on, and adds the one fact that separates the two
	// causes: a session with no display at all, or a display GTK cannot open.
	if !gtkwindow.Available() {
		message := "the GTK/WebKit GUI runtime is unavailable: GTK could not open the display"
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			message = "the GTK/WebKit GUI runtime is unavailable: DISPLAY and WAYLAND_DISPLAY are unset, so there is no graphical session to open a window on"
		}
		logger.Warn("native window unavailable", "reason", message)
		return status.fail(message)
	}
	// The renderer sandbox is the reason this window is safe to show at all, so its
	// prerequisites are measured before a window exists instead of being discovered
	// as a fatal library abort inside window creation. WebKitGTK warns and then
	// aborts the whole process when it cannot start its sandbox helper, which would
	// end this component with no reason for the supervisor to log.
	if err := sandboxUnavailable(); err != nil {
		logger.Warn("native window unavailable", "reason", err.Error())
		return status.fail(err.Error())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	origin, err := waitForOrigin(ctx, cfg.RuntimeDir, originTimeout)
	if err != nil {
		logger.Warn("native window unavailable", "reason", err.Error())
		return status.fail(err.Error())
	}
	secret, err := ipc.LoadSecret(ipc.SecretPath(cfg.RuntimeDir))
	if err != nil {
		logger.Warn("native window unavailable", "reason", "the private IPC secret is not readable")
		return status.fail("the private IPC secret is not readable")
	}
	url, err := dashboardURL(ctx, cfg, secret, origin)
	if err != nil {
		logger.Warn("native window unavailable", "reason", err.Error())
		return status.fail(err.Error())
	}

	loaded := make(chan struct{})
	var loadedOnce sync.Once
	failed := make(chan string, 1)
	closed := make(chan struct{}, 1)
	options := gtkwindow.Options{
		URL:     url,
		Title:   branding.Product,
		DataDir: filepath.Join(cfg.StateDir, "webkit"),
		// Only this dashboard's own origin may be loaded; every other navigation
		// is ignored, and the helper never hands a URL to the desktop.
		Allow: gtkwindow.SameOrigin(origin),
		// WebKit reports a finished load for every document, not just the first:
		// the dashboard navigating within its own origin (or following a
		// same-origin redirect) fires this again, and closing the channel twice
		// would panic on the GTK thread. Only the first finish is the readiness
		// signal, and the window keeps running after it.
		Loaded: func() { loadedOnce.Do(func() { close(loaded) }) },
		Failed: func(reason string) {
			select {
			case failed <- reason:
			default:
			}
		},
		Closed: func() {
			// Nothing about the authenticated session is logged. The exit policy
			// (hide instead of quit) arrives with the lifecycle milestone.
			select {
			case closed <- struct{}{}:
			default:
			}
			logger.Info("window closed")
			gtkwindow.Quit()
		},
	}

	window := make(chan error, 1)
	go func() { window <- gtkwindow.Run(options) }()
	// The supervisor owns this process's lifetime. It starts the helper with
	// Pdeathsig, and an explicit SIGTERM arrives when it stops or when the user
	// chooses Exit, so the signal has to end the window rather than be swallowed:
	// a helper that ignored it would outlive monitoring with a window on screen.
	go func() {
		<-ctx.Done()
		gtkwindow.Quit()
	}()

	select {
	case <-ctx.Done():
		gtkwindow.Quit()
		return status.fail("the native window was stopped before the dashboard loaded")
	case err := <-window:
		// Creation failed before the dashboard could load; the supervisor's own
		// message covers the fallback, and this one covers the reason.
		if err == nil {
			err = errors.New("the native window closed before the dashboard loaded")
		}
		return status.fail("the native window did not open: " + err.Error())
	case reason := <-failed:
		gtkwindow.Quit()
		return status.fail("the dashboard did not load: " + reason)
	case <-closed:
		<-window
		return status.fail("the window was closed before the dashboard loaded")
	case <-time.After(loadTimeout):
		gtkwindow.Quit()
		return status.fail("the dashboard did not finish loading in time")
	case <-loaded:
	}

	// The dashboard is on screen and authenticated. From here the window owns
	// the process: the supervisor's readiness contract is satisfied, and the
	// process ends when the user closes the window or the supervisor exits.
	logger.Info("dashboard window shown", "title", branding.Product, "icon", gtkwindow.HasIcon())
	status.ready()
	if err := <-window; err != nil {
		logger.Warn("the native window ended with an error", "reason", err.Error())
		return 1
	}
	return 0
}

// Paths and timing of the sandbox preflight. WebKitGTK starts its own bubblewrap
// helper from this exact path, so the same path is what a missing bubblewrap has
// to be reported for. They are variables so tests can point them at a temporary
// tree and prove both the failure report and the accepted case.
var (
	bubblewrapPath     = "/usr/bin/bwrap"
	bubblewrapProbe    = "/bin/true"
	sandboxProbeWindow = 10 * time.Second
	// apparmorUserNamespaceControl is the kernel setting Ubuntu 24.04 introduced
	// to restrict unprivileged user namespaces to binaries whose AppArmor profile
	// grants `userns`. It is a file rather than a sysctl read so a test can state
	// the policy instead of inheriting the test machine's.
	apparmorUserNamespaceControl = "/proc/sys/kernel/apparmor_restrict_unprivileged_userns"
)

// apparmorBlockedUserNamespaces reports whether this host restricts unprivileged
// user namespaces and whether the probe's failure is the permission refusal that
// restriction produces. Both are required before AppArmor is named: a probe can
// fail for unrelated reasons on a restricted host, and blaming the policy for
// those would send an administrator to the wrong place.
//
// A missing or unreadable setting means "not restricted": every release before
// 24.04 lacks the file, and the sandbox is judged on the probe alone there.
func apparmorBlockedUserNamespaces(detail string) bool {
	raw, err := os.ReadFile(apparmorUserNamespaceControl)
	if err != nil || strings.TrimSpace(string(raw)) != "1" {
		return false
	}
	for _, marker := range []string{"uid map", "Permission denied", "Operation not permitted"} {
		if strings.Contains(detail, marker) {
			return true
		}
	}
	return false
}

// sandboxUnavailable returns the reason the WebKit subprocess sandbox cannot be
// used on this system, or nil when it can be. It measures what the sandbox
// actually needs rather than trusting that the library call was made: bubblewrap
// must exist and be executable, and it must be able to create a namespace for this
// user, which a sysctl or an AppArmor profile can forbid on newer releases.
//
// This is not a way to run without the sandbox. When bubblewrap cannot be used,
// this component reports the reason and presents no window; the supervisor shows
// the authenticated browser instead.
func sandboxUnavailable() error {
	info, err := os.Stat(bubblewrapPath)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return fmt.Errorf("the WebKit sandbox needs bubblewrap at %s, which is not present and executable", bubblewrapPath)
	}
	ctx, cancel := context.WithTimeout(context.Background(), sandboxProbeWindow)
	defer cancel()
	// A read-only mount of the running system is the least the helper needs; if it
	// cannot be created, the real sandbox cannot either.
	probe := exec.CommandContext(ctx, bubblewrapPath, "--ro-bind", "/", "/", "--proc", "/proc", "--", bubblewrapProbe)
	output, err := probe.CombinedOutput()
	if err == nil {
		return nil
	}
	detail := bound(strings.TrimSpace(string(output)))
	if detail == "" {
		detail = err.Error()
	}
	// Ubuntu 24.04 restricts unprivileged user namespaces through AppArmor, so
	// bubblewrap is stopped while writing the namespace's uid map. The generic
	// message would leave an administrator with a permission error and no policy
	// to look at; this names the setting, and still offers the browser, which the
	// supervisor is about to open anyway. The window stays refused either way.
	if apparmorBlockedUserNamespaces(detail) {
		return fmt.Errorf("AppArmor blocks unprivileged user namespaces (kernel.apparmor_restrict_unprivileged_userns=1), so the WebKit sandbox cannot start; use the browser or a reviewed bwrap profile")
	}
	return fmt.Errorf("the WebKit sandbox cannot start bubblewrap at %s here: %s", bubblewrapPath, detail)
}

// waitForOrigin reads the discovery record the supervisor publishes, using the
// supervisor's own validation so both sides agree on what a legitimate origin is.
func waitForOrigin(ctx context.Context, runtimeDir string, timeout time.Duration) (string, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", errors.New("the native window was stopped before the dashboard was ready")
		case <-deadline.C:
			return "", errors.New("the dashboard origin was not published in time")
		case <-ticker.C:
			file, err := os.Open(filepath.Join(runtimeDir, "desktop-origin"))
			if err != nil {
				continue
			}
			value, err := io.ReadAll(io.LimitReader(file, 129))
			_ = file.Close()
			if err != nil || len(value) > 128 {
				continue
			}
			if origin := string(value); desktop.ValidOrigin(origin) {
				return origin, nil
			}
		}
	}
}

// dashboardURL asks the running core for one launch ticket over the existing
// authenticated IPC socket. The ticket is single-use and carries no authority of
// its own: the gateway still requires the exchange request to come from this
// origin.
func dashboardURL(ctx context.Context, cfg config.Config, secret []byte, origin string) (string, error) {
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	response, err := ipc.Call(probe, cfg.RuntimeDir, secret, "issue_ticket", "")
	if err != nil || !response.OK || response.Value == "" {
		return "", errors.New("cannot issue a dashboard launch ticket")
	}
	return origin + "/#ticket=" + response.Value, nil
}

// openWindowLog keeps this component's diagnostics in the user's own private
// state directory, like the supervisor's application log.
func openWindowLog(stateDir string) (*slog.Logger, func(), error) {
	path := filepath.Join(stateDir, "desktop-window.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, func() {}, err
	}
	if err := privatefs.ProtectFile(path); err != nil {
		_ = file.Close()
		return nil, func() {}, err
	}
	return sparrowlog.New(file, slog.LevelInfo), func() { _ = file.Close() }, nil
}

// statusReporter writes this helper's single bounded line to the supervisor.
// Only the first call is sent: the supervisor has already made its decision.
type statusReporter struct {
	file *os.File
	once sync.Once
}

func openStatus() *statusReporter {
	value := os.Getenv("RADANOR_SPARROW_WINDOW_READY_FD")
	if value == "" {
		return &statusReporter{}
	}
	fd, err := parseFileDescriptor(value)
	if err != nil {
		return &statusReporter{}
	}
	return &statusReporter{file: os.NewFile(fd, "window-status")}
}

func parseFileDescriptor(value string) (uintptr, error) {
	fd, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || fd < 3 || fd > 1024 {
		return 0, errors.New("unusable readiness descriptor")
	}
	return uintptr(fd), nil
}

func (s *statusReporter) ready() {
	s.send("ready")
}

// fail reports a reason and returns the exit code for a helper that did not
// become usable, which is what makes the supervisor use the browser fallback.
func (s *statusReporter) fail(reason string) int {
	s.send("error: " + reason)
	return 1
}

func (s *statusReporter) send(message string) {
	s.once.Do(func() {
		if s.file == nil {
			return
		}
		_, _ = s.file.WriteString(bound(message) + "\n")
		_ = s.file.Close()
	})
}

// addressPattern matches anything URL-shaped. A reason reported here comes from a
// library, and a library message can quote the address it failed to load; the
// supervisor writes this line into the application log, so the address is
// removed before it can get there. The ticket lives in the fragment and never
// belongs to a library message, but the whole address goes anyway.
var addressPattern = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://\S*`)

// bound keeps a status line to one short line: the supervisor logs it verbatim,
// and neither a stack trace nor a wrapped error may become a log flood.
func bound(message string) string {
	const limit = 200
	message = addressPattern.ReplaceAllString(message, "<address>")
	message = strings.Join(strings.Fields(strings.ReplaceAll(message, "\x00", " ")), " ")
	if len(message) > limit {
		return message[:limit]
	}
	return message
}

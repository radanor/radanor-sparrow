package gtkwindow

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/radanor/radanor-shield/internal/branding"
)

// The window is a presentation surface with a deliberately small contract. These
// assertions read the source of this package, of the helper command and of the
// supervisor's Linux integration, and fail when something is added that would
// widen it: a JavaScript-to-native bridge, a weakened sandbox, a second window,
// or a way for the authenticated address to reach a log. The Windows window has
// its own contract test for its surface; neither is changed by the other.
func TestWindowKeepsItsSmallContract(t *testing.T) {
	sources := map[string]string{
		"shell.h":                            mustRead(t, "shell.h"),
		"gtkwindow.go":                       mustRead(t, "gtkwindow.go"),
		"callbacks.go":                       mustRead(t, "callbacks.go"),
		"stub.go":                            mustRead(t, "stub.go"),
		"navigation.go":                      mustRead(t, "navigation.go"),
		"cmd/radanor-sparrow-window/main.go": mustRead(t, filepath.Join("..", "..", "cmd", "radanor-sparrow-window", "main.go")),
		"platform_linux.go":                  mustRead(t, filepath.Join("..", "desktop", "platform_linux.go")),
	}

	for name, source := range sources {
		for _, forbidden := range []string{
			"register_script_message_handler", // no JavaScript can call into Go
			"register_uri_scheme",             // no privileged scheme is installed
			"set_enable_developer_extras",     // no developer tooling in a shipped window
			"enable_developer_extras",
			"run_javascript", // nothing is injected into, or read out of, the page
			"evaluate_javascript",
			"--no-sandbox",           // a weakened WebKit sandbox is never the fix
			"WEBKIT_DISABLE_SANDBOX", // nor is switching it off from the environment
			"WEBKIT_FORCE_SANDBOX",   // nor is deciding it from the environment at all
			"disable_sandbox",
			"set_sandbox_enabled(radanor_window_current->context, FALSE)", // the subprocess sandbox is only ever turned on
		} {
			if strings.Contains(source, forbidden) {
				t.Errorf("%s contains %q, which would widen the window's contract", name, forbidden)
			}
		}
	}

	// Denial is wired to the one WebKit call that ignores a navigation, the
	// decision itself is made in Go, and a requested second window is refused.
	if !strings.Contains(sources["shell.h"], "webkit_policy_decision_ignore") {
		t.Error("shell.h does not ignore denied navigations")
	}
	if !strings.Contains(sources["shell.h"], "decide-policy") {
		t.Error("shell.h does not install the navigation policy handler")
	}
	if !strings.Contains(sources["callbacks.go"], "radanorGoDecideNavigation") {
		t.Error("the navigation decision is not made in Go")
	}
	if !strings.Contains(sources["shell.h"], `"create"`) {
		t.Error("shell.h does not refuse requests for a second window")
	}
	// The embosser-side navigation entry point applies the same rule before it
	// reaches the engine: WebKit does not consult the policy handler for every
	// scheme when the application itself asks for a load, so a `javascript:` URL
	// would otherwise replace the view's address without a page ever changing.
	if !strings.Contains(sources["gtkwindow.go"], "!options.Allow(uri)") {
		t.Error("Navigate does not apply the navigation rule before asking WebKit to load a URI")
	}

	// The renderer sandbox is requested, and requested at the only moment WebKit
	// accepts it: before the view that spawns the first web process. Calling it
	// afterwards is a fatal error in WebKit, and not calling it at all leaves the
	// subprocesses unsandboxed, so both the call and its position are pinned here.
	const enableSandbox = "webkit_web_context_set_sandbox_enabled(radanor_window_current->context, TRUE)"
	sandbox := strings.Index(sources["shell.h"], enableSandbox)
	view := strings.Index(sources["shell.h"], "webkit_web_view_new_with_context")
	switch {
	case sandbox < 0:
		t.Error("shell.h never enables the WebKit subprocess sandbox, so the window shows the dashboard in unsandboxed processes")
	case view < 0:
		t.Error("shell.h does not create the web view where expected")
	case sandbox > view:
		t.Error("the sandbox is enabled after the web view is created; WebKit makes that a fatal error and the window would be unsandboxed")
	}

	// The URL carries the single-use launch ticket, so this package must have no
	// logging at all, and every reason the helper reports must be scrubbed of
	// anything that looks like an address before it can reach the supervisor's
	// log.
	if strings.Contains(sources["gtkwindow.go"], "log/slog") {
		t.Error("gtkwindow.go logs; the authenticated URL must not be able to reach a log")
	}
	if !strings.Contains(sources["cmd/radanor-sparrow-window/main.go"], "ReplaceAllString(message, \"<address>\")") {
		t.Error("the helper does not scrub addresses out of the reasons it reports")
	}

	// The window is described as the product, and it is the only Linux
	// presentation surface: a second implementation would be a second thing to
	// audit.
	if !strings.Contains(sources["cmd/radanor-sparrow-window/main.go"], "Title:   branding.Product") {
		t.Errorf("the native window title is not the product name %q", branding.Product)
	}
	command := sources["cmd/radanor-sparrow-window/main.go"]
	if runs := strings.Count(command, "gtkwindow.Run("); runs != 1 {
		t.Errorf("the helper calls gtkwindow.Run %d times; it must present exactly one window", runs)
	}
	// WebKit reports a finished load per document, so a navigation inside the
	// dashboard fires the readiness callback again. Closing the channel twice
	// would panic on the GTK thread, and the window would die on a same-origin
	// redirect; the callback is therefore one-shot.
	if !strings.Contains(command, "loadedOnce.Do(func() { close(loaded) })") {
		t.Error("the helper's readiness signal is not one-shot; a second in-origin load would panic")
	}
}

// The supervisor must never hand the helper the authenticated address or the
// launch ticket on its command line: a process listing is world-readable, and the
// Windows window can pass them in memory. This pins the whole argument list.
func TestSupervisorGivesTheHelperOnlyPrivateDirectories(t *testing.T) {
	source := mustRead(t, filepath.Join("..", "desktop", "platform_linux.go"))
	if strings.Contains(source, "#ticket=") {
		t.Error("the supervisor builds a ticket URL for the helper; the helper must obtain the ticket itself over IPC")
	}
	if !regexp.MustCompile(`exec\.Command\(helper, "-runtime-dir", runtimeDir, "-state-dir", stateDir\)`).MatchString(source) {
		t.Error("the helper's argument list changed; it must carry only the private directories")
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

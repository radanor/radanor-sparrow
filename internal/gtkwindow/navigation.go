package gtkwindow

import (
	"errors"
	"net/url"
	"strings"
)

// ErrUnsupported is what Run reports when this build has no native window: a
// non-Linux platform, a build without cgo, or a helper built without `-tags
// gtk`. It exists in every build so a caller can test for it with errors.Is and
// fall back to the authenticated browser.
var ErrUnsupported = errors.New("this build has no native GTK/WebKitGTK window; the window helper must be built with -tags gtk where GTK3 and WebKitGTK 4.1 are installed")

// SameOrigin returns the navigation rule for a window that is pointed at
// origin: only that exact origin, over http, may be loaded.
//
// The rule is deliberately narrow, and it is the one rule the native window
// applies — the same function the C policy handler asks Go for, so there is no
// second copy of it in C. Everything else is refused, including file:, data: and
// javascript: URLs, a different loopback port, and any external host. It does
// not decide whether the origin itself is trustworthy: the caller validates the
// published loopback origin before the window is created.
func SameOrigin(origin string) func(uri string) bool {
	return func(uri string) bool {
		allowed, err := url.Parse(origin)
		if err != nil || allowed.Hostname() == "" || allowed.Port() == "" {
			return false
		}
		requested, err := url.Parse(uri)
		if err != nil {
			return false
		}
		// The dashboard is served over plain http on the loopback interface, so
		// anything else is refused rather than trusted.
		if !strings.EqualFold(requested.Scheme, "http") {
			return false
		}
		if !strings.EqualFold(requested.Hostname(), allowed.Hostname()) {
			return false
		}
		return requested.Port() == allowed.Port()
	}
}

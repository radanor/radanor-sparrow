//go:build linux && cgo && gtk

// Package gtkwindow is the Linux presentation component: a GTK3 window with a
// WebKitGTK 4.1 view that shows the authenticated Sparrow dashboard.
//
// It is deliberately not part of the monitoring core. The core stays CGO-free,
// and this package is compiled only into the separate helper process
// (cmd/radanor-sparrow-window) and only when it is asked for with `-tags gtk`,
// because a machine without the GTK/WebKitGTK development files must still be
// able to build and run everything else. the private engineering record
// records why the window cannot be hard-linked into the main binary: the loader
// resolves a hard link before any Go code runs, which would destroy the browser
// fallback.
//
// The window is a presentation surface only. It reuses the existing loopback
// gateway, the existing session and the existing single-use launch ticket, and
// it registers no JavaScript-to-native bridge whatsoever: the only thing the
// dashboard can ask of this process is to navigate, and only its own origin is
// allowed.
package gtkwindow

/*
#cgo pkg-config: gtk+-3.0 webkit2gtk-4.1
#include "shell.h"
*/
import "C"

import (
	_ "embed"
	"errors"
	"os"
	"runtime"
	"sync"
	"unsafe"

	"github.com/radanor/radanor-shield/internal/branding"
)

// The approved Radanor artwork, byte for byte from web/public/favicon.ico. Its
// digest is pinned by the tests in this package and in internal/desktop, so the
// native window can never show redrawn or recoloured artwork.
//
//go:embed assets/radanor.ico
var approvedIcon []byte

// Options describe the one window this process shows.
type Options struct {
	// URL is the authenticated loopback dashboard URL, including the single-use
	// launch ticket. It is never logged.
	URL string
	// Title is the native window title. It defaults to the product name.
	Title string
	// DataDir is a private directory for the view's cookies and cache. It is
	// created if missing.
	DataDir string
	// Width and Height are the initial window size in logical pixels.
	Width, Height int
	// Allow decides whether the view may navigate to a URI. A navigation it
	// rejects is ignored by WebKit, never handed to the desktop.
	Allow func(uri string) bool
	// Loaded reports that the dashboard document finished loading. It is the
	// point at which the window is genuinely usable.
	Loaded func()
	// Failed reports that the dashboard document could not be loaded.
	Failed func(reason string)
	// Closed reports that the user asked to close the window.
	Closed func()
}

// session is the window currently being run. GTK signals arrive on the main
// thread while Run is executing, so it is only ever read from there, but it is
// written by the goroutine that calls Run.
var (
	sessions sync.Mutex
	session  *Options
)

func setSession(options *Options) {
	sessions.Lock()
	defer sessions.Unlock()
	session = options
}

func currentSession() *Options {
	sessions.Lock()
	defer sessions.Unlock()
	return session
}

// Available reports whether a native window can actually be shown here: it
// probes GTK, so a missing or unusable display is a false rather than a crash.
// Call it on the main goroutine, before Run.
func Available() bool {
	return C.radanor_window_available() != 0
}

// Run creates the window, shows the authenticated dashboard in it and does not
// return until the window is gone or Quit is called. GTK is driven on one
// thread, so Run locks the calling goroutine to its operating-system thread
// itself rather than relying on every caller to remember. Run it on the same
// goroutine that called Available: GTK was initialized there, and a window
// created on a different thread is not a supported GTK pattern.
func Run(options Options) error {
	if options.URL == "" {
		return errors.New("no dashboard URL was given")
	}
	if options.DataDir == "" {
		return errors.New("no private directory for the view was given")
	}
	if options.Title == "" {
		options.Title = branding.Product
	}
	if options.Width <= 0 {
		options.Width = 1180
	}
	if options.Height <= 0 {
		options.Height = 780
	}
	if err := os.MkdirAll(options.DataDir, 0o700); err != nil {
		return errors.New("cannot prepare the private view directory")
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	setSession(&options)
	defer setSession(nil)

	url := C.CString(options.URL)
	defer C.free(unsafe.Pointer(url))
	title := C.CString(options.Title)
	defer C.free(unsafe.Pointer(title))
	directory := C.CString(options.DataDir)
	defer C.free(unsafe.Pointer(directory))
	var icon unsafe.Pointer
	if len(approvedIcon) > 0 {
		icon = C.CBytes(approvedIcon)
		defer C.free(icon)
	}

	if C.radanor_window_create(title, url, (*C.guchar)(icon), C.gsize(len(approvedIcon)), directory,
		C.int(options.Width), C.int(options.Height)) == 0 {
		reason := C.GoString(C.radanor_window_error())
		if reason == "" {
			reason = "the native window could not be created"
		}
		return errors.New(reason)
	}
	C.radanor_window_run()
	return nil
}

// Quit asks the window to close and the main loop to end. GTK itself is not
// thread-safe, so this is safe to call from any goroutine: the request is
// deferred onto the main loop.
func Quit() {
	C.radanor_window_quit()
}

// Navigate asks the view to load a URI. The navigation rule is applied here as
// well as in WebKit's policy handler, so a denied URI leaves the current page in
// place instead of being handed to the engine at all: WebKit does not consult
// the policy handler for every scheme when the embedder itself asks for the
// load, and a `javascript:` URL would otherwise replace the view's address
// string. A URI the dashboard's own rule rejects never reaches C.
func Navigate(uri string) {
	if uri == "" {
		return
	}
	options := currentSession()
	if options == nil || options.Allow == nil || !options.Allow(uri) {
		return
	}
	converted := C.CString(uri)
	defer C.free(unsafe.Pointer(converted))
	C.radanor_window_navigate(converted)
}

// Title is the title of the live native window, for tests and diagnostics.
func Title() string {
	return C.GoString(C.radanor_window_title())
}

// URI is the address the view currently shows, for tests and diagnostics. It is
// the authenticated loopback address and is never logged by this package.
func URI() string {
	return C.GoString(C.radanor_window_uri())
}

// HasIcon reports whether the approved artwork was decoded and applied to the
// window. A false here means the window is usable but unbranded.
func HasIcon() bool {
	return C.radanor_window_has_icon() != 0
}

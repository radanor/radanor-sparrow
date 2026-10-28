//go:build !linux || !cgo || !gtk

// This build has no GTK/WebKitGTK window: either the platform is not Linux, cgo
// is switched off for the monitoring core, or the helper was built without
// `-tags gtk`. The package still exists in those builds so the rest of the
// product compiles unchanged, and so every caller gets the same answer: no
// native window, use the authenticated browser instead.
package gtkwindow

// Options mirror the real component so callers compile unchanged.
type Options struct {
	URL           string
	Title         string
	DataDir       string
	Width, Height int
	Allow         func(uri string) bool
	Loaded        func()
	Failed        func(reason string)
	Closed        func()
}

// Available reports that no native window can be shown by this build.
func Available() bool { return false }

// Run always reports that this build has no native window.
func Run(Options) error { return ErrUnsupported }

// Quit does nothing when there is no window.
func Quit() {}

// Navigate does nothing when there is no window.
func Navigate(string) {}

// Title is empty when there is no window.
func Title() string { return "" }

// URI is empty when there is no window.
func URI() string { return "" }

// HasIcon is false when there is no window.
func HasIcon() bool { return false }

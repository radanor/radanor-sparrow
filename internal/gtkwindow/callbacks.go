//go:build linux && cgo && gtk

package gtkwindow

// The C glue in shell.h calls these four functions. They run on the GTK main
// thread, inside the cgo call that runs the main loop, so they must not block and
// must never panic across the language boundary: each one answers with the safe
// default when there is no session.
//
// The //export directives live in this file on purpose: cgo compiles the comment
// immediately above the cgo import into C source, so that comment stays blank
// here and the C implementation lives in shell.h, included by gtkwindow.go.

import "C"

// radanorGoDecideNavigation is the single navigation rule. Returning 0 makes the
// C policy handler ignore the navigation, which is what happens for every URI
// that is not the dashboard's own origin.
//
//export radanorGoDecideNavigation
func radanorGoDecideNavigation(uri *C.char) C.int {
	if uri == nil {
		return 0
	}
	options := currentSession()
	if options == nil || options.Allow == nil {
		return 0
	}
	if options.Allow(C.GoString(uri)) {
		return 1
	}
	return 0
}

//export radanorGoLoaded
func radanorGoLoaded() {
	if options := currentSession(); options != nil && options.Loaded != nil {
		options.Loaded()
	}
}

//export radanorGoLoadFailed
func radanorGoLoadFailed(reason *C.char) {
	message := "the dashboard could not be loaded"
	if reason != nil {
		message = C.GoString(reason)
	}
	if options := currentSession(); options != nil && options.Failed != nil {
		options.Failed(message)
	}
}

//export radanorGoWindowClosed
func radanorGoWindowClosed() {
	if options := currentSession(); options != nil && options.Closed != nil {
		options.Closed()
	}
}

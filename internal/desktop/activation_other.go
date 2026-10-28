//go:build !linux

// Every platform except Linux answers a second launch in its own way: Windows
// owns its window inside the same process and finds it through
// activateDesktopWindow, and a platform without a desktop window has nothing to
// present. There is therefore no request to hand to a running instance here, and
// the shared reopen path keeps its existing browser behaviour.
package desktop

import "errors"

// requestDesktopActivation reports that this platform has no window request
// protocol, so a second launch never waits for one.
func requestDesktopActivation(string) error {
	return errors.New("no dashboard window request protocol on this platform")
}

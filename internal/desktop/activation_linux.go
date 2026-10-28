//go:build linux

package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/radanor/radanor-shield/internal/privatefs"
)

// The Linux reopen protocol is one file in the private runtime directory: a
// launch that finds the instance already running creates it, and the running
// supervisor removes it. Its existence is the whole request — its contents are
// never read, so it can carry nothing, and there is no new socket, no new IPC
// operation and no new privileged surface. Authentication is the same trust the
// rest of the private runtime directory already relies on: the directory is 0700
// and owner-only, so only this user can ask, and a same-user process could
// already start the window component or the launcher entry directly.
//
// Handing the request to the supervisor instead of starting a window here is what
// keeps a single owner of the window's lifetime: a component started by a
// short-lived launcher would either die with it or be left unreaped, and a window
// nothing owns can outlive monitoring.
const activationRequestFile = "desktop-activate"

// desktopActivationPoll is how often the supervisor looks for a request, and how
// long the requester waits for one to be taken. They are variables so tests can
// shorten them; the shipped values keep a double-click responsive while never
// making a launch hang on an instance that will not answer.
var (
	desktopActivationPoll = 500 * time.Millisecond
	desktopActivationWait = 5 * time.Second
)

func activationRequestPath(runtimeDir string) string {
	return filepath.Join(runtimeDir, activationRequestFile)
}

// requestDesktopActivation asks the supervisor that already owns this instance to
// present its dashboard window again, and reports success only once that
// supervisor has taken the request. A timeout means nobody is listening — an
// older build, or no window component installed — and the caller keeps its
// existing authenticated-browser path, which is why a timeout is not an error the
// user ever sees.
func requestDesktopActivation(runtimeDir string) error {
	// Without the window component beside this executable the instance that is
	// already running has no window to present either — the supervisor starts the
	// component only when it finds it there, and only then does it watch for
	// requests. Answering at once keeps a launch on its browser path instead of
	// waiting for a request nobody is watching for, and leaves no stale request
	// behind.
	if _, found := windowHelperPath(); !found {
		return errors.New("no native window component is installed beside the application")
	}
	path := activationRequestPath(runtimeDir)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return errors.New("cannot ask the running instance for its dashboard window")
	}
	_, writeErr := file.WriteString("present\n")
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return errors.New("cannot ask the running instance for its dashboard window")
	}
	// The file is created 0600 and never holds a secret, so this confirmation is
	// not what makes it private. The supervisor can take the request between the
	// close and here, and a request that is already gone was served: that is
	// success, not a reason to fall back to the browser.
	if err := privatefs.ProtectFile(path); err != nil {
		if _, statErr := os.Stat(path); statErr == nil {
			return errors.New("cannot protect the window request")
		}
	}

	deadline := time.Now().Add(desktopActivationWait)
	for {
		// Removal is the acknowledgement: the supervisor takes the request before
		// it starts anything, so an accepted request is never lost.
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("the running instance did not answer a dashboard window request in time")
		}
		time.Sleep(desktopActivationPoll / 5)
	}
}

// consumeActivationRequest removes a pending request and reports whether there was
// one. Only the supervisor calls it, and only removal counts as taking it.
func consumeActivationRequest(runtimeDir string) bool {
	return os.Remove(activationRequestPath(runtimeDir)) == nil
}

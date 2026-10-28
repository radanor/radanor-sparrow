package desktop

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/radanor/radanor-shield/internal/ipc"
	"github.com/radanor/radanor-shield/internal/privatefs"
)

// Discovery contains only a protected loopback origin, never a ticket/session.
// It is consulted only when the OS lock proves another desktop owner is alive.
func reopenDashboard(parent context.Context, runtimeDir string, noBrowser bool) error {
	if noBrowser {
		return nil
	}
	if activateDesktopWindow() {
		return nil
	}
	// Windows raised the window it already owns above. On Linux the window is a
	// separate process belonging to the running supervisor, so the request is
	// handed to that supervisor, which presents the window and keeps ownership of
	// its lifetime; this launch then exits without starting a second core or a
	// second gateway. A request nobody takes falls through to the browser below,
	// which is also what an older instance or a missing window component does.
	if requestDesktopActivation(runtimeDir) == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return errors.New("Radanor Sparrow is running but its dashboard is not ready. Retry shortly")
		case <-ticker.C:
			path := filepath.Join(runtimeDir, "desktop-origin")
			file, err := os.Open(path)
			if err != nil {
				continue
			}
			value, err := io.ReadAll(io.LimitReader(file, 129))
			_ = file.Close()
			if err != nil {
				continue
			}
			if len(value) > 128 {
				return errors.New("Invalid private dashboard discovery state")
			}
			origin := string(value)
			if !validOrigin(origin) {
				return errors.New("Invalid private dashboard origin")
			}
			secret, err := ipc.LoadSecret(ipc.SecretPath(runtimeDir))
			if err != nil {
				continue
			}
			return launchDashboard(ctx, origin, runtimeDir, secret, openBrowser)
		}
	}
}

// Publish a complete, protected record so a concurrent duplicate launch never
// observes a truncated origin while the first launcher is starting.
func publishOrigin(path, origin string) error {
	if !validOrigin(origin) {
		return errors.New("invalid dashboard origin")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".desktop-origin-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.WriteString(origin); err != nil {
		return err
	}
	if err := privatefs.ProtectFile(file.Name()); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func validOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Hostname() != "127.0.0.1" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	port, err := strconv.Atoi(u.Port())
	return err == nil && port > 0 && port <= 65535 && origin == "http://127.0.0.1:"+strconv.Itoa(port)
}

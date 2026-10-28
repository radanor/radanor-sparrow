//go:build !windows && !linux

package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"log/slog"
)

type containment struct{}

func newContainment() (containment, error) {
	return containment{}, errors.New("Desktop delivery supports Windows and Linux")
}
func (containment) attach(_ *os.Process) error { return nil }
func (containment) kill(process *os.Process)   { _ = process.Kill() }
func (containment) close()                     {}
func configureChild(_ *exec.Cmd)               {}
func openBrowser(_ string) error               { return errors.New("unsupported desktop platform") }
func activateDesktopWindow() bool              { return false }
func launchDesktopDashboard(ctx context.Context, _ context.CancelFunc, origin, runtimeDir, _ string, secret []byte, _ *slog.Logger) (func(), error) {
	return func() {}, launchDashboard(ctx, origin, runtimeDir, secret, openBrowser)
}
func ShowError(err error) { fmt.Fprintln(os.Stderr, err) }

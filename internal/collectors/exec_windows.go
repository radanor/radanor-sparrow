//go:build windows

package collectors

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// configureCommandCleanup hides read-only collector command windows.
//
// The cross-platform WaitDelay already bounds how long a cancelled command can
// keep a collector waiting on its output, which is the defect this cleanup
// exists to fix. Killing a Windows process tree requires a job object, which
// would be a new privileged concept in an otherwise unprivileged, read-only
// collector; it is deferred to the outstanding native Windows validation pass
// rather than introduced here on reasoning alone.
func configureCommandCleanup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}

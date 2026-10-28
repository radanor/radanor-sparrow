//go:build unix

package collectors

import (
	"os/exec"
	"syscall"
)

// configureCommandCleanup puts each collector command in its own process group
// and kills that group when the command is cancelled.
//
// Killing only the direct child is not enough: a tool that spawns a helper
// leaves the helper running with the inherited stdout/stderr pipes, and Wait
// cannot return until those pipes close. Running the command in a dedicated
// group means the cancellation kills the helper too, so a timed-out collector
// cannot leave processes behind or hold the collector open.
//
// The group is created for the child alone, so nothing outside that command's
// own tree is ever signalled.
func configureCommandCleanup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if cmd.Cancel != nil {
		previous := cmd.Cancel
		cmd.Cancel = func() error {
			if cmd.Process == nil {
				return previous()
			}
			// A negative pid signals the whole process group, which the child
			// leads because of Setpgid above.
			if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
				return previous()
			}
			return nil
		}
	}
}

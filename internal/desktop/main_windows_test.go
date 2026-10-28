//go:build windows

package desktop

import (
	"os"
	"testing"

	"github.com/radanor/radanor-shield/internal/testsupport"
)

func TestMain(m *testing.M) {
	// Containment fixtures are children of the package holding the lock and
	// never open IPC; trying to lock again would deadlock their supervisor.
	if os.Getenv("RADANOR_CHILD_FIXTURE") != "" {
		os.Exit(m.Run())
	}
	os.Exit(testsupport.WithWindowsIPC(m.Run))
}

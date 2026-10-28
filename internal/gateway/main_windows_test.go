//go:build windows

package gateway

import (
	"os"
	"testing"

	"github.com/radanor/radanor-shield/internal/testsupport"
)

func TestMain(m *testing.M) { os.Exit(testsupport.WithWindowsIPC(m.Run)) }

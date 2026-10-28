//go:build windows

// Package testsupport contains helpers used only by test entry points.
package testsupport

import (
	"errors"
	"fmt"
	"os"
	"runtime"

	"golang.org/x/sys/windows"
)

// WithWindowsIPC serializes test packages that exercise the compatibility pipe.
// The production endpoint is deliberately fixed, so directory isolation cannot
// prevent package-parallel test binaries from contending for it. This separate
// mutex never participates in production single-instance or authentication code.
func WithWindowsIPC(run func() int) int {
	return withWindowsIPCMutex(`Local\RadanorSparrow-TestPackages-IPC`, 300000, run)
}

func withWindowsIPCMutex(mutexName string, waitMilliseconds uint32, run func() int) int {
	runtime.LockOSThread() // Windows mutex ownership belongs to an OS thread.
	defer runtime.UnlockOSThread()
	name, err := windows.UTF16PtrFromString(mutexName)
	if err != nil {
		return 1
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		fmt.Fprintln(os.Stderr, "cannot create Windows IPC test serialization mutex:", err)
		return 1
	}
	defer windows.CloseHandle(handle)
	status, err := windows.WaitForSingleObject(handle, waitMilliseconds)
	if err != nil || (status != windows.WAIT_OBJECT_0 && status != windows.WAIT_ABANDONED) {
		fmt.Fprintln(os.Stderr, "cannot acquire Windows IPC test serialization mutex:", status, err)
		return 1
	}
	defer windows.ReleaseMutex(handle)
	return run()
}

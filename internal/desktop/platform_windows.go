//go:build windows

package desktop

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/radanor/radanor-shield/internal/branding"
	"golang.org/x/sys/windows"
)

type containment struct{ job windows.Handle }

func newContainment() (containment, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return containment{}, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)))
	if err != nil {
		windows.CloseHandle(job)
		return containment{}, err
	}
	return containment{job}, nil
}

func (guard containment) attach(process *os.Process) error {
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	return windows.AssignProcessToJobObject(guard.job, handle)
}
func (guard containment) kill(_ *os.Process) { _ = windows.TerminateJobObject(guard.job, 1) }
func (guard containment) close()             { _ = windows.CloseHandle(guard.job) }
func configureChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}

func openBrowser(url string) error {
	// ShellExecute may use COM; keep initialization and execution on one thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err != nil && !errors.Is(err, windows.Errno(windows.S_FALSE)) {
		return err
	}
	defer windows.CoUninitialize()
	verb, _ := windows.UTF16PtrFromString("open")
	target, err := windows.UTF16PtrFromString(url)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, target, nil, nil, windows.SW_SHOWNORMAL)
}

func ShowError(err error) {
	text, _ := windows.UTF16PtrFromString(err.Error())
	title, _ := windows.UTF16PtrFromString(branding.DisplayName)
	windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
	// Useful when explicitly run from a diagnostic terminal; no credentials.
	fmt.Fprintln(os.Stderr, err)
}

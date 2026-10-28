//go:build windows

package lifecycle

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	kernel32     = syscall.NewLazyDLL("kernel32.dll")
	createMutex  = kernel32.NewProc("CreateMutexW")
	releaseMutex = kernel32.NewProc("ReleaseMutex")
	closeHandle  = kernel32.NewProc("CloseHandle")
)

type Lock struct{ handle syscall.Handle }

func Acquire(runtimeDir, name string) (*Lock, error) {
	p, _ := syscall.UTF16PtrFromString(`Local\RadanorShield-` + name)
	h, _, err := createMutex.Call(0, 1, uintptr(unsafe.Pointer(p)))
	if h == 0 {
		return nil, err
	}
	const errorAlreadyExists syscall.Errno = 183
	if err == errorAlreadyExists {
		closeHandle.Call(h)
		return nil, fmt.Errorf("%s %w", name, ErrAlreadyRunning)
	}
	return &Lock{handle: syscall.Handle(h)}, nil
}

func (l *Lock) Close() error {
	if l == nil || l.handle == 0 {
		return nil
	}
	releaseMutex.Call(uintptr(l.handle))
	closeHandle.Call(uintptr(l.handle))
	l.handle = 0
	return nil
}

//go:build !windows

package ipc

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

func endpoint(runtimeDir string) string { return filepath.Join(runtimeDir, "core.sock") }

func listen(path string) (net.Listener, error) {
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

func dial(ctx context.Context, path string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", path)
}

func verifyPeer(conn net.Conn) error {
	u, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("unexpected IPC transport")
	}
	raw, err := u.SyscallConn()
	if err != nil {
		return err
	}
	var cred *syscall.Ucred
	var inner error
	err = raw.Control(func(fd uintptr) {
		cred, inner = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if err != nil {
		return err
	}
	if inner != nil {
		return inner
	}
	if cred == nil || int(cred.Uid) != os.Geteuid() {
		return fmt.Errorf("IPC peer user mismatch")
	}
	return nil
}

func protectSecret(path string) error { return os.Chmod(path, 0600) }
